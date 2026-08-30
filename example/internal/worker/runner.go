package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Runtime defaults.
const (
	// DefaultShutdownTimeout is how long a cancelled job has to return before
	// the runner reports it abandoned and stops waiting.
	DefaultShutdownTimeout = 30 * time.Second
	// leaseFraction divides the interval to produce the default lease. A lease
	// shorter than the interval means a replica that dies mid-job frees the
	// name before the next interval, instead of stopping the schedule.
	leaseFraction = 3
	// renewFraction divides the lease to produce the renewal period, so a lease
	// survives two lost renewals before it expires.
	renewFraction = 3
)

// Schedule states how often a job runs and how long its lease lasts.
type Schedule struct {
	// Interval is the period between executions. It must be positive.
	Interval time.Duration
	// Lease is how long the lock is held before it expires. Zero means a third
	// of the interval. It must be shorter than the interval.
	Lease time.Duration
	// Timeout bounds one execution attempt of a scheduled job. Zero means no
	// bound, and the job then runs until it returns or until shutdown cancels
	// it. A continuous job runs for the life of the process and a command job
	// is bounded by the context its caller passes, so neither reads this field.
	Timeout time.Duration
}

// kind separates how a registered job is started.
type kind int

const (
	// kindScheduled runs on an interval under a lock.
	kindScheduled kind = iota
	// kindContinuous runs once for the life of the process, which is the shape
	// of a queue consumer.
	kindContinuous
	// kindCommand runs only when it is named on the command line.
	kindCommand
)

// entry is one registered job with the parameters of its shape.
type entry struct {
	job      Job
	kind     kind
	schedule Schedule
}

// Runner owns the registered jobs and everything around them: the lock a
// scheduled job takes, the retry budget, the shutdown window, and the telemetry
// each execution emits.
//
// The exported fields are set after [New] and before [Runner.Run].
type Runner struct {
	// Locker leases a job name. Scheduled jobs require it, so a deployment
	// states whether executions are serialised across replicas rather than
	// inheriting an answer.
	Locker Locker

	// Retry is the attempt budget and the backoff of one execution.
	Retry RetryPolicy

	// ShutdownTimeout is how long a cancelled job has to return.
	ShutdownTimeout time.Duration

	// Logger receives the execution records. It defaults to slog.Default().
	Logger *slog.Logger

	// clock drives the schedule, the backoff, the renewal, and the shutdown
	// window. It is a field so a test drives them instead of waiting them out.
	clock clock
	// random supplies the jitter fraction. It is a field so a test asserts the
	// backoff bounds.
	random func() float64
	// onExecution observes every recorded execution. It is a field so a test
	// reads outcomes without polling exported telemetry.
	onExecution func(execution)

	mu          sync.Mutex
	order       []string
	jobs        map[string]*entry
	lastSuccess map[string]time.Time
	running     map[string]int

	instr *instruments
}

// New returns a runner with no jobs and the default budgets.
func New() *Runner {
	r := &Runner{
		ShutdownTimeout: DefaultShutdownTimeout,
		Logger:          slog.Default(),
		clock:           systemClock{},
		random:          randomFraction,
		jobs:            make(map[string]*entry),
		lastSuccess:     make(map[string]time.Time),
		running:         make(map[string]int),
	}
	r.instr = newInstruments(r.sinceLastSuccess)
	return r
}

// Close releases the runner's telemetry registration. A runner is not usable
// afterwards.
func (r *Runner) Close() error {
	if r.instr == nil {
		return nil
	}
	return r.instr.close()
}

// Schedule registers a job that runs every interval under a lock keyed by its
// name, so replicas of one deployment produce one execution per interval.
func (r *Runner) Schedule(j Job, s Schedule) error {
	if s.Interval <= 0 {
		return fmt.Errorf("worker: schedule interval must be positive, got %s", s.Interval)
	}
	if s.Lease == 0 {
		s.Lease = s.Interval / leaseFraction
	}
	if s.Lease <= 0 {
		return fmt.Errorf("worker: lease must be positive, got %s", s.Lease)
	}
	if s.Lease >= s.Interval {
		return fmt.Errorf("worker: lease %s must be shorter than the interval %s, "+
			"otherwise a replica that dies mid-job blocks the next execution", s.Lease, s.Interval)
	}
	return r.add(j, kindScheduled, s)
}

