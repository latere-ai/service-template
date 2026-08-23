package worker

import (
	"context"
	"slices"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// newTelemetry installs recording providers for the duration of one test and
// restores the previous ones after it. The tests that use it do not run in
// parallel, because the providers are process-wide.
func newTelemetry(t *testing.T) (*tracetest.SpanRecorder, *sdkmetric.ManualReader) {
	t.Helper()

	spans := tracetest.NewSpanRecorder()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	reader := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	prevTracer := otel.GetTracerProvider()
	prevMeter := otel.GetMeterProvider()
	otel.SetTracerProvider(tracer)
	otel.SetMeterProvider(meter)

	t.Cleanup(func() {
		otel.SetTracerProvider(prevTracer)
		otel.SetMeterProvider(prevMeter)
		ctx := context.Background()
		if err := tracer.Shutdown(ctx); err != nil {
			t.Errorf("shutting the tracer provider down: %v", err)
		}
		if err := meter.Shutdown(ctx); err != nil {
			t.Errorf("shutting the meter provider down: %v", err)
		}
	})
	return spans, reader
}

// spansNamed reports the ended spans with the given name. The recorder is
// process-wide, so a test filters by the name it produced.
func spansNamed(spans *tracetest.SpanRecorder, name string) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, s := range spans.Ended() {
		if s.Name() == name {
			out = append(out, s)
		}
	}
	return out
}

// attr reports one span attribute.
func attr(s sdktrace.ReadOnlySpan, key attribute.Key) (attribute.Value, bool) {
	for _, kv := range s.Attributes() {
		if kv.Key == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

// gaugeValue reports the time since last success recorded for one job.
func gaugeValue(t *testing.T, reader *sdkmetric.ManualReader, job string) (float64, bool) {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("collecting metrics: %v", err)
	}

	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "worker.job.time_since_last_success" {
				continue
			}
			data, ok := m.Data.(metricdata.Gauge[float64])
			if !ok {
				t.Fatalf("%s data type = %T, want a float64 gauge", m.Name, m.Data)
			}
			for _, point := range data.DataPoints {
				if v, ok := point.Attributes.Value(jobNameKey); ok && v.AsString() == job {
					return point.Value, true
				}
			}
		}
	}
	return 0, false
}

// durationResults reports the result attribute of every duration point of one
// job, so a test reads how an execution was classified.
func durationResults(t *testing.T, reader *sdkmetric.ManualReader, job string) []string {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("collecting metrics: %v", err)
	}

	var out []string
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "worker.job.execution.duration" {
				continue
			}
			data, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("%s data type = %T, want a float64 histogram", m.Name, m.Data)
			}
			for _, point := range data.DataPoints {
				name, ok := point.Attributes.Value(jobNameKey)
				if !ok || name.AsString() != job {
					continue
				}
				result, ok := point.Attributes.Value(resultKey)
				if !ok {
					t.Fatalf("a duration point of %q carries no result attribute", job)
				}
				for range point.Count {
					out = append(out, result.AsString())
				}
			}
		}
	}
	return out
}

func TestEachAttemptOpensItsOwnTrace(t *testing.T) {
	spans, _ := newTelemetry(t)

	r, _ := newTestRunner(t, systemClock{}, nil)
	r.Retry = RetryPolicy{MaxAttempts: 2, Base: time.Millisecond, Max: time.Millisecond}
	if err := r.Command(JobFunc{"traced-backfill", func(context.Context) error {
		return errBoom
	}}); err != nil {
		t.Fatalf("Command: %v", err)
	}

	if err := r.RunOnce(context.Background(), "traced-backfill"); err == nil {
		t.Fatal("RunOnce reported no error for a failing job")
	}

	ended := spansNamed(spans, "job traced-backfill")
	if len(ended) != 2 {
		t.Fatalf("spans = %d, want one per attempt (2)", len(ended))
	}
	for i, s := range ended {
		name, ok := attr(s, jobNameKey)
		if !ok || name.AsString() != "traced-backfill" {
			t.Errorf("span %d carries job name %v, want traced-backfill", i, name.AsString())
		}
		attempt, ok := attr(s, jobAttemptKey)
		if !ok || attempt.AsInt64() != int64(i+1) {
			t.Errorf("span %d carries attempt %v, want %d", i, attempt.AsInt64(), i+1)
		}
		trigger, ok := attr(s, triggerKey)
		if !ok || trigger.AsString() != TriggerManual {
			t.Errorf("span %d carries trigger %v, want %s", i, trigger.AsString(), TriggerManual)
		}
		if s.Status().Code != codes.Error {
			t.Errorf("span %d status = %v, want an error status", i, s.Status().Code)
		}
		if len(s.Events()) == 0 {
			t.Errorf("span %d records no error event", i)
		}
	}
}

