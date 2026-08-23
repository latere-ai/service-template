package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Consumer defaults.
const (
	// DefaultFinishTimeout is how long the message in hand has to finish after
	// shutdown starts. It is shorter than the runner's shutdown window, so the
	// consumer returns before the runner gives up on it.
	DefaultFinishTimeout = 15 * time.Second
	// DefaultAckTimeout bounds one acknowledgement.
	DefaultAckTimeout = 5 * time.Second
)

// ErrQueueClosed reports a queue that will deliver nothing more. A consumer
// treats it as a clean stop rather than a failure.
var ErrQueueClosed = errors.New("worker: queue closed")

// Message is one unit of work taken from a queue.
//
// Ack tells the queue the work is done and the message may be dropped. Nack
// returns it for redelivery. Exactly one of them is called per message.
type Message interface {
	Body() []byte
	Ack(ctx context.Context) error
	Nack(ctx context.Context) error
}

// Queue delivers messages one at a time. It is the seam a broker plugs into:
// the template selects none and requires none.
//
// Receive blocks until a message is available, the context ends, or the queue
// closes. It reports the context error on cancellation and [ErrQueueClosed]
// when nothing more will arrive.
type Queue interface {
	Receive(ctx context.Context) (Message, error)
}

// Handler processes one message. It returns nil only when the work is complete,
// because completion is what the acknowledgement asserts.
type Handler func(ctx context.Context, m Message) error

// Consumer is a [Job] that receives from a queue until the context ends.
//
// Acknowledgement always follows completion. A message acknowledged before its
// work finishes is lost when the process stops halfway through, and it is lost
// silently: the queue has no record and the work has no result. On shutdown the
// consumer stops receiving and gives the message in hand a bounded window to
// finish. Work that finishes in the window is acknowledged; work that does not
// is left unacknowledged for redelivery.
type Consumer struct {
	// ConsumerName is the job name.
	ConsumerName string
	// Queue is where messages come from.
	Queue Queue
	// Handler processes one message.
	Handler Handler
	// FinishTimeout is the window the message in hand gets after shutdown
	// starts. Zero means DefaultFinishTimeout.
	FinishTimeout time.Duration
	// AckTimeout bounds one acknowledgement. Zero means DefaultAckTimeout.
	AckTimeout time.Duration
	// Logger receives acknowledgement failures. It defaults to slog.Default().
	Logger *slog.Logger

	// clock drives the finish window. It is a field so a test drives shutdown
	// without waiting the window out.
	clock clock
}

// NewConsumer returns a consumer with the default windows.
func NewConsumer(name string, q Queue, h Handler) *Consumer {
	return &Consumer{ConsumerName: name, Queue: q, Handler: h, clock: systemClock{}}
}

// Name reports the job name.
func (c *Consumer) Name() string { return c.ConsumerName }

// Run receives and processes messages until the context ends or the queue
// closes. It reports an error only when the queue itself failed.
func (c *Consumer) Run(ctx context.Context) error {
	if c.Queue == nil || c.Handler == nil {
		return fmt.Errorf("worker: consumer %q needs a queue and a handler", c.ConsumerName)
	}

	for {
		// The context is checked between messages, so a cancelled consumer
		// takes no new work even when the queue has a message ready.
		if ctx.Err() != nil {
			return nil
		}

		m, err := c.Queue.Receive(ctx)
		switch {
		case errors.Is(err, ErrQueueClosed):
			return nil
		case err != nil:
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("worker: consumer %q receive: %w", c.ConsumerName, err)
		case m == nil:
			continue
		}

		c.process(ctx, m)
	}
}

// process runs the handler and then acknowledges the outcome.
func (c *Consumer) process(ctx context.Context, m Message) {
	err := c.handle(ctx, m)

	ackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.ackTimeout())
	defer cancel()

	if err == nil {
		if ackErr := m.Ack(ackCtx); ackErr != nil {
			c.logger().ErrorContext(ctx, "acknowledging a message failed",
				"job", c.ConsumerName, "error", ackErr)
		}
		return
	}

	c.logger().WarnContext(ctx, "message handling failed",
		"job", c.ConsumerName, "error", err.Error())
	if nackErr := m.Nack(ackCtx); nackErr != nil {
		c.logger().ErrorContext(ctx, "returning a message failed",
			"job", c.ConsumerName, "error", nackErr)
	}
}

