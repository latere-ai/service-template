package httpx

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
)

// panicValue carries a recovered panic outward with the stack captured where
// it was first seen. A stage that re-panics would otherwise replace the frames
// of the panic site with its own, and a stack that starts at the middleware is
// worthless for finding the defect.
type panicValue struct {
	value any
	stack []byte
}

// capturePanic normalizes a recovered value. A value that already travelled
// through an inner stage keeps its original stack.
func capturePanic(recovered any) panicValue {
	if p, ok := recovered.(panicValue); ok {
		return p
	}
	return panicValue{value: recovered, stack: debug.Stack()}
}

// err renders the panic as an error for the log and for the envelope's cause.
func (p panicValue) err() error {
	if err, ok := p.value.(error); ok {
		return fmt.Errorf("panic: %w", err)
	}
	return fmt.Errorf("panic: %v", p.value)
}

// Recover is the outermost stage. A panic anywhere inside becomes a 500 with
// the envelope and a logged stack, rather than a connection the client sees
// close mid-response.
//
// http.ErrAbortHandler is the one value that passes through untouched: net/http
// defines it as the way a handler abandons a response deliberately, and turning
// it into a 500 would both hide that intent and log a stack for it.
func Recover() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The recorder is installed here, outermost, so every stage that
			// reports a status reports the same one and a committed response
			// is recognisable from any of them.
			rec := newRecorder(w)
			// The state is installed here so the stages inside can fill in
			// the identifier and the route, and this stage can still name the
			// request when it renders a panic.
			ctx, _ := withState(r.Context())
			r = r.WithContext(ctx)
			// Everything below reads the request context off r, which the
			// line above has just replaced. The linter cannot follow that
			// through the closure and reads it as a context being dropped.
			//nolint:contextcheck // the request context is carried on r and read from it here
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}
				p := capturePanic(recovered)
				if errors.Is(asError(p.value), http.ErrAbortHandler) {
					panic(recovered)
				}

				slog.ErrorContext(r.Context(), "handler panicked",
					slog.Any("error", p.err()),
					slog.String("request_id", RequestID(r.Context())),
					slog.String("method", r.Method),
					slog.String("route", RoutePattern(r.Context())),
					slog.String("stack", string(p.stack)))

				// A response that is already committed cannot be replaced, so
				// the record above is all the request gets.
				if rec.wroteHeader() || rec.hijacked {
					return
				}
				WriteError(rec, r, Internal(p.err()))
			}()
			next.ServeHTTP(rec, r)
		})
	}
}

// asError adapts a recovered value for errors.Is. A panic value that is not an
// error compares against nothing.
func asError(v any) error {
	if err, ok := v.(error); ok {
		return err
	}
	return nil
}
