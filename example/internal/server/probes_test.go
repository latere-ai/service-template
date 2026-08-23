package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/reference-service/internal/version"
)

// probe drives one probe path through the mounted routes without a listener.
func probe(t *testing.T, s *Server, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// decodeReady reads the readiness body.
func decodeReady(t *testing.T, body string) readyResponse {
	t.Helper()
	var got readyResponse
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return got
}

// TestLiveStaysUpWhileADependencyIsDown covers acceptance criterion 1. A
// restart does not fix an unreachable database, so liveness ignores it.
func TestLiveStaysUpWhileADependencyIsDown(t *testing.T) {
	s := newServer(nil)
	s.ready.Store(true)
	s.AddReadyCheck("database", func(context.Context) error { return errors.New("connection refused") })
	s.AddReadyCheck("cache", func(context.Context) error { return nil })

	if code, _ := probe(t, s, LivePath); code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", LivePath, code)
	}

	code, body := probe(t, s, ReadyPath)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("GET %s = %d, want 503", ReadyPath, code)
	}
	got := decodeReady(t, body)
	if got.Status != statusFail {
		t.Errorf("status = %q, want %q", got.Status, statusFail)
	}
	if len(got.Checks) != 2 {
		t.Fatalf("checks = %v, want two entries", got.Checks)
	}
	// Sorted by name, so the body is byte-identical between requests.
	if got.Checks[0].Name != "cache" || got.Checks[0].Status != statusOK {
		t.Errorf("checks[0] = %+v, want cache ok", got.Checks[0])
	}
	if got.Checks[1].Name != "database" || got.Checks[1].Status != statusFail {
		t.Errorf("checks[1] = %+v, want database fail", got.Checks[1])
	}
	if !strings.Contains(got.Checks[1].Error, "connection refused") {
		t.Errorf("database error = %q, want the reason the dependency gave", got.Checks[1].Error)
	}
}

// TestReadyWithoutChecksIsReady fixes the state of a service that registered
// no dependency: ready, with an empty list rather than a missing field.
func TestReadyWithoutChecksIsReady(t *testing.T) {
	s := newServer(nil)
	s.ready.Store(true)

	code, body := probe(t, s, ReadyPath)
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", ReadyPath, code)
	}
	got := decodeReady(t, body)
	if got.Status != statusOK {
		t.Errorf("status = %q, want %q", got.Status, statusOK)
	}
	if len(got.Checks) != 0 {
		t.Errorf("checks = %v, want none", got.Checks)
	}
}

// TestReadyCheckHonoursItsTimeout proves a hanging dependency fails the probe
// instead of holding the response open until the orchestrator gives up.
func TestReadyCheckHonoursItsTimeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	s := newServer(nil)
	s.ready.Store(true)
	s.ReadyCheckTimeout = 20 * time.Millisecond
	s.AddReadyCheck("slow", func(ctx context.Context) error {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return ctx.Err()
	})

	start := time.Now()
	code, body := probe(t, s, ReadyPath)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("probe took %v, want it bounded by the check timeout", elapsed)
	}
	if code != http.StatusServiceUnavailable {
		t.Fatalf("GET %s = %d, want 503", ReadyPath, code)
	}
	got := decodeReady(t, body)
	if len(got.Checks) != 1 || got.Checks[0].Name != "slow" || got.Checks[0].Status != statusFail {
		t.Fatalf("checks = %+v, want the slow check failed", got.Checks)
	}
	if !strings.Contains(got.Checks[0].Error, context.DeadlineExceeded.Error()) {
		t.Errorf("error = %q, want the deadline named", got.Checks[0].Error)
	}
}

// TestReadyReportsDrainingWithoutRunningChecks covers the drain state: the
// answer is already no, so a dependency call would add nothing.
func TestReadyReportsDrainingWithoutRunningChecks(t *testing.T) {
	s := newServer(nil)
	s.ready.Store(false)
	s.AddReadyCheck("database", func(context.Context) error {
		t.Error("a readiness check ran while the service was draining")
		return nil
	})

	code, body := probe(t, s, ReadyPath)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("GET %s = %d, want 503", ReadyPath, code)
	}
	if got := decodeReady(t, body); got.Status != statusDraining {
		t.Errorf("status = %q, want %q", got.Status, statusDraining)
	}
}

// TestVersionReportsTheCompiledBuild covers acceptance criterion 5.
func TestVersionReportsTheCompiledBuild(t *testing.T) {
	s := newServer(nil)

	code, body := probe(t, s, VersionPath)
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", VersionPath, code)
	}
	var got versionResponse
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	b := version.Info()
	want := versionResponse{Version: b.Version, Commit: b.Commit, BuildTime: b.BuildTime, AssetHash: b.AssetHash}
	if got != want {
		t.Fatalf("body = %+v, want %+v", got, want)
	}
}

// TestProbesAnswerAheadOfTheApplicationHandler proves the probe paths are not
// reachable by the application handler, so no middleware chain can gate them.
func TestProbesAnswerAheadOfTheApplicationHandler(t *testing.T) {
	s := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the application handler served %s", r.URL.Path)
	}))
	s.ready.Store(true)

	for _, path := range []string{LivePath, ReadyPath, VersionPath} {
		if code, _ := probe(t, s, path); code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, code)
		}
	}
}

// TestApplicationHandlerServesEverythingElse fixes the routing boundary.
func TestApplicationHandlerServesEverythingElse(t *testing.T) {
	s := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte("app")); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))

	if code, body := probe(t, s, "/api/things"); code != http.StatusOK || body != "app" {
		t.Fatalf("GET /api/things = %d %q, want 200 %q", code, body, "app")
	}
}
