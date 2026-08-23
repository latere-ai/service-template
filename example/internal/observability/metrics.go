package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// scopeName is the instrumentation scope every signal this package emits is
// attributed to. It is the import path of the package, which is what the
// convention asks for and what makes the origin of a record unambiguous.
const scopeName = "github.com/example/reference-service/internal/observability"

// Metric attribute keys for a dependency check.
const (
	dependencyNameKey    = attribute.Key("dependency.name")
	dependencyOutcomeKey = attribute.Key("dependency.outcome")
)

// Outcome values for a dependency check.
const (
	outcomeOK    = "ok"
	outcomeError = "error"
)

// durationBuckets are the histogram boundaries for a duration in seconds. They
// are dense below one second, where a healthy dependency answers, and sparse
// above it, where only the order of magnitude matters.
var durationBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10,
}

// ObserveDependencyCheck records the outcome and the duration of one dependency
// check, which is how a readiness probe becomes a time series: the graph shows
// which dependency was unreachable and for how long, after the probe result
// itself is gone.
func ObserveDependencyCheck(ctx context.Context, name string, err error, d time.Duration) {
	meter := otel.GetMeterProvider().Meter(scopeName)
	hist, herr := meter.Float64Histogram(
		"dependency.check.duration",
		metric.WithDescription("Duration of a dependency readiness check."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(durationBuckets...),
	)
	if herr != nil {
		otel.Handle(fmt.Errorf("observability: dependency check instrument: %w", herr))
		return
	}

	outcome := outcomeOK
	if err != nil {
		outcome = outcomeError
	}
	hist.Record(ctx, d.Seconds(), metric.WithAttributes(
		dependencyNameKey.String(name),
		dependencyOutcomeKey.String(outcome),
	))
}
