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

	"latere.ai/x/pkg/health"

	"example.com/service/internal/version"
)

// probe drives one probe path through the mounted routes without a listener.
func probe(t *testing.T, s *Server, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// TestLiveStaysUpWhileADependencyIsDown covers acceptance criterion 1. A
// restart does not fix an unreachable database, so liveness ignores it.
func TestLiveStaysUpWhileADependencyIsDown(t *testing.T) {
	s := newServer(nil)
	s.ready.Store(true)
	s.AddReadyCheck("database", func(context.Context) error { return errors.New("connection refused") })
	s.AddReadyCheck("cache", func(context.Context) error { return nil })

	if code, body := probe(t, s, LivePath); code != http.StatusOK || body != "ok\n" {
		t.Fatalf("GET %s = %d %q, want 200 ok", LivePath, code, body)
	}

	code, body := probe(t, s, ReadyPath)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("GET %s = %d, want 503", ReadyPath, code)
	}
	// The body names the failing dependency and the reason it gave, and
	// says nothing about the one that passed.
	if body != "not ready: database: connection refused\n" {
		t.Errorf("body = %q, want the failing dependency named", body)
	}
}

// TestReadyWithoutChecksIsReady fixes the state of a service that registered
// no dependency: ready.
func TestReadyWithoutChecksIsReady(t *testing.T) {
	s := newServer(nil)
	s.ready.Store(true)

	if code, body := probe(t, s, ReadyPath); code != http.StatusOK || body != "ok\n" {
		t.Fatalf("GET %s = %d %q, want 200 ok", ReadyPath, code, body)
	}
}

// TestReadyNamesEveryFailingDependency proves the body lists each failure,
// in registration order, so one probe read says what is down.
func TestReadyNamesEveryFailingDependency(t *testing.T) {
	s := newServer(nil)
	s.ready.Store(true)
	s.AddReadyCheck("database", func(context.Context) error { return errors.New("refused") })
	s.AddReadyCheck("cache", func(context.Context) error { return errors.New("timeout") })
	s.AddReadyCheck("noop", nil)

	code, body := probe(t, s, ReadyPath)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("GET %s = %d, want 503", ReadyPath, code)
	}
	if body != "not ready: database: refused\ncache: timeout\n" {
		t.Errorf("body = %q, want both failures named", body)
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
	// The check ignores its context, which is the case the bound exists for.
	s.AddReadyCheck("slow", func(context.Context) error {
		<-release
		return nil
	})

	start := time.Now()
	code, body := probe(t, s, ReadyPath)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("probe took %v, want it bounded by the check timeout", elapsed)
	}
	if code != http.StatusServiceUnavailable {
		t.Fatalf("GET %s = %d, want 503", ReadyPath, code)
	}
	if !strings.HasPrefix(body, "not ready: slow: ") || !strings.Contains(body, context.DeadlineExceeded.Error()) {
		t.Errorf("body = %q, want the slow check failed with the deadline named", body)
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
	if body != "not ready: draining\n" {
		t.Errorf("body = %q, want draining reported", body)
	}
}

// TestVersionReportsTheCompiledBuild covers acceptance criterion 5.
func TestVersionReportsTheCompiledBuild(t *testing.T) {
	s := newServer(nil)

	code, body := probe(t, s, VersionPath)
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", VersionPath, code)
	}
	var got health.Build
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	b := version.Info()
	want := health.Build{Version: b.Version, Commit: b.Commit, BuildTime: b.BuildTime}
	if got != want {
		t.Fatalf("body = %+v, want %+v", got, want)
	}
}

// TestLegacyHealthzAliasesLivez holds the old liveness path up for one
// release while manifests move to /livez. It answers 200 whatever readiness
// says, because it is liveness and not readiness.
func TestLegacyHealthzAliasesLivez(t *testing.T) {
	s := newServer(nil)
	s.ready.Store(false)

	if code, body := probe(t, s, legacyHealthPath); code != http.StatusOK || body != "ok\n" {
		t.Fatalf("GET %s = %d %q, want 200 ok", legacyHealthPath, code, body)
	}
}

// TestProbesAnswerAheadOfTheApplicationHandler proves the probe paths are not
// reachable by the application handler, so no middleware chain can gate them.
func TestProbesAnswerAheadOfTheApplicationHandler(t *testing.T) {
	s := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the application handler served %s", r.URL.Path)
	}))
	s.ready.Store(true)

	for _, path := range []string{LivePath, ReadyPath, VersionPath, legacyHealthPath} {
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
