package worker

import (
	"crypto/rand"
	"encoding/binary"
	"time"
)

// Retry defaults. The budget is finite on purpose: unbounded retry against a
// permanently failing input is an infinite loop that reads as activity on every
// dashboard except the one showing the work is not getting done.
const (
	// DefaultMaxAttempts is the total attempts one execution gets, the first
	// included.
	DefaultMaxAttempts = 3
	// DefaultBaseDelay is the wait after the first failed attempt.
	DefaultBaseDelay = time.Second
	// DefaultMaxDelay caps the exponential growth.
	DefaultMaxDelay = 30 * time.Second
	// DefaultJitter is the fraction of a computed delay that is randomised.
	// Without it, replicas that failed together retry together.
	DefaultJitter = 0.2
)

// RetryPolicy is bounded exponential backoff with jitter.
//
// The zero value means the defaults above, so a runner that sets no policy
// still retries a finite number of times rather than once or forever.
type RetryPolicy struct {
	// MaxAttempts is the total attempts, the first included. Zero or less
	// means DefaultMaxAttempts.
	MaxAttempts int
	// Base is the delay after the first failure. Zero or less means
	// DefaultBaseDelay.
	Base time.Duration
	// Max caps the delay. Zero or less means DefaultMaxDelay.
	Max time.Duration
	// Jitter is the fraction of the delay that is randomised downward, between
	// 0 and 1. A negative value means DefaultJitter; zero means no jitter.
	Jitter float64
}

// attempts reports the attempt budget of one execution.
func (p RetryPolicy) attempts() int {
	if p.MaxAttempts <= 0 {
		return DefaultMaxAttempts
	}
	return p.MaxAttempts
}

// base reports the first delay.
func (p RetryPolicy) base() time.Duration {
	if p.Base <= 0 {
		return DefaultBaseDelay
	}
	return p.Base
}

// max reports the delay ceiling, never below the base.
func (p RetryPolicy) max() time.Duration {
	if p.Max <= 0 {
		return DefaultMaxDelay
	}
	if p.Max < p.base() {
		return p.base()
	}
	return p.Max
}

// jitter reports the randomised fraction, clamped to the unit interval.
func (p RetryPolicy) jitter() float64 {
	switch {
	case p.Jitter < 0:
		return DefaultJitter
	case p.Jitter > 1:
		return 1
	default:
		return p.Jitter
	}
}

// delay reports how long to wait after a failed attempt. The attempt is
// 1-based, so delay(1) is the wait between the first and the second attempt.
//
// The delay doubles per attempt up to the ceiling, then a random fraction is
// subtracted. Jitter reduces rather than extends, so the ceiling is a ceiling.
// The random source is a parameter so the bounds are assertable.
func (p RetryPolicy) delay(attempt int, random func() float64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}

	d := p.base()
	ceiling := p.max()
	for i := 1; i < attempt; i++ {
		if d >= ceiling/2 {
			d = ceiling
			break
		}
		d *= 2
	}
	if d > ceiling {
		d = ceiling
	}

	j := p.jitter()
	if j == 0 || random == nil {
		return d
	}
	return time.Duration(float64(d) * (1 - j*random()))
}

// randomFraction draws the jitter fraction from the unit interval.
//
// The source is the system random generator. It needs no seeding, so replicas
// started from one image do not share a sequence, and the cost is paid only
// after an attempt has already failed. Read fills the buffer completely and
// reports no error.
func randomFraction() float64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	// 53 bits is the precision of a float64 mantissa, so the quotient is exact
	// and lands in [0, 1).
	return float64(binary.BigEndian.Uint64(b[:])>>11) / (1 << 53)
}
