package httpx

import (
	"net/http"
)

// DefaultMaxBodyBytes is the request body budget a service starts with. A
// route that accepts uploads raises it for itself rather than raising it for
// every route, because an unbounded body is memory a client chooses.
const DefaultMaxBodyBytes int64 = 1 << 20

// BodyLimit caps the request body. A body that exceeds the cap fails the
// handler's read with *http.MaxBytesError, which the envelope writer renders
// as 413, so a handler needs no special case for it.
//
// A declared length above the cap is refused before the body is read at all,
// which saves reading a body that is already known to be too large.
func BodyLimit(max int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if max <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > max {
				WriteError(w, r, &http.MaxBytesError{Limit: max})
				return
			}
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, max)
			}
			next.ServeHTTP(w, r)
		})
	}
}
