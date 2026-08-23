package main

import (
	"context"
	"io"

	"example.com/service/internal/server"

	"example.com/service/internal/worker"
)

// This file is present when the repository selected the background feature. It
// assigns the seams the entry point calls, so the entry point itself never
// names the worker package and compiles without it.
func init() {
	readInvocation = readWorkerInvocation
	startBackground = registerJobs
}

// readWorkerInvocation reads the execution mode from the command line, so one
// image runs the web deployment, the worker deployment, and a one-off task.
func readWorkerInvocation(args []string, out io.Writer) (invocation, error) {
	inv, err := worker.ParseInvocation(args, out)
	if err != nil {
		return invocation{}, err
	}
	return invocation{serve: inv.Serves(), work: inv.Works(), job: inv.Job}, nil
}

// registerJobs builds the runner and reports how to run the jobs.
//
// The runner is not started here. The entry point decides whether the jobs run
// beside the listener, alone, or once, which is what makes the split between a
// web deployment and a worker deployment a deployment change and not a code
// change.
func registerJobs(_ context.Context, a *assembly) error {
	runner := worker.New()
	runner.Logger = a.logger
	// The in-process lock serialises a scheduled job inside one process only.
	// A deployment with more than one replica supplies a lock over shared
	// storage, a database row or a key with a time to live, or the same
	// schedule runs once per replica.
	runner.Locker = worker.NewMemoryLocker()

	registerServiceJobs(runner)

	a.addComponent(closeRunner(runner))
	a.work = runner.Run
	a.runJob = runner.RunOnce
	return nil
}

// registerServiceJobs registers the jobs this service owns. It is the one place
// a schedule, a queue consumer, or a one-off command is added.
func registerServiceJobs(r *worker.Runner) {
	_ = r
}

// closeRunner releases the runner's telemetry registration at shutdown. The
// gauge that reports the time since a job last succeeded is an observable
// instrument, and its callback outlives the runner unless it is unregistered.
func closeRunner(r *worker.Runner) server.Component {
	return server.Component{
		Name:  "jobs.telemetry",
		Start: func(context.Context) error { return nil },
		Stop:  func(context.Context) error { return r.Close() },
	}
}
