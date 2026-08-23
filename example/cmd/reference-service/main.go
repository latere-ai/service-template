// Command service is the service entry point. It reads configuration, installs
// telemetry, assembles the HTTP surface, and blocks on the process lifecycle.
//
// This file holds wiring and nothing else. Business logic lives in the packages
// it composes, so the order of start-up is readable in one place and a defect
// in a handler is never a defect in the entry point.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/example/reference-service/internal/auth"
	"github.com/example/reference-service/internal/config"
	"github.com/example/reference-service/internal/httpx"
	"github.com/example/reference-service/internal/observability"
	"github.com/example/reference-service/internal/server"
	"github.com/example/reference-service/internal/version"
)

// Exit codes. A usage failure is separated from a runtime failure because a
// deployment retries one and not the other.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

func main() {
	os.Exit(main1(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

// main1 holds the body so a test can drive every exit code without starting a
// process.
func main1(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	// A failed write to the process streams cannot be reported: the report
	// would go to the same stream. The error is dropped once, here, so the
	// start-up path below reads as start-up and not as error handling.
	fail := func(err error) int {
		_, _ = fmt.Fprintln(stderr, "service:", err)
		return exitError
	}

	args, showVersion := takeVersionFlag(args)
	if showVersion {
		if _, err := fmt.Fprintln(stdout, version.Info()); err != nil {
			return fail(err)
		}
		return exitOK
	}

	inv, err := readInvocation(args, stderr)
	switch {
	case errors.Is(err, flag.ErrHelp):
		// Help was requested and printed. Asking for help is a successful run.
		return exitOK
	case err != nil:
		_, _ = fmt.Fprintln(stderr, "service:", err)
		return exitUsage
	}

	// The process logs to standard output. Passing the destination in keeps
	// the start-up path drivable, so the record that reports the resolved
	// configuration is asserted rather than assumed.
	if err := run(ctx, inv, stdout); err != nil {
		return fail(err)
	}
	return exitOK
}

// invocation is what the command line asked the process to do. It is stated
// here rather than imported, because the flags that fill it belong to the
// background feature and the entry point compiles without that feature.
type invocation struct {
	// serve reports whether the process handles requests.
	serve bool
	// work reports whether the process runs scheduled and continuous jobs.
	work bool
	// job names the single job to run before exiting. It is empty unless the
	// process was invoked to run one.
	job string
}

// Feature seams. A repository that did not select a feature holds no file
// assigning the seam, and start-up skips that step. This is what lets one entry
// point compile whatever subset of the features the repository selected,
// without a build tag and without a package that exists only to be empty.
var (
	// readInvocation reads the execution mode from the command line. The
	// background feature replaces it with the mode flags.
	readInvocation = serveOnly

	// openDatabase connects the store, registers its readiness check, and
	// registers its shutdown. The database feature assigns it.
	openDatabase func(ctx context.Context, a *assembly) error

	// mountFrontend replaces the fallback handler with the single-page
	// application. The frontend feature assigns it.
	mountFrontend func(a *assembly) error

	// startBackground registers the jobs and reports how to run them. The
	// background feature assigns it.
	startBackground func(ctx context.Context, a *assembly) error

	// loadConfig reads the process configuration. It is a variable so a test
	// can drive start-up without the process command line.
	loadConfig = config.Load
)

// serveOnly is the invocation reader of a repository without the background
// feature: the process serves requests and takes no arguments.
func serveOnly(args []string, _ io.Writer) (invocation, error) {
	if len(args) > 0 {
		return invocation{}, fmt.Errorf("unexpected argument %q", args[0])
	}
	return invocation{serve: true}, nil
}

// takeVersionFlag removes the build-identity flag from the arguments and
// reports whether it was present.
//
// It is read before the mode flags because the flag has to answer in every
// configuration, including one whose mode parser would reject it, and because
// an image is probed for its build identity before it is trusted to start.
func takeVersionFlag(args []string) ([]string, bool) {
	rest := make([]string, 0, len(args))
	found := false
	for i, a := range args {
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		if a == "-version" || a == "--version" {
			found = true
			continue
		}
		rest = append(rest, a)
	}
	return rest, found
}

// assembly is the service under construction. Each start-up step fills the
// slots it owns, and the entry point turns the result into a running server.
type assembly struct {
	cfg    *config.Config
	logger *slog.Logger

	// guard authenticates and authorizes every application route.
	guard *auth.Guard
	// routes is the application route table. Every route carries a policy,
	// which is what makes an undecided route fail before it serves.
	routes *auth.RouteTable
	// fallback answers a path no route claimed. It is the error envelope
	// until the frontend feature replaces it with the application shell.
	fallback http.Handler

	// components are started in registration order and stopped in reverse.
	components []server.Component
	// ready maps a dependency name to its readiness check.
	ready []readyCheck

	// work runs the scheduled and continuous jobs until the context ends.
	work func(context.Context) error
	// runJob runs one named job to completion.
	runJob func(context.Context, string) error
}

// readyCheck is one named readiness check awaiting a server to register on.
type readyCheck struct {
	name string
	fn   func(context.Context) error
}

// addComponent registers a dependency with a lifecycle.
func (a *assembly) addComponent(c server.Component) { a.components = append(a.components, c) }

// addReadyCheck registers a readiness check.
func (a *assembly) addReadyCheck(name string, fn func(context.Context) error) {
	a.ready = append(a.ready, readyCheck{name: name, fn: fn})
}

// run builds the service and blocks until the process is asked to stop.
//
// The order is fixed and each step depends on the one before it: configuration
// is read before anything reads a value, telemetry is installed before the
// logger any component captures is taken, the dependencies are opened before
// the readiness checks that report them, and the route table is validated
// before the listener binds.
func run(ctx context.Context, inv invocation, logOutput io.Writer) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	// Telemetry is installed before anything else is constructed. It replaces
	// the process logger, and every component built afterwards therefore logs
	// through the exporting handler and inside the request trace.
	shutdownTelemetry, err := observability.Setup(ctx, observability.Options{
		ServiceName:  cfg.ServiceName,
		Environment:  cfg.Environment,
		SampleRatio:  cfg.SampleRatio,
		OTLPEndpoint: cfg.OTLPEndpoint,
		OTLPHeaders:  otlpHeaders(cfg),
		LogLevel:     cfg.LogLevel,
		LogFormat:    observability.LogFormat(cfg.LogFormat),
		LogOutput:    logOutput,
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := shutdownTelemetry(context.WithoutCancel(ctx)); err != nil {
			slog.Error("telemetry shutdown", "error", err)
		}
	}()

	// The resolved configuration is recorded once at start-up, so an incident
	// is diagnosed against what the process read rather than against what a
	// deployment manifest was believed to say. Secrets render as redacted.
	slog.Info("configuration loaded", "config", cfg)

	a := newAssembly(cfg)

	if openDatabase != nil {
		if err := openDatabase(ctx, a); err != nil {
			return err
		}
	}
	if startBackground != nil {
		if err := startBackground(ctx, a); err != nil {
			return err
		}
	}

	registerRoutes(a)
	// A route with no decision fails here rather than serving open. The check
	// runs before the listener binds, so a missing policy is a start-up
	// failure and never a request that was allowed by accident.
	if err := a.routes.Validate(); err != nil {
		return err
	}

	if mountFrontend != nil {
		if err := mountFrontend(a); err != nil {
			return err
		}
	}

	if inv.job != "" {
		return runNamedJob(ctx, a, inv.job)
	}
	return serve(ctx, a, inv)
}

// newAssembly builds the parts every configuration has.
func newAssembly(cfg *config.Config) *assembly {
	a := &assembly{cfg: cfg, logger: slog.Default()}
	a.guard = &auth.Guard{
		// The scaffold identifies every caller as anonymous, so a public route
		// serves and a guarded route is denied. A service replaces this with
		// the verifier for its own credentials, for example
		// auth.BearerAuthenticator or auth.StaticKeyAuthenticator, and a
		// guarded route then admits the identities that carry the scope.
		Authenticator: auth.AnonymousAuthenticator{},
		Logger:        a.logger,
		OnDeny:        denyWithEnvelope,
	}
	a.routes = auth.NewRouteTable(a.guard)
	a.fallback = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, r, httpx.New(http.StatusNotFound, "no route matches this path"))
	})
	return a
}

