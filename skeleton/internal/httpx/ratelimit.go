package httpx

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimitOptions configures the token bucket the rate limit stage applies.
//
// The bucket is per key and refills continuously, so a caller may spend Burst
// requests at once and then sustain Rate requests per second. A fixed window
// would let a caller spend two windows' worth of requests across a window
// boundary.
type RateLimitOptions struct {
	// Rate is the sustained requests per second allowed per key. A rate of
	// zero or less disables the stage.
	Rate float64
	// Burst is how many requests a key may make back to back. It defaults to
	// one second of Rate, rounded up, and is never below one.
	Burst int
	// Key identifies the caller. It defaults to the peer address. A service
	// that limits per identity supplies a function reading its principal from
	// the context, which is possible because this stage runs after
	// authentication.
	Key func(*http.Request) string
	// Now is the clock, present so a test can advance time without sleeping.
	Now func() time.Time
	// IdleTimeout is how long an unused bucket is kept before it is dropped.
	// Without eviction the map grows with the number of distinct callers.
	IdleTimeout time.Duration
}

// DefaultRateLimitIdleTimeout keeps a bucket long enough that a regular caller
// keeps its allowance across think time, and short enough that a scan of
// distinct addresses does not accumulate.
const DefaultRateLimitIdleTimeout = 10 * time.Minute

// RateLimit rejects a caller that exceeds its budget with 429 and a Retry-After
// header naming when the next request will be accepted.
//
// It is the innermost stage of the shared chain, after authentication, so the
// budget can be attached to the caller rather than to the address, and so an
// authenticated caller is not charged for another caller behind the same
// address.
func RateLimit(o RateLimitOptions) func(http.Handler) http.Handler {
	if o.Rate <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	limiter := newLimiter(o)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ok, retryAfter := limiter.allow(limiter.key(r))
			w.Header().Set("RateLimit-Limit", strconv.Itoa(limiter.burst))
			if ok {
				next.ServeHTTP(w, r)
				return
			}
			seconds := max(int(math.Ceil(retryAfter.Seconds())), 1)
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			WriteError(w, r, New(http.StatusTooManyRequests,
				"the request rate exceeds the limit for this caller"))
		})
	}
}

// limiter holds one token bucket per key.
type limiter struct {
	rate        float64
	burst       int
	now         func() time.Time
	key         func(*http.Request) string
	idleTimeout time.Duration

	mu      sync.Mutex
	buckets map[string]*bucket
	// sweepAt is when the next eviction pass runs. Eviction is amortized onto
	// request handling so the limiter needs no goroutine of its own.
	sweepAt time.Time
}

type bucket struct {
	tokens float64
	seen   time.Time
}

func newLimiter(o RateLimitOptions) *limiter {
	burst := o.Burst
	if burst <= 0 {
		burst = int(math.Ceil(o.Rate))
	}
	if burst < 1 {
		burst = 1
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	key := o.Key
	if key == nil {
		key = clientIP
	}
	idle := o.IdleTimeout
	if idle <= 0 {
		idle = DefaultRateLimitIdleTimeout
	}
	return &limiter{
		rate:        o.Rate,
		burst:       burst,
		now:         now,
		key:         key,
		idleTimeout: idle,
		buckets:     map[string]*bucket{},
		sweepAt:     now().Add(idle),
	}
}

// allow spends one token for key. It reports whether the request proceeds and,
// when it does not, how long until one token is available.
func (l *limiter) allow(key string) (bool, time.Duration) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: float64(l.burst)}
		l.buckets[key] = b
	} else {
		b.tokens = min(float64(l.burst), b.tokens+now.Sub(b.seen).Seconds()*l.rate)
	}
	b.seen = now

	if b.tokens < 1 {
		missing := 1 - b.tokens
		return false, time.Duration(missing / l.rate * float64(time.Second))
	}
	b.tokens--
	return true, 0
}

// sweep drops buckets no caller has touched for the idle timeout. It runs at
// most once per timeout, so the cost is amortized to nothing per request.
func (l *limiter) sweep(now time.Time) {
	if now.Before(l.sweepAt) {
		return
	}
	l.sweepAt = now.Add(l.idleTimeout)
	for key, b := range l.buckets {
		if now.Sub(b.seen) >= l.idleTimeout {
			delete(l.buckets, key)
		}
	}
}