// handle runs the handler on a context that survives shutdown for the finish
// window. The handler is cancelled only once the window expires, so a message
// in hand at shutdown is finished rather than cut short.
func (c *Consumer) handle(ctx context.Context, m Message) error {
	workCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()

	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-done:
			return
		case <-ctx.Done():
		}
		select {
		case <-done:
		case <-c.tick(c.finishTimeout()):
			cancel()
		}
	}()

	return c.Handler(workCtx, m)
}

// tick waits out d on the consumer's clock.
func (c *Consumer) tick(d time.Duration) <-chan time.Time {
	if c.clock == nil {
		return systemClock{}.After(d)
	}
	return c.clock.After(d)
}

// finishTimeout reports the configured finish window or the default.
func (c *Consumer) finishTimeout() time.Duration {
	if c.FinishTimeout <= 0 {
		return DefaultFinishTimeout
	}
	return c.FinishTimeout
}

// ackTimeout reports the configured acknowledgement bound or the default.
func (c *Consumer) ackTimeout() time.Duration {
	if c.AckTimeout <= 0 {
		return DefaultAckTimeout
	}
	return c.AckTimeout
}

// logger reports the configured logger or the process default.
func (c *Consumer) logger() *slog.Logger {
	if c.Logger == nil {
		return slog.Default()
	}
	return c.Logger
}

// MemoryQueue is an in-process [Queue].
//
// It delivers what is published to it, in order, to one receiver at a time. It
// carries work between goroutines in one process and is what the tests run
// against. A nacked message is not redelivered, because redelivery is a
// property of the broker rather than of the interface.
type MemoryQueue struct {
	messages chan *MemoryMessage

	mu     sync.Mutex
	closed bool
}

// NewMemoryQueue returns a queue that buffers capacity messages.
func NewMemoryQueue(capacity int) *MemoryQueue {
	if capacity < 0 {
		capacity = 0
	}
	return &MemoryQueue{messages: make(chan *MemoryMessage, capacity)}
}

// Publish adds a message and returns it, so a caller can assert what happened
// to it. It reports [ErrQueueClosed] after Close.
func (q *MemoryQueue) Publish(ctx context.Context, body []byte) (*MemoryMessage, error) {
	q.mu.Lock()
	closed := q.closed
	q.mu.Unlock()
	if closed {
		return nil, ErrQueueClosed
	}

	m := &MemoryMessage{body: body}
	select {
	case q.messages <- m:
		return m, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Receive returns the next message.
func (q *MemoryQueue) Receive(ctx context.Context) (Message, error) {
	select {
	case m, ok := <-q.messages:
		if !ok {
			return nil, ErrQueueClosed
		}
		return m, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close stops delivery once the buffered messages are drained.
func (q *MemoryQueue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	close(q.messages)
}

// MemoryMessage is a message held in memory. It records its acknowledgement, so
// a test asserts that work completed before the message was released.
type MemoryMessage struct {
	body []byte

	mu     sync.Mutex
	acked  bool
	nacked bool
}

// Body reports the payload.
func (m *MemoryMessage) Body() []byte { return m.body }

// Ack marks the message done.
func (m *MemoryMessage) Ack(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.acked || m.nacked {
		return errors.New("worker: message already settled")
	}
	m.acked = true
	return nil
}

// Nack returns the message unacknowledged.
func (m *MemoryMessage) Nack(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.acked || m.nacked {
		return errors.New("worker: message already settled")
	}
	m.nacked = true
	return nil
}

// Acked reports whether the message was acknowledged.
func (m *MemoryMessage) Acked() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.acked
}

// Nacked reports whether the message was returned unacknowledged.
func (m *MemoryMessage) Nacked() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nacked
}
