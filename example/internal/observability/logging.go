package observability

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	otellog "go.opentelemetry.io/otel/log"
	pkgotel "latere.ai/x/pkg/otel"
)

// Log record keys for the correlation identifiers. They are the field names a
// log backend joins on to find the trace a line belongs to.
const (
	traceIDKey = "trace_id"
	spanIDKey  = "span_id"
)

// newLogger builds the logger the service installs as the slog default.
//
// One record reaches two destinations: the local stream, which a developer and
// a container log collector read, and the logging bridge, which exports it
// through the OpenTelemetry protocol. Both carry the trace and span
// identifiers of the request that produced the record, so a line found in
// either place leads back to its trace.
func newLogger(out io.Writer, level slog.Level, format LogFormat, provider otellog.LoggerProvider) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}

	var local slog.Handler
	if format == LogFormatText {
		local = slog.NewTextHandler(out, opts)
	} else {
		local = slog.NewJSONHandler(out, opts)
	}

	return slog.New(levelHandler{
		level: level,
		next: fanoutHandler{handlers: []slog.Handler{
			traceContextHandler{next: local},
			otelslog.NewHandler(scopeName, otelslog.WithLoggerProvider(provider)),
		}},
	})
}

// levelHandler applies one minimum severity to every destination.
//
// The local handler holds its own level, while the bridge defers the decision
// to the log provider. Without a single gate in front of both, a level change
// would move the local stream and leave the exported stream where it was.
type levelHandler struct {
	level slog.Leveler
	next  slog.Handler
}

func (h levelHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.level.Level() && h.next.Enabled(ctx, level)
}

func (h levelHandler) Handle(ctx context.Context, rec slog.Record) error {
	return h.next.Handle(ctx, rec)
}

func (h levelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return levelHandler{level: h.level, next: h.next.WithAttrs(attrs)}
}

func (h levelHandler) WithGroup(name string) slog.Handler {
	return levelHandler{level: h.level, next: h.next.WithGroup(name)}
}

// traceContextHandler adds the trace and span identifiers of the calling
// context to every record.
//
// The logging bridge sets these fields on the exported record on its own. The
// local stream has no such mechanism, and a local line without them cannot be
// joined to the request it describes, which is the failure this package exists
// to prevent.
//
// The identifiers are read through the shared telemetry package, so the local
// stream, the browser, and every other service state the same two values in the
// same form. A backend joins a log line to a trace by exact match, so a second
// rendering of the same identifier is a join that silently returns nothing.
type traceContextHandler struct {
	next slog.Handler
}

func (h traceContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h traceContextHandler) Handle(ctx context.Context, rec slog.Record) error {
	if traceID, spanID := pkgotel.TraceIDs(ctx); traceID != "" {
		rec = rec.Clone()
		rec.AddAttrs(
			slog.String(traceIDKey, traceID),
			slog.String(spanIDKey, spanID),
		)
	}
	return h.next.Handle(ctx, rec)
}

func (h traceContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceContextHandler{next: h.next.WithAttrs(attrs)}
}

func (h traceContextHandler) WithGroup(name string) slog.Handler {
	return traceContextHandler{next: h.next.WithGroup(name)}
}

// fanoutHandler delivers one record to every handler it holds.
type fanoutHandler struct {
	handlers []slog.Handler
}

func (h fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, next := range h.handlers {
		if next.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h fanoutHandler) Handle(ctx context.Context, rec slog.Record) error {
	var errs []error
	for _, next := range h.handlers {
		if !next.Enabled(ctx, rec.Level) {
			continue
		}
		// Each handler receives its own copy, because a handler may add
		// attributes to the record it is given.
		if err := next.Handle(ctx, rec.Clone()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (h fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		next[i] = handler.WithAttrs(attrs)
	}
	return fanoutHandler{handlers: next}
}

func (h fanoutHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		next[i] = handler.WithGroup(name)
	}
	return fanoutHandler{handlers: next}
}
