// Package config holds the service configuration as one type, loaded once.
//
// No other package reads the environment. Every input the process needs is a
// field below, so the full set is greppable in one file, and a test constructs
// a Config directly instead of manipulating process-level state.
//
// A field is declared with struct tags: env names the variable, default gives
// the value used when nothing supplies one, required marks a value the process
// cannot start without, and doc is the line written into .env.example.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"time"
)

// Config is the effective configuration of the process.
//
// Field order is the order of the generated example file and of the start-up
// log line, so it groups by concern: identity, then logging, then the HTTP
// server, then shutdown, then dependencies.
type Config struct {
	// ServiceName is the name reported in telemetry and logs.
	ServiceName string `env:"SERVICE_NAME" default:"service" doc:"Name reported in telemetry and logs."`
	// Environment names the deployment, for example development or production.
	Environment string `env:"ENVIRONMENT" default:"development" doc:"Deployment environment: development, staging, or production."`

	// Addr is the listen address of the HTTP server, as host:port. An empty
	// host listens on every interface.
	Addr string `env:"ADDR" default:":8080" doc:"HTTP listen address as host:port."`

	// LogLevel is the minimum record level the logger emits.
	LogLevel slog.Level `env:"LOG_LEVEL" default:"info" doc:"Minimum log level: debug, info, warn, or error."`
	// LogFormat selects the handler: json for a collector, text for a terminal.
	LogFormat string `env:"LOG_FORMAT" default:"json" doc:"Log handler: json or text."`

	// ReadHeaderTimeout bounds the time a client may take to send request
	// headers. It is the guard against a slow-header denial of service.
	ReadHeaderTimeout time.Duration `env:"HTTP_READ_HEADER_TIMEOUT" default:"5s" doc:"Deadline for reading request headers."`
	// ReadTimeout bounds reading the whole request.
	ReadTimeout time.Duration `env:"HTTP_READ_TIMEOUT" default:"30s" doc:"Deadline for reading the whole request."`
	// WriteTimeout bounds writing the response.
	WriteTimeout time.Duration `env:"HTTP_WRITE_TIMEOUT" default:"30s" doc:"Deadline for writing the response."`
	// IdleTimeout bounds how long a keep-alive connection may sit unused.
	IdleTimeout time.Duration `env:"HTTP_IDLE_TIMEOUT" default:"120s" doc:"Idle keep-alive timeout."`

	// DrainDelay is the wait between marking the process unready and refusing
	// new connections. It covers load balancer propagation, so requests already
	// dispatched are still served.
	DrainDelay time.Duration `env:"DRAIN_DELAY" default:"5s" doc:"Wait after SIGTERM before refusing new connections."`
	// GracePeriod is how long in-flight requests may run after the listener
	// stops. Requests still running when it expires are cancelled.
	GracePeriod time.Duration `env:"GRACE_PERIOD" default:"30s" doc:"Time in-flight requests may finish after the listener stops."`
	// StopTimeout bounds one component's shutdown. A component that has not
	// returned when it expires is abandoned, so one stuck dependency cannot
	// hold the process past the deployment's termination grace period.
	StopTimeout time.Duration `env:"STOP_TIMEOUT" default:"15s" doc:"Time one component has to stop before it is abandoned."`
	// ReadyCheckTimeout bounds one readiness check. A dependency that answers
	// slowly must fail the probe rather than hold it open, because a probe
	// that never returns reads as a healthy replica.
	ReadyCheckTimeout time.Duration `env:"READY_CHECK_TIMEOUT" default:"2s" doc:"Deadline for one readiness check."`

	// DatabaseURL is the connection string. It is empty when the service runs
	// without a database.
	DatabaseURL Secret `env:"DATABASE_URL" doc:"PostgreSQL connection string. Empty disables the database."`

	// OTLPEndpoint is the collector base URL. Empty disables telemetry export.
	OTLPEndpoint string `env:"OTEL_EXPORTER_OTLP_ENDPOINT" doc:"OTLP collector base URL. Empty disables telemetry export."`
	// OTLPHeaders carries collector credentials as key=value pairs separated by
	// commas.
	OTLPHeaders Secret `env:"OTEL_EXPORTER_OTLP_HEADERS" doc:"OTLP headers as comma-separated key=value pairs."`
	// SampleRatio is the head sampling ratio for traces, from 0 to 1.
	SampleRatio float64 `env:"OTEL_TRACES_SAMPLE_RATIO" default:"1.0" doc:"Head trace sampling ratio between 0 and 1."`

	// sources records where each field's value came from, keyed by the env
	// name. It drives the start-up log line and is never part of the value.
	sources map[string]Source
}

