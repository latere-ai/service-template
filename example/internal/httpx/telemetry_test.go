package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// itemsRouter is a router with one parameterized route, which is the case a
// telemetry label set is most easily broken by.
func itemsRouter(t *testing.T) *http.ServeMux {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/items/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

// collectSpans routes spans into memory for the duration of the test.
func collectSpans(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()

	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shut down the tracer provider: %v", err)
		}
	})
	return exporter
}

// collectMetrics routes metrics into a manual reader for the duration of the
// test.
func collectMetrics(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() {
		otel.SetMeterProvider(previous)
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shut down the meter provider: %v", err)
		}
	})
	return reader
}

func TestServerSpanIsNamedByTheRoutePattern(t *testing.T) {
	captureDefaultLogger(t)
	spans := collectSpans(t)

	mux := itemsRouter(t)
	h := Handler(mux, Options{Router: mux})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/items/42", nil))

	recorded := spans.GetSpans()
	if len(recorded) != 1 {
		t.Fatalf("recorded %d spans, want one server span", len(recorded))
	}
	if want := "GET /v1/items/{id}"; recorded[0].Name != want {
		t.Errorf("span name = %q, want %q", recorded[0].Name, want)
	}

	var route string
	for _, a := range recorded[0].Attributes {
		if a.Key == "http.route" {
			route = a.Value.AsString()
		}
	}
	if want := "/v1/items/{id}"; route != want {
		t.Errorf("http.route = %q, want %q", route, want)
	}
}

func TestServerSpanMarksOnlyAServerFaultAsAnError(t *testing.T) {
	captureDefaultLogger(t)

	cases := map[string]struct {
		status  int
		isError bool
	}{
		"not found":   {http.StatusNotFound, false},
		"bad gateway": {http.StatusBadGateway, true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			spans := collectSpans(t)
			h := Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				WriteError(w, r, New(tc.status, "for the test"))
			}), Options{})
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/items", nil))

			recorded := spans.GetSpans()
			if len(recorded) != 1 {
				t.Fatalf("recorded %d spans, want one", len(recorded))
			}
			gotError := recorded[0].Status.Code.String() == "Error"
			if gotError != tc.isError {
				t.Fatalf("span error = %v, want %v for status %d", gotError, tc.isError, tc.status)
			}
		})
	}
}

func TestMetricsLabelOneSeriesPerRouteNotPerIdentifier(t *testing.T) {
	captureDefaultLogger(t)
	reader := collectMetrics(t)

	mux := itemsRouter(t)
	h := Handler(mux, Options{Router: mux})
	for _, path := range []string{"/v1/items/1", "/v1/items/2", "/v1/items/3"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	points := histogramPoints(t, reader, "http.server.request.duration")
	if len(points) != 1 {
		t.Fatalf("recorded %d series, want one for the parameterized route", len(points))
	}
	if got, ok := points[0].Attributes.Value(attribute.Key("http.route")); !ok || got.AsString() != "/v1/items/{id}" {
		t.Errorf("http.route = %v, want the pattern", got.AsString())
	}
	if points[0].Count != 3 {
		t.Errorf("count = %d, want the three requests in one series", points[0].Count)
	}
}

func TestMetricsLabelAnUnmatchedPathWithTheFixedLabel(t *testing.T) {
	captureDefaultLogger(t)
	reader := collectMetrics(t)

	mux := itemsRouter(t)
	h := Handler(mux, Options{Router: mux})
	for _, path := range []string{"/wp-login.php", "/.env", "/admin"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	points := histogramPoints(t, reader, "http.server.request.duration")
	if len(points) != 1 {
		t.Fatalf("recorded %d series, want one for every unmatched path", len(points))
	}
	if got, ok := points[0].Attributes.Value(attribute.Key("http.route")); !ok || got.AsString() != unmatchedRoute {
		t.Errorf("http.route = %v, want %q", got.AsString(), unmatchedRoute)
	}
}

// histogramPoints collects the data points of one histogram instrument.
func histogramPoints(t *testing.T, reader *sdkmetric.ManualReader, name string) []metricdata.HistogramDataPoint[float64] {
	t.Helper()

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != name {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("%s is %T, want a float histogram", name, m.Data)
			}
			return hist.DataPoints
		}
	}
	t.Fatalf("no instrument named %s was recorded", name)
	return nil
}

