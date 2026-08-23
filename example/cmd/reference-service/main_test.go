package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/reference-service/internal/auth"
	"github.com/example/reference-service/internal/config"
	"github.com/example/reference-service/internal/httpx"
	"github.com/example/reference-service/internal/server"
)

func TestTheVersionFlagReportsTheBuildAndExitsCleanly(t *testing.T) {
	var out, errs strings.Builder
	if code := main1(context.Background(), []string{"-version"}, &out, &errs); code != exitOK {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, exitOK, errs.String())
	}
	for _, field := range []string{"version=", "commit=", "built=", "assets="} {
		if !strings.Contains(out.String(), field) {
			t.Errorf("the output does not carry %q: %q", field, out.String())
		}
	}
}

// The build identity has to answer in every configuration, including one whose
// mode parser would reject the flag, because an image is probed for its build
// before it is trusted to start.
func TestTheVersionFlagIsReadBeforeTheModeFlags(t *testing.T) {
	tests := map[string]struct {
		args []string
		rest []string
		want bool
	}{
		"absent":            {args: []string{"-mode", "work"}, rest: []string{"-mode", "work"}, want: false},
		"single hyphen":     {args: []string{"-version"}, rest: []string{}, want: true},
		"double hyphen":     {args: []string{"--version"}, rest: []string{}, want: true},
		"beside a mode":     {args: []string{"-mode=work", "-version"}, rest: []string{"-mode=work"}, want: true},
		"after a separator": {args: []string{"--", "-version"}, rest: []string{"--", "-version"}, want: false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			rest, found := takeVersionFlag(tc.args)
			if found != tc.want {
				t.Errorf("found = %v, want %v", found, tc.want)
			}
			if strings.Join(rest, " ") != strings.Join(tc.rest, " ") {
				t.Errorf("remaining arguments = %v, want %v", rest, tc.rest)
			}
		})
	}
}

func TestAnUnknownArgumentIsAUsageFailure(t *testing.T) {
	var out, errs strings.Builder
	code := main1(context.Background(), []string{"serve-please"}, &out, &errs)
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(errs.String(), "serve-please") {
		t.Errorf("the message does not name the argument: %q", errs.String())
	}
}

// newTestAssembly builds the assembly the entry point builds, without reading
// the environment.
func newTestAssembly(t *testing.T) *assembly {
	t.Helper()
	return newAssembly(&config.Config{
		ServiceName:  "widget",
		Environment:  "test",
		Addr:         "127.0.0.1:0",
		WriteTimeout: 5 * time.Second,
	})
}

// serveThrough sends one request through the assembled chain.
func serveThrough(a *assembly, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	a.handler(true).ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// A path no route claims is answered inside the error envelope, so a client
// parses one shape whether the failure came from a handler or from the router.
func TestAnUnmatchedPathIsAnsweredWithTheEnvelope(t *testing.T) {
	a := newTestAssembly(t)
	rec := serveThrough(a, http.MethodGet, "/nothing/here")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if got := rec.Header().Get("Content-Type"); got != httpx.ProblemContentType {
		t.Errorf("content type = %q, want %q", got, httpx.ProblemContentType)
	}
	var problem httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("the body is not the envelope: %v: %s", err, rec.Body.String())
	}
	if problem.Status != http.StatusNotFound || problem.Type == "" || problem.Title == "" {
		t.Errorf("the envelope is incomplete: %+v", problem)
	}
}

// A guarded route reached without a credential is refused with the envelope and
// the status of its denial class, not with the 500 an unrecognized error would
// produce.
func TestAGuardedRouteIsRefusedInsideTheEnvelope(t *testing.T) {
	a := newTestAssembly(t)
	a.routes.HandleFunc(http.MethodGet, "/v1/orders", auth.Guarded("read", "orders"),
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	rec := serveThrough(a, http.MethodGet, "/v1/orders")

	if rec.Code != http.StatusForbidden && rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want a denial status", rec.Code)
	}
	var problem httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("the refusal is not the envelope: %v: %s", err, rec.Body.String())
	}
	if problem.Status != rec.Code {
		t.Errorf("the envelope reports %d and the response is %d", problem.Status, rec.Code)
	}
	if rec.Header().Get(httpx.HeaderRequestID) == "" {
		t.Error("the refusal carries no request identifier")
	}
	if strings.Contains(strings.ToLower(problem.Detail), "anonymous") {
		t.Errorf("the denial reason reached the body: %q", problem.Detail)
	}
}