// registerRoutes registers the application routes. It is the one place a
// service adds its own surface, and every route states its policy, because an
// undecided route fails Validate before the listener binds.
//
// The probe paths are not here: the runtime owns /livez, /readyz, and /version
// and serves them outside this chain, so a probe is answered while the
// application surface is draining.
func registerRoutes(a *assembly) {
	_ = a
}

// serve runs the HTTP surface, and the jobs alongside it when the invocation
// asked for both.
//
// The listener binds in every mode. An orchestrator probes a worker deployment
// the same way it probes a web deployment, and the runtime answers the probe
// paths outside the application chain.
func serve(ctx context.Context, a *assembly, inv invocation) error {
	srv := server.New(a.cfg, a.handler(inv.serve))
	srv.Logger = a.logger
	// The runtime carries its own defaults, so an unset budget keeps them
	// rather than becoming an immediate deadline.
	if a.cfg.StopTimeout > 0 {
		srv.StopTimeout = a.cfg.StopTimeout
	}
	if a.cfg.ReadyCheckTimeout > 0 {
		srv.ReadyCheckTimeout = a.cfg.ReadyCheckTimeout
	}
	for _, c := range a.components {
		srv.AddComponent(c)
	}
	for _, c := range a.ready {
		srv.AddReadyCheck(c.name, observedCheck(c.name, c.fn))
	}
	if inv.work && a.work != nil {
		srv.AddComponent(jobComponent(a.work))
	}
	return srv.Run(ctx)
}

