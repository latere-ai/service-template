package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// errBoom is the failure a test job returns.
var errBoom = errors.New("boom")

// recorder collects the executions a runner records, which is how a test reads
// outcomes that telemetry would otherwise be the only witness to.
type recorder struct {
	mu   sync.Mutex
	seen []execution
}

// add records one execution.
func (rec *recorder) add(ex execution) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.seen = append(rec.seen, ex)
}

// count reports how many executions ended with the given result.
func (rec *recorder) count(result string) int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	n := 0
	for _, ex := range rec.seen {
		if ex.result == result {
			n++
		}
	}
	return n
}

// results reports the recorded results in order.
func (rec *recorder) results() []string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	out := make([]string, 0, len(rec.seen))
	for _, ex := range rec.seen {
		out = append(out, ex.result)
	}
	return out
}

// last reports the most recent execution with the given result.
func (rec *recorder) last(result string) (execution, bool) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, v := range slices.Backward(rec.seen) {
		if v.result == result {
			return v, true
		}
	}
	return execution{}, false
}

// newTestRunner returns a runner driven by the given clock, with its log
// discarded and its telemetry registration released at the end of the test.
func newTestRunner(t *testing.T, c clock, locker Locker) (*Runner, *recorder) {
	t.Helper()
	rec := &recorder{}
	r := New()
	r.clock = c
	r.Locker = locker
	r.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	r.onExecution = rec.add
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return r, rec
}

// runInBackground starts Run and returns the channel its error arrives on.
func runInBackground(t *testing.T, r *Runner, ctx context.Context) <-chan error {
	t.Helper()
	errc := make(chan error, 1)
	go func() { errc <- r.Run(ctx) }()
	return errc
}

// waitErr reports the error Run returned, or fails when it did not return.
func waitErr(t *testing.T, errc <-chan error) error {
	t.Helper()
	select {
	case err := <-errc:
		return err
	case <-time.After(pollDeadline):
		t.Fatal("Run did not return")
		return nil
	}
}

func TestScheduledJobRunsOnItsInterval(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	r, rec := newTestRunner(t, clk, newTestLocker(clk))

	var runs atomic.Int64
	err := r.Schedule(JobFunc{"reconcile", func(context.Context) error {
		runs.Add(1)
		return nil
	}}, Schedule{Interval: time.Minute})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := runInBackground(t, r, ctx)

	clk.advanceUntil(t, 6*time.Second, func() bool { return runs.Load() >= 1 },
		"the job did not run on its first interval")
	if got := runs.Load(); got != 1 {
		t.Fatalf("executions after one interval = %d, want 1", got)
	}

	clk.advanceUntil(t, 6*time.Second, func() bool { return runs.Load() >= 2 },
		"the job did not run on its second interval")

	cancel()
	if err := waitErr(t, errc); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := rec.count(ResultSuccess); got < 2 {
		t.Errorf("successful executions recorded = %d, want at least 2", got)
	}
}