// A public route serves without a credential, which is what proves the guard
// admits as well as refuses.
func TestAPublicRouteServesWithoutACredential(t *testing.T) {
	a := newTestAssembly(t)
	a.routes.HandleFunc(http.MethodGet, "/v1/status", auth.PublicPolicy(),
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	if rec := serveThrough(a, http.MethodGet, "/v1/status"); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

// A route registered without a decision fails start-up rather than serving
// open, which is what deny by default means for the route table.
func TestARouteWithNoPolicyFailsBeforeTheListenerBinds(t *testing.T) {
	a := newTestAssembly(t)
	a.routes.HandleFunc(http.MethodGet, "/v1/undecided", auth.Policy{},
		func(w http.ResponseWriter, _ *http.Request) {})

	err := a.routes.Validate()
	if err == nil {
		t.Fatal("an undecided route passed validation")
	}
	if !strings.Contains(err.Error(), "/v1/undecided") {
		t.Errorf("the message does not name the route: %v", err)
	}
}

// The routes the scaffold registers are all decided. The check runs against the
// service's own table, so a route added later without a policy fails here.
func TestTheRegisteredRoutesAreAllDecided(t *testing.T) {
	a := newTestAssembly(t)
	registerRoutes(a)
	if err := a.routes.Validate(); err != nil {
		t.Fatalf("the service registers an undecided route: %v", err)
	}
}

// A worker deployment answers the application surface honestly instead of
// serving routes the process was not started to handle.
func TestAWorkerProcessRefusesTheApplicationSurface(t *testing.T) {
	a := newTestAssembly(t)
	a.routes.HandleFunc(http.MethodGet, "/v1/status", auth.PublicPolicy(),
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	rec := httptest.NewRecorder()
	a.handler(false).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/status", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestServeOnlyRejectsArguments(t *testing.T) {
	if _, err := serveOnly(nil, nil); err != nil {
		t.Fatalf("serveOnly with no arguments: %v", err)
	}
	inv, err := serveOnly([]string{}, nil)
	if err != nil || !inv.serve || inv.work || inv.job != "" {
		t.Fatalf("serveOnly = %+v, %v, want a serving invocation", inv, err)
	}
	if _, err := serveOnly([]string{"-mode", "work"}, nil); err == nil {
		t.Fatal("serveOnly accepted a mode flag it cannot honour")
	}
}

// A named job that no runner knows fails with a message naming it, rather than
// exiting zero and reporting that nothing ran.
func TestANamedJobWithoutARunnerFails(t *testing.T) {
	a := newTestAssembly(t)
	err := runNamedJob(context.Background(), a, "backfill")
	if err == nil || !strings.Contains(err.Error(), "backfill") {
		t.Fatalf("error = %v, want one naming the job", err)
	}
}

func TestANamedJobStartsAndStopsTheComponents(t *testing.T) {
	a := newTestAssembly(t)
	started, stopped, ran := false, false, false
	a.addComponent(server.Component{
		Name:  "store",
		Start: func(context.Context) error { started = true; return nil },
		Stop:  func(context.Context) error { stopped = true; return nil },
	})
	a.runJob = func(context.Context, string) error { ran = true; return nil }

	if err := runNamedJob(context.Background(), a, "backfill"); err != nil {
		t.Fatalf("runNamedJob: %v", err)
	}
	if !started || !ran || !stopped {
		t.Errorf("started=%v ran=%v stopped=%v, want every step", started, ran, stopped)
	}
}

var errStartFailed = errors.New("the pool refused to connect")

func TestANamedJobSurfacesAComponentFailure(t *testing.T) {
	a := newTestAssembly(t)
	a.addComponent(server.Component{
		Name:  "store",
		Start: func(context.Context) error { return errStartFailed },
		Stop:  func(context.Context) error { return nil },
	})
	a.runJob = func(context.Context, string) error {
		t.Error("the job ran with a failed dependency")
		return nil
	}
	if err := runNamedJob(context.Background(), a, "backfill"); !errors.Is(err, errStartFailed) {
		t.Fatalf("error = %v, want the component failure", err)
	}
}

// The job component runs the work on its own goroutine and the stop waits for
// it, which is the contract the runtime states for a component.
func TestTheJobComponentStartsAndWaitsForTheWork(t *testing.T) {
	returned := make(chan struct{})
	c := jobComponent(func(ctx context.Context) error {
		<-ctx.Done()
		close(returned)
		return nil
	})
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-returned:
		t.Fatal("Start blocked until the work finished")
	default:
	}
	if err := c.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	<-returned
}

func TestTheJobComponentReportsAStopTimeout(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	c := jobComponent(func(context.Context) error {
		<-release
		return nil
	})
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Stop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop = %v, want the bounded context error", err)
	}
}

// A readiness check keeps its result. The observation is a side effect and must
// never change what the probe reports.
func TestTheObservedCheckPassesTheResultThrough(t *testing.T) {
	want := errors.New("unreachable")
	if err := observedCheck("store", func(context.Context) error { return want })(context.Background()); !errors.Is(err, want) {
		t.Fatalf("error = %v, want the check result", err)
	}
	if err := observedCheck("store", func(context.Context) error { return nil })(context.Background()); err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
}

func TestTheCollectorHeadersAreReadFromTheConfiguration(t *testing.T) {
	headers := otlpHeaders(&config.Config{OTLPHeaders: config.Secret("api-key=secret,tenant=widget")})
	if headers["api-key"] != "secret" || headers["tenant"] != "widget" {
		t.Fatalf("headers = %v, want both pairs", headers)
	}
	if got := otlpHeaders(&config.Config{}); got != nil {
		t.Errorf("headers with no value = %v, want nil", got)
	}
	// A list that cannot be read leaves the exporter on its environment
	// fallback rather than refusing to start the service.
	if got := otlpHeaders(&config.Config{OTLPHeaders: config.Secret("not a pair")}); got != nil {
		t.Errorf("headers from an unreadable list = %v, want nil", got)
	}
}

// A registered path reached with a method nothing registered falls through to
// the fallback rather than to the router's own 405.
//
// The multiplexer reports no pattern for that request, and the fallback is
// method-agnostic on purpose: that is what lets the application shell answer a
// client-side route, and it means the router never reaches its method branch.
// The behaviour is pinned here because it is a consequence of the mounting and
// not of any single handler.
func TestARegisteredPathWithAnUnregisteredMethodReachesTheFallback(t *testing.T) {
	a := newTestAssembly(t)
	a.routes.HandleFunc(http.MethodGet, "/v1/orders", auth.PublicPolicy(),
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	reached := false
	a.fallback = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		httpx.WriteError(w, r, httpx.New(http.StatusNotFound, "no route matches this path"))
	})

	rec := serveThrough(a, http.MethodDelete, "/v1/orders")

	if !reached {
		t.Fatal("the request reached the router's own method branch, not the fallback")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if got := rec.Header().Get("Content-Type"); got != httpx.ProblemContentType {
		t.Errorf("content type = %q, want the envelope", got)
	}
}
