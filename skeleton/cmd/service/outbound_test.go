package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// recordingTracer installs a trace provider that keeps every span in memory and
// the propagator the template configures at start-up, then restores the globals
// it replaced. The instrumented transport reads both when it is built, so a
// test that wants to observe an outbound call has to install them first.
func recordingTracer(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	priorProvider := otel.GetTracerProvider()
	priorPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	t.Cleanup(func() {
		otel.SetTracerProvider(priorProvider)
		otel.SetTextMapPropagator(priorPropagator)
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown trace provider: %v", err)
		}
	})
	return recorder
}

// TestOutboundClientContinuesTheTrace asserts the behaviour the assembly's
// client exists for: the receiving service is handed the trace context of the
// call, so the two sides join into one trace instead of two.
func TestOutboundClientContinuesTheTrace(t *testing.T) {
	recorder := recordingTracer(t)

	var received http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	client := outboundClient()

	ctx, span := otel.Tracer("test").Start(context.Background(), "caller")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("call upstream: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close body: %v", err)
	}
	span.End()

	carrier := propagation.HeaderCarrier(received)
	remote := trace.SpanContextFromContext(
		otel.GetTextMapPropagator().Extract(context.Background(), carrier))
	if !remote.IsValid() {
		t.Fatalf("the upstream received no usable trace context, headers: %v", received)
	}
	if remote.TraceID() != span.SpanContext().TraceID() {
		t.Errorf("upstream trace id = %s, want %s", remote.TraceID(), span.SpanContext().TraceID())
	}

	var clientSpan sdktrace.ReadOnlySpan
	for _, s := range recorder.Ended() {
		if s.SpanKind() == trace.SpanKindClient {
			clientSpan = s
			break
		}
	}
	if clientSpan == nil {
		t.Fatalf("the call produced no client span")
	}
	if clientSpan.SpanContext().TraceID() != span.SpanContext().TraceID() {
		t.Errorf("client span trace id = %s, want %s",
			clientSpan.SpanContext().TraceID(), span.SpanContext().TraceID())
	}
	if remote.SpanID() != clientSpan.SpanContext().SpanID() {
		t.Errorf("upstream parent span id = %s, want the client span %s",
			remote.SpanID(), clientSpan.SpanContext().SpanID())
	}
}

// TestOutboundClientIsBounded asserts the deadline the assembly sets, because
// the shared client carries none and an unbounded outbound call is what turns
// one unhealthy dependency into an unavailable service.
func TestOutboundClientIsBounded(t *testing.T) {
	if got := outboundClient().Timeout; got != outboundTimeout {
		t.Errorf("outbound timeout = %v, want %v", got, outboundTimeout)
	}
}

// TestAssemblyCarriesTheOutboundClient asserts the seam is filled, so a handler
// that reaches another service finds an instrumented client rather than nil.
func TestAssemblyCarriesTheOutboundClient(t *testing.T) {
	a := newTestAssembly(t)
	if a.client == nil {
		t.Fatal("the assembly carries no outbound client")
	}
	if a.client.Transport == nil {
		t.Error("the outbound client uses the default transport, which is not instrumented")
	}
}