func TestReplicasProduceOneExecutionPerInterval(t *testing.T) {
	t.Parallel()

	// The gate holds every replica inside the job, so a replica that acquired
	// the lock still holds it while the others reach their own interval. Only
	// the lock decides how many executions happen.
	type replicas struct {
		started chan string
		runs    *atomic.Int64
		release func()
		stop    func()
	}

	start := func(t *testing.T, clk *fakeClock, lockers []Locker) (replicas, []*recorder) {
		t.Helper()
		gate := make(chan struct{})
		started := make(chan string, len(lockers))
		var runs atomic.Int64

		ctx, cancel := context.WithCancel(context.Background())
		var errcs []<-chan error
		var recs []*recorder
		for i, locker := range lockers {
			r, rec := newTestRunner(t, clk, locker)
			recs = append(recs, rec)
			replica := i
			err := r.Schedule(JobFunc{"reconcile", func(context.Context) error {
				runs.Add(1)
				started <- string(rune('a' + replica))
				<-gate
				return nil
			}}, Schedule{Interval: time.Minute, Lease: 20 * time.Second})
			if err != nil {
				t.Fatalf("Schedule: %v", err)
			}
			errcs = append(errcs, runInBackground(t, r, ctx))
		}

		// Every replica registers its first interval before the clock moves, so
		// the interval fires for all of them at the same instant and the lock
		// is what decides the outcome rather than the order they started in.
		clk.waitForWaiters(t, len(lockers))

		var once sync.Once
		return replicas{
			started: started,
			runs:    &runs,
			release: func() { once.Do(func() { close(gate) }) },
			stop: func() {
				once.Do(func() { close(gate) })
				cancel()
				for _, errc := range errcs {
					if err := waitErr(t, errc); err != nil {
						t.Errorf("Run: %v", err)
					}
				}
			},
		}, recs
	}

	t.Run("one shared lock", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock()
		shared := newTestLocker(clk)
		rs, recs := start(t, clk, []Locker{shared, shared, shared})
		defer rs.stop()

		clk.advanceUntil(t, 6*time.Second, func() bool { return rs.runs.Load() >= 1 },
			"no replica ran the job")
		// The two replicas that lost the race record a skip, which is how the
		// test knows they reached the interval and did not simply lag.
		skipped := func() int {
			total := 0
			for _, rec := range recs {
				total += rec.count(ResultSkipped)
			}
			return total
		}
		waitFor(t, func() bool { return skipped() >= 2 },
			"only %d replicas skipped the interval, want 2", skipped())

		if got := rs.runs.Load(); got != 1 {
			t.Fatalf("executions across three replicas = %d, want 1", got)
		}
		rs.release()
	})

	t.Run("a lock per replica", func(t *testing.T) {
		t.Parallel()
		clk := newFakeClock()
		lockers := []Locker{newTestLocker(clk), newTestLocker(clk), newTestLocker(clk)}
		rs, _ := start(t, clk, lockers)
		defer rs.stop()

		// Three replicas that share no lock run three times. The check proves
		// the shared-lock case is testing the lock and not the schedule.
		clk.advanceUntil(t, 6*time.Second, func() bool { return rs.runs.Load() >= 3 },
			"replicas with their own locks ran %d times, want 3", rs.runs.Load())
	})
}

