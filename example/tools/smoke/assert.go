package main

import (
	"context"
	"fmt"
	"time"
)

// Assertion is one statement about the live target. Check returns the value it
// observed together with the reason the value is wrong, so a failure and a
// pass are described the same way and the evidence block can print both.
type Assertion struct {
	// Name identifies the assertion in the evidence block.
	Name string
	// Expected is what the release requires, written for a reader.
	Expected string
	// Check performs one attempt.
	Check func(ctx context.Context) (observed string, err error)
}

// Result is the outcome of one assertion, including the attempt count. A check
// that passes on its last attempt is visible in the evidence rather than
// hidden behind a pass mark.
type Result struct {
	Name     string
	Expected string
	Observed string
	Attempts int
	Err      error
}

// OK reports whether the assertion held.
func (r Result) OK() bool { return r.Err == nil }

// RunAll runs every assertion in order, retrying each one inside the window.
// sleep is a parameter so a test drives the backoff without waiting.
func RunAll(ctx context.Context, assertions []Assertion, window, backoff time.Duration, sleep func(time.Duration)) []Result {
	deadline := time.Now().Add(window)
	results := make([]Result, 0, len(assertions))
	for _, a := range assertions {
		results = append(results, runOne(ctx, a, deadline, backoff, sleep))
	}
	return results
}

// runOne retries a single assertion with exponential backoff until it holds or
// the window closes. The window is shared across assertions, so a run cannot
// take the window multiplied by the number of checks.
func runOne(ctx context.Context, a Assertion, deadline time.Time, backoff time.Duration, sleep func(time.Duration)) Result {
	result := Result{Name: a.Name, Expected: a.Expected}
	wait := backoff
	for {
		result.Attempts++
		observed, err := a.Check(ctx)
		result.Observed = observed
		result.Err = err
		if err == nil {
			return result
		}
		if ctx.Err() != nil {
			result.Err = fmt.Errorf("%w (cancelled after %d attempts)", err, result.Attempts)
			return result
		}
		if !time.Now().Add(wait).Before(deadline) {
			result.Err = fmt.Errorf("%w (last of %d attempts)", err, result.Attempts)
			return result
		}
		sleep(wait)
		if wait *= 2; wait > MaxBackoff {
			wait = MaxBackoff
		}
	}
}

// Failures returns the results that did not hold.
func Failures(results []Result) []Result {
	var failed []Result
	for _, r := range results {
		if !r.OK() {
			failed = append(failed, r)
		}
	}
	return failed
}
