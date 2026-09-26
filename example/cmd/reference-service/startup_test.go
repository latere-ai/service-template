package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/reference-service/internal/config"
	"github.com/example/reference-service/internal/server"
)

// syncBuffer collects the log stream. The writer is used from the goroutine
// that runs the service and read from the test, so it carries its own lock.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startupConfig is a configuration a test can serve under: an ephemeral port,
// no drain wait, and budgets short enough that a shutdown is immediate.
func startupConfig() *config.Config {
	return &config.Config{
		ServiceName:       "widget",
		Environment:       "test",
		Addr:              "127.0.0.1:0",
		LogLevel:          slog.LevelInfo,
		LogFormat:         "json",
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       10 * time.Second,
		DrainDelay:        0,
		GracePeriod:       time.Second,
		StopTimeout:       time.Second,
		ReadyCheckTimeout: time.Second,
		SampleRatio:       1,
	}
}

// driveStartUp runs the whole start-up path against a configuration the test
// supplies, cancels it, and returns the log stream.
//
// It restores every global the run installs. The telemetry setup replaces the
// process logger, and a test that left that in place would change what every
// later test records.
func driveStartUp(t *testing.T, inv invocation, cfg *config.Config) (string, error) {
	t.Helper()

	previousLogger := slog.Default()
	previousLoad := loadConfig
	t.Cleanup(func() {
		slog.SetDefault(previousLogger)
		loadConfig = previousLoad
	})
	loadConfig = func() (*config.Config, error) { return cfg, nil }

	logs := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, inv, logs) }()

	// The listener is up once the start-up records are written. Waiting for
	// the serving record rather than sleeping keeps the test deterministic.
	deadline := time.After(10 * time.Second)
	for !strings.Contains(logs.String(), "configuration loaded") {
		select {
		case err := <-done:
			cancel()
			return logs.String(), err
		case <-deadline:
			cancel()
			<-done
			t.Fatalf("the service did not start; captured:\n%s", logs.String())
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	select {
	case err := <-done:
		return logs.String(), err
	case <-time.After(30 * time.Second):
		t.Fatalf("the service did not stop; captured:\n%s", logs.String())
		return "", nil
	}
}

// The resolved configuration is recorded once at start-up, so an incident is
// diagnosed against what the process read and not against what a deployment
// manifest was believed to say. Secrets are redacted by their type.
func TestStartUpRecordsTheResolvedConfiguration(t *testing.T) {
	cfg := startupConfig()
	// A secret is set so the record can be checked for it. The database
	// connection string is left empty on purpose: opening a pool is a
	// dependency this test does not have and does not need.
	cfg.OTLPHeaders = config.Secret("api-key=hunter2")

	logs, err := driveStartUp(t, invocation{serve: true}, cfg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	record := findRecord(t, logs, "configuration loaded")
	if record["config"] == nil {
		t.Fatalf("the record carries no configuration: %v", record)
	}
	if strings.Contains(logs, "hunter2") {
		t.Error("a secret reached the log stream")
	}
	rendered, err := json.Marshal(record["config"])
	if err != nil {
		t.Fatalf("re-encode the configuration: %v", err)
	}
	for _, field := range []string{"SERVICE_NAME", "ADDR", "OTEL_EXPORTER_OTLP_HEADERS"} {
		if !strings.Contains(string(rendered), field) {
			t.Errorf("the record does not report %s: %s", field, rendered)
		}
	}
}

// findRecord returns the first log record with the given message.
func findRecord(t *testing.T, logs, message string) map[string]any {
	t.Helper()
	for line := range strings.SplitSeq(logs, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		if record["msg"] == message {
			return record
		}
	}
	t.Fatalf("no record with message %q; captured:\n%s", message, logs)
	return nil
}

// Telemetry is installed before the configuration record is written, so every
// record a component emits afterwards reaches the exporting handler. A record
// written through the process logger before Setup would go to the handler the
// runtime started with and never be exported.
func TestTelemetryIsInstalledBeforeTheFirstRecord(t *testing.T) {
	logs, err := driveStartUp(t, invocation{serve: true}, startupConfig())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// The record is JSON because the telemetry setup installed the handler the
	// configuration selected. A record written before Setup would carry the
	// text format the process starts with.
	if _, err := json.Marshal(findRecord(t, logs, "configuration loaded")); err != nil {
		t.Fatalf("the start-up record is not structured: %v", err)
	}
}

// A configuration that does not load stops the process before anything is
// constructed, and the exit code and the message say so. A service that
// started on a configuration it could not read would serve on defaults nobody
// chose.
func TestAConfigurationThatDoesNotLoadFailsTheProcess(t *testing.T) {
	previous := loadConfig
	t.Cleanup(func() { loadConfig = previous })
	loadConfig = func() (*config.Config, error) {
		return nil, errors.New("HTTP_IDLE_TIMEOUT: not a duration")
	}

	var out, errs strings.Builder
	if code := main1(context.Background(), nil, &out, &errs); code != exitError {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, exitError, errs.String())
	}
	if !strings.Contains(errs.String(), "service: HTTP_IDLE_TIMEOUT: not a duration") {
		t.Errorf("the message does not carry the configuration error: %q", errs.String())
	}
}

// A dependency that fails to open stops start-up: the listener never binds,
// so no replica reports ready over a store it cannot reach.
func TestADependencyThatFailsToOpenStopsStartUp(t *testing.T) {
	previous := openDatabase
	t.Cleanup(func() { openDatabase = previous })
	openDatabase = func(context.Context, *assembly) error { return errors.New("the store is unreachable") }

	_, err := driveStartUp(t, invocation{serve: true}, startupConfig())
	if err == nil || !strings.Contains(err.Error(), "the store is unreachable") {
		t.Fatalf("run = %v, want the open failure", err)
	}
}

// What a dependency registers while it opens is what the server runs: its
// component starts with the process and stops with it, and its readiness
// check is registered with the probe.
func TestADependencyRegisteredAtStartUpRunsWithTheServer(t *testing.T) {
	previous := openDatabase
	t.Cleanup(func() { openDatabase = previous })
	var started, stopped, registered bool
	var mu sync.Mutex
	openDatabase = func(_ context.Context, a *assembly) error {
		a.addComponent(server.Component{
			Name: "stub",
			Start: func(context.Context) error {
				mu.Lock()
				defer mu.Unlock()
				started = true
				return nil
			},
			Stop: func(context.Context) error {
				mu.Lock()
				defer mu.Unlock()
				stopped = true
				return nil
			},
		})
		a.addReadyCheck("stub", func(context.Context) error { return nil })
		registered = len(a.ready) == 1
		return nil
	}

	if _, err := driveStartUp(t, invocation{serve: true}, startupConfig()); err != nil {
		t.Fatalf("run: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !registered {
		t.Error("the readiness check was not registered")
	}
	if !started || !stopped {
		t.Errorf("the component started %v and stopped %v, want both", started, stopped)
	}
}

// Components stop in the reverse of their start order, and one that fails to
// stop does not keep the rest running.
func TestComponentsStopInReverseAndPastAFailure(t *testing.T) {
	var order []string
	stop := func(name string, err error) func(context.Context) error {
		return func(context.Context) error {
			order = append(order, name)
			return err
		}
	}
	stopComponents([]server.Component{
		{Name: "store", Stop: stop("store", nil)},
		{Name: "queue", Stop: stop("queue", errors.New("the queue did not drain"))},
		{Name: "cache", Stop: stop("cache", nil)},
	})
	if got := strings.Join(order, ","); got != "cache,queue,store" {
		t.Errorf("stop order = %s, want cache,queue,store", got)
	}
}

// Background work and the frontend open after the store, and either failing
// stops start-up the same way: no listener binds over a process that is only
// partly assembled.
func TestAFeatureThatFailsToStartStopsStartUp(t *testing.T) {
	previousBackground, previousFrontend := startBackground, mountFrontend
	t.Cleanup(func() { startBackground, mountFrontend = previousBackground, previousFrontend })

	startBackground = func(context.Context, *assembly) error { return errors.New("the job runner did not start") }
	if _, err := driveStartUp(t, invocation{serve: true}, startupConfig()); err == nil ||
		!strings.Contains(err.Error(), "the job runner did not start") {
		t.Fatalf("run = %v, want the background failure", err)
	}

	startBackground = nil
	mountFrontend = func(*assembly) error { return errors.New("the bundle is missing") }
	if _, err := driveStartUp(t, invocation{serve: true}, startupConfig()); err == nil ||
		!strings.Contains(err.Error(), "the bundle is missing") {
		t.Fatalf("run = %v, want the frontend failure", err)
	}
}
