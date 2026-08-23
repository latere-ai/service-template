package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// decodeEnvelope parses a response body as the envelope and fails when it is
// any other shape. Every error path in this package is asserted through it.
func decodeEnvelope(t *testing.T, res *httptest.ResponseRecorder) Problem {
	t.Helper()

	if ct := res.Header().Get("Content-Type"); ct != ProblemContentType {
		t.Errorf("content type = %q, want %q", ct, ProblemContentType)
	}

	var p Problem
	decoder := json.NewDecoder(strings.NewReader(res.Body.String()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		t.Fatalf("the body is not the envelope: %v (%s)", err, res.Body.String())
	}
	if p.Status != res.Code {
		t.Errorf("envelope status %d does not match the response status %d", p.Status, res.Code)
	}
	if p.Type == "" || p.Title == "" {
		t.Errorf("envelope = %+v, want a type and a title", p)
	}
	return p
}

// serveError runs one request through the shared chain with a handler that
// fails with err, and returns the response.
func serveError(t *testing.T, err error) *httptest.ResponseRecorder {
	t.Helper()

	h := Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, err)
	}), Options{})
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/items", nil))
	return res
}

func TestEveryErrorClassRendersTheEnvelope(t *testing.T) {
	captureDefaultLogger(t)

	cases := map[string]struct {
		err    error
		status int
	}{
		"client fault":  {New(http.StatusBadRequest, "the id is not a number"), http.StatusBadRequest},
		"not found":     {New(http.StatusNotFound, "no item with that id"), http.StatusNotFound},
		"validation":    {Validation(FieldError{Field: "email", Code: "format"}), http.StatusUnprocessableEntity},
		"internal":      {Internal(errors.New("dial tcp 10.0.0.7:5432: refused")), http.StatusInternalServerError},
		"bare error":    {errors.New("something the handler did not classify"), http.StatusInternalServerError},
		"wrapped":       {fmt.Errorf("load item: %w", New(http.StatusConflict, "the item changed")), http.StatusConflict},
		"body too big":  {&http.MaxBytesError{Limit: 1024}, http.StatusRequestEntityTooLarge},
		"deadline":      {context.DeadlineExceeded, http.StatusGatewayTimeout},
		"client closed": {context.Canceled, StatusClientClosedRequest},
		"nil":           {nil, http.StatusInternalServerError},
		"no status":     {&Problem{Title: "hand built"}, http.StatusInternalServerError},
		"bogus status":  {New(0, "a status net/http would refuse"), http.StatusInternalServerError},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res := serveError(t, tc.err)
			if res.Code != tc.status {
				t.Fatalf("status = %d, want %d", res.Code, tc.status)
			}
			p := decodeEnvelope(t, res)
			if p.Instance == "" {
				t.Error("the envelope carries no request identifier")
			}
		})
	}
}

func TestInternalErrorMessageReachesTheLogAndNotTheBody(t *testing.T) {
	logs := captureDefaultLogger(t)

	const secret = "dial tcp 10.0.0.7:5432: password authentication failed for user \"svc\""
	res := serveError(t, Internal(errors.New(secret)))

	body := res.Body.String()
	if strings.Contains(body, secret) || strings.Contains(body, "password") {
		t.Fatalf("the underlying message reached the response body: %s", body)
	}
	p := decodeEnvelope(t, res)
	if p.Detail != "" {
		t.Errorf("detail = %q, want it empty for a server fault", p.Detail)
	}

	record := logs.find(t, func(r logRecord) bool { return r.Msg == "request failed" })
	if !strings.Contains(record.Error, secret) {
		t.Errorf("log error = %q, want it to carry the underlying message", record.Error)
	}
	if record.RequestID != p.Instance {
		t.Errorf("log request_id = %q, want the envelope instance %q", record.RequestID, p.Instance)
	}
}

