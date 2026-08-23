package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/service/internal/config"
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
