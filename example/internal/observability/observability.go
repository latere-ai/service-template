// Package observability starts the telemetry a service emits: traces, metrics,
// and logs that carry the identifiers of the trace they belong to.
//
// One call to Setup builds every provider from the standard OpenTelemetry
// protocol environment variables and returns the function that flushes and
// stops them. When no endpoint is configured the providers are no-ops, so a
// local run needs no collector and reports no export failures.
//
// The package also owns the request telemetry middleware. Route labels are
// always registered patterns, never request paths, because a label that holds
// an identifier produces one time series per identifier.
package observability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	lognoop "go.opentelemetry.io/otel/log/noop"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// Environment variables the OpenTelemetry specification defines. Reading the
// standard names means a deployment configures the exporter the same way it
// configures any other instrumented process.
const (
	envDisabled       = "OTEL_SDK_DISABLED"
	envEndpoint       = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envTracesEndpoint = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	envMetricEndpoint = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
	envLogsEndpoint   = "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"
)

// Signal paths appended to a collector base URL. The general environment
// variable appends them on its own; an endpoint passed as an option is joined
// here so both routes reach the same address.
const (
	tracesPath  = "/v1/traces"
	metricsPath = "/v1/metrics"
	logsPath    = "/v1/logs"
)

// DefaultSlowRequest is the duration at or above which a request trace is kept
// regardless of the sampling ratio.
const DefaultSlowRequest = time.Second

// productionSampleRatio is the fraction of traces a deployed service keeps by
// head sampling. Failures and slow requests are kept on top of it.
const productionSampleRatio = 0.1

// LogFormat selects the encoding of the local log stream.
type LogFormat string

// Log encodings. Text is readable in a terminal; JSON is what a log collector
// parses without a pattern.
const (
	LogFormatText LogFormat = "text"
	LogFormatJSON LogFormat = "json"
)

// developmentEnvironments are the deployment environment names that mean "a
// developer is watching this process". They select the readable log encoding
// and full trace sampling.
var developmentEnvironments = map[string]bool{
	"":            true,
	"dev":         true,
	"development": true,
	"local":       true,
	"test":        true,
}

// Options configures Setup.
type Options struct {
	// ServiceName names the service in every resource. It is required.
	ServiceName string
	// Environment is the deployment environment, such as production or
	// staging. It selects the log encoding and appears on every resource.
	Environment string
	// OTLPEndpoint is the collector base URL. Empty falls back to the standard
	// environment variables, and no endpoint from either source selects the
	// no-op providers.
	//
	// The field exists because configuration has a precedence order: a value
	// may arrive from a flag or from a mounted secret file, and an exporter
	// that only reads the environment would never see it.
	OTLPEndpoint string
	// OTLPHeaders carries collector credentials. Empty falls back to the
	// standard environment variable.
	OTLPHeaders map[string]string
	// SampleRatio is the fraction of root traces the head sampler keeps, from
	// 0 to 1. A value of zero keeps only the traces that failed or ran slowly.
	// DefaultSampleRatio reports the value an environment expects.
	SampleRatio float64
	// SlowRequest is the duration at or above which a trace is exported even
	// though the ratio did not select it. Zero selects DefaultSlowRequest.
	SlowRequest time.Duration
	// LogLevel is the lowest severity that is written and exported.
	LogLevel slog.Level
	// LogFormat encodes the local log stream. Empty selects text in a
	// development environment and JSON everywhere else.
	LogFormat LogFormat
	// LogOutput receives the local log stream. Nil selects standard output.
	LogOutput io.Writer
}

// DefaultSampleRatio reports the head sampling ratio an environment expects:
// every trace where a developer is reading them, and a fraction of them where
// volume is the constraint.
func DefaultSampleRatio(environment string) float64 {
	if developmentEnvironments[strings.ToLower(environment)] {
		return 1
	}
	return productionSampleRatio
}

// withDefaults fills the optional fields.
func (o Options) withDefaults() Options {
	if o.SlowRequest <= 0 {
		o.SlowRequest = DefaultSlowRequest
	}
	if o.LogFormat == "" {
		o.LogFormat = LogFormatJSON
		if developmentEnvironments[strings.ToLower(o.Environment)] {
			o.LogFormat = LogFormatText
		}
	}
	if o.LogOutput == nil {
		o.LogOutput = os.Stdout
	}
	return o
}

// shutdownFunc releases one provider.
type shutdownFunc func(context.Context) error

// Setup installs the trace, metric, and log providers, the context
// propagators, and the default logger, and returns the function that flushes
// and stops them.
//
// The returned function is never nil, so a caller can defer it without a
// preceding check. It is safe to call once.
func Setup(ctx context.Context, o Options) (func(context.Context) error, error) {
	o = o.withDefaults()
	if o.ServiceName == "" {
		return nil, errors.New("observability: service name is required")
	}

	res, err := newResource(o.ServiceName, o.Environment)
	if err != nil {
		return nil, fmt.Errorf("observability: resource: %w", err)
	}

	var stops []shutdownFunc
	// A provider that started before a later one failed still holds a
	// connection and a background goroutine, so a partial start is unwound
	// rather than leaked.
	abort := func(err error) (func(context.Context) error, error) {
		if stopErr := shutdownAll(ctx, stops); stopErr != nil {
			err = errors.Join(err, stopErr)
		}
		return nil, err
	}

	tracerProvider, stopTraces, err := newTracerProvider(ctx, res, o)
	if err != nil {
		return abort(err)
	}
	stops = append(stops, stopTraces)

	meterProvider, stopMetrics, err := newMeterProvider(ctx, res, o)
	if err != nil {
		return abort(err)
	}
	stops = append(stops, stopMetrics)

	loggerProvider, stopLogs, err := newLoggerProvider(ctx, res, o)
	if err != nil {
		return abort(err)
	}
	stops = append(stops, stopLogs)

	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	global.SetLoggerProvider(loggerProvider)
	// Trace context carries the trace across a service boundary; baggage
	// carries the request-scoped values that ride with it.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	slog.SetDefault(newLogger(o.LogOutput, o.LogLevel, o.LogFormat, loggerProvider))

	return func(ctx context.Context) error { return shutdownAll(ctx, stops) }, nil
}