func TestServerFaultDropsADetailAHandlerSupplied(t *testing.T) {
	captureDefaultLogger(t)

	res := serveError(t, New(http.StatusServiceUnavailable, "the pgbouncer pool at 10.0.0.7 is saturated"))
	p := decodeEnvelope(t, res)
	if p.Detail != "" {
		t.Fatalf("detail = %q, want it dropped on a 5xx", p.Detail)
	}
}

func TestValidationEnvelopeNamesEveryField(t *testing.T) {
	captureDefaultLogger(t)

	res := serveError(t, Validation(
		FieldError{Field: "email", Code: "format", Detail: "not a valid address"},
		FieldError{Field: "age", Code: "range"},
	))
	p := decodeEnvelope(t, res)
	if len(p.Errors) != 2 {
		t.Fatalf("errors = %+v, want two entries", p.Errors)
	}
	if p.Errors[0].Field != "email" || p.Errors[1].Code != "range" {
		t.Errorf("errors = %+v, want the fields as supplied", p.Errors)
	}
}

func TestWriteErrorDoesNotMutateASharedProblem(t *testing.T) {
	captureDefaultLogger(t)

	shared := New(http.StatusForbidden, "the caller may not read this item")
	for range 2 {
		res := serveError(t, shared)
		if p := decodeEnvelope(t, res); p.Instance == "" {
			t.Fatal("the envelope carries no request identifier")
		}
	}
	if shared.Instance != "" {
		t.Fatalf("the shared problem was mutated: instance = %q", shared.Instance)
	}
}

func TestProblemUnwrapsToItsCause(t *testing.T) {
	cause := errors.New("the row is gone")
	p := New(http.StatusNotFound, "no item with that id").WithCause(cause)

	if !errors.Is(p, cause) {
		t.Fatal("the problem does not unwrap to its cause")
	}
	if !strings.Contains(p.Error(), "the row is gone") {
		t.Errorf("Error() = %q, want it to name the cause", p.Error())
	}
	var target *Problem
	if !errors.As(fmt.Errorf("wrap: %w", p), &target) || target.Status != http.StatusNotFound {
		t.Error("a wrapped problem is not recovered by errors.As")
	}
}

func TestWriteJSONRendersASuccessBody(t *testing.T) {
	captureDefaultLogger(t)

	res := httptest.NewRecorder()
	WriteJSON(res, httptest.NewRequest(http.MethodGet, "/v1/items", nil),
		http.StatusCreated, map[string]string{"id": "42"})

	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusCreated)
	}
	if ct := res.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content type = %q", ct)
	}
	if got := strings.TrimSpace(res.Body.String()); got != `{"id":"42"}` {
		t.Errorf("body = %s", got)
	}
}

func TestWriteJSONFallsBackToTheEnvelopeOnAnUnmarshallableValue(t *testing.T) {
	captureDefaultLogger(t)

	res := httptest.NewRecorder()
	WriteJSON(res, httptest.NewRequest(http.MethodGet, "/v1/items", nil),
		http.StatusOK, make(chan int))

	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusInternalServerError)
	}
	decodeEnvelope(t, res)
}

func TestAStatusNetHTTPWouldRefuseBecomesAServerFault(t *testing.T) {
	captureDefaultLogger(t)

	// The writer is called directly, with no recovery stage above it, because
	// the point is that it does not panic. A status of zero reaches
	// http.ResponseWriter.WriteHeader, which panics on it.
	for _, err := range []error{&Problem{Title: "hand built"}, New(0, "unclassified"), New(999, "out of range")} {
		res := httptest.NewRecorder()
		WriteError(res, httptest.NewRequest(http.MethodGet, "/v1/items", nil), err)

		if res.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d for %v", res.Code, http.StatusInternalServerError, err)
		}
		if p := decodeEnvelope(t, res); p.Detail != "" {
			t.Errorf("detail = %q, want it dropped on a server fault", p.Detail)
		}
	}
}
