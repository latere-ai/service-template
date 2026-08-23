package httpx

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStreamingHandlerCanFlushThroughTheChain(t *testing.T) {
	captureDefaultLogger(t)

	h := Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("the chain hid http.Flusher from the handler")
			return
		}
		if _, err := w.Write([]byte("first")); err != nil {
			t.Errorf("write the first chunk: %v", err)
		}
		flusher.Flush()
		if _, err := w.Write([]byte("second")); err != nil {
			t.Errorf("write the second chunk: %v", err)
		}
	}), Options{Timeout: time.Second})

	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/stream", nil))

	if !res.Flushed {
		t.Error("the flush never reached the underlying writer")
	}
	if res.Body.String() != "firstsecond" {
		t.Fatalf("body = %q, want both chunks", res.Body.String())
	}
}

func TestResponseControllerReachesTheUnderlyingWriter(t *testing.T) {
	captureDefaultLogger(t)

	h := Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte("chunk")); err != nil {
			t.Errorf("write the chunk: %v", err)
		}
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("flush through the controller: %v", err)
		}
	}), Options{Timeout: time.Second})

	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/stream", nil))

	if !res.Flushed {
		t.Fatal("the controller did not reach the underlying writer")
	}
}

func TestRecorderReportsTheStatusAndTheSize(t *testing.T) {
	res := httptest.NewRecorder()
	rec := newRecorder(res)

	if got := rec.Status(); got != http.StatusOK {
		t.Errorf("status before a write = %d, want the implicit %d", got, http.StatusOK)
	}
	if rec.wroteHeader() {
		t.Error("the recorder reports a committed response before any write")
	}
	if same := newRecorder(rec); same != rec {
		t.Error("wrapping a recorder produced a second set of counters")
	}

	rec.WriteHeader(http.StatusTeapot)
	rec.WriteHeader(http.StatusOK)
	if _, err := rec.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := rec.Status(); got != http.StatusTeapot {
		t.Errorf("status = %d, want the first one written", got)
	}
	if rec.written != 5 {
		t.Errorf("written = %d, want 5", rec.written)
	}
	if _, _, err := rec.Hijack(); err == nil {
		t.Error("hijacking a writer that does not support it reported no error")
	}
}

func TestSchemeAndMethodLabelsAreBounded(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/v1/items", nil)
	if got := scheme(r); got != "http" {
		t.Errorf("scheme = %q, want http", got)
	}
	r.TLS = &tls.ConnectionState{}
	if got := scheme(r); got != "https" {
		t.Errorf("scheme = %q, want https", got)
	}

	if got := normalizeMethod("PROPFIND"); got != "_OTHER" {
		t.Errorf("normalizeMethod = %q, want _OTHER for an unregistered verb", got)
	}
	if got := normalizeMethod(http.MethodPatch); got != http.MethodPatch {
		t.Errorf("normalizeMethod = %q, want PATCH", got)
	}
	if got := spanName("PROPFIND", ""); got != "_OTHER" {
		t.Errorf("spanName = %q, want _OTHER for a request that matched no route", got)
	}
}

func TestClientIPFallsBackToTheRawAddress(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "unix"
	if got := clientIP(r); got != "unix" {
		t.Errorf("clientIP = %q, want the raw address when it carries no port", got)
	}
}

func TestStatusClassGroupsTheFamilies(t *testing.T) {
	cases := map[int]string{
		100: "1xx", 204: "2xx", 302: "3xx", 404: "4xx", 503: "5xx",
	}
	for status, want := range cases {
		if got := statusClass(status); got != want {
			t.Errorf("statusClass(%d) = %q, want %q", status, got, want)
		}
	}
}

func TestProblemConstructorsCopyRatherThanMutate(t *testing.T) {
	base := Newf(http.StatusBadRequest, "the field %q is not a number", "age")
	if base.Detail != `the field "age" is not a number` {
		t.Fatalf("detail = %q", base.Detail)
	}

	detailed := base.WithDetail("a different reason")
	if base.Detail == detailed.Detail {
		t.Fatal("WithDetail mutated the original problem")
	}
	if detailed.Status != base.Status {
		t.Errorf("status = %d, want it carried over", detailed.Status)
	}
}

func TestRouteFromPatternDropsOnlyAMethod(t *testing.T) {
	cases := map[string]string{
		"GET /v1/items/{id}": "/v1/items/{id}",
		"/v1/items":          "/v1/items",
		"POST /v1/items":     "/v1/items",
		"example.com/v1/x":   "example.com/v1/x",
	}
	for pattern, want := range cases {
		if got := routeFromPattern(pattern); got != want {
			t.Errorf("routeFromPattern(%q) = %q, want %q", pattern, got, want)
		}
	}
}

func TestDisabledStagesArePassThrough(t *testing.T) {
	captureDefaultLogger(t)

	h := Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), Options{
		Timeout:      -1,
		MaxBodyBytes: -1,
		RateLimit:    &RateLimitOptions{Rate: 0},
	})

	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/items", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with every optional stage disabled", res.Code)
	}
}