// handler assembles the request chain in front of the route table.
//
// The router is passed to the chain, because only the router knows which
// registered pattern a request matched, and a telemetry label that falls back
// to the request path produces one time series per identifier.
func (a *assembly) handler(serving bool) http.Handler {
	application := a.route()
	if !serving {
		// A worker deployment keeps the probe paths, which the runtime serves
		// outside this chain, and refuses the application surface. A request
		// that reaches a process not built to answer it gets an honest status
		// rather than a route that silently does nothing.
		application = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			httpx.WriteError(w, r, httpx.New(http.StatusServiceUnavailable,
				"this process runs jobs and does not serve the application"))
		})
	}
	return httpx.Handler(application, httpx.Options{
		Logger:  a.logger,
		Router:  a.routes,
		Timeout: a.cfg.WriteTimeout,
	})
}

// route dispatches to the application route table, and to the fallback for a
// path no route claimed. The fallback is reached for every method, which is
// what lets the shell answer a client-side route and what keeps an unmatched
// path inside the error envelope.
func (a *assembly) route() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, pattern := a.routes.Handler(r); pattern != "" {
			h.ServeHTTP(w, r)
			return
		}
		a.fallback.ServeHTTP(w, r)
	})
}

// denyWithEnvelope renders an authentication or authorization denial as the
// service's error envelope, so a refusal carries the request identifier and the
// same shape as every other error.
//
// The denial reason never reaches the body. Naming the check that failed would
// tell a caller whether a credential is unknown, expired, or merely revoked.
func denyWithEnvelope(w http.ResponseWriter, r *http.Request, err error) {
	status, title, ok := auth.PublicStatus(err)
	if !ok {
		httpx.WriteError(w, r, err)
		return
	}
	httpx.WriteError(w, r, httpx.New(status, title))
}

// observedCheck records the outcome and the duration of a readiness check, so
// a dependency that was unreachable leaves a time series behind after the probe
// result itself is gone.
func observedCheck(name string, fn func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		start := time.Now()
		err := fn(ctx)
		observability.ObserveDependencyCheck(ctx, name, err, time.Since(start))
		return err
	}
}

// jobComponent adapts the job runner to the component lifecycle: it starts on
// its own goroutine and the stop waits for it to return.
func jobComponent(work func(context.Context) error) server.Component {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	return server.Component{
		Name: "jobs",
		Start: func(context.Context) error {
			go func() { done <- work(ctx) }()
			return nil
		},
		Stop: func(stopCtx context.Context) error {
			cancel()
			select {
			case err := <-done:
				return err
			case <-stopCtx.Done():
				return stopCtx.Err()
			}
		},
	}
}

// runNamedJob runs one job to completion and returns its result, which is the
// process exit status of a one-shot invocation.
func runNamedJob(ctx context.Context, a *assembly, name string) error {
	if a.runJob == nil {
		return fmt.Errorf("this service runs no jobs, so %q cannot be run", name)
	}
	for _, c := range a.components {
		if err := c.Start(ctx); err != nil {
			return err
		}
	}
	defer stopComponents(a.components)
	return a.runJob(ctx, name)
}

// stopComponents stops every started component in reverse registration order.
func stopComponents(components []server.Component) {
	for _, c := range slices.Backward(components) {
		if err := c.Stop(context.Background()); err != nil {
			slog.Error("component stop", "component", c.Name, "error", err)
		}
	}
}

// otlpHeaders reads the collector credentials from the configuration. A list
// that cannot be parsed is reported and dropped: the exporter then falls back
// to the environment rather than refusing to start the service.
func otlpHeaders(cfg *config.Config) map[string]string {
	raw := cfg.OTLPHeaders.Reveal()
	if raw == "" {
		return nil
	}
	headers, err := observability.ParseOTLPHeaders(raw)
	if err != nil {
		slog.Warn("the collector header list was not read", "error", err)
		return nil
	}
	return headers
}
