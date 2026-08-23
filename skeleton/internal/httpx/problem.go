// Package httpx holds the HTTP surface every handler in this service shares:
// the middleware chain in a fixed order, one error envelope, and the helpers
// that carry the API version contract.
//
// Two properties are enforced here rather than left to each handler. The
// middleware order is a correctness property: recovery is outermost so a panic
// becomes a logged 500 instead of a dropped connection, the request identifier
// and the server span precede the access log so every record joins to one
// request, and rate limiting follows authentication so a limit can key on the
// caller. The error envelope has one writer, so a client parses one shape from
// every route and an internal message cannot reach a response body.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

// ProblemContentType is the media type of the error envelope. It is distinct
// from application/json so a client can tell an error body from a success body
// without parsing it.
const ProblemContentType = "application/problem+json; charset=utf-8"

// StatusClientClosedRequest reports that the client went away before the
// response was produced. The code is outside the registry and follows the
// convention proxies use, because a cancelled request is neither a server
// fault nor a request the server refused.
const StatusClientClosedRequest = 499

// TypeBase is the prefix of the `type` member of the envelope. A service sets
// it once at start-up to the host that documents its error catalogue. The
// value is a URI, and it need not resolve, but a resolvable one is what makes
// the member useful to a client developer.
var TypeBase = "https://errors.example.com/"

// FieldError names one rejected input field. `Code` is a stable machine token
// such as "required" or "format", so a client can branch on it without
// matching prose.
type FieldError struct {
	Field  string `json:"field"`
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// Problem is the response body of every error, derived from the
// problem-details convention. It is also an error value, so a handler returns
// it and the writer renders it.
//
// `Instance` carries the request identifier, which is what turns a
// user-reported error string into a trace and a log query. It is filled by the
// writer, not by the handler, so it cannot be forgotten.
type Problem struct {
	Type     string       `json:"type"`
	Title    string       `json:"title"`
	Status   int          `json:"status"`
	Detail   string       `json:"detail,omitempty"`
	Instance string       `json:"instance,omitempty"`
	Errors   []FieldError `json:"errors,omitempty"`

	// cause is the underlying error. It reaches the log and never the
	// response, so a message naming an internal host, a query, or a
	// credential cannot be served to a client.
	cause error
}

// New returns a problem for a status and a client-safe detail. A detail on a
// server-fault status is dropped by the writer, so the safe use of this
// constructor is a 4xx.
func New(status int, detail string) *Problem {
	return &Problem{
		Type:   typeURI(status),
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
	}
}

// Newf is New with a formatted detail.
func Newf(status int, format string, args ...any) *Problem {
	return New(status, fmt.Sprintf(format, args...))
}

// Internal returns a server-fault problem that carries err to the log. The
// response body holds the status, the title, and the request identifier only.
func Internal(err error) *Problem {
	p := New(http.StatusInternalServerError, "")
	p.cause = err
	return p
}

// Validation returns a 422 naming every rejected field. Reporting all of them
// at once means a client fixes one form submission in one round trip.
func Validation(fields ...FieldError) *Problem {
	p := New(http.StatusUnprocessableEntity, "")
	p.Title = "Validation failed"
	p.Type = TypeBase + "validation"
	p.Errors = fields
	return p
}

// WithCause returns a copy of p that carries err to the log. The copy keeps
// the caller's value usable as a package-level sentinel.
func (p *Problem) WithCause(err error) *Problem {
	c := *p
	c.cause = err
	return &c
}

// WithDetail returns a copy of p with a client-safe detail.
func (p *Problem) WithDetail(detail string) *Problem {
	c := *p
	c.Detail = detail
	return &c
}

// Error renders the problem for a log record or a wrapping error. The
// rendering is never a response body; WriteError produces those.
func (p *Problem) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d %s", p.Status, p.Title)
	if p.Detail != "" {
		fmt.Fprintf(&b, ": %s", p.Detail)
	}
	if p.cause != nil {
		fmt.Fprintf(&b, ": %v", p.cause)
	}
	return b.String()
}

// Unwrap exposes the cause to errors.Is and errors.As.
func (p *Problem) Unwrap() error { return p.cause }

// typeURI derives the type member from the status. A service that documents a
// specific failure overrides it with a stable slug of its own.
func typeURI(status int) string {
	slug := strings.ToLower(http.StatusText(status))
	slug = strings.ReplaceAll(slug, " ", "-")
	if slug == "" {
		slug = "error"
	}
	return TypeBase + slug
}

