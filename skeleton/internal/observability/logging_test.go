package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// recordingProcessor keeps every log record the bridge exports.
type recordingProcessor struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (p *recordingProcessor) OnEmit(_ context.Context, record *sdklog.Record) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.records = append(p.records, record.Clone())
	return nil
}

func (p *recordingProcessor) Enabled(context.Context, sdklog.EnabledParameters) bool { return true }
func (p *recordingProcessor) Shutdown(context.Context) error                         { return nil }
func (p *recordingProcessor) ForceFlush(context.Context) error                       { return nil }

func (p *recordingProcessor) emitted() []sdklog.Record {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]sdklog.Record(nil), p.records...)
}

// newTestLogger builds the service logger over a buffer and a recording log
// provider, which is the pair of destinations a real record reaches.
func newTestLogger(t *testing.T, format LogFormat) (*slog.Logger, *bytes.Buffer, *recordingProcessor) {
	t.Helper()
	processor := &recordingProcessor{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(processor))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("logger provider shutdown: %v", err)
		}
	})
	buf := &bytes.Buffer{}
	return newLogger(buf, slog.LevelInfo, format, provider), buf, processor
}

// TestLogRecordInsideARequestCarriesTheTraceIdentifiers is the correlation
// this package exists to guarantee. A line without these fields cannot be
// joined to the request that produced it, and the absence is invisible until
// an incident needs the join.
func TestLogRecordInsideARequestCarriesTheTraceIdentifiers(t *testing.T) {
	logger, buf, processor := newTestLogger(t, LogFormatJSON)

	provider, _ := testTracerProvider(1, DefaultSlowRequest)
	defer func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("tracer provider shutdown: %v", err)
		}
	}()
	ctx, span := provider.Tracer("test").Start(context.Background(), "GET /widgets/{id}")
	defer span.End()

	logger.InfoContext(ctx, "handled request", "route", widgetRoute)

	expectedTrace := span.SpanContext().TraceID().String()
	expectedSpan := span.SpanContext().SpanID().String()

	var line map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line); err != nil {
		t.Fatalf("parse the local log line %q: %v", buf.String(), err)
	}
	if line[traceIDKey] != expectedTrace {
		t.Errorf("local %s = %v, want %s", traceIDKey, line[traceIDKey], expectedTrace)
	}
	if line[spanIDKey] != expectedSpan {
		t.Errorf("local %s = %v, want %s", spanIDKey, line[spanIDKey], expectedSpan)
	}

	emitted := processor.emitted()
	if len(emitted) != 1 {
		t.Fatalf("the bridge exported %d records, want 1", len(emitted))
	}
	if got := emitted[0].TraceID().String(); got != expectedTrace {
		t.Errorf("exported trace id = %s, want %s", got, expectedTrace)
	}
	if got := emitted[0].SpanID().String(); got != expectedSpan {
		t.Errorf("exported span id = %s, want %s", got, expectedSpan)
	}
}

// TestLogRecordOutsideARequestHasNoIdentifiers covers start-up and background
// logging, where there is no trace to name and an invalid identifier would be
// worse than none.
func TestLogRecordOutsideARequestHasNoIdentifiers(t *testing.T) {
	logger, buf, processor := newTestLogger(t, LogFormatJSON)

	logger.InfoContext(context.Background(), "starting")

	var line map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line); err != nil {
		t.Fatalf("parse the local log line %q: %v", buf.String(), err)
	}
	if _, ok := line[traceIDKey]; ok {
		t.Errorf("record outside a request carries %s", traceIDKey)
	}
	if _, ok := line[spanIDKey]; ok {
		t.Errorf("record outside a request carries %s", spanIDKey)
	}

	emitted := processor.emitted()
	if len(emitted) != 1 {
		t.Fatalf("the bridge exported %d records, want 1", len(emitted))
	}
	if emitted[0].TraceID().IsValid() {
		t.Error("exported record carries a trace id with no trace in scope")
	}
}

