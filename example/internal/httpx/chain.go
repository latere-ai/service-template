package httpx

import (
	"log/slog"
	"net/http"
	"slices"
	"time"
)

// Stage names in the shared chain. The names exist so the assembled order can
// be asserted, and so a service that inserts a stage of its own says where it
// goes by name rather than by index.
const (
	StageRecover   = "recover"
	StageRequestID = "request_id"
	StageTraceSpan = "trace_span"
	StageAccessLog = "access_log"
	StageMetrics   = "metrics"
	StageTimeout   = "timeout"
	StageBodyLimit = "body_limit"
	StageCORS      = "cors"
	StageAuth      = "auth"
	StageRateLimit = "rate_limit"
)

// order is the chain from outermost to innermost. It is fixed by the template
// because it is a correctness property, not a preference:
//
//   - Recovery is outermost, so a panic in any later stage still becomes a
//     logged 500 rather than a dropped connection.
//   - The request identifier and the server span precede the access log, so
//     every log line and every error body joins to one request and one trace.
//   - The timeout precedes the body limit, so a slow body is bounded in time
//     as well as in size.
//   - CORS precedes authentication, so a preflight, which carries no
//     credentials, is answered instead of rejected.
//   - Rate limiting follows authentication, so a budget can be attached to the
//     caller rather than to the address.
var order = []string{
	StageRecover,
	StageRequestID,
	StageTraceSpan,
	StageAccessLog,
	StageMetrics,
	StageTimeout,
	StageBodyLimit,
	StageCORS,
	StageAuth,
	StageRateLimit,
}

// Order reports the stage names from outermost to innermost.
func Order() []string {
	out := make([]string, len(order))
	copy(out, order)
	return out
}

// DefaultTimeout is the per-request budget a service starts with.
const DefaultTimeout = 30 * time.Second

// Stage is one named middleware in the chain.
type Stage struct {
	Name string
	Wrap func(http.Handler) http.Handler
}

// Options configures the shared chain. The zero value is usable: it produces
// every stage with its default, no cross-origin access, no authentication, and
// no rate limit.
type Options struct {
	// Logger receives the access log. It defaults to the process logger, which
	// the observability setup replaces with the exporting one.
	Logger *slog.Logger
	// Router resolves the route pattern for telemetry labels. Passing the
	// service's http.ServeMux here is what keeps a metric labelled by route
	// instead of by path.
	Router Router
	// Timeout is the per-request budget. Zero selects DefaultTimeout; a
	// negative value disables the stage.
	Timeout time.Duration
	// MaxBodyBytes caps the request body. Zero selects DefaultMaxBodyBytes; a
	// negative value disables the cap.
	MaxBodyBytes int64
	// CORS enables cross-origin access. A nil value serves same-origin clients
	// only.
	CORS *CORSOptions
	// RateLimit enables the token bucket. A nil value applies no limit.
	RateLimit *RateLimitOptions
	// Auth is the authentication stage. It is a slot rather than a
	// configuration block because the identity provider belongs to the auth
	// package, and the chain only fixes where it runs. A nil value leaves
	// every route unauthenticated.
	Auth func(http.Handler) http.Handler
}

// Standard builds the shared chain in the documented order. Every stage is
// present whether or not it is configured, so the order a service assembles is
// the order this package documents and a disabled stage is a pass-through
// rather than a hole.
func Standard(o Options) []Stage {
	return []Stage{
		{Name: StageRecover, Wrap: Recover()},
		{Name: StageRequestID, Wrap: AssignRequestID()},
		{Name: StageTraceSpan, Wrap: ServerSpan(o.Router)},
		{Name: StageAccessLog, Wrap: AccessLog(o.Logger)},
		{Name: StageMetrics, Wrap: Metrics()},
		{Name: StageTimeout, Wrap: Timeout(o.timeout())},
		{Name: StageBodyLimit, Wrap: BodyLimit(o.maxBodyBytes())},
		{Name: StageCORS, Wrap: corsStage(o.CORS)},
		{Name: StageAuth, Wrap: orPassthrough(o.Auth)},
		{Name: StageRateLimit, Wrap: rateLimitStage(o.RateLimit)},
	}
}

// Handler applies the shared chain to h. It is the one call a service makes to
// serve its router under the template's HTTP surface.
func Handler(h http.Handler, o Options) http.Handler {
	return Wrap(h, Standard(o))
}

// Wrap applies stages to h, the first stage outermost.
func Wrap(h http.Handler, stages []Stage) http.Handler {
	mw := make([]func(http.Handler) http.Handler, 0, len(stages))
	for _, s := range stages {
		mw = append(mw, s.Wrap)
	}
	return Chain(h, mw...)
}

// Chain applies middleware to h, the first argument outermost, so the call
// reads in the order a request travels.
func Chain(h http.Handler, mw ...func(http.Handler) http.Handler) http.Handler {
	for _, wrap := range slices.Backward(mw) {
		if wrap == nil {
			continue
		}
		h = wrap(h)
	}
	return h
}

func (o Options) timeout() time.Duration {
	if o.Timeout == 0 {
		return DefaultTimeout
	}
	return o.Timeout
}

func (o Options) maxBodyBytes() int64 {
	if o.MaxBodyBytes == 0 {
		return DefaultMaxBodyBytes
	}
	return o.MaxBodyBytes
}

func corsStage(o *CORSOptions) func(http.Handler) http.Handler {
	if o == nil {
		return passthrough()
	}
	return CORS(*o)
}

func rateLimitStage(o *RateLimitOptions) func(http.Handler) http.Handler {
	if o == nil {
		return passthrough()
	}
	return RateLimit(*o)
}

func orPassthrough(mw func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	if mw == nil {
		return passthrough()
	}
	return mw
}

// passthrough is the stage a disabled feature contributes. It keeps the stage
// list complete, so the assembled order can be asserted against the documented
// one whatever a service turns off.
func passthrough() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}
