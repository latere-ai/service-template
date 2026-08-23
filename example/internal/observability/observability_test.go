package observability

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// clearEndpoints removes every exporter endpoint from the environment of one
// test, which is the state a local run has.
func clearEndpoints(t *testing.T) {
	t.Helper()
	for _, key := range []string{envDisabled, envEndpoint, envTracesEndpoint, envMetricEndpoint, envLogsEndpoint} {
		t.Setenv(key, "")
	}
}

// testTracerProvider builds a provider with the sampling policy this package
// installs and a recorder in place of an exporter.
func testTracerProvider(ratio float64, slow time.Duration) (*sdktrace.TracerProvider, *tracetest.SpanRecorder) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(newSampler(ratio)),
		sdktrace.WithSpanProcessor(retainProcessor{next: recorder, slowRequest: slow}),
	)
	return provider, recorder
}

// installGlobals points the global providers at the given ones for the
// duration of one test.
func installGlobals(t *testing.T, tp trace.TracerProvider, mp metric.MeterProvider) {
	t.Helper()
	prevTracer := otel.GetTracerProvider()
	prevMeter := otel.GetMeterProvider()
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTracer)
		otel.SetMeterProvider(prevMeter)
	})
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
}

// collectingErrorHandler captures the errors the SDK reports internally.
type collectingErrorHandler struct {
	mu   sync.Mutex
	errs []error
}

func (h *collectingErrorHandler) Handle(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.errs = append(h.errs, err)
}

func (h *collectingErrorHandler) collected() []error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]error(nil), h.errs...)
}

// installErrorHandler routes SDK errors to a collector for one test.
func installErrorHandler(t *testing.T) *collectingErrorHandler {
	t.Helper()
	handler := &collectingErrorHandler{}
	prev := otel.GetErrorHandler()
	t.Cleanup(func() { otel.SetErrorHandler(prev) })
	otel.SetErrorHandler(handler)
	return handler
}

// TestSetupWithoutEndpointIsSilent covers the local run: no collector is
// configured, the service still starts, serves, and stops, and nothing in the
// telemetry stack reports a failure.
func TestSetupWithoutEndpointIsSilent(t *testing.T) {
	clearEndpoints(t)
	handler := installErrorHandler(t)

	var out strings.Builder
	ctx := context.Background()
	shutdown, err := Setup(ctx, Options{
		ServiceName: "widget",
		Environment: "local",
		SampleRatio: 1,
		LogOutput:   &out,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Setup returned a nil shutdown function")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /widgets/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(spanMiddleware(mux)(mux))
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/widgets/42")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close body: %v", err)
	}

	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if errs := handler.collected(); len(errs) != 0 {
		t.Fatalf("telemetry reported %d errors with no endpoint configured: %v", len(errs), errs)
	}
}

// TestSetupWithoutEndpointUsesNoopProviders proves the fallback is a no-op
// provider and not an exporter aimed at a default address: a span started
// through the installed provider is not recording.
func TestSetupWithoutEndpointUsesNoopProviders(t *testing.T) {
	clearEndpoints(t)
	restoreGlobals(t)

	var out strings.Builder
	shutdown, err := Setup(context.Background(), Options{ServiceName: "widget", LogOutput: &out})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	defer func() {
		if err := shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	}()

	_, span := otel.GetTracerProvider().Tracer("test").Start(context.Background(), "probe")
	defer span.End()
	if span.IsRecording() {
		t.Error("span is recording with no endpoint configured; the provider is not a no-op")
	}
	if span.SpanContext().IsValid() {
		t.Error("no-op provider produced a valid span context")
	}
}

// restoreGlobals returns the global providers to their previous values after a
// test that calls Setup.
func restoreGlobals(t *testing.T) {
	t.Helper()
	prevTracer := otel.GetTracerProvider()
	prevMeter := otel.GetMeterProvider()
	prevPropagator := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTracer)
		otel.SetMeterProvider(prevMeter)
		otel.SetTextMapPropagator(prevPropagator)
	})
}

