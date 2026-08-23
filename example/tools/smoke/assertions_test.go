package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// service is a fake target. Each field is what the live service reports, so a
// test states the fault it wants and reads the assertion that catches it.
type service struct {
	readyStatus  int
	readyBody    readyBody
	versionBody  versionBody
	document     string
	failReadyFor int32
	readyCalls   atomic.Int32
}

func (s *service) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(readyPath, func(w http.ResponseWriter, r *http.Request) {
		if s.readyCalls.Add(1) <= s.failReadyFor {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(readyBody{Status: "fail"})
			return
		}
		w.WriteHeader(s.readyStatus)
		_ = json.NewEncoder(w).Encode(s.readyBody)
	})
	mux.HandleFunc(versionPath, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(s.versionBody)
	})
	mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, s.document)
	})
	return mux
}

// healthy is a target serving the release the test expects.
func healthy() *service {
	return &service{
		readyStatus: http.StatusOK,
		readyBody: readyBody{
			Status: "ok",
			Checks: []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
				Error  string `json:"error,omitempty"`
			}{{Name: "postgres", Status: "ok"}},
		},
		versionBody: versionBody{Version: "v1.2.3", Commit: "abc123", AssetHash: "index-C3xK9pQ2.js"},
		document:    `<html><body><script src="/assets/index-C3xK9pQ2.js"></script></body></html>`,
	}
}

// start serves the fake and returns a config pointed at it.
func start(t *testing.T, s *service) Config {
	t.Helper()
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)
	return Config{
		BaseURL:        srv.URL,
		Target:         "test",
		ExpectVersion:  "v1.2.3",
		ExpectCommit:   "abc123",
		ExpectAsset:    "index-C3xK9pQ2.js",
		ChecksPath:     "checks.yaml",
		Window:         time.Second,
		Backoff:        time.Millisecond,
		RequestTimeout: 5 * time.Second,
	}
}

// runAssertions executes every assertion with no real waiting.
func runAssertions(t *testing.T, cfg Config) []Result {
	t.Helper()
	list, err := Assertions(cfg, &http.Client{Timeout: cfg.RequestTimeout})
	if err != nil {
		t.Fatalf("Assertions: %v", err)
	}
	return RunAll(context.Background(), list, cfg.Window, cfg.Backoff, func(time.Duration) {})
}

// find returns the named result.
func find(t *testing.T, results []Result, name string) Result {
	t.Helper()
	for _, r := range results {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no assertion named %q in %v", name, names(results))
	return Result{}
}

func names(results []Result) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.Name)
	}
	return out
}

func TestHealthyTargetPassesEveryAssertion(t *testing.T) {
	results := runAssertions(t, start(t, healthy()))
	if len(Failures(results)) != 0 {
		t.Fatalf("healthy target failed: %v", Failures(results))
	}
	for _, r := range results {
		if r.Observed == "" {
			t.Errorf("assertion %q recorded no observed value", r.Name)
		}
		if r.Attempts < 1 {
			t.Errorf("assertion %q recorded %d attempts", r.Name, r.Attempts)
		}
	}
}

func TestReadinessNamesTheFailingDependency(t *testing.T) {
	s := healthy()
	s.readyStatus = http.StatusServiceUnavailable
	s.readyBody.Status = "fail"
	s.readyBody.Checks[0].Status = "fail"
	s.readyBody.Checks[0].Error = "dial tcp: connection refused"

	r := find(t, runAssertions(t, start(t, s)), "readiness")
	if r.OK() {
		t.Fatal("readiness passed while a dependency was down")
	}
	if !strings.Contains(r.Observed, "postgres=fail") {
		t.Errorf("observed value %q does not name the failing dependency", r.Observed)
	}
}

// A rollout that leaves the previous replicas serving reports the previous
// version, which is the fault the build identity assertion exists to catch.
func TestStaleVersionFailsBuildIdentity(t *testing.T) {
	s := healthy()
	s.versionBody.Version = "v1.2.2"

	r := find(t, runAssertions(t, start(t, s)), "build identity: version")
	if r.OK() {
		t.Fatal("build identity passed against the previous version")
	}
	if r.Observed != "v1.2.2" {
		t.Errorf("observed = %q, want the version the target reported", r.Observed)
	}
}

