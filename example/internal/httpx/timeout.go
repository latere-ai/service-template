package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Timeout bounds how long a handler may take. It sets a deadline on the
// request context, and when the deadline passes before the response is
// committed it answers with the envelope so a stalled request produces the
// same body shape as any other failure.
//
// The handler runs on its own goroutine so a handler that ignores its context
// cannot hold the connection past the budget. Writes from a handler that
// finishes after the deadline are dropped, because the response already went
// out and the writer belongs to net/http again once this stage returns.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if d <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()

			tw := &timeoutWriter{ResponseWriter: w}
			done := make(chan struct{})
			// The channel wakes this goroutine; the sink decides who owns the
			// panic, so a panic raised as the deadline passes is recorded
			// exactly once and never dropped.
			wake := make(chan panicValue, 1)
			sink := &panicSink{}

			go func() {
				defer func() {
					recovered := recover()
					if recovered == nil {
						return
					}
					p := capturePanic(recovered)
					if !sink.deliver(p) {
						logLatePanic(ctx, r, p)
						return
					}
					wake <- p
				}()
				next.ServeHTTP(tw, r.WithContext(ctx))
				close(done)
			}()

			select {
			case <-done:
			case p := <-wake:
				panic(p)
			case <-ctx.Done():
				tw.expire(r)
				// A panic delivered as the deadline passed cannot replace the
				// response that just went out, so it is recorded here.
				if p, ok := sink.abandon(); ok {
					logLatePanic(ctx, r, p)
				}
			}
		})
	}
}

// panicSink decides whether a panic from the handler goroutine still has a
// stage waiting for it. Ownership is taken under a lock, so a panic raised at
// the same moment the deadline passes is either handed to the waiting stage or
// logged by the goroutine, and never both or neither.
type panicSink struct {
	mu        sync.Mutex
	abandoned bool
	value     *panicValue
	delivered bool
}

// deliver offers p to the waiting stage. It reports false once the stage has
// stopped waiting, which makes the caller responsible for the record.
func (s *panicSink) deliver(p panicValue) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.abandoned {
		return false
	}
	s.value = &p
	s.delivered = true
	return true
}

// abandon stops waiting and reports a panic that was delivered but not yet
// observed, so the caller can record it.
func (s *panicSink) abandon() (panicValue, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.abandoned = true
	if !s.delivered {
		return panicValue{}, false
	}
	s.delivered = false
	return *s.value, true
}

// logLatePanic records a panic that arrived after the response was sent. No
// stage can render it, and a panic that leaves no record is the defect the
// recovery stage exists to prevent.
func logLatePanic(ctx context.Context, r *http.Request, p panicValue) {
	slog.ErrorContext(ctx, "handler panicked after the deadline",
		slog.Any("error", p.err()),
		slog.String("request_id", RequestID(ctx)),
		slog.String("method", r.Method),
		slog.String("route", RoutePattern(ctx)),
		slog.String("stack", string(p.stack)))
}

// timeoutWriter guards the response so the handler goroutine and the timeout
// never write at the same time, and so nothing the handler produces after the
// deadline reaches the client.
type timeoutWriter struct {
	http.ResponseWriter

	mu sync.Mutex
	// committed reports that a status line already went out, which makes the
	// response unreplaceable.
	committed bool
	// expired reports that the timeout owns the response now.
	expired bool
	// detached receives the handler's header writes after the deadline, so a
	// late handler touches no map net/http is still reading.
	detached http.Header
}

func (tw *timeoutWriter) Header() http.Header {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.expired {
		if tw.detached == nil {
			tw.detached = http.Header{}
		}
		return tw.detached
	}
	return tw.ResponseWriter.Header()
}

func (tw *timeoutWriter) WriteHeader(status int) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.expired || tw.committed {
		return
	}
	tw.committed = true
	tw.ResponseWriter.WriteHeader(status)
}

func (tw *timeoutWriter) Write(b []byte) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.expired {
		return 0, http.ErrHandlerTimeout
	}
	tw.committed = true
	return tw.ResponseWriter.Write(b)
}

// Unwrap exposes the wrapped writer to http.ResponseController.
func (tw *timeoutWriter) Unwrap() http.ResponseWriter { return tw.ResponseWriter }

// Flush keeps a streaming handler working through this stage, for code that
// asserts http.Flusher rather than using http.ResponseController. A flush
// commits the response, so the timeout can no longer replace it.
func (tw *timeoutWriter) Flush() {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.expired {
		return
	}
	tw.committed = true
	// Flush reports no error to a handler that asserts http.Flusher, and a
	// writer that cannot flush has nothing buffered to lose.
	_ = http.NewResponseController(tw.ResponseWriter).Flush()
}

// expire takes ownership of the response from the handler. A response that is
// already committed is left as it is, because a status line cannot be recalled.
func (tw *timeoutWriter) expire(r *http.Request) {
	tw.mu.Lock()
	committed := tw.committed
	tw.expired = true
	tw.mu.Unlock()

	if committed {
		return
	}
	WriteError(directWriter{tw: tw}, r, New(http.StatusGatewayTimeout,
		"the request exceeded the server time budget"))
}

// directWriter is the path the timeout uses to render the envelope. It holds
// the same lock as the handler's writes, so the two cannot interleave, and it
// bypasses the expired check that silences the handler.
type directWriter struct{ tw *timeoutWriter }

func (d directWriter) Header() http.Header {
	d.tw.mu.Lock()
	defer d.tw.mu.Unlock()
	return d.tw.ResponseWriter.Header()
}

func (d directWriter) WriteHeader(status int) {
	d.tw.mu.Lock()
	defer d.tw.mu.Unlock()
	if d.tw.committed {
		return
	}
	d.tw.committed = true
	d.tw.ResponseWriter.WriteHeader(status)
}

func (d directWriter) Write(b []byte) (int, error) {
	d.tw.mu.Lock()
	defer d.tw.mu.Unlock()
	d.tw.committed = true
	return d.tw.ResponseWriter.Write(b)
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (d directWriter) Unwrap() http.ResponseWriter { return d.tw.ResponseWriter }