// TestSetupRequiresServiceName keeps an unnamed service out of the telemetry
// backend, where a resource without a service name cannot be attributed.
func TestSetupRequiresServiceName(t *testing.T) {
	clearEndpoints(t)
	if _, err := Setup(context.Background(), Options{}); err == nil {
		t.Fatal("Setup accepted an empty service name")
	}
}

// TestSetupExportsThroughTheConfiguredEndpoint covers the wiring end to end:
// with an endpoint configured, a span reaches the collector, and shutdown is
// what flushes it.
func TestSetupExportsThroughTheConfiguredEndpoint(t *testing.T) {
	clearEndpoints(t)
	restoreGlobals(t)

	var mu sync.Mutex
	received := map[string]int{}
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		received[r.URL.Path]++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	var out strings.Builder
	ctx := context.Background()
	// The endpoint is passed as an option rather than through the
	// environment, which is the path a value from a flag or a mounted secret
	// file takes.
	shutdown, err := Setup(ctx, Options{
		ServiceName:  "widget",
		Environment:  "staging",
		SampleRatio:  1,
		OTLPEndpoint: collector.URL,
		OTLPHeaders:  map[string]string{"x-tenant": "widgets"},
		LogOutput:    &out,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	_, span := otel.GetTracerProvider().Tracer("test").Start(ctx, "probe")
	if !span.IsRecording() {
		t.Error("span is not recording with an endpoint configured")
	}
	span.End()

	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if received["/v1/traces"] == 0 {
		t.Errorf("collector received no trace export; paths seen: %v", received)
	}
}

// TestExportEnabled covers the rule that decides between an exporter and a
// no-op provider.
func TestExportEnabled(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		options  Options
		signal   string
		expected bool
	}{
		{name: "nothing configured", signal: envTracesEndpoint},
		{
			name:     "endpoint passed as an option",
			options:  Options{OTLPEndpoint: "http://collector:4318"},
			signal:   envTracesEndpoint,
			expected: true,
		},
		{
			name:    "disabled overrides the option",
			env:     map[string]string{envDisabled: "true"},
			options: Options{OTLPEndpoint: "http://collector:4318"},
			signal:  envTracesEndpoint,
		},
		{
			name:     "general endpoint",
			env:      map[string]string{envEndpoint: "http://collector:4318"},
			signal:   envTracesEndpoint,
			expected: true,
		},
		{
			name:     "signal endpoint only",
			env:      map[string]string{envLogsEndpoint: "http://collector:4318/v1/logs"},
			signal:   envLogsEndpoint,
			expected: true,
		},
		{
			name:   "another signal endpoint",
			env:    map[string]string{envLogsEndpoint: "http://collector:4318/v1/logs"},
			signal: envTracesEndpoint,
		},
		{
			name: "disabled overrides the endpoint",
			env: map[string]string{
				envDisabled: "true",
				envEndpoint: "http://collector:4318",
			},
			signal: envTracesEndpoint,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearEndpoints(t)
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			if got := exportEnabled(test.options, test.signal); got != test.expected {
				t.Errorf("exportEnabled(%s) = %t, want %t", test.signal, got, test.expected)
			}
		})
	}
}

// TestSignalEndpointJoinsTheSignalPath covers the difference between the two
// ways an endpoint arrives: the environment variable appends the signal path
// and the exporter option does not.
func TestSignalEndpointJoinsTheSignalPath(t *testing.T) {
	tests := []struct {
		base     string
		expected string
		ok       bool
	}{
		{base: "http://collector:4318", expected: "http://collector:4318/v1/traces", ok: true},
		{base: "http://collector:4318/", expected: "http://collector:4318/v1/traces", ok: true},
		{base: "https://collector.example/otlp", expected: "https://collector.example/otlp/v1/traces", ok: true},
		{base: ""},
		{base: "://not a url"},
	}
	for _, test := range tests {
		got, ok := signalEndpoint(test.base, tracesPath)
		if ok != test.ok {
			t.Errorf("signalEndpoint(%q) usable = %t, want %t", test.base, ok, test.ok)
			continue
		}
		if got != test.expected {
			t.Errorf("signalEndpoint(%q) = %q, want %q", test.base, got, test.expected)
		}
	}
}

