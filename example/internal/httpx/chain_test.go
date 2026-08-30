package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// probe records the moment the chain enters and leaves the stage it sits
// directly outside of. Recording both sides is what proves the stages nest
// rather than merely run.
func probe(name string, mu *sync.Mutex, entered, exited *[]string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			*entered = append(*entered, name)
			mu.Unlock()
			defer func() {
				mu.Lock()
				*exited = append(*exited, name)
				mu.Unlock()
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// runProbed serves one request through stages with a probe outside each stage,
// and reports the entry and exit sequences.
func runProbed(t *testing.T, stages []Stage, r *http.Request) (entered, exited []string) {
	t.Helper()
	captureDefaultLogger(t)

	var mu sync.Mutex
	mw := make([]func(http.Handler) http.Handler, 0, len(stages)*2)
	for _, s := range stages {
		mw = append(mw, probe(s.Name, &mu, &entered, &exited), s.Wrap)
	}
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), mw...)

	h.ServeHTTP(httptest.NewRecorder(), r)

	mu.Lock()
	defer mu.Unlock()
	return slices.Clone(entered), slices.Clone(exited)
}

func TestStandardStagesAreTheDocumentedOrder(t *testing.T) {
	var names []string
	for _, s := range Standard(Options{}) {
		names = append(names, s.Name)
	}
	if !slices.Equal(names, Order()) {
		t.Fatalf("Standard stage names = %v, want %v", names, Order())
	}
}

func TestChainEntersAndExitsInTheDocumentedOrder(t *testing.T) {
	entered, exited := runProbed(t, Standard(Options{}), httptest.NewRequest(http.MethodGet, "/v1/items", nil))

	if !slices.Equal(entered, Order()) {
		t.Errorf("entry order = %v, want %v", entered, Order())
	}

	want := Order()
	slices.Reverse(want)
	if !slices.Equal(exited, want) {
		t.Errorf("exit order = %v, want %v", exited, want)
	}
}

// TestChainOrderAssertionFailsOnAWrongOrder proves the assertion above can
// fail. A chain assembled in the wrong order must not produce the documented
// sequence, otherwise the test proves nothing.
func TestChainOrderAssertionFailsOnAWrongOrder(t *testing.T) {
	stages := Standard(Options{})
	stages[0], stages[3] = stages[3], stages[0]

	entered, _ := runProbed(t, stages, httptest.NewRequest(http.MethodGet, "/v1/items", nil))
	if slices.Equal(entered, Order()) {
		t.Fatal("a scrambled chain produced the documented order, so the order assertion cannot fail")
	}
}

func TestChainAppliesTheFirstMiddlewareOutermost(t *testing.T) {
	var seen []string
	mark := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = append(seen, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		seen = append(seen, "handler")
	}), mark("outer"), nil, mark("inner"))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if want := []string{"outer", "inner", "handler"}; !slices.Equal(seen, want) {
		t.Fatalf("call order = %v, want %v", seen, want)
	}
}

func TestPanicYieldsTheEnvelopeAndAnAccessLogEntry(t *testing.T) {
	logs := captureDefaultLogger(t)

	h := Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("the handler exploded")
	}), Options{Logger: slog.New(logs.handler)})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/items", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if ct := rec.Header().Get("Content-Type"); ct != ProblemContentType {
		t.Errorf("content type = %q, want %q", ct, ProblemContentType)
	}

	var p Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("the body is not the envelope: %v (%s)", err, rec.Body.String())
	}
	if p.Status != http.StatusInternalServerError || p.Instance == "" {
		t.Errorf("envelope = %+v, want status 500 with an instance", p)
	}
	if p.Detail != "" {
		t.Errorf("detail = %q, want it empty for a server fault", p.Detail)
	}
	if id := rec.Header().Get(HeaderRequestID); id != p.Instance {
		t.Errorf("response header %s = %q, want the envelope instance %q", HeaderRequestID, id, p.Instance)
	}

	access := logs.find(t, func(r logRecord) bool { return r.Msg == "request" })
	if access.RequestID != p.Instance {
		t.Errorf("access log request_id = %q, want %q", access.RequestID, p.Instance)
	}
	if access.Status != http.StatusInternalServerError {
		t.Errorf("access log status = %d, want 500", access.Status)
	}
	if !access.Panic {
		t.Error("the access log entry does not report the panic")
	}

	panicked := logs.find(t, func(r logRecord) bool { return r.Msg == "handler panicked" })
	if !strings.Contains(panicked.Stack, "chain_test.go") {
		t.Errorf("the logged stack does not reach the panic site: %q", panicked.Stack)
	}
	if !strings.Contains(rec.Body.String(), "instance") || strings.Contains(rec.Body.String(), "exploded") {
		t.Errorf("the panic message reached the response body: %s", rec.Body.String())
	}
}

func TestAbortHandlerPanicIsNotConvertedToAnEnvelope(t *testing.T) {
	captureDefaultLogger(t)

	h := Recover()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		// The panic value is matched rather than compared, so a handler that
		// wraps the sentinel before re-panicking still counts as the abort it
		// is.
		recovered := recover()
		err, ok := recovered.(error)
		if !ok || !errors.Is(err, http.ErrAbortHandler) {
			t.Fatalf("recovered = %v, want http.ErrAbortHandler", recovered)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	t.Fatal("the abort panic did not pass through the recovery stage")
}

func TestCommittedResponseIsNotReplacedByTheEnvelope(t *testing.T) {
	captureDefaultLogger(t)

	h := Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		if _, err := w.Write([]byte("partial")); err != nil {
			t.Errorf("write the partial body: %v", err)
		}
		panic("too late")
	}), Options{})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d because the response was already committed", rec.Code, http.StatusAccepted)
	}
	if rec.Body.String() != "partial" {
		t.Fatalf("body = %q, want the committed body", rec.Body.String())
	}
}