func TestStaleCommitFailsBuildIdentity(t *testing.T) {
	s := healthy()
	s.versionBody.Commit = "deadbee"

	r := find(t, runAssertions(t, start(t, s)), "build identity: commit")
	if r.OK() || r.Observed != "deadbee" {
		t.Fatalf("commit assertion = %+v, want a failure naming the observed commit", r)
	}
}

// The binary reports the released asset but the document still references the
// previous bundle. Everything else about this deploy looks successful.
func TestPreviousBundleServedFailsTheServedBundleAssertion(t *testing.T) {
	s := healthy()
	s.document = `<html><body><script src="/assets/index-OLDHASH1.js"></script></body></html>`

	results := runAssertions(t, start(t, s))
	r := find(t, results, "served bundle")
	if r.OK() {
		t.Fatal("served bundle passed while the previous bundle was served")
	}
	if !strings.Contains(r.Observed, "index-OLDHASH1.js") {
		t.Errorf("observed = %q, want the reference the document actually carried", r.Observed)
	}
	if !find(t, results, "build identity: version").OK() {
		t.Error("the version assertion should still pass; only the bundle is stale")
	}
}

func TestReportedAssetHashMismatchFails(t *testing.T) {
	s := healthy()
	s.versionBody.AssetHash = "index-OTHER123.js"

	r := find(t, runAssertions(t, start(t, s)), "build identity: entry asset")
	if r.OK() || r.Observed != "index-OTHER123.js" {
		t.Fatalf("entry asset assertion = %+v, want a failure naming the observed asset", r)
	}
}

func TestNoFrontendOmitsTheBundleAssertions(t *testing.T) {
	cfg := start(t, healthy())
	cfg.ExpectAsset = NoFrontend
	for _, r := range runAssertions(t, cfg) {
		if r.Name == "served bundle" || r.Name == "build identity: entry asset" {
			t.Errorf("assertion %q ran for a service with no frontend", r.Name)
		}
	}
}

// A rollout completes slightly before the load balancer converges, so the
// window absorbs a short unhealthy period and the evidence records how many
// attempts it took.
func TestRetryRecordsTheAttemptCount(t *testing.T) {
	s := healthy()
	s.failReadyFor = 2

	r := find(t, runAssertions(t, start(t, s)), "readiness")
	if !r.OK() {
		t.Fatalf("readiness failed inside the window: %v", r.Err)
	}
	if r.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", r.Attempts)
	}
}

func TestConsumerCheckFailureNamesTheStatus(t *testing.T) {
	cfg := start(t, healthy())
	path := write(t, t.TempDir(), "checks.yaml", "checks:\n  - name: widgets\n    path: /v1/widgets\n    status: 204\n")
	cfg.ChecksPath = path

	r := find(t, runAssertions(t, cfg), "widgets")
	if r.OK() {
		t.Fatal("a consumer check passed against a path the target does not serve")
	}
	if !strings.Contains(r.Observed, "200") {
		t.Errorf("observed = %q, want the status the target returned", r.Observed)
	}
}

func TestUnreachableTargetFailsWithoutHanging(t *testing.T) {
	cfg := Config{
		BaseURL:        "http://127.0.0.1:1",
		Target:         "test",
		ExpectVersion:  "v1",
		ExpectCommit:   "abc",
		ExpectAsset:    NoFrontend,
		ChecksPath:     "checks.yaml",
		Window:         50 * time.Millisecond,
		Backoff:        time.Millisecond,
		RequestTimeout: 50 * time.Millisecond,
	}
	results := runAssertions(t, cfg)
	if len(Failures(results)) != len(results) {
		t.Fatalf("%d of %d assertions passed against a closed port", len(results)-len(Failures(results)), len(results))
	}
	if results[0].Observed != "no response" {
		t.Errorf("observed = %q, want the no-response marker", results[0].Observed)
	}
}

func TestCancelledContextStopsRetrying(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assertion := Assertion{
		Name:     "always fails",
		Expected: "never",
		Check:    func(context.Context) (string, error) { return "no", fmt.Errorf("down") },
	}
	results := RunAll(ctx, []Assertion{assertion}, time.Minute, time.Millisecond, func(time.Duration) {})
	if results[0].Attempts != 1 {
		t.Errorf("Attempts = %d, want the run to stop after the first attempt", results[0].Attempts)
	}
}
