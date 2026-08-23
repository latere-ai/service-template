package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestConsumer returns a consumer with a discarded log and the given clock.
func newTestConsumer(t *testing.T, q Queue, h Handler, c clock) *Consumer {
	t.Helper()
	cons := NewConsumer("orders", q, h)
	cons.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	cons.clock = c
	return cons
}

func TestConsumerAcknowledgesAfterTheWorkCompletes(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	queue := NewMemoryQueue(2)
	handled := make(chan struct{}, 1)
	var ackedDuringWork atomic.Bool

	cons := newTestConsumer(t, queue, func(_ context.Context, m Message) error {
		// Acknowledgement asserts completion, so nothing may be acknowledged
		// while the work is still running.
		if mm, ok := m.(*MemoryMessage); ok && mm.Acked() {
			ackedDuringWork.Store(true)
		}
		handled <- struct{}{}
		return nil
	}, systemClock{})

	msg, err := queue.Publish(ctx, []byte("order-1"))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cons.Run(ctx) }()

	<-handled
	waitFor(t, msg.Acked, "the message was not acknowledged after the work completed")
	if ackedDuringWork.Load() {
		t.Error("the message was acknowledged before the work completed")
	}
	if msg.Nacked() {
		t.Error("a completed message was also returned unacknowledged")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestShutdownFinishesTheMessageInHandAndAcknowledges(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	queue := NewMemoryQueue(1)
	started := make(chan struct{})
	release := make(chan struct{})

	cons := newTestConsumer(t, queue, func(context.Context, Message) error {
		close(started)
		<-release
		return nil
	}, newFakeClock())

	msg, err := queue.Publish(ctx, []byte("order-1"))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cons.Run(ctx) }()

	<-started
	// Shutdown starts while the work is in hand. The work finishes inside the
	// window, so the message is acknowledged rather than redelivered.
	cancel()
	close(release)

	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !msg.Acked() {
		t.Fatalf("the message finished inside the window was not acknowledged, nacked = %v", msg.Nacked())
	}
}

func TestShutdownMidMessageLeavesTheMessageUnacknowledged(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clk := newFakeClock()
	queue := NewMemoryQueue(1)
	started := make(chan struct{})

	cons := newTestConsumer(t, queue, func(ctx context.Context, _ Message) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}, clk)
	cons.FinishTimeout = 5 * time.Second

	msg, err := queue.Publish(ctx, []byte("order-1"))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cons.Run(ctx) }()

	<-started
	cancel()

	// The finish window expires with the work incomplete. An unacknowledged
	// message is redelivered; an acknowledged one would be lost.
	clk.waitForWaiters(t, 1)
	clk.Advance(6 * time.Second)

	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if msg.Acked() {
		t.Fatal("an unfinished message was acknowledged")
	}
	if !msg.Nacked() {
		t.Fatal("an unfinished message was not returned for redelivery")
	}
}

func TestFailedWorkReturnsTheMessage(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	queue := NewMemoryQueue(1)
	cons := newTestConsumer(t, queue, func(context.Context, Message) error {
		return errBoom
	}, systemClock{})

	msg, err := queue.Publish(ctx, []byte("order-1"))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cons.Run(ctx) }()

	waitFor(t, msg.Nacked, "the failed message was not returned unacknowledged")
	if msg.Acked() {
		t.Error("a failed message was acknowledged")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestConsumerTakesNoNewMessageAfterCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	queue := NewMemoryQueue(2)
	var handled atomic.Int64

	cons := newTestConsumer(t, queue, func(context.Context, Message) error {
		handled.Add(1)
		cancel()
		return nil
	}, systemClock{})

	first, err := queue.Publish(context.Background(), []byte("order-1"))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	second, err := queue.Publish(context.Background(), []byte("order-2"))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cons.Run(ctx) }()

	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := handled.Load(); got != 1 {
		t.Fatalf("messages handled = %d, want 1", got)
	}
	if !first.Acked() {
		t.Error("the message in hand was not acknowledged")
	}
	if second.Acked() || second.Nacked() {
		t.Error("a message was taken after cancellation")
	}
	if string(second.Body()) != "order-2" {
		t.Errorf("Body = %q, want order-2", second.Body())
	}
}