func TestASuccessfulExecutionTracesAsOneOkSpan(t *testing.T) {
	spans, reader := newTelemetry(t)

	r, _ := newTestRunner(t, systemClock{}, nil)
	if err := r.Command(JobFunc{"traced-ok", func(context.Context) error { return nil }}); err != nil {
		t.Fatalf("Command: %v", err)
	}
	if err := r.RunOnce(context.Background(), "traced-ok"); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	ended := spansNamed(spans, "job traced-ok")
	if len(ended) != 1 {
		t.Fatalf("spans = %d, want 1", len(ended))
	}
	if got := ended[0].Status().Code; got != codes.Ok {
		t.Errorf("span status = %v, want ok", got)
	}
	if got := durationResults(t, reader, "traced-ok"); len(got) != 1 || got[0] != ResultSuccess {
		t.Errorf("recorded durations = %v, want one success", got)
	}
}

func TestTimeSinceLastSuccessRisesWhenAJobStopsRunning(t *testing.T) {
	_, reader := newTelemetry(t)

	clk := newFakeClock()
	r, _ := newTestRunner(t, clk, nil)
	if err := r.Command(JobFunc{"stalled", func(context.Context) error { return nil }}); err != nil {
		t.Fatalf("Command: %v", err)
	}
	if err := r.Command(JobFunc{"never-ran", func(context.Context) error { return nil }}); err != nil {
		t.Fatalf("Command: %v", err)
	}
	if err := r.RunOnce(context.Background(), "stalled"); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	first, ok := gaugeValue(t, reader, "stalled")
	if !ok {
		t.Fatal("no time since last success recorded for a job that ran")
	}
	if first != 0 {
		t.Errorf("time since last success right after a success = %v, want 0", first)
	}

	// Nothing runs from here. The signal that catches a schedule which stopped
	// firing is this value rising, because no error rate rises when no
	// execution happens at all.
	clk.Advance(90 * time.Second)
	second, ok := gaugeValue(t, reader, "stalled")
	if !ok {
		t.Fatal("the time since last success disappeared")
	}
	if second <= first || second != 90 {
		t.Errorf("time since last success = %v, want 90 seconds after the clock advanced", second)
	}

	// A job that has never succeeded reports the time since it was registered,
	// so a schedule that never fired once alarms the same way.
	if never, ok := gaugeValue(t, reader, "never-ran"); !ok || never != 90 {
		t.Errorf("time since last success for a job that never ran = %v (recorded %v), want 90", never, ok)
	}
}

func TestTerminalFailuresAndSkipsAreRecordedSeparately(t *testing.T) {
	_, reader := newTelemetry(t)

	clk := newFakeClock()
	locker := newTestLocker(clk)
	r, _ := newTestRunner(t, clk, locker)
	r.Retry = RetryPolicy{MaxAttempts: 1}

	if err := r.Schedule(JobFunc{"contested", func(context.Context) error { return errBoom }},
		Schedule{Interval: time.Minute, Lease: 10 * time.Second}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Another replica holds the name, so the first interval is skipped.
	held, err := locker.Acquire(ctx, "contested", 3*time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	errc := runInBackground(t, r, ctx)
	clk.advanceUntil(t, 6*time.Second, func() bool {
		return len(durationResults(t, reader, "contested")) >= 1
	}, "the skipped interval was not recorded")

	if got := durationResults(t, reader, "contested"); got[0] != ResultSkipped {
		t.Fatalf("first recorded result = %v, want %s", got, ResultSkipped)
	}

	if err := held.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}
	clk.advanceUntil(t, 6*time.Second, func() bool {
		return slices.Contains(durationResults(t, reader, "contested"), ResultFailed)
	}, "the terminal failure was not recorded")

	cancel()
	if err := waitErr(t, errc); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
