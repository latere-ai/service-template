package server

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"time"

	"example.com/service/internal/version"
)

// Probe paths. They are fixed by the runtime contract so an orchestrator
// manifest, a smoke script, and a dashboard hold the same three strings for
// every service.
const (
	// LivePath reports that the process is not deadlocked.
	LivePath = "/livez"
	// ReadyPath reports that every registered dependency is reachable.
	ReadyPath = "/readyz"
	// VersionPath reports the build identity of the running binary.
	VersionPath = "/version"
)

// Reported states in the readiness body.
const (
	statusOK       = "ok"
	statusFail     = "fail"
	statusDraining = "draining"
)

// checkResult is one dependency's state in the readiness body.
type checkResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// readyResponse is the /readyz body. It names each check so a failing probe
// identifies the dependency that is down instead of only the replica.
type readyResponse struct {
	Status string        `json:"status"`
	Checks []checkResult `json:"checks"`
}

// versionResponse is the /version body. The release pipeline reads it to prove
// the intended build is serving.
type versionResponse struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	AssetHash string `json:"asset_hash"`
}

// routes mounts the probes ahead of the application handler. The probes are
// registered here rather than by the caller so no middleware chain can put
// authentication or rate limiting in front of a liveness check.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(LivePath, s.handleLive)
	mux.HandleFunc(ReadyPath, s.handleReady)
	mux.HandleFunc(VersionPath, s.handleVersion)
	mux.Handle("/", s.handler)
	return s.countInFlight(mux)
}

// handleLive answers without touching a dependency. A restart does not fix an
// unreachable database, and a restart loop makes the outage worse.
func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, r, http.StatusOK, map[string]string{"status": statusOK})
}

// handleReady runs every registered check with a per-check timeout.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if !s.ready.Load() {
		// During the drain window the listener is still open on purpose. The
		// checks are skipped because the answer is already no.
		s.writeJSON(w, r, http.StatusServiceUnavailable, readyResponse{
			Status: statusDraining,
			Checks: []checkResult{},
		})
		return
	}

	results := s.runReadyChecks(r.Context())
	body := readyResponse{Status: statusOK, Checks: results}
	code := http.StatusOK
	for _, result := range results {
		if result.Status != statusOK {
			body.Status = statusFail
			code = http.StatusServiceUnavailable
			break
		}
	}
	s.writeJSON(w, r, code, body)
}

// runReadyChecks runs the checks concurrently, each under its own timeout, and
// returns the results sorted by name so the body is deterministic.
func (s *Server) runReadyChecks(ctx context.Context) []checkResult {
	s.mu.Lock()
	checks := make([]readyCheck, len(s.checks))
	copy(checks, s.checks)
	timeout := s.ReadyCheckTimeout
	s.mu.Unlock()

	if timeout <= 0 {
		timeout = DefaultReadyCheckTimeout
	}

	results := make([]checkResult, len(checks))
	var wg sync.WaitGroup
	for i, check := range checks {
		wg.Go(func() { results[i] = runReadyCheck(ctx, check, timeout) })
	}
	wg.Wait()

	slices.SortFunc(results, func(a, b checkResult) int { return cmp.Compare(a.Name, b.Name) })
	return results
}

// runReadyCheck runs one check under a bounded context. A check that hangs
// fails the probe instead of holding the response open.
func runReadyCheck(ctx context.Context, check readyCheck, timeout time.Duration) checkResult {
	result := checkResult{Name: check.name, Status: statusOK}
	if check.fn == nil {
		return result
	}

	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- check.fn(checkCtx) }()

	select {
	case err := <-done:
		if err != nil {
			result.Status = statusFail
			result.Error = err.Error()
		}
	case <-checkCtx.Done():
		result.Status = statusFail
		result.Error = checkCtx.Err().Error()
	}
	return result
}

// handleVersion reports the build metadata compiled into the binary.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	b := version.Info()
	s.writeJSON(w, r, http.StatusOK, versionResponse{
		Version:   b.Version,
		Commit:    b.Commit,
		BuildTime: b.BuildTime,
		AssetHash: b.AssetHash,
	})
}

// writeJSON writes a probe body. A probe answers with its own encoder rather
// than the API error envelope, because an orchestrator reads these paths
// before any application middleware is reachable.
func (s *Server) writeJSON(w http.ResponseWriter, r *http.Request, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already written, so the response cannot carry the
		// failure. The record is the only place it can surface.
		s.logger().WarnContext(r.Context(), "write probe response", "path", r.URL.Path, "error", err.Error())
	}
}