// Continuous registers a job that runs once for the life of the process and is
// restarted, within the retry budget, when it returns an error. A queue
// consumer has this shape: it runs on every replica, so it takes no lock.
//
// A continuous job has no execution timeout, because its execution is the
// lifetime of the process. It is bounded by the context passed to
// [Runner.Run] and by the shutdown window.
func (r *Runner) Continuous(j Job) error {
	return r.add(j, kindContinuous, Schedule{})
}

// Command registers a job that only runs when it is named on the command line.
//
// A command job is bounded by the context passed to [Runner.RunOnce], so a
// caller that wants a deadline sets one there. The runtime imposes none,
// because how long a backfill may take is a property of the invocation.
func (r *Runner) Command(j Job) error {
	return r.add(j, kindCommand, Schedule{})
}

// add records one job under its name.
func (r *Runner) add(j Job, k kind, s Schedule) error {
	if j == nil {
		return errors.New("worker: job is nil")
	}
	name := j.Name()
	if strings.TrimSpace(name) == "" {
		return errors.New("worker: job name is empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.jobs[name]; exists {
		return fmt.Errorf("worker: job %q is already registered", name)
	}
	r.jobs[name] = &entry{job: j, kind: k, schedule: s}
	r.order = append(r.order, name)
	// The success clock starts at registration, so a job that has never
	// succeeded reports a rising time since last success rather than none.
	r.lastSuccess[name] = r.clock.Now()
	return nil
}

// Jobs reports the registered job names in registration order.
func (r *Runner) Jobs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

// entryFor looks one job up by name.
func (r *Runner) entryFor(name string) (*entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.jobs[name]
	return e, ok
}

// startable reports the scheduled and continuous jobs in registration order.
func (r *Runner) startable() []*entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*entry, 0, len(r.order))
	for _, name := range r.order {
		if e := r.jobs[name]; e.kind != kindCommand {
			out = append(out, e)
		}
	}
	return out
}

// Run starts every scheduled and continuous job and blocks until the context is
// cancelled or a continuous job exhausts its retry budget. It then cancels the
// running jobs and waits out the shutdown window.
//
// It reports an error when a job did not stop inside the window, naming the
// job, and when a continuous job failed terminally.
func (r *Runner) Run(ctx context.Context) error {
	entries := r.startable()
	if len(entries) == 0 {
		return errors.New("worker: no scheduled or continuous job is registered")
	}
	for _, e := range entries {
		if e.kind == kindScheduled && r.Locker == nil {
			return fmt.Errorf("worker: job %q is scheduled but Locker is nil: "+
				"set MemoryLocker for a single replica, or a shared implementation for several", e.job.Name())
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg      sync.WaitGroup
		errMu   sync.Mutex
		errs    []error
		failed  = make(chan struct{})
		failure sync.Once
	)
	for _, e := range entries {
		wg.Go(func() {
			var err error
			switch e.kind {
			case kindScheduled:
				err = r.runScheduled(runCtx, e)
			case kindContinuous:
				err = r.runContinuous(runCtx, e)
			case kindCommand:
			}
			if err != nil {
				errMu.Lock()
				errs = append(errs, err)
				errMu.Unlock()
				failure.Do(func() { close(failed) })
			}
		})
	}

	stopped := make(chan struct{})
	go func() {
		wg.Wait()
		close(stopped)
	}()

	select {
	case <-stopped:
		return r.joined(&errMu, &errs)
	case <-failed:
		cancel()
	case <-ctx.Done():
		cancel()
	}

	// Shutdown ends the wait either way: a job that ignores cancellation is
	// reported and left behind, because a process that waits forever for it is
	// killed by the orchestrator with no record of which job hung.
	select {
	case <-stopped:
	case <-r.clock.After(r.shutdownTimeout()):
		names := r.stillRunning()
		for _, name := range names {
			r.record(ctx, execution{job: name, result: ResultAbandoned, err: context.DeadlineExceeded})
		}
		r.logger().ErrorContext(ctx, "jobs did not stop within the shutdown window",
			"jobs", strings.Join(names, ","), "window", r.shutdownTimeout().String())
		errMu.Lock()
		errs = append(errs, fmt.Errorf("worker: job(s) %s did not stop within %s",
			strings.Join(names, ", "), r.shutdownTimeout()))
		errMu.Unlock()
	}
	return r.joined(&errMu, &errs)
}

// joined collects the errors the job goroutines recorded.
func (r *Runner) joined(mu *sync.Mutex, errs *[]error) error {
	mu.Lock()
	defer mu.Unlock()
	return errors.Join(*errs...)
}

// RunOnce executes one registered job by name, with the retry budget and the
// telemetry a scheduled execution gets and without taking the lock: a manual
// invocation that silently did nothing because another replica held the name is
// worse than one that ran twice.
func (r *Runner) RunOnce(ctx context.Context, name string) error {
	e, ok := r.entryFor(name)
	if !ok {
		return fmt.Errorf("worker: no job named %q, registered jobs are %s",
			name, strings.Join(r.Jobs(), ", "))
	}
	return r.execute(ctx, e, TriggerManual)
}

// runScheduled fires one job on its interval until the context ends.
func (r *Runner) runScheduled(ctx context.Context, e *entry) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.clock.After(e.schedule.Interval):
		}
		r.runLocked(ctx, e)
	}
}

