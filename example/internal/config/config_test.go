package config

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDefaultsProduceAServingConfiguration(t *testing.T) {
	cfg, err := load(nil, envOf(nil), filesOf(nil))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := Config{
		ServiceName:       "service",
		Environment:       "development",
		Addr:              ":8080",
		LogLevel:          slog.LevelInfo,
		LogFormat:         "json",
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		DrainDelay:        5 * time.Second,
		GracePeriod:       30 * time.Second,
		StopTimeout:       15 * time.Second,
		ReadyCheckTimeout: 2 * time.Second,
		SampleRatio:       1,
	}
	got := *cfg
	got.sources = nil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}
	if !cfg.DatabaseURL.IsZero() || cfg.OTLPEndpoint != "" {
		t.Fatal("an optional dependency defaulted to a value")
	}
}

// TestValidationReportsEveryProblem is the boot-time contract: one start-up
// names every invalid value.
func TestValidationReportsEveryProblem(t *testing.T) {
	_, err := load(nil, envOf(map[string]string{
		"SERVICE_NAME":                " ",
		"ADDR":                        "8080",
		"LOG_FORMAT":                  "yaml",
		"HTTP_READ_TIMEOUT":           "0s",
		"GRACE_PERIOD":                "-1s",
		"OTEL_TRACES_SAMPLE_RATIO":    "2",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "collector:4318",
	}), filesOf(nil))
	if err == nil {
		t.Fatal("an invalid configuration loaded without error")
	}
	msg := err.Error()
	for _, want := range []string{
		"ADDR", "LOG_FORMAT", "HTTP_READ_TIMEOUT", "GRACE_PERIOD",
		"OTEL_TRACES_SAMPLE_RATIO", "OTEL_EXPORTER_OTLP_ENDPOINT",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not name %s:\n%s", want, msg)
		}
	}
}

func TestValidationRejectsHeadersWithNoEndpoint(t *testing.T) {
	_, err := load(nil, envOf(map[string]string{
		"OTEL_EXPORTER_OTLP_HEADERS": "authorization=Bearer token",
	}), filesOf(nil))
	if err == nil {
		t.Fatal("collector credentials with no endpoint loaded without error")
	}
	if !strings.Contains(err.Error(), "OTEL_EXPORTER_OTLP_HEADERS") {
		t.Fatalf("error %q does not name the variable", err)
	}
	if strings.Contains(err.Error(), "Bearer token") {
		t.Fatalf("the validation error leaked the header value: %v", err)
	}
}

func TestValidationRejectsAnEmptyServiceName(t *testing.T) {
	cfg := &Config{}
	err := cfg.validate()
	if err == nil {
		t.Fatal("a zero configuration passed validation")
	}
	for _, want := range []string{"SERVICE_NAME", "ENVIRONMENT", "ADDR", "LOG_FORMAT", "GRACE_PERIOD"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %s:\n%s", want, err)
		}
	}
}

func TestValidationAcceptsAConfiguredExporter(t *testing.T) {
	cfg, err := load(nil, envOf(map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": "https://collector.example:4318",
		"OTEL_EXPORTER_OTLP_HEADERS":  "authorization=Bearer token",
		"OTEL_TRACES_SAMPLE_RATIO":    "0",
		"DRAIN_DELAY":                 "0s",
	}), filesOf(nil))
	if err != nil {
		t.Fatalf("a valid exporter configuration failed: %v", err)
	}
	if cfg.SampleRatio != 0 || cfg.DrainDelay != 0 {
		t.Fatalf("SampleRatio = %v, DrainDelay = %v", cfg.SampleRatio, cfg.DrainDelay)
	}
}

// startupRecord logs the configuration the way the entry point does and returns
// the decoded record.
func startupRecord(t *testing.T, cfg *Config) (map[string]any, string) {
	t.Helper()
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("configuration loaded", "config", cfg)

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("decode the log record: %v", err)
	}
	return record, buf.String()
}

// TestStartupLogMarksDefaultsAndRedactsSecrets covers the line an operator
// reads to see what the process actually resolved.
func TestStartupLogMarksDefaultsAndRedactsSecrets(t *testing.T) {
	const dsn = "postgres://user:hunter2@db:5432/app"
	cfg, err := load([]string{"-addr=:9090"}, envOf(map[string]string{
		"DATABASE_URL": dsn,
	}), filesOf(nil))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	record, raw := startupRecord(t, cfg)
	if strings.Contains(raw, "hunter2") {
		t.Fatalf("the start-up log leaked the database password:\n%s", raw)
	}

	config, ok := record["config"].(map[string]any)
	if !ok {
		t.Fatalf("the record holds no config group:\n%s", raw)
	}
	field := func(name string) map[string]any {
		t.Helper()
		group, ok := config[name].(map[string]any)
		if !ok {
			t.Fatalf("the record holds no %s group:\n%s", name, raw)
		}
		return group
	}

	if got := field("DATABASE_URL")["value"]; got != Redacted {
		t.Errorf("DATABASE_URL value = %v, want %q", got, Redacted)
	}
	if got := field("DATABASE_URL")["source"]; got != string(SourceEnv) {
		t.Errorf("DATABASE_URL source = %v, want %q", got, SourceEnv)
	}
	if got := field("ADDR")["source"]; got != string(SourceFlag) {
		t.Errorf("ADDR source = %v, want %q", got, SourceFlag)
	}
	if got := field("ADDR")["value"]; got != ":9090" {
		t.Errorf("ADDR value = %v, want the flag value", got)
	}
	if got := field("GRACE_PERIOD")["source"]; got != string(SourceDefault) {
		t.Errorf("GRACE_PERIOD source = %v, want %q", got, SourceDefault)
	}
	if got := field("GRACE_PERIOD")["value"]; got != "30s" {
		t.Errorf("GRACE_PERIOD value = %v, want 30s", got)
	}
	if got := field("OTEL_EXPORTER_OTLP_ENDPOINT")["source"]; got != string(SourceUnset) {
		t.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT source = %v, want %q", got, SourceUnset)
	}
	// An unset secret renders empty rather than redacted, so the line does not
	// read as though a value were configured.
	if got := field("OTEL_EXPORTER_OTLP_HEADERS")["value"]; got != "" {
		t.Errorf("OTEL_EXPORTER_OTLP_HEADERS value = %v, want the empty string", got)
	}
}

