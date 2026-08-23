package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// widgetRoute is the parameterized route the sampler and logging tests
// exercise. It is the case route labelling exists for: the path holds an
// identifier and the pattern does not.
const widgetRoute = "/widgets/{id}"

// spanMiddleware opens one server span per request, named by the route pattern
// the multiplexer reports.
//
// It is a test fixture and not a shipped middleware. The request chain the
// service runs lives in internal/httpx, which owns the stage order; this
// package owns the providers, the sampler, and the log bridge, and the tests
// for those need a request that produces a span without depending on the
// transport package.
func spanMiddleware(mux *http.ServeMux) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			name := r.Method
			if _, pattern := mux.Handler(r); pattern != "" {
				name = r.Method + " " + widgetRoute
			}
			ctx, span := otel.GetTracerProvider().Tracer(scopeName).Start(
				r.Context(), name, trace.WithSpanKind(trace.SpanKindServer))
			defer span.End()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r.WithContext(ctx))
			// The retain rule reads the span status, so the fixture has to
			// mark a server fault the way the request chain does.
			if rec.status >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, http.StatusText(rec.status))
			}
		})
	}
}

// newTestStack installs recording providers and returns a handler that opens a
// server span in front of a multiplexer.
func newTestStack(t *testing.T, ratio float64, handler http.HandlerFunc) (http.Handler, *tracetest.SpanRecorder, *sdkmetric.ManualReader) {
	t.Helper()

	tracerProvider, recorder := testTracerProvider(ratio, DefaultSlowRequest)
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	installGlobals(t, tracerProvider, meterProvider)
	t.Cleanup(func() {
		if err := tracerProvider.Shutdown(context.Background()); err != nil {
			t.Errorf("tracer provider shutdown: %v", err)
		}
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+widgetRoute, handler)
	return spanMiddleware(mux)(mux), recorder, reader
}

// get issues one request through the handler under test.
func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

// spanNamed returns the single ended span with the given name.
func spanNamed(t *testing.T, recorder *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	var found sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.Name() != name {
			continue
		}
		if found != nil {
			t.Fatalf("more than one span named %q", name)
		}
		found = span
	}
	if found == nil {
		t.Fatalf("no span named %q; recorded %v", name, spanNames(recorder))
	}
	return found
}

func spanNames(recorder *tracetest.SpanRecorder) []string {
	names := make([]string, 0, len(recorder.Ended()))
	for _, span := range recorder.Ended() {
		names = append(names, span.Name())
	}
	return names
}

// histogramPoints returns the series of one histogram instrument.
func histogramPoints(t *testing.T, reader *sdkmetric.ManualReader, name string) []metricdata.HistogramDataPoint[float64] {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != name {
				continue
			}
			data, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("metric %q is %T, want a float64 histogram", name, m.Data)
			}
			return data.DataPoints
		}
	}
	t.Fatalf("metric %q was not recorded", name)
	return nil
}

// statusRecorder captures the status a handler wrote, so the fixture can mark
// the span the way the request chain marks it.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.wroteHeader = true
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
