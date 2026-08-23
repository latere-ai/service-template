package worker

import (
	"sync"
	"testing"
	"time"
)

// pollInterval and pollDeadline bound the helpers that wait for a state a
// goroutine reaches. They are wall-clock values, so a test that never reaches
// the state fails with a message instead of hanging until the package timeout.
const (
	pollInterval = time.Millisecond
	pollDeadline = 5 * time.Second
)

// fakeClock drives the schedule, the lease, and the shutdown window from a
// test. Advancing it fires every wait whose deadline has passed.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*fakeWaiter
}

// fakeWaiter is one pending wait.
type fakeWaiter struct {
	deadline time.Time
	ch       chan time.Time
}

// newFakeClock starts at a fixed instant, so a failure message reads the same
// on every run.
func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)}
}

// Now reports the current fake time.
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// After registers a wait. A non-positive duration fires at once.
func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)

	c.mu.Lock()
	defer c.mu.Unlock()
	if d <= 0 {
		ch <- c.now
		return ch
	}
	c.waiters = append(c.waiters, &fakeWaiter{deadline: c.now.Add(d), ch: ch})
	return ch
}

// Advance moves the clock and fires every wait it passed.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	var fired, pending []*fakeWaiter
	for _, w := range c.waiters {
		if !w.deadline.After(now) {
			fired = append(fired, w)
			continue
		}
		pending = append(pending, w)
	}
	c.waiters = pending
	c.mu.Unlock()

	for _, w := range fired {
		w.ch <- now
	}
}

// pending reports how many waits are registered.
func (c *fakeClock) pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiters)
}

// waitForWaiters blocks until at least n waits are pending, so a test advances
// the clock only after the goroutines it drives are waiting on it.
func (c *fakeClock) waitForWaiters(t testing.TB, n int) {
	t.Helper()
	waitFor(t, func() bool { return c.pending() >= n },
		"waiting for %d pending clock waits, have %d", n, c.pending())
}

// waitFor blocks until cond reports true, and fails the test with the message
// when it does not inside the deadline.
func waitFor(t testing.TB, cond func() bool, format string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(pollDeadline)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(pollInterval)
	}
	t.Fatalf(format, args...)
}

// advanceUntil moves the clock in steps until cond reports true. The step is
// small compared with the interval under test, so the clock stops just past the
// deadline that satisfied cond rather than several intervals beyond it.
func (c *fakeClock) advanceUntil(t testing.TB, step time.Duration, cond func() bool, format string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(pollDeadline)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		c.Advance(step)
		time.Sleep(pollInterval)
	}
	t.Fatalf(format, args...)
}