// shutdownAll stops the providers in the reverse of the order they started, so
// a provider is never asked to export after the one it depends on is gone.
func shutdownAll(ctx context.Context, stops []shutdownFunc) error {
	var errs []error
	for _, stop := range slices.Backward(stops) {
		if stop == nil {
			continue
		}
		if err := stop(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// newTracerProvider builds the trace provider, or a no-op when no trace
// endpoint is configured.
func newTracerProvider(ctx context.Context, res *resource.Resource, o Options) (trace.TracerProvider, shutdownFunc, error) {
	if !exportEnabled(o, envTracesEndpoint) {
		return tracenoop.NewTracerProvider(), nil, nil
	}

	var opts []otlptracehttp.Option
	if endpoint, ok := signalEndpoint(o.OTLPEndpoint, tracesPath); ok {
		opts = append(opts, otlptracehttp.WithEndpointURL(endpoint))
	}
	if len(o.OTLPHeaders) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(o.OTLPHeaders))
	}
	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("observability: trace exporter: %w", err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(newSampler(o.SampleRatio)),
		sdktrace.WithSpanProcessor(retainProcessor{
			next:        sdktrace.NewBatchSpanProcessor(exporter),
			slowRequest: o.SlowRequest,
		}),
	)
	return provider, provider.Shutdown, nil
}

// newMeterProvider builds the metric provider, or a no-op when no metric
// endpoint is configured.
func newMeterProvider(ctx context.Context, res *resource.Resource, o Options) (metric.MeterProvider, shutdownFunc, error) {
	if !exportEnabled(o, envMetricEndpoint) {
		return metricnoop.NewMeterProvider(), nil, nil
	}

	var opts []otlpmetrichttp.Option
	if endpoint, ok := signalEndpoint(o.OTLPEndpoint, metricsPath); ok {
		opts = append(opts, otlpmetrichttp.WithEndpointURL(endpoint))
	}
	if len(o.OTLPHeaders) > 0 {
		opts = append(opts, otlpmetrichttp.WithHeaders(o.OTLPHeaders))
	}
	exporter, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("observability: metric exporter: %w", err)
	}
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
	)
	return provider, provider.Shutdown, nil
}

// newLoggerProvider builds the log provider, or a no-op when no log endpoint
// is configured. The local log stream is written either way.
func newLoggerProvider(ctx context.Context, res *resource.Resource, o Options) (otellog.LoggerProvider, shutdownFunc, error) {
	if !exportEnabled(o, envLogsEndpoint) {
		return lognoop.NewLoggerProvider(), nil, nil
	}

	var opts []otlploghttp.Option
	if endpoint, ok := signalEndpoint(o.OTLPEndpoint, logsPath); ok {
		opts = append(opts, otlploghttp.WithEndpointURL(endpoint))
	}
	if len(o.OTLPHeaders) > 0 {
		opts = append(opts, otlploghttp.WithHeaders(o.OTLPHeaders))
	}
	exporter, err := otlploghttp.New(ctx, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("observability: log exporter: %w", err)
	}
	provider := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
	)
	return provider, provider.Shutdown, nil
}

// exportEnabled reports whether one signal has somewhere to go. An exporter
// built without an endpoint would default to a local collector address and
// report a connection failure on every export attempt.
func exportEnabled(o Options, signalEnv string) bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv(envDisabled)), "true") {
		return false
	}
	return o.OTLPEndpoint != "" || os.Getenv(signalEnv) != "" || os.Getenv(envEndpoint) != ""
}

// signalEndpoint joins the signal path onto a collector base URL. The exporter
// option takes the path as given and appends nothing, while the general
// environment variable appends the signal path, so the join happens here to
// keep one configured endpoint meaning one address.
//
// An unparsable base URL is reported as absent, which leaves the exporter on
// its environment configuration rather than on a silently wrong address.
func signalEndpoint(base, path string) (string, bool) {
	if base == "" {
		return "", false
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" {
		otel.Handle(fmt.Errorf("observability: unusable collector endpoint %q: %w", base, err))
		return "", false
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + path
	return parsed.String(), true
}

// ParseOTLPHeaders parses the collector header list: key=value pairs separated
// by commas, with percent-encoded values, which is the form the OpenTelemetry
// protocol environment variable defines.
//
// It exists so a header list that arrives through the configuration
// precedence, such as a mounted secret file, can be handed to Setup in the
// same form the environment variable would have taken.
func ParseOTLPHeaders(value string) (map[string]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	headers := map[string]string{}
	for pair := range strings.SplitSeq(value, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, raw, found := strings.Cut(pair, "=")
		name = strings.TrimSpace(name)
		if !found || name == "" {
			return nil, fmt.Errorf("observability: header %q is not a key=value pair", pair)
		}
		decoded, err := url.PathUnescape(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("observability: header %s has an unusable value: %w", name, err)
		}
		headers[name] = decoded
	}
	return headers, nil
}