func TestExpiredLeaseFromADeadReplicaFreesTheNextInterval(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clk := newFakeClock()
	locker := newTestLocker(clk)
	r, rec := newTestRunner(t, clk, locker)

	var runs atomic.Int64
	err := r.Schedule(JobFunc{"reconcile", func(context.Context) error {
		runs.Add(1)
		return nil
	}}, Schedule{Interval: 30 * time.Second, Lease: 10 * time.Second})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	errc := runInBackground(t, r, ctx)
	clk.waitForWaiters(t, 1)

	// A replica that died mid-job leaves a lease behind and never renews it.
	clk.Advance(25 * time.Second)
	if _, err := locker.Acquire(ctx, "reconcile", 10*time.Second); err != nil {
		t.Fatalf("the dead replica could not acquire the lock: %v", err)
	}

	clk.advanceUntil(t, 2*time.Second, func() bool { return rec.count(ResultSkipped) >= 1 },
		"the interval under a held lease did not record a skip")
	if got := runs.Load(); got != 0 {
		t.Fatalf("executions while the lease was held = %d, want 0", got)
	}

	// The lease expires on its own, so the schedule resumes with no
	// intervention.
	clk.advanceUntil(t, 3*time.Second, func() bool { return runs.Load() >= 1 },
		"the schedule did not resume after the lease expired")

	cancel()
	if err := waitErr(t, errc); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestLeaseIsRenewedWhileTheJobRuns(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clk := newFakeClock()
	locker := newTestLocker(clk)
	r, _ := newTestRunner(t, clk, locker)

	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	err := r.Schedule(JobFunc{"reconcile", func(context.Context) error {
		started <- struct{}{}
		<-gate
		return nil
	}}, Schedule{Interval: time.Minute, Lease: 15 * time.Second})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	errc := runInBackground(t, r, ctx)
	clk.advanceUntil(t, 6*time.Second, func() bool { return len(started) > 0 },
		"the job did not start")
	<-started

	expiry := func() time.Time {
		at, ok := locker.expiry("reconcile")
		if !ok {
			t.Fatal("the lock is not held while the job runs")
		}
		return at
	}

	// The lease is shorter than the job. Renewal is what keeps the name held,
	// so past the original lease the lock is still not available.
	previous := expiry()
	for range 4 {
		clk.Advance(6 * time.Second)
		waitFor(t, func() bool { return expiry().After(previous) },
			"the lease was not renewed while the job ran")
		previous = expiry()
	}
	if _, err := locker.Acquire(ctx, "reconcile", time.Second); !errors.Is(err, ErrLockHeld) {
		t.Fatalf("Acquire during a renewed lease = %v, want ErrLockHeld", err)
	}

	close(gate)
	cancel()
	if err := waitErr(t, errc); err != nil {
		t.Fatalf("Run: %v", err)
	}
	waitFor(t, func() bool { _, held := locker.expiry("reconcile"); return !held },
		"the lock was not released after the job finished")
}

func TestRetryStopsAtTheAttemptBudget(t *testing.T) {
	t.Parallel()
	r, rec := newTestRunner(t, systemClock{}, nil)
	r.Retry = RetryPolicy{MaxAttempts: 3, Base: time.Millisecond, Max: time.Millisecond}

	var calls atomic.Int64
	if err := r.Command(JobFunc{"backfill", func(context.Context) error {
		calls.Add(1)
		return errBoom
	}}); err != nil {
		t.Fatalf("Command: %v", err)
	}

	err := r.RunOnce(context.Background(), "backfill")
	var terminal *TerminalError
	if !errors.As(err, &terminal) {
		t.Fatalf("RunOnce error = %v, want a *TerminalError", err)
	}
	if terminal.Attempts != 3 || terminal.Job != "backfill" {
		t.Errorf("terminal failure = %+v, want 3 attempts of backfill", terminal)
	}
	if !errors.Is(err, errBoom) {
		t.Errorf("terminal failure does not carry the reason %v", errBoom)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}

	want := []string{ResultError, ResultError, ResultFailed}
	if got := rec.results(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("recorded results = %v, want %v", got, want)
	}
}

func TestCancellationStopsTheJobAndRecordsTheReason(t *testing.T) {
	t.Parallel()
	r, rec := newTestRunner(t, systemClock{}, nil)

	started := make(chan struct{})
	stopped := make(chan struct{})
	if err := r.Command(JobFunc{"drain", func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}}); err != nil {
		t.Fatalf("Command: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- r.RunOnce(ctx, "drain") }()

	<-started
	cancel()

	err := waitErr(t, errc)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunOnce error = %v, want context.Canceled", err)
	}
	<-stopped

	ex, ok := rec.last(ResultCancelled)
	if !ok {
		t.Fatalf("no cancelled execution recorded, results = %v", rec.results())
	}
	if ex.job != "drain" || !errors.Is(ex.err, context.Canceled) {
		t.Errorf("cancelled execution = %+v, want the drain job with the cancellation reason", ex)
	}
	// A cancelled execution is not retried: the budget is for failures, not for
	// a shutdown that is going to cancel the next attempt as well.
	if got := rec.count(ResultError); got != 0 {
		t.Errorf("retries after cancellation = %d, want 0", got)
	}
}

func TestAJobThatIgnoresCancellationIsReportedAbandoned(t *testing.T) {
	t.Parallel()
	r, rec := newTestRunner(t, systemClock{}, nil)
	r.ShutdownTimeout = 50 * time.Millisecond

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	started := make(chan struct{})
	if err := r.Continuous(JobFunc{"stubborn", func(context.Context) error {
		close(started)
		<-release
		return nil
	}}); err != nil {
		t.Fatalf("Continuous: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errc := runInBackground(t, r, ctx)
	<-started
	cancel()

	err := waitErr(t, errc)
	if err == nil || !strings.Contains(err.Error(), "stubborn") {
		t.Fatalf("Run error = %v, want one naming the job that did not stop", err)
	}
	if got := rec.count(ResultAbandoned); got != 1 {
		t.Errorf("abandoned executions = %d, want 1", got)
	}
}

func TestContinuousJobFailureStopsTheRunner(t *testing.T) {
	t.Parallel()
	r, _ := newTestRunner(t, systemClock{}, nil)
	r.Retry = RetryPolicy{MaxAttempts: 2, Base: time.Millisecond, Max: time.Millisecond}

	var calls atomic.Int64
	if err := r.Continuous(JobFunc{"consumer", func(context.Context) error {
		calls.Add(1)
		return errBoom
	}}); err != nil {
		t.Fatalf("Continuous: %v", err)
	}

	err := waitErr(t, runInBackground(t, r, context.Background()))
	if !errors.Is(err, errBoom) {
		t.Fatalf("Run error = %v, want the job failure %v", err, errBoom)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
}

func TestPanicInAJobBecomesAFailure(t *testing.T) {
	t.Parallel()
	r, _ := newTestRunner(t, systemClock{}, nil)
	r.Retry = RetryPolicy{MaxAttempts: 1}

	if err := r.Command(JobFunc{"panicky", func(context.Context) error {
		panic("no")
	}}); err != nil {
		t.Fatalf("Command: %v", err)
	}

	err := r.RunOnce(context.Background(), "panicky")
	if err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("RunOnce error = %v, want a panic reported as a failure", err)
	}
}

func TestRegistrationRejectsUnusableJobs(t *testing.T) {
	t.Parallel()
	r, _ := newTestRunner(t, systemClock{}, NewMemoryLocker())

	if err := r.Command(nil); err == nil {
		t.Error("Command accepted a nil job")
	}
	if err := r.Command(JobFunc{"  ", func(context.Context) error { return nil }}); err == nil {
		t.Error("Command accepted an empty job name")
	}
	job := JobFunc{"reconcile", func(context.Context) error { return nil }}
	if err := r.Command(job); err != nil {
		t.Fatalf("Command: %v", err)
	}
	if err := r.Command(job); err == nil {
		t.Error("Command accepted a duplicate job name")
	}

	cases := []struct {
		name     string
		schedule Schedule
		wantErr  string
	}{
		{"no interval", Schedule{}, "interval must be positive"},
		{"lease equal to the interval", Schedule{Interval: time.Minute, Lease: time.Minute}, "shorter than the interval"},
		{"lease longer than the interval", Schedule{Interval: time.Minute, Lease: 2 * time.Minute}, "shorter than the interval"},
		{"negative lease", Schedule{Interval: time.Minute, Lease: -time.Second}, "lease must be positive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := r.Schedule(JobFunc{tc.name, func(context.Context) error { return nil }}, tc.schedule)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Schedule error = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}

	if err := r.Schedule(JobFunc{"defaulted", func(context.Context) error { return nil }},
		Schedule{Interval: time.Minute}); err != nil {
		t.Fatalf("Schedule with a defaulted lease: %v", err)
	}
	e, ok := r.entryFor("defaulted")
	if !ok {
		t.Fatal("the scheduled job was not registered")
	}
	if e.schedule.Lease != time.Minute/leaseFraction {
		t.Errorf("default lease = %s, want %s", e.schedule.Lease, time.Minute/leaseFraction)
	}
}

func TestRunRefusesToStartWithoutWhatItNeeds(t *testing.T) {
	t.Parallel()

	empty, _ := newTestRunner(t, systemClock{}, NewMemoryLocker())
	if err := empty.Run(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "no scheduled or continuous job") {
		t.Errorf("Run with no jobs = %v, want a refusal naming the reason", err)
	}

	unlocked, _ := newTestRunner(t, systemClock{}, nil)
	if err := unlocked.Schedule(JobFunc{"reconcile", func(context.Context) error { return nil }},
		Schedule{Interval: time.Minute}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	err := unlocked.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Locker is nil") {
		t.Errorf("Run without a locker = %v, want a refusal naming the locker", err)
	}
}

func TestRunOnceReportsAnUnknownJob(t *testing.T) {
	t.Parallel()
	r, _ := newTestRunner(t, systemClock{}, nil)
	if err := r.Command(JobFunc{"backfill", func(context.Context) error { return nil }}); err != nil {
		t.Fatalf("Command: %v", err)
	}

	err := r.RunOnce(context.Background(), "reindex")
	if err == nil || !strings.Contains(err.Error(), "backfill") {
		t.Fatalf("RunOnce of an unknown job = %v, want an error listing the registered jobs", err)
	}
	if got := r.Jobs(); len(got) != 1 || got[0] != "backfill" {
		t.Errorf("Jobs = %v, want [backfill]", got)
	}
}

func TestRunOnceRunsACommandJob(t *testing.T) {
	t.Parallel()
	r, rec := newTestRunner(t, systemClock{}, nil)

	var calls atomic.Int64
	if err := r.Command(JobFunc{"backfill", func(context.Context) error {
		calls.Add(1)
		return nil
	}}); err != nil {
		t.Fatalf("Command: %v", err)
	}

	if err := r.RunOnce(context.Background(), "backfill"); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("executions = %d, want 1", got)
	}
	ex, ok := rec.last(ResultSuccess)
	if !ok || ex.trigger != TriggerManual {
		t.Errorf("recorded execution = %+v, want a manual trigger", ex)
	}

	// A command job is not started by Run, so a process in work mode does not
	// run a backfill because someone registered one.
	if err := r.Run(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "no scheduled or continuous job") {
		t.Errorf("Run with only a command job = %v, want a refusal", err)
	}
}

func TestExecutionTimeoutBoundsOneAttempt(t *testing.T) {
	t.Parallel()
	clk := newFakeClock()
	r, rec := newTestRunner(t, systemClock{}, newTestLocker(clk))
	r.Retry = RetryPolicy{MaxAttempts: 1}

	// The timeout is what stops a job that hangs on a dependency. Without it a
	// scheduled job holds its lock until the process is replaced.
	err := r.Schedule(JobFunc{"hangs", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}}, Schedule{Interval: 40 * time.Millisecond, Lease: 10 * time.Millisecond, Timeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := runInBackground(t, r, ctx)

	waitFor(t, func() bool { return rec.count(ResultFailed) >= 1 },
		"the attempt was not stopped by its timeout, results = %v", rec.results())
	ex, _ := rec.last(ResultFailed)
	if !errors.Is(ex.err, context.DeadlineExceeded) {
		t.Errorf("recorded failure = %v, want the attempt deadline", ex.err)
	}

	cancel()
	if err := waitErr(t, errc); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// erroringLocker reports an infrastructure failure rather than a held lock.
type erroringLocker struct{ err error }

// Acquire always fails.
func (l erroringLocker) Acquire(context.Context, string, time.Duration) (Lock, error) {
	return nil, l.err
}

func TestALockServiceFailureSkipsTheExecution(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clk := newFakeClock()
	r, rec := newTestRunner(t, clk, erroringLocker{err: errBoom})

	var runs atomic.Int64
	err := r.Schedule(JobFunc{"reconcile", func(context.Context) error {
		runs.Add(1)
		return nil
	}}, Schedule{Interval: time.Minute})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	errc := runInBackground(t, r, ctx)
	clk.advanceUntil(t, 6*time.Second, func() bool { return rec.count(ResultError) >= 1 },
		"an unreachable lock service was not recorded")

	// A lock that cannot be taken is not a licence to run unlocked: a job that
	// ran on every replica because the lock service was down is the failure the
	// lock exists to prevent.
	if got := runs.Load(); got != 0 {
		t.Errorf("executions without a lock = %d, want 0", got)
	}

	cancel()
	if err := waitErr(t, errc); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestCancellationDuringBackoffStopsTheRetries(t *testing.T) {
	t.Parallel()
	r, rec := newTestRunner(t, systemClock{}, nil)
	r.Retry = RetryPolicy{MaxAttempts: 3, Base: time.Second, Max: time.Second}

	attempted := make(chan struct{}, 1)
	var calls atomic.Int64
	if err := r.Command(JobFunc{"backfill", func(context.Context) error {
		calls.Add(1)
		attempted <- struct{}{}
		return errBoom
	}}); err != nil {
		t.Fatalf("Command: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- r.RunOnce(ctx, "backfill") }()

	<-attempted
	cancel()

	if err := waitErr(t, errc); err == nil {
		t.Fatal("RunOnce reported no error for a cancelled retry")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1: a cancelled execution does not wait out its backoff", got)
	}
	if got := rec.count(ResultCancelled); got != 1 {
		t.Errorf("cancelled executions = %d, want 1, results = %v", got, rec.results())
	}
}

func TestRunnerDefaultsAreUsedWhenNothingIsSet(t *testing.T) {
	t.Parallel()
	r := &Runner{}

	if got := r.shutdownTimeout(); got != DefaultShutdownTimeout {
		t.Errorf("shutdownTimeout = %s, want %s", got, DefaultShutdownTimeout)
	}
	if r.logger() == nil {
		t.Error("logger reported nil")
	}
	if err := r.Close(); err != nil {
		t.Errorf("Close without telemetry: %v", err)
	}
	if got := New().shutdownTimeout(); got != DefaultShutdownTimeout {
		t.Errorf("New shutdownTimeout = %s, want %s", got, DefaultShutdownTimeout)
	}
}

func TestACallerDeadlineBoundsAOneShotJob(t *testing.T) {
	t.Parallel()
	r, rec := newTestRunner(t, systemClock{}, nil)
	r.Retry = RetryPolicy{MaxAttempts: 1}

	// A command job takes its bound from the caller, which is how job mode
	// stops a backfill that hangs on a dependency.
	if err := r.Command(JobFunc{"bounded-backfill", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}}); err != nil {
		t.Fatalf("Command: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := r.RunOnce(ctx, "bounded-backfill")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RunOnce error = %v, want the caller deadline", err)
	}
	ex, ok := rec.last(ResultCancelled)
	if !ok || ex.job != "bounded-backfill" {
		t.Fatalf("recorded execution = %+v, want a cancelled bounded-backfill", ex)
	}
}