// TestTextFormatCarriesTheIdentifiers covers the local encoding, where the
// correlation has to survive a different handler.
func TestTextFormatCarriesTheIdentifiers(t *testing.T) {
	logger, buf, _ := newTestLogger(t, LogFormatText)

	provider, _ := testTracerProvider(1, DefaultSlowRequest)
	defer func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("tracer provider shutdown: %v", err)
		}
	}()
	ctx, span := provider.Tracer("test").Start(context.Background(), "GET /widgets/{id}")
	defer span.End()

	logger.InfoContext(ctx, "handled request")

	expected := traceIDKey + "=" + span.SpanContext().TraceID().String()
	if !strings.Contains(buf.String(), expected) {
		t.Errorf("text log line %q does not carry %q", buf.String(), expected)
	}
}

// TestLoggerAttributesReachBothDestinations covers the handler wrappers, where
// a lost WithAttrs would silently drop context from one of the two streams.
func TestLoggerAttributesReachBothDestinations(t *testing.T) {
	logger, buf, processor := newTestLogger(t, LogFormatJSON)

	logger.With("component", "worker").InfoContext(context.Background(), "tick")

	if !strings.Contains(buf.String(), `"component":"worker"`) {
		t.Errorf("local line %q lost the logger attribute", buf.String())
	}

	emitted := processor.emitted()
	if len(emitted) != 1 {
		t.Fatalf("the bridge exported %d records, want 1", len(emitted))
	}
	var found bool
	emitted[0].WalkAttributes(func(kv attribute.KeyValue) bool {
		if string(kv.Key) == "component" && kv.Value.AsString() == "worker" {
			found = true
			return false
		}
		return true
	})
	if !found {
		t.Error("exported record lost the logger attribute")
	}
}

// TestLoggerLevelIsEnforced keeps a debug record out of both destinations.
func TestLoggerLevelIsEnforced(t *testing.T) {
	logger, buf, processor := newTestLogger(t, LogFormatJSON)

	logger.DebugContext(context.Background(), "verbose")

	if buf.Len() != 0 {
		t.Errorf("local stream wrote a record below the level: %q", buf.String())
	}
	if emitted := processor.emitted(); len(emitted) != 0 {
		t.Errorf("the bridge exported %d records below the level", len(emitted))
	}
}

// TestFanoutHandlerReportsEveryFailure keeps one broken destination from
// hiding another.
func TestFanoutHandlerReportsEveryFailure(t *testing.T) {
	first := errors.New("first destination")
	second := errors.New("second destination")
	handler := fanoutHandler{handlers: []slog.Handler{
		failingHandler{err: first},
		failingHandler{err: second},
	}}

	err := handler.Handle(context.Background(), slog.Record{Level: slog.LevelInfo})
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("Handle error = %v, want both destination errors", err)
	}
}

// TestFanoutHandlerGroupsReachEveryDestination covers WithGroup, which the
// bridge and the local handler each implement differently.
func TestFanoutHandlerGroupsReachEveryDestination(t *testing.T) {
	logger, buf, processor := newTestLogger(t, LogFormatJSON)

	logger.WithGroup("request").InfoContext(context.Background(), "done", "route", widgetRoute)

	if !strings.Contains(buf.String(), `"request":{"route":"`+widgetRoute+`"}`) {
		t.Errorf("local line %q lost the group", buf.String())
	}
	if emitted := processor.emitted(); len(emitted) != 1 {
		t.Fatalf("the bridge exported %d records, want 1", len(emitted))
	}
}

// failingHandler always reports an error, so the fanout error path is
// reachable in a test.
type failingHandler struct {
	err error
}

func (h failingHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (h failingHandler) Handle(context.Context, slog.Record) error { return h.err }
func (h failingHandler) WithAttrs([]slog.Attr) slog.Handler        { return h }
func (h failingHandler) WithGroup(string) slog.Handler             { return h }
