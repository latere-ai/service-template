package worker

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ScopeName identifies this package as the instrumentation scope of the spans
// and metrics it produces.
const ScopeName = "example.com/service/internal/worker"

// Metric and span attribute keys. Work with no request behind it carries no
// route and no status code, so the job name is the dimension every signal is
// read along.
const (
	jobNameKey    = attribute.Key("worker.job.name")
	jobAttemptKey = attribute.Key("worker.job.attempt")
	triggerKey    = attribute.Key("worker.trigger")
	resultKey     = attribute.Key("worker.result")
)

// durationBuckets are the histogram boundaries in seconds. Background work runs
// longer than a request, so the range extends past a minute.
var durationBuckets = []float64{
	0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30, 60, 300, 900,
}

// instruments holds the work metrics.
//
// Duration carries a count, so the execution rate and the failure rate come
// from the histogram split by result. Time since last success is separate
// because it is the only signal that catches a scheduled job which stopped
// firing: no error rate rises when nothing runs at all.
type instruments struct {
	duration     metric.Float64Histogram
	registration metric.Registration
}

// newInstruments creates the work instruments on the global meter provider.
//
// observe reports the seconds since the last success per job name and is called
// on every metric collection. An instrument the provider refuses is reported
// through the configured error handler and dropped, because a runner that
// cannot build a histogram still has work to do.
func newInstruments(observe func() map[string]float64) *instruments {
	meter := otel.Meter(ScopeName)
	in := &instruments{}

	duration, err := meter.Float64Histogram("worker.job.execution.duration",
		metric.WithDescription("Duration of one background job execution attempt."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(durationBuckets...))
	if err != nil {
		otel.Handle(err)
	} else {
		in.duration = duration
	}

	since, err := meter.Float64ObservableGauge("worker.job.time_since_last_success",
		metric.WithDescription("Seconds since a background job last completed successfully."),
		metric.WithUnit("s"))
	if err != nil {
		otel.Handle(err)
		return in
	}

	reg, err := meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		for name, seconds := range observe() {
			o.ObserveFloat64(since, seconds, metric.WithAttributes(jobNameKey.String(name)))
		}
		return nil
	}, since)
	if err != nil {
		otel.Handle(err)
		return in
	}
	in.registration = reg
	return in
}

// record writes one execution attempt to the duration histogram.
func (in *instruments) record(ctx context.Context, ex execution) {
	if in.duration == nil {
		return
	}
	in.duration.Record(ctx, ex.duration.Seconds(), metric.WithAttributes(
		jobNameKey.String(ex.job),
		triggerKey.String(ex.trigger),
		resultKey.String(ex.result),
	))
}

// close unregisters the observable callback, so a runner that is done stops
// being polled by the meter provider.
func (in *instruments) close() error {
	if in.registration == nil {
		return nil
	}
	return in.registration.Unregister()
}