// runLocked takes the lease, renews it while the job runs, and frees it after.
func (r *Runner) runLocked(ctx context.Context, e *entry) {
	name := e.job.Name()
	lease := e.schedule.Lease

	lock, err := r.Locker.Acquire(ctx, name, lease)
	switch {
	case errors.Is(err, ErrLockHeld):
		// Another replica is running this interval. That is the mechanism
		// working, so it is recorded and not logged as a failure.
		r.record(ctx, execution{job: name, trigger: TriggerSchedule, result: ResultSkipped})
		return
	case err != nil:
		r.logger().ErrorContext(ctx, "acquiring the job lock failed", "job", name, "error", err)
		r.record(ctx, execution{job: name, trigger: TriggerSchedule, result: ResultError, err: err})
		return
	}

	stopRenew := r.renewWhileRunning(lock, name, lease)
	_ = r.execute(ctx, e, TriggerSchedule)
	stopRenew()

	// The release outlives cancellation. A lock left behind at shutdown blocks
	// the name until the lease expires, which delays the next interval for no
	// reason.
	if err := lock.Release(context.WithoutCancel(ctx)); err != nil {
		r.logger().WarnContext(ctx, "releasing the job lock failed", "job", name, "error", err)
	}
}

// renewWhileRunning extends the lease until the returned function is called.
//
// The renewal outlives cancellation of the run context, because a job finishing
// inside the shutdown window still holds its name. It stops when the job stops,
// so a replica that dies frees the name by expiry.
//
//nolint:contextcheck // the lease must outlive the run context, or another replica takes a running job
func (r *Runner) renewWhileRunning(lock Lock, name string, lease time.Duration) func() {
	period := lease / renewFraction
	if period <= 0 {
		period = lease
	}
	done := make(chan struct{})
	var once sync.Once

	go func() {
		for {
			select {
			case <-done:
				return
			case <-r.clock.After(period):
			}
			if err := lock.Renew(context.Background(), lease); err != nil {
				r.logger().WarnContext(context.Background(), "renewing the job lock failed",
					"job", name, "error", err)
				return
			}
		}
	}()

	return func() { once.Do(func() { close(done) }) }
}

// runContinuous runs a long-lived job. A job that returns because the context
// ended is a clean shutdown, not a failure.
func (r *Runner) runContinuous(ctx context.Context, e *entry) error {
	err := r.execute(ctx, e, TriggerContinuous)
	if ctx.Err() != nil {
		//nolint:nilerr // the job ended because the context did, which is a clean shutdown
		return nil
	}
	return err
}