// TestParseOTLPHeaders covers the header list a collector credential arrives
// in, including the percent encoding the specification requires.
func TestParseOTLPHeaders(t *testing.T) {
	headers, err := ParseOTLPHeaders(" authorization=Bearer%20token , x-tenant=widgets ")
	if err != nil {
		t.Fatalf("ParseOTLPHeaders: %v", err)
	}
	if headers["authorization"] != "Bearer token" {
		t.Errorf("authorization = %q, want the decoded value", headers["authorization"])
	}
	if headers["x-tenant"] != "widgets" {
		t.Errorf("x-tenant = %q, want widgets", headers["x-tenant"])
	}

	empty, err := ParseOTLPHeaders("")
	if err != nil || empty != nil {
		t.Errorf("ParseOTLPHeaders(\"\") = %v, %v, want no headers and no error", empty, err)
	}

	if _, err := ParseOTLPHeaders("authorization"); err == nil {
		t.Error("ParseOTLPHeaders accepted a value that is not a key=value pair")
	}
}

// TestDefaultSampleRatio holds the policy in place: a developer sees every
// trace, and a deployment keeps a fraction plus the failures.
func TestDefaultSampleRatio(t *testing.T) {
	if got := DefaultSampleRatio("development"); got != 1 {
		t.Errorf("DefaultSampleRatio(development) = %v, want 1", got)
	}
	if got := DefaultSampleRatio("production"); got != productionSampleRatio {
		t.Errorf("DefaultSampleRatio(production) = %v, want %v", got, productionSampleRatio)
	}
}

// TestOptionsDefaults covers the fields a caller may leave unset.
func TestOptionsDefaults(t *testing.T) {
	local := Options{Environment: "local"}.withDefaults()
	if local.LogFormat != LogFormatText {
		t.Errorf("local log format = %q, want %q", local.LogFormat, LogFormatText)
	}
	if local.SlowRequest != DefaultSlowRequest {
		t.Errorf("slow request threshold = %v, want %v", local.SlowRequest, DefaultSlowRequest)
	}
	if local.LogOutput == nil {
		t.Error("log output was left nil")
	}

	deployed := Options{Environment: "production"}.withDefaults()
	if deployed.LogFormat != LogFormatJSON {
		t.Errorf("production log format = %q, want %q", deployed.LogFormat, LogFormatJSON)
	}
}

// TestShutdownAllRunsInReverseAndJoinsErrors covers the release order and the
// rule that one failing provider does not hide the others.
func TestShutdownAllRunsInReverseAndJoinsErrors(t *testing.T) {
	var order []int
	first := errors.New("first")
	second := errors.New("second")

	stops := []shutdownFunc{
		func(context.Context) error { order = append(order, 0); return first },
		nil,
		func(context.Context) error { order = append(order, 2); return second },
	}

	err := shutdownAll(context.Background(), stops)
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("shutdownAll error = %v, want both provider errors", err)
	}
	if len(order) != 2 || order[0] != 2 || order[1] != 0 {
		t.Fatalf("shutdown order = %v, want [2 0]", order)
	}
}

// TestObserveDependencyCheckRecordsOutcome covers the metric a readiness probe
// leaves behind, which is what shows how long a dependency was unreachable.
func TestObserveDependencyCheckRecordsOutcome(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	installGlobals(t, otel.GetTracerProvider(), provider)

	ctx := context.Background()
	ObserveDependencyCheck(ctx, "database", nil, 3*time.Millisecond)
	ObserveDependencyCheck(ctx, "database", errors.New("unreachable"), 12*time.Millisecond)

	points := histogramPoints(t, reader, "dependency.check.duration")
	if len(points) != 2 {
		t.Fatalf("dependency check series = %d, want 2", len(points))
	}

	outcomes := map[string]uint64{}
	for _, point := range points {
		value, ok := point.Attributes.Value(dependencyOutcomeKey)
		if !ok {
			t.Fatalf("series has no %s attribute", dependencyOutcomeKey)
		}
		outcomes[value.AsString()] = point.Count
	}
	if outcomes[outcomeOK] != 1 || outcomes[outcomeError] != 1 {
		t.Fatalf("outcome counts = %v, want one of each", outcomes)
	}
}
