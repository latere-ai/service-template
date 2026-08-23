package httpx

import (
	"context"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
)

// Metrics records the request rate, the error rate, the duration distribution,
// and the number of requests in flight.
//
// Every series is labelled with the route pattern the span stage resolved, so
// the label set is bounded by the route table rather than by the paths clients
// send.
func Metrics() func(http.Handler) http.Handler {
	meter := otel.Meter(ScopeName)

	duration, err := meter.Float64Histogram("http.server.request.duration",
		metric.WithDescription("Duration of HTTP server requests."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(
			0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10))
	if err != nil {
		// An instrument the provider refused is reported through the
		// configured error handler and then dropped. A service that cannot
		// build a histogram still has to serve traffic.
		otel.Handle(err)
		duration = nil
	}

	active, err := meter.Int64UpDownCounter("http.server.active_requests",
		metric.WithDescription("Number of active HTTP server requests."),
		metric.WithUnit("{request}"))
	if err != nil {
		otel.Handle(err)
		active = nil
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			base := metric.WithAttributes(
				semconv.HTTPRequestMethodKey.String(normalizeMethod(r.Method)),
				semconv.URLScheme(scheme(r)),
				semconv.HTTPRoute(routeLabel(ctx)),
			)
			if active != nil {
				active.Add(ctx, 1, base)
				defer active.Add(ctx, -1, base)
			}

			rec := newRecorder(w)
			start := time.Now()
			// A panic still produced a request, and the status the client
			// receives is the 500 the recovery stage writes.
			status := http.StatusInternalServerError
			defer func() {
				if duration == nil {
					return
				}
				duration.Record(ctx, time.Since(start).Seconds(), base,
					metric.WithAttributes(
						semconv.HTTPResponseStatusCode(status),
						attribute.String("http.response.status_class", statusClass(status))))
			}()

			next.ServeHTTP(rec, r)
			status = rec.Status()
		})
	}
}

// routeLabel keeps an unresolved route out of the label set. A request that
// reached the chain without a router carries the fixed label instead of an
// empty one, so a series is never attributed to "no route".
func routeLabel(ctx context.Context) string {
	if pattern := RoutePattern(ctx); pattern != "" {
		return pattern
	}
	return unmatchedRoute
}

// statusClass groups statuses into the five families, which is the dimension
// an availability figure is computed over.
func statusClass(status int) string {
	switch {
	case status < 200:
		return "1xx"
	case status < 300:
		return "2xx"
	case status < 400:
		return "3xx"
	case status < 500:
		return "4xx"
	default:
		return "5xx"
	}
}
