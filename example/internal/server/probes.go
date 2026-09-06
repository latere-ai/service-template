package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"latere.ai/x/pkg/health"

	"github.com/example/reference-service/internal/version"
)

// Probe paths. They are fixed by the fleet's runtime contract (pkg/health) so
// an orchestrator manifest, a smoke script, and a dashboard hold the same
// three strings for every service.
const (
	// LivePath reports that the process is not deadlocked.
	LivePath = "/livez"
	// ReadyPath reports that every registered dependency is reachable.
	ReadyPath = "/readyz"
	// VersionPath reports the build identity of the running binary.
	VersionPath = "/version"
	// legacyHealthPath is the old liveness path. It stays an alias of
	// LivePath for one release while manifests move, then goes.
	legacyHealthPath = "/healthz"
)

// errDraining is the readiness error during the drain window.
var errDraining = errors.New("draining")

// routes mounts the probes ahead of the application handler. The probes are
// registered here rather than by the caller so no middleware chain can put
// authentication or rate limiting in front of a liveness check.
func (s *Server) routes() http.Handler {
	b := version.Info()
	probes := health.Handler(health.Options{
		Ready:         s.readiness,
		Version:       b.Version,
		Commit:        b.Commit,
		BuildTime:     b.BuildTime,
		LegacyHealthz: true,
	})
	mux := http.NewServeMux()
	for _, p := range []string{LivePath, ReadyPath, VersionPath, legacyHealthPath} {
		mux.Handle(p, probes)
	}
	mux.Handle("/", s.handler)
	return s.countInFlight(mux)
}

// readiness is the /readyz check. During the drain window the answer is
// already no, so the dependency checks are skipped. Otherwise every
// registered check runs concurrently under its own timeout, and
// health.Checks folds the results so the body names each failing
// dependency rather than only that one failed.
func (s *Server) readiness(ctx context.Context) error {
	if !s.ready.Load() {
		return errDraining
	}
	s.mu.Lock()
	checks := make([]readyCheck, len(s.checks))
	copy(checks, s.checks)
	timeout := s.ReadyCheckTimeout
	s.mu.Unlock()
	if timeout <= 0 {
		timeout = DefaultReadyCheckTimeout
	}

	results := make([]error, len(checks))
	done := make(chan struct{})
	for i, check := range checks {
		go func() {
			results[i] = runReadyCheck(ctx, check, timeout)
			done <- struct{}{}
		}()
	}
	for range checks {
		<-done
	}

	folded := make([]health.Check, len(checks))
	for i, check := range checks {
		err := results[i]
		folded[i] = health.Check{Name: check.name, Run: func(context.Context) error { return err }}
	}
	return health.Checks(folded...)(ctx)
}

// runReadyCheck runs one check under a bounded context. A check that hangs
// fails the probe instead of holding the response open.
func runReadyCheck(ctx context.Context, check readyCheck, timeout time.Duration) error {
	if check.fn == nil {
		return nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- check.fn(checkCtx) }()

	select {
	case err := <-done:
		return err
	case <-checkCtx.Done():
		return checkCtx.Err()
	}
}
