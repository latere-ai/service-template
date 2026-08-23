package worker

import "time"

// clock supplies the time the runtime reads and the timers it waits on.
//
// It is an interface so a test drives a schedule interval, a lease expiry, and
// a shutdown window without waiting them out in wall-clock time. The runner and
// the in-process locker share one instance, so a test that advances the clock
// moves both.
type clock interface {
	// Now reports the current time.
	Now() time.Time
	// After returns a channel that receives once d has passed.
	After(d time.Duration) <-chan time.Time
}

// systemClock reads the process clock.
type systemClock struct{}

// Now reports the wall-clock time.
func (systemClock) Now() time.Time { return time.Now() }

// After returns a timer channel. The timer is collected when the caller stops
// referring to the channel, so an abandoned wait costs nothing.
func (systemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
