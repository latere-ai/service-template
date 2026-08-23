package httpx

import (
	"log/slog"
	"net"
	"net/http"
	"time"
)

// AccessLog records one line per request. It sits inside recovery and inside
// the request identifier, so a panicking request still produces a line and
// every line names the request.
//
// A panic is recorded here and re-raised: the access log is the record of what
// the service was asked to do, and a request that ended in a panic is exactly
// the one an operator looks for. The stack travels with the re-raised value so
// the recovery stage still logs the frames of the panic site.
func AccessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log := loggerOrDefault(logger)
			rec := newRecorder(w)
			start := time.Now()

			defer func() {
				recovered := recover()
				if recovered == nil {
					write(log, r, rec.Status(), rec.written, time.Since(start), false)
					return
				}
				p := capturePanic(recovered)
				// The recovery stage is about to render a 500 for this
				// request, so the line reports the status the client receives.
				write(log, r, http.StatusInternalServerError, rec.written, time.Since(start), true)
				panic(p)
			}()

			next.ServeHTTP(rec, r)
		})
	}
}

// write emits the line. The route is the registered pattern rather than the
// raw path, so the field groups by endpoint.
func write(log *slog.Logger, r *http.Request, status int, bytes int64, elapsed time.Duration, panicked bool) {
	ctx := r.Context()
	attrs := []any{
		slog.String("request_id", RequestID(ctx)),
		slog.String("method", r.Method),
		slog.String("route", RoutePattern(ctx)),
		slog.String("path", r.URL.Path),
		slog.Int("status", status),
		slog.Int64("bytes", bytes),
		slog.Duration("duration", elapsed),
		slog.String("client_ip", clientIP(r)),
		slog.String("user_agent", r.UserAgent()),
	}
	if panicked {
		attrs = append(attrs, slog.Bool("panic", true))
	}

	switch {
	case panicked || status >= http.StatusInternalServerError:
		log.ErrorContext(ctx, "request", attrs...)
	case status >= http.StatusBadRequest:
		log.WarnContext(ctx, "request", attrs...)
	default:
		log.InfoContext(ctx, "request", attrs...)
	}
}

// clientIP reports the peer address without its port. Forwarded headers are
// not consulted, because trusting them without knowing the proxy in front of
// the service lets any caller choose the value the rate limiter and the log
// record.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// loggerOrDefault falls back to the process logger, which the observability
// setup replaces with the exporting one.
func loggerOrDefault(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.Default()
}