// WriteError renders err as the envelope. It is the only exported way to
// produce an error body, because a handler that writes one itself produces a
// second shape that no client parses.
//
// A server-fault status never carries a detail: the underlying message goes to
// the log with the request identifier, and the response carries the identifier
// alone.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	p := problemFor(err)
	p.Instance = RequestID(r.Context())
	// A status outside the range net/http accepts is a defect in the caller's
	// problem value, and writing it would panic inside the writer that exists
	// to keep failures orderly. It is reported as a server fault, which is
	// what an unclassifiable failure is.
	if p.Status < 100 || p.Status > 599 {
		p.Status = http.StatusInternalServerError
		p.Type = ""
		p.Title = ""
	}
	if p.Status >= http.StatusInternalServerError {
		p.Detail = ""
		p.Errors = nil
	}
	if p.Title == "" {
		p.Title = http.StatusText(p.Status)
	}
	if p.Type == "" {
		p.Type = typeURI(p.Status)
	}

	logProblem(r.Context(), r, p, err)

	body, merr := json.Marshal(p)
	if merr != nil {
		// A problem that cannot be marshalled is a defect in a caller-supplied
		// field. The client still needs a status, so the envelope degrades to
		// a fixed one rather than an empty 200.
		slog.ErrorContext(r.Context(), "render the error envelope",
			slog.Any("error", merr), slog.String("request_id", p.Instance))
		body = fallbackEnvelope(p.Instance)
		p.Status = http.StatusInternalServerError
	}

	w.Header().Set("Content-Type", ProblemContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(p.Status)
	// A HEAD request and a client that hung up both make this fail, and
	// neither is actionable beyond the record.
	if _, werr := w.Write(body); werr != nil {
		slog.DebugContext(r.Context(), "write the error envelope",
			slog.Any("error", werr), slog.String("request_id", p.Instance))
	}
}

// fallbackEnvelope is the hand-built body used when marshalling fails. It is
// built with the encoder so the identifier is escaped rather than interpolated.
func fallbackEnvelope(instance string) []byte {
	body, err := json.Marshal(&Problem{
		Type:     typeURI(http.StatusInternalServerError),
		Title:    http.StatusText(http.StatusInternalServerError),
		Status:   http.StatusInternalServerError,
		Instance: instance,
	})
	if err != nil {
		return []byte(`{"title":"Internal Server Error","status":500}`)
	}
	return body
}

// problemFor maps an error to the envelope. It returns a copy in every branch,
// so a package-level sentinel problem is never mutated by a response.
func problemFor(err error) *Problem {
	if err == nil {
		return Internal(errors.New("WriteError called with a nil error"))
	}

	if p, ok := errors.AsType[*Problem](err); ok {
		c := *p
		if c.cause == nil {
			c.cause = err
		}
		return &c
	}

	if tooLarge, ok := errors.AsType[*http.MaxBytesError](err); ok {
		q := New(http.StatusRequestEntityTooLarge,
			fmt.Sprintf("the request body exceeds the %d byte limit", tooLarge.Limit))
		q.cause = err
		return q
	}

	switch {
	case errors.Is(err, context.DeadlineExceeded):
		q := New(http.StatusGatewayTimeout, "the request exceeded the server time budget")
		q.cause = err
		return q
	case errors.Is(err, context.Canceled):
		q := New(StatusClientClosedRequest, "the client closed the request")
		q.Title = "Client Closed Request"
		q.Type = TypeBase + "client-closed-request"
		q.cause = err
		return q
	}

	return Internal(err)
}

// logProblem records the failure server-side. A server fault is logged with
// its cause at error level, which is the only place the underlying message
// appears; a client fault is logged at debug because the access log already
// carries the status.
func logProblem(ctx context.Context, r *http.Request, p *Problem, err error) {
	attrs := []any{
		slog.String("request_id", p.Instance),
		slog.Int("status", p.Status),
		slog.String("method", r.Method),
		slog.String("route", RoutePattern(ctx)),
		slog.Any("error", err),
	}
	if p.Status >= http.StatusInternalServerError {
		slog.ErrorContext(ctx, "request failed", attrs...)
		return
	}
	slog.DebugContext(ctx, "request rejected", attrs...)
}

// WriteJSON renders a success body. It exists beside WriteError so both sides
// of a handler set the same headers and neither hand-writes a status.
func WriteJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		WriteError(w, r, fmt.Errorf("marshal the response body: %w", err))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		slog.DebugContext(r.Context(), "write the response body",
			slog.Any("error", err), slog.String("request_id", RequestID(r.Context())))
	}
}