func TestConsumerRunsAsAContinuousJob(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	queue := NewMemoryQueue(1)
	handled := make(chan struct{}, 1)
	cons := newTestConsumer(t, queue, func(context.Context, Message) error {
		handled <- struct{}{}
		return nil
	}, systemClock{})

	r, _ := newTestRunner(t, systemClock{}, nil)
	if err := r.Continuous(cons); err != nil {
		t.Fatalf("Continuous: %v", err)
	}
	errc := runInBackground(t, r, ctx)

	if _, err := queue.Publish(ctx, []byte("order-1")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	<-handled

	cancel()
	if err := waitErr(t, errc); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestConsumerNeedsAQueueAndAHandler(t *testing.T) {
	t.Parallel()
	cons := NewConsumer("orders", nil, nil)
	if err := cons.Run(context.Background()); err == nil {
		t.Fatal("Run accepted a consumer with no queue and no handler")
	}
	if cons.Name() != "orders" {
		t.Errorf("Name = %q, want orders", cons.Name())
	}
	if got := cons.finishTimeout(); got != DefaultFinishTimeout {
		t.Errorf("finishTimeout = %s, want %s", got, DefaultFinishTimeout)
	}
	if got := cons.ackTimeout(); got != DefaultAckTimeout {
		t.Errorf("ackTimeout = %s, want %s", got, DefaultAckTimeout)
	}
}

func TestMemoryQueueDeliveryEnds(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	queue := NewMemoryQueue(1)
	if _, err := queue.Publish(ctx, []byte("order-1")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	queue.Close()
	queue.Close()

	if _, err := queue.Publish(ctx, []byte("order-2")); !errors.Is(err, ErrQueueClosed) {
		t.Errorf("Publish after Close = %v, want ErrQueueClosed", err)
	}
	if _, err := queue.Receive(ctx); err != nil {
		t.Fatalf("Receive of a buffered message after Close: %v", err)
	}
	if _, err := queue.Receive(ctx); !errors.Is(err, ErrQueueClosed) {
		t.Errorf("Receive on a drained closed queue = %v, want ErrQueueClosed", err)
	}

	// A closed queue stops the consumer without reporting a failure, because
	// nothing is wrong when delivery is over.
	cons := newTestConsumer(t, queue, func(context.Context, Message) error { return nil }, systemClock{})
	if err := cons.Run(ctx); err != nil {
		t.Errorf("Run against a closed queue = %v, want no error", err)
	}
}

func TestMemoryQueueReceiveRespectsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := NewMemoryQueue(0).Receive(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Receive on a cancelled context = %v, want context.Canceled", err)
	}
}

func TestMemoryMessageSettlesOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	acked := &MemoryMessage{body: []byte("order-1")}
	if err := acked.Ack(ctx); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if err := acked.Ack(ctx); err == nil {
		t.Error("a message was acknowledged twice")
	}
	if err := acked.Nack(ctx); err == nil {
		t.Error("an acknowledged message was also returned")
	}

	nacked := &MemoryMessage{}
	if err := nacked.Nack(ctx); err != nil {
		t.Fatalf("Nack: %v", err)
	}
	if err := nacked.Nack(ctx); err == nil {
		t.Error("a message was returned twice")
	}
}

// erroringQueue reports a delivery failure, and then reports the queue closed
// so a consumer under test stops.
type erroringQueue struct {
	err    error
	calls  atomic.Int64
	closed bool
}

// Receive fails on the first call and then ends delivery.
func (q *erroringQueue) Receive(context.Context) (Message, error) {
	if q.calls.Add(1) == 1 {
		return nil, q.err
	}
	if q.closed {
		return nil, ErrQueueClosed
	}
	return nil, nil
}

func TestAQueueFailureStopsTheConsumerWithTheReason(t *testing.T) {
	t.Parallel()
	cons := newTestConsumer(t, &erroringQueue{err: errBoom}, func(context.Context, Message) error {
		return nil
	}, systemClock{})

	err := cons.Run(context.Background())
	if !errors.Is(err, errBoom) {
		t.Fatalf("Run error = %v, want the delivery failure %v", err, errBoom)
	}
	if !strings.Contains(err.Error(), "orders") {
		t.Errorf("Run error %v does not name the consumer", err)
	}
}

func TestAQueueThatDeliversNothingIsNotAMessage(t *testing.T) {
	t.Parallel()
	queue := &erroringQueue{err: nil, closed: true}
	var handled atomic.Int64

	cons := newTestConsumer(t, queue, func(context.Context, Message) error {
		handled.Add(1)
		return nil
	}, systemClock{})

	if err := cons.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := handled.Load(); got != 0 {
		t.Errorf("handled = %d, want 0: an absent message is not work", got)
	}
}

func TestConsumerDefaultsWithoutAClock(t *testing.T) {
	t.Parallel()
	cons := NewConsumer("orders", NewMemoryQueue(0), func(context.Context, Message) error { return nil })
	cons.clock = nil

	select {
	case <-cons.tick(time.Millisecond):
	case <-time.After(pollDeadline):
		t.Fatal("a consumer with no clock never finished its window")
	}
	if got := NewMemoryQueue(-1); cap(got.messages) != 0 {
		t.Errorf("capacity for a negative size = %d, want 0", cap(got.messages))
	}
}