// Load reads the configuration from the process: command-line flags, then the
// environment, then the files named by <NAME>_FILE, then the declared
// defaults. It reports every problem it finds rather than the first.
func Load() (*Config, error) {
	return load(os.Args[1:], os.LookupEnv, os.ReadFile)
}

// load is the body of Load with the process inputs injected, so precedence and
// validation are testable without process-level state.
func load(args []string, lookupEnv lookupFunc, readFile readFileFunc) (*Config, error) {
	cfg := &Config{}
	sources, err := bind(cfg, args, lookupEnv, readFile)
	if err != nil {
		return nil, err
	}
	cfg.sources = sources
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Source reports where a field's value came from.
func (c *Config) Source(env string) Source {
	if s, ok := c.sources[env]; ok {
		return s
	}
	return SourceUnset
}

// validate applies the rules a parse cannot express and joins every failure, so
// one restart reports every problem instead of one per deployment cycle.
func (c *Config) validate() error {
	var problems []error

	if c.ServiceName == "" {
		problems = append(problems, errors.New("SERVICE_NAME: must not be empty"))
	}
	if c.Environment == "" {
		problems = append(problems, errors.New("ENVIRONMENT: must not be empty"))
	}
	if _, _, err := net.SplitHostPort(c.Addr); err != nil {
		problems = append(problems, fmt.Errorf("ADDR: %q is not a host:port address", c.Addr))
	}
	if c.LogFormat != "json" && c.LogFormat != "text" {
		problems = append(problems, fmt.Errorf("LOG_FORMAT: %q is not json or text", c.LogFormat))
	}

	positive := []struct {
		env string
		d   time.Duration
	}{
		{"HTTP_READ_HEADER_TIMEOUT", c.ReadHeaderTimeout},
		{"HTTP_READ_TIMEOUT", c.ReadTimeout},
		{"HTTP_WRITE_TIMEOUT", c.WriteTimeout},
		{"HTTP_IDLE_TIMEOUT", c.IdleTimeout},
		{"GRACE_PERIOD", c.GracePeriod},
		{"STOP_TIMEOUT", c.StopTimeout},
		{"READY_CHECK_TIMEOUT", c.ReadyCheckTimeout},
	}
	for _, p := range positive {
		if p.d <= 0 {
			problems = append(problems, fmt.Errorf("%s: must be greater than zero, got %s", p.env, p.d))
		}
	}
	if c.DrainDelay < 0 {
		problems = append(problems, fmt.Errorf("DRAIN_DELAY: must not be negative, got %s", c.DrainDelay))
	}

	if c.SampleRatio < 0 || c.SampleRatio > 1 {
		problems = append(problems,
			fmt.Errorf("OTEL_TRACES_SAMPLE_RATIO: must be between 0 and 1, got %v", c.SampleRatio))
	}
	if c.OTLPEndpoint != "" {
		u, err := url.Parse(c.OTLPEndpoint)
		switch {
		case err != nil:
			problems = append(problems, fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT: %w", err))
		case u.Scheme != "http" && u.Scheme != "https":
			problems = append(problems,
				fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT: scheme %q is not http or https", u.Scheme))
		case u.Host == "":
			problems = append(problems, errors.New("OTEL_EXPORTER_OTLP_ENDPOINT: no host"))
		}
	}
	// Cross-field: credentials with no destination mean the operator configured
	// half of the exporter, and telemetry would silently go nowhere.
	if !c.OTLPHeaders.IsZero() && c.OTLPEndpoint == "" {
		problems = append(problems,
			errors.New("OTEL_EXPORTER_OTLP_HEADERS: set with no OTEL_EXPORTER_OTLP_ENDPOINT"))
	}

	return errors.Join(problems...)
}

// LogValue renders the effective configuration for the start-up log record.
// Secrets are redacted by their type, and every field carries the source it
// resolved from, so an operator sees which values the process defaulted.
func (c *Config) LogValue() slog.Value {
	specs, err := specsOf(c)
	if err != nil {
		return slog.StringValue("configuration is not describable: " + err.Error())
	}
	attrs := make([]slog.Attr, 0, len(specs))
	for _, s := range specs {
		attrs = append(attrs, slog.Group(s.Env,
			slog.String("value", displayValue(c, s)),
			slog.String("source", string(c.Source(s.Env)))))
	}
	return slog.GroupValue(attrs...)
}