// sumPoints returns the series of one counter instrument.
func sumPoints(t *testing.T, reader *sdkmetric.ManualReader, name string) []metricdata.DataPoint[int64] {
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
			data, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("metric %q is %T, want an int64 sum", name, m.Data)
			}
			return data.DataPoints
		}
	}
	t.Fatalf("metric %q was not recorded", name)
	return nil
}

// TestActiveRequestsRisesAndFalls covers the in-flight counter: it reads one
// while the handler runs and zero once it returns. A counter that only ever
// rises reports saturation that is not there.
func TestActiveRequestsRisesAndFalls(t *testing.T) {
	reader := collectMetrics(t)

	var during []metricdata.DataPoint[int64]
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/items/{id}", func(w http.ResponseWriter, _ *http.Request) {
		during = sumPoints(t, reader, "http.server.active_requests")
		w.WriteHeader(http.StatusOK)
	})
	handler := Wrap(mux, []Stage{
		{Name: StageTraceSpan, Wrap: ServerSpan(mux)},
		{Name: StageMetrics, Wrap: Metrics()},
	})

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/items/42", nil))

	if len(during) != 1 || during[0].Value != 1 {
		t.Fatalf("in flight during the request = %v, want a single series holding 1", during)
	}
	after := sumPoints(t, reader, "http.server.active_requests")
	if len(after) != 1 || after[0].Value != 0 {
		t.Fatalf("in flight after the request = %v, want a single series holding 0", after)
	}
}

// TestRemoteParentContinuesTheTrace covers propagation: a request arriving with
// a trace context joins that trace instead of starting a second one, which is
// what makes a request traceable across services.
func TestRemoteParentContinuesTheTrace(t *testing.T) {
	previous := otel.GetTextMapPropagator()
	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })
	otel.SetTextMapPropagator(propagation.TraceContext{})

	exporter := collectSpans(t)
	mux := itemsRouter(t)
	handler := ServerSpan(mux)(mux)

	req := httptest.NewRequest(http.MethodGet, "/v1/items/42", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("the chain recorded %d spans, want exactly one server span", len(spans))
	}
	if got := spans[0].SpanContext.TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("trace id = %q, want the one the client sent", got)
	}
	if got := spans[0].Parent.SpanID().String(); got != "00f067aa0ba902b7" {
		t.Errorf("parent span id = %q, want the one the client sent", got)
	}
}

// TestTheAssembledChainProducesOneServerSpanAndOneSeries covers the property
// that no single stage can: the whole chain a service installs emits exactly
// one server span and one series per instrument for one request.
//
// Two request middlewares under different instrumentation scopes produce two
// server spans and split one metric name across two attribute sets, and the
// SDK reports neither, because the scopes differ. The chain is therefore
// asserted as a whole rather than stage by stage.
func TestTheAssembledChainProducesOneServerSpanAndOneSeries(t *testing.T) {
	exporter := collectSpans(t)
	reader := collectMetrics(t)

	mux := itemsRouter(t)
	handler := Handler(mux, Options{Router: mux})
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/items/42", nil))

	var server []string
	for _, span := range exporter.GetSpans() {
		if span.SpanKind == trace.SpanKindServer {
			server = append(server, span.Name)
		}
	}
	if len(server) != 1 {
		t.Errorf("the chain produced %d server spans %v, want exactly one", len(server), server)
	}

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	series := map[string]int{}
	scopes := map[string]map[string]bool{}
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			switch data := m.Data.(type) {
			case metricdata.Histogram[float64]:
				series[m.Name] += len(data.DataPoints)
			case metricdata.Sum[int64]:
				series[m.Name] += len(data.DataPoints)
			}
			if scopes[m.Name] == nil {
				scopes[m.Name] = map[string]bool{}
			}
			scopes[m.Name][scope.Scope.Name] = true
		}
	}
	for _, name := range []string{"http.server.request.duration", "http.server.active_requests"} {
		if series[name] != 1 {
			t.Errorf("%s carries %d series after one request, want exactly one", name, series[name])
		}
		if len(scopes[name]) != 1 {
			t.Errorf("%s is emitted by %d instrumentation scopes %v, want one owner",
				name, len(scopes[name]), scopes[name])
		}
	}
}
