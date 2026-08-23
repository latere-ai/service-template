package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// newSampler returns the head sampling policy: parent-based, so a trace is
// kept or dropped as a whole, with a ratio applied at the root.
//
// The result is wrapped in AlwaysRecord, which turns a drop into a record-only
// decision. A record-only span is built and handed to the span processors but
// carries an unsampled trace flag, so retainProcessor can still export the
// spans that describe a failure. Nothing else observes the difference.
func newSampler(ratio float64) sdktrace.Sampler {
	var root sdktrace.Sampler
	switch {
	case ratio >= 1:
		root = sdktrace.AlwaysSample()
	case ratio <= 0:
		root = sdktrace.NeverSample()
	default:
		root = sdktrace.TraceIDRatioBased(ratio)
	}
	return sdktrace.AlwaysRecord(sdktrace.ParentBased(root))
}

// retainProcessor forwards a finished span to the next processor when the head
// sampler kept it, and also when the span failed or ran longer than the slow
// threshold.
//
// It makes the ratio a floor rather than a ceiling. A ratio exists to bound
// export volume in production, but the traces worth keeping are the ones that
// went wrong, and a head sampler cannot know that at the moment it decides.
type retainProcessor struct {
	// next receives the spans this processor keeps.
	next sdktrace.SpanProcessor
	// slowRequest is the duration at or above which an unsampled span is kept.
	// Zero disables the duration rule.
	slowRequest time.Duration
}

func (p retainProcessor) OnStart(parent context.Context, s sdktrace.ReadWriteSpan) {
	p.next.OnStart(parent, s)
}

func (p retainProcessor) OnEnd(s sdktrace.ReadOnlySpan) {
	if !p.retain(s) {
		return
	}
	p.next.OnEnd(s)
}

func (p retainProcessor) Shutdown(ctx context.Context) error { return p.next.Shutdown(ctx) }

func (p retainProcessor) ForceFlush(ctx context.Context) error { return p.next.ForceFlush(ctx) }

// retain reports whether a finished span is exported.
func (p retainProcessor) retain(s sdktrace.ReadOnlySpan) bool {
	if s.SpanContext().IsSampled() {
		return true
	}
	if s.Status().Code == codes.Error {
		return true
	}
	return p.slowRequest > 0 && s.EndTime().Sub(s.StartTime()) >= p.slowRequest
}
