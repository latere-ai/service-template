package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// TestErrorResponseIsSampledAtZeroRatio is the rule that makes a low ratio
// safe to deploy: the ratio bounds the volume of healthy traffic, and the
// requests that failed are kept whatever the ratio says.
func TestErrorResponseIsSampledAtZeroRatio(t *testing.T) {
	handler, recorder, _ := newTestStack(t, 0, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	if got := get(t, handler, "/widgets/42").Code; got != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", got)
	}

	span := spanNamed(t, recorder, "GET "+widgetRoute)
	if span.Status().Code != codes.Error {
		t.Fatalf("span status = %v, want error", span.Status().Code)
	}
	// The span was exported although the head sampler did not select it, which
	// is what distinguishes the override from a ratio that quietly kept it.
	if span.SpanContext().IsSampled() {
		t.Error("the head sampler selected the span, so the error rule was not exercised")
	}
}

// TestHealthyResponseIsDroppedAtZeroRatio is the other half of the rule: the
// override keeps the failures without keeping everything.
func TestHealthyResponseIsDroppedAtZeroRatio(t *testing.T) {
	handler, recorder, _ := newTestStack(t, 0, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	if got := get(t, handler, "/widgets/42").Code; got != http.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
	if ended := recorder.Ended(); len(ended) != 0 {
		t.Fatalf("exported %d spans at ratio zero, want none: %v", len(ended), spanNames(recorder))
	}
}

// TestSlowSpanIsRetainedAtZeroRatio covers the duration half of the override.
// A request that took too long is the other trace worth keeping.
func TestSlowSpanIsRetainedAtZeroRatio(t *testing.T) {
	const threshold = 10 * time.Millisecond
	provider, recorder := testTracerProvider(0, threshold)
	defer func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	}()
	tracer := provider.Tracer("test")

	_, quick := tracer.Start(context.Background(), "quick")
	quick.End()

	_, slow := tracer.Start(context.Background(), "slow")
	time.Sleep(2 * threshold)
	slow.End()

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("exported %d spans, want only the slow one: %v", len(ended), spanNames(recorder))
	}
	if ended[0].Name() != "slow" {
		t.Errorf("exported span = %q, want the slow one", ended[0].Name())
	}
}

// TestFullRatioSamplesEverything covers the development default, where a
// developer expects to find the trace of the request just made.
func TestFullRatioSamplesEverything(t *testing.T) {
	provider, recorder := testTracerProvider(1, DefaultSlowRequest)
	defer func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	}()

	_, span := provider.Tracer("test").Start(context.Background(), "healthy")
	span.End()

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("exported %d spans, want 1", len(ended))
	}
	if !ended[0].SpanContext().IsSampled() {
		t.Error("span is not marked sampled at full ratio")
	}
}

// TestRemoteSampledParentIsHonoured covers the parent-based half of the
// policy: a trace another service decided to keep is not truncated here.
func TestRemoteSampledParentIsHonoured(t *testing.T) {
	provider, recorder := testTracerProvider(0, DefaultSlowRequest)
	defer func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	}()

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatalf("trace id: %v", err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatalf("span id: %v", err)
	}
	parent := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	}))

	_, span := provider.Tracer("test").Start(parent, "continued")
	span.End()

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("exported %d spans, want the continued trace", len(ended))
	}
	if !ended[0].SpanContext().IsSampled() {
		t.Error("a span continuing a sampled trace was not marked sampled")
	}
}

// TestSamplerDescriptions pin the policy in a form an operator can read off a
// running process.
func TestSamplerDescriptions(t *testing.T) {
	tests := map[float64]string{
		1:   "AlwaysOnSampler",
		0:   "AlwaysOffSampler",
		0.5: "TraceIDRatioBased{0.5}",
	}
	for ratio, root := range tests {
		description := newSampler(ratio).Description()
		if !strings.Contains(description, root) {
			t.Errorf("sampler at ratio %v = %q, want it to build on %q", ratio, description, root)
		}
		if !strings.Contains(description, "AlwaysRecord") {
			t.Errorf("sampler at ratio %v = %q, want the record-only wrapper", ratio, description)
		}
		if !strings.Contains(description, "ParentBased") {
			t.Errorf("sampler at ratio %v = %q, want the parent-based wrapper", ratio, description)
		}
	}
}

// TestRetainProcessorDelegates covers the lifecycle calls the processor passes
// through, which is what makes a flush at shutdown reach the exporter.
func TestRetainProcessorDelegates(t *testing.T) {
	next := &countingProcessor{}
	processor := retainProcessor{next: next, slowRequest: time.Second}

	if err := processor.ForceFlush(context.Background()); err != nil {
		t.Fatalf("force flush: %v", err)
	}
	if err := processor.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if next.flushes != 1 || next.shutdowns != 1 {
		t.Errorf("delegated %d flushes and %d shutdowns, want 1 of each", next.flushes, next.shutdowns)
	}
}

// countingProcessor records the lifecycle calls it receives.
type countingProcessor struct {
	starts    int
	ends      int
	flushes   int
	shutdowns int
}

func (p *countingProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) { p.starts++ }
func (p *countingProcessor) OnEnd(sdktrace.ReadOnlySpan)                     { p.ends++ }
func (p *countingProcessor) Shutdown(context.Context) error                  { p.shutdowns++; return nil }
func (p *countingProcessor) ForceFlush(context.Context) error                { p.flushes++; return nil }

// TestMiddlewareRatioZeroStillServes proves the sampling policy never changes
// what a client receives.
func TestMiddlewareRatioZeroStillServes(t *testing.T) {
	handler, _, _ := newTestStack(t, 0, func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte("widget")); err != nil {
			t.Errorf("write: %v", err)
		}
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/widgets/42", nil))
	if rec.Body.String() != "widget" {
		t.Errorf("body = %q, want the handler output", rec.Body.String())
	}
}