// execute runs one job through its attempt budget and records every attempt.
func (r *Runner) execute(ctx context.Context, e *entry, trigger string) error {
	name := e.job.Name()
	budget := r.Retry.attempts()

	for attempt := 1; attempt <= budget; attempt++ {
		start := r.clock.Now()
		err := r.attempt(ctx, e, trigger, attempt)
		ex := execution{
			job:      name,
			attempt:  attempt,
			trigger:  trigger,
			duration: r.clock.Now().Sub(start),
			err:      err,
		}

		switch {
		case err == nil:
			r.markSuccess(name)
			ex.result = ResultSuccess
			r.record(ctx, ex)
			return nil

		case ctx.Err() != nil:
			ex.result = ResultCancelled
			r.record(ctx, ex)
			r.logger().WarnContext(ctx, "job execution cancelled",
				"job", name, "attempt", attempt, "reason", err.Error())
			return err

		case attempt == budget:
			terminal := &TerminalError{Job: name, Attempts: budget, Err: err}
			ex.result = ResultFailed
			ex.err = terminal
			r.record(ctx, ex)
			r.logger().ErrorContext(ctx, "job failed terminally",
				"job", name, "attempts", budget, "reason", err.Error())
			return terminal

		default:
			ex.result = ResultError
			r.record(ctx, ex)
			r.logger().WarnContext(ctx, "job attempt failed",
				"job", name, "attempt", attempt, "error", err.Error())

			select {
			case <-ctx.Done():
				r.record(ctx, execution{job: name, attempt: attempt, trigger: trigger,
					result: ResultCancelled, err: ctx.Err()})
				return ctx.Err()
			case <-r.clock.After(r.Retry.delay(attempt, r.random)):
			}
		}
	}
	return nil
}

// attempt opens the execution span and runs the job once.
func (r *Runner) attempt(ctx context.Context, e *entry, trigger string, n int) error {
	name := e.job.Name()
	ctx, span := otel.Tracer(ScopeName).Start(ctx, "job "+name,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			jobNameKey.String(name),
			jobAttemptKey.Int(n),
			triggerKey.String(trigger),
		))
	defer span.End()

	if timeout := e.schedule.Timeout; timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	r.setRunning(name, 1)
	defer r.setRunning(name, -1)

	err := r.safeRun(ctx, e.job)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// safeRun turns a panic in a job into an error, so one broken job does not take
// the process and every other job with it.
func (r *Runner) safeRun(ctx context.Context, j Job) (err error) {
	defer func() {
		if v := recover(); v != nil {
			r.logger().ErrorContext(ctx, "job panicked",
				"job", j.Name(), "panic", fmt.Sprint(v), "stack", string(debug.Stack()))
			err = fmt.Errorf("worker: job %q panicked: %v", j.Name(), v)
		}
	}()
	return j.Run(ctx)
}

// record writes one execution to telemetry and to the test observer.
func (r *Runner) record(ctx context.Context, ex execution) {
	if r.instr != nil {
		r.instr.record(ctx, ex)
	}
	if r.onExecution != nil {
		r.onExecution(ex)
	}
}

// markSuccess resets the time since last success for one job.
func (r *Runner) markSuccess(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastSuccess[name] = r.clock.Now()
}

// sinceLastSuccess reports the seconds since each job last succeeded. It is the
// callback behind the observable gauge.
func (r *Runner) sinceLastSuccess() map[string]float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clock.Now()
	out := make(map[string]float64, len(r.lastSuccess))
	for name, at := range r.lastSuccess {
		seconds := now.Sub(at).Seconds()
		if seconds < 0 {
			seconds = 0
		}
		out[name] = seconds
	}
	return out
}

// setRunning adjusts the count of in-flight executions of one job.
func (r *Runner) setRunning(name string, delta int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.running[name] += delta
	if r.running[name] <= 0 {
		delete(r.running, name)
	}
}

// stillRunning reports the jobs with an execution in flight, in registration
// order, so the shutdown record names them the way the configuration does.
func (r *Runner) stillRunning() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, name := range r.order {
		if r.running[name] > 0 {
			out = append(out, name)
		}
	}
	return out
}

// shutdownTimeout reports the configured window or the default.
func (r *Runner) shutdownTimeout() time.Duration {
	if r.ShutdownTimeout <= 0 {
		return DefaultShutdownTimeout
	}
	return r.ShutdownTimeout
}

// logger reports the configured logger or the process default.
func (r *Runner) logger() *slog.Logger {
	if r.Logger == nil {
		return slog.Default()
	}
	return r.Logger
}
