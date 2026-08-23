package worker

import (
	"testing"
	"time"
)

func TestRetryPolicyZeroValueUsesTheDefaults(t *testing.T) {
	t.Parallel()
	var p RetryPolicy

	if got := p.attempts(); got != DefaultMaxAttempts {
		t.Errorf("attempts = %d, want %d", got, DefaultMaxAttempts)
	}
	if got := p.base(); got != DefaultBaseDelay {
		t.Errorf("base = %s, want %s", got, DefaultBaseDelay)
	}
	if got := p.max(); got != DefaultMaxDelay {
		t.Errorf("max = %s, want %s", got, DefaultMaxDelay)
	}
	if got := p.jitter(); got != 0 {
		t.Errorf("jitter = %v, want 0 for the zero value", got)
	}
	if got := (RetryPolicy{Jitter: -1}).jitter(); got != DefaultJitter {
		t.Errorf("jitter for a negative value = %v, want %v", got, DefaultJitter)
	}
	if got := (RetryPolicy{Jitter: 4}).jitter(); got != 1 {
		t.Errorf("jitter above one = %v, want 1", got)
	}
	if got := (RetryPolicy{Base: time.Minute, Max: time.Second}).max(); got != time.Minute {
		t.Errorf("max below base = %s, want the base %s", got, time.Minute)
	}
}

func TestDelayDoublesAndStopsAtTheCeiling(t *testing.T) {
	t.Parallel()
	p := RetryPolicy{Base: time.Second, Max: 8 * time.Second}

	want := []time.Duration{
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		8 * time.Second,
		8 * time.Second,
	}
	for i, w := range want {
		attempt := i + 1
		if got := p.delay(attempt, nil); got != w {
			t.Errorf("delay(%d) = %s, want %s", attempt, got, w)
		}
	}
	if got := p.delay(0, nil); got != time.Second {
		t.Errorf("delay(0) = %s, want the base delay %s", got, time.Second)
	}
}

func TestJitterOnlySubtractsAndStaysInBounds(t *testing.T) {
	t.Parallel()
	p := RetryPolicy{Base: time.Second, Max: 8 * time.Second, Jitter: 0.5}

	// The jitter fraction is subtracted, so the ceiling stays a ceiling and a
	// delay never reaches zero.
	if got := p.delay(4, func() float64 { return 0 }); got != 8*time.Second {
		t.Errorf("delay with no jitter drawn = %s, want %s", got, 8*time.Second)
	}
	if got := p.delay(4, func() float64 { return 1 }); got != 4*time.Second {
		t.Errorf("delay with full jitter drawn = %s, want %s", got, 4*time.Second)
	}

	for draw := 0.0; draw <= 1.0; draw += 0.1 {
		got := p.delay(3, func() float64 { return draw })
		if got < 2*time.Second || got > 4*time.Second {
			t.Fatalf("delay(3) with draw %.1f = %s, want within [2s, 4s]", draw, got)
		}
	}
}

func TestRandomFractionStaysInTheUnitInterval(t *testing.T) {
	t.Parallel()

	distinct := make(map[float64]struct{})
	for range 100 {
		got := randomFraction()
		if got < 0 || got >= 1 {
			t.Fatalf("randomFraction = %v, want a value within [0, 1)", got)
		}
		distinct[got] = struct{}{}
	}
	// A source that returns one value gives every replica the same delay, which
	// is the pile-up jitter exists to break.
	if len(distinct) < 90 {
		t.Errorf("distinct draws = %d out of 100, want a varying source", len(distinct))
	}
}