func TestStartupLogNamesEveryDeclaredField(t *testing.T) {
	cfg, err := load(nil, envOf(nil), filesOf(nil))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	specs, err := specsOf(cfg)
	if err != nil {
		t.Fatalf("specsOf: %v", err)
	}
	record, raw := startupRecord(t, cfg)
	config, ok := record["config"].(map[string]any)
	if !ok {
		t.Fatalf("the record holds no config group:\n%s", raw)
	}
	if len(config) != len(specs) {
		t.Fatalf("the log line holds %d fields, want %d", len(config), len(specs))
	}
	for _, s := range specs {
		if _, ok := config[s.Env]; !ok {
			t.Errorf("the log line omits %s", s.Env)
		}
	}
}

// TestLogValueReportsADeclarationFailure asserts the log line degrades to a
// message rather than panicking when the configuration cannot be described.
func TestLogValueReportsADeclarationFailure(t *testing.T) {
	var broken *Config
	got := broken.LogValue()
	if got.Kind() != slog.KindString {
		t.Fatalf("LogValue on a nil configuration = %v, want a message", got)
	}
	if !strings.Contains(got.String(), "not describable") {
		t.Fatalf("LogValue = %q, want the failure message", got.String())
	}
}

func TestValidationRejectsAMalformedEndpoint(t *testing.T) {
	cases := map[string]string{
		"unparseable": "http://[::1",
		"no host":     "http://",
		"no scheme":   "collector.example:4318",
	}
	for name, endpoint := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := load(nil, envOf(map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT": endpoint,
			}), filesOf(nil))
			if err == nil {
				t.Fatalf("endpoint %q loaded without error", endpoint)
			}
			if !strings.Contains(err.Error(), "OTEL_EXPORTER_OTLP_ENDPOINT") {
				t.Fatalf("error %q does not name the variable", err)
			}
		})
	}
}

func TestLoadReportsABindFailureBeforeValidating(t *testing.T) {
	_, err := load(nil, envOf(map[string]string{"HTTP_READ_TIMEOUT": "soon"}), filesOf(nil))
	if err == nil {
		t.Fatal("an unparseable duration loaded without error")
	}
	if !strings.Contains(err.Error(), "not a duration") {
		t.Fatalf("error %q is not the parse failure", err)
	}
}

// TestLoadReadsTheProcess covers the wrapper that Load is, using a real file on
// disk so the injected readFile is not the only path exercised.
func TestLoadReadsTheProcess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "database-url")
	if err := os.WriteFile(path, []byte("postgres://localhost/app\n"), 0o600); err != nil {
		t.Fatalf("write the secret file: %v", err)
	}
	t.Setenv("DATABASE_URL_FILE", path)
	t.Setenv("LOG_LEVEL", "debug")
	// The ambient environment is cleared for every name the test does not set
	// itself. Load reads the process, and a pipeline that exports a connection
	// string or a collector endpoint for another tier would otherwise decide
	// what this test observes.
	clearDeclaredEnv(t, "DATABASE_URL_FILE", "LOG_LEVEL")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DatabaseURL.Reveal() != "postgres://localhost/app" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL.Reveal())
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
	}
	if cfg.Source("DATABASE_URL") != SourceFile {
		t.Errorf("DATABASE_URL source = %q", cfg.Source("DATABASE_URL"))
	}
	if cfg.Source("UNDECLARED") != SourceUnset {
		t.Errorf("an undeclared name reported source %q", cfg.Source("UNDECLARED"))
	}
}

// clearDeclaredEnv removes every variable the configuration declares, and the
// file variable beside it, except the names the caller keeps. It is what makes
// a Load test a test of precedence rather than a test of the machine.
func clearDeclaredEnv(t *testing.T, keep ...string) {
	t.Helper()
	specs, err := specsOf(&Config{})
	if err != nil {
		t.Fatalf("read the field specifications: %v", err)
	}
	for _, sp := range specs {
		for _, name := range []string{sp.Env, sp.Env + fileSuffix} {
			if slices.Contains(keep, name) {
				continue
			}
			unset(t, name)
		}
	}
}

// unset removes an environment variable for the duration of a test. t.Setenv
// registers the restore, and the removal afterwards is what makes the name
// absent rather than empty: an empty value is a value the loader would read.
func unset(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "")
	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("unset %s: %v", name, err)
	}
}
