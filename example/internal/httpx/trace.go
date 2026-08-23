package httpx

import (
	"net/http"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

// ScopeName identifies this package as the instrumentation scope of the spans
// and metrics it produces.
const ScopeName = "github.com/example/reference-service/internal/httpx"

// unmatchedRoute labels a request that matched no registered route. The label
// is fixed rather than the raw path, because an unmatched path is attacker
// controlled and would otherwise create one time series per probe.
const unmatchedRoute = "unmatched"

// Router resolves a request to the route pattern it matches, without serving
// it. http.ServeMux satisfies the interface, which is how the outer stages
// learn the pattern that only the router knows.
type Router interface {
	Handler(r *http.Request) (h http.Handler, pattern string)
}

// ServerSpan continues the caller's trace and opens one server span per
// request. It runs before the access log so every record inside the request,
// log line and error envelope alike, joins to the same trace.
//
// The router is optional. Without one the span carries no route attribute,
// because a span named after a raw path is worse than one with no name at all.
func ServerSpan(router Router) func(http.Handler) http.Handler {
	tracer := otel.Tracer(ScopeName)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, state := withState(ctx)
			pattern := matchRoute(router, r)
			state.setRoutePattern(pattern)

			ctx, span := tracer.Start(ctx, spanName(r.Method, pattern),
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(requestAttributes(r, pattern)...))
			defer span.End()

			rec := newRecorder(w)
			defer func() {
				if recovered := recover(); recovered != nil {
					p := capturePanic(recovered)
					span.RecordError(p.err(), trace.WithStackTrace(false),
						trace.WithAttributes(attribute.String("exception.stacktrace", string(p.stack))))
					span.SetStatus(codes.Error, "handler panicked")
					panic(p)
				}
				status := rec.Status()
				span.SetAttributes(semconv.HTTPResponseStatusCode(status))
				// Only a server fault marks the span as an error. A 404 or a
				// 401 is the service working, and marking those errors makes
				// the error rate of a trace backend useless.
				if status >= http.StatusInternalServerError {
					span.SetStatus(codes.Error, http.StatusText(status))
				}
			}()

			next.ServeHTTP(rec, r.WithContext(ctx))
		})
	}
}

// matchRoute asks the router which pattern the request matches. A request that
// matches nothing gets the fixed label.
func matchRoute(router Router, r *http.Request) string {
	if router == nil {
		return ""
	}
	_, pattern := router.Handler(r)
	if pattern == "" {
		return unmatchedRoute
	}
	return routeFromPattern(pattern)
}

// routeFromPattern drops the method from a registered pattern. http.ServeMux
// patterns read "GET /v1/items/{id}", while the route attribute is the path
// template alone, and keeping the method in it would split one route across
// its methods twice: once in the route label and once in the method label.
func routeFromPattern(pattern string) string {
	if method, rest, found := strings.Cut(pattern, " "); found && normalizeMethod(method) != "_OTHER" {
		pattern = rest
	}
	return strings.TrimSpace(pattern)
}

// spanName follows the convention "{method} {route}". A request with no known
// route is named by its method alone, which keeps the name set bounded.
func spanName(method, pattern string) string {
	method = normalizeMethod(method)
	if pattern == "" {
		return method
	}
	return method + " " + pattern
}

// requestAttributes are the attributes known before the handler runs.
func requestAttributes(r *http.Request, pattern string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		semconv.HTTPRequestMethodKey.String(normalizeMethod(r.Method)),
		semconv.URLScheme(scheme(r)),
		semconv.URLPath(r.URL.Path),
		semconv.UserAgentOriginal(r.UserAgent()),
		semconv.ClientAddress(clientIP(r)),
	}
	if pattern != "" {
		attrs = append(attrs, semconv.HTTPRoute(pattern))
	}
	return attrs
}

// scheme reports the request scheme. A server behind a terminating proxy sees
// no TLS state, so the absence of it is reported as plain HTTP rather than
// guessed from a forwarded header the service cannot verify.
func scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// normalizeMethod bounds the method label. An unregistered method is reported
// as _OTHER, so a client sending arbitrary verbs cannot expand the label set.
func normalizeMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodConnect,
		http.MethodOptions, http.MethodTrace:
		return method
	default:
		return "_OTHER"
	}
}
