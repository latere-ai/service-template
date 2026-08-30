package httpx

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRequestIDIsAssignedEchoedAndReadable(t *testing.T) {
	captureDefaultLogger(t)

	var seen string
	h := AssignRequestID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = RequestID(r.Context())
	}))

	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/", nil))

	if seen == "" || !strings.HasPrefix(seen, requestIDPrefix) {
		t.Fatalf("request id = %q, want one with the %q prefix", seen, requestIDPrefix)
	}
	if got := res.Header().Get(HeaderRequestID); got != seen {
		t.Errorf("response header = %q, want %q", got, seen)
	}
}

func TestRequestIDAdoptsASaneInboundValueAndRejectsOthers(t *testing.T) {
	captureDefaultLogger(t)

	cases := map[string]struct {
		inbound string
		adopted bool
	}{
		"caller value":  {"req_from_the_gateway", true},
		"empty":         {"", false},
		"too long":      {strings.Repeat("a", maxInboundRequestID+1), false},
		"newline":       {"abc\ndef", false},
		"control byte":  {"abc\x00", false},
		"space":         {"abc def", false},
		"non ascii":     {"reqé", false},
		"at the length": {strings.Repeat("b", maxInboundRequestID), true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var seen string
			h := AssignRequestID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				seen = RequestID(r.Context())
			}))
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.inbound != "" {
				r.Header.Set(HeaderRequestID, tc.inbound)
			}
			h.ServeHTTP(httptest.NewRecorder(), r)

			if tc.adopted && seen != tc.inbound {
				t.Fatalf("request id = %q, want the inbound %q", seen, tc.inbound)
			}
			if !tc.adopted && seen == tc.inbound {
				t.Fatalf("the inbound value %q was adopted", tc.inbound)
			}
		})
	}
}

func TestRequestIDsAreUniqueAndSortByTime(t *testing.T) {
	// The timestamp occupies the leading characters, so identifiers minted
	// later never sort before earlier ones. The random tail decides the order
	// inside one millisecond, which is why only the timestamp is compared.
	// Nine characters carry forty-five of the forty-eight timestamp bits,
	// which resolves to eight milliseconds.
	const timestampChars = 9
	const stampResolution = 8 * time.Millisecond

	seen := map[string]bool{}
	previous := ""
	for range 1000 {
		id := NewRequestID()
		if seen[id] {
			t.Fatalf("duplicate request id %q", id)
		}
		seen[id] = true

		stamp := strings.TrimPrefix(id, requestIDPrefix)[:timestampChars]
		if stamp < previous {
			t.Fatalf("request id %q sorts before the earlier stamp %q", id, previous)
		}
		previous = stamp
	}

	time.Sleep(2 * stampResolution)
	later := strings.TrimPrefix(NewRequestID(), requestIDPrefix)[:timestampChars]
	if later <= previous {
		t.Fatalf("a later identifier stamp %q does not sort after %q", later, previous)
	}
}

func TestBodyLimitRejectsALargeBody(t *testing.T) {
	captureDefaultLogger(t)

	h := Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			WriteError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}), Options{MaxBodyBytes: 16})

	t.Run("declared length above the cap", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/v1/items", strings.NewReader(strings.Repeat("x", 64)))
		res := httptest.NewRecorder()
		h.ServeHTTP(res, r)

		if res.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusRequestEntityTooLarge)
		}
		decodeEnvelope(t, res)
	})

	t.Run("undeclared body above the cap", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/v1/items", strings.NewReader(strings.Repeat("x", 64)))
		r.ContentLength = -1
		res := httptest.NewRecorder()
		h.ServeHTTP(res, r)

		if res.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusRequestEntityTooLarge)
		}
		decodeEnvelope(t, res)
	})

	t.Run("body inside the cap", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/v1/items", strings.NewReader("small"))
		res := httptest.NewRecorder()
		h.ServeHTTP(res, r)

		if res.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", res.Code, http.StatusNoContent)
		}
	})
}

func TestCORSAnswersAPreflightForAnAllowedOrigin(t *testing.T) {
	h := CORS(CORSOptions{
		AllowedOrigins:   []string{"https://app.example.com"},
		AllowCredentials: true,
		MaxAge:           10 * time.Minute,
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a preflight reached the handler")
	}))

	r := httptest.NewRequest(http.MethodOptions, "/v1/items", nil)
	r.Header.Set("Origin", "https://app.example.com")
	r.Header.Set("Access-Control-Request-Method", http.MethodPost)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, r)

	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusNoContent)
	}
	if got := res.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("allow-origin = %q", got)
	}
	if got := res.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("allow-credentials = %q", got)
	}
	if got := res.Header().Get("Access-Control-Max-Age"); got != "600" {
		t.Errorf("max-age = %q", got)
	}
	if !strings.Contains(res.Header().Get("Vary"), "Origin") {
		t.Errorf("vary = %q, want it to name Origin", res.Header().Get("Vary"))
	}
}

func TestCORSRefusesAnUnknownOriginAndKeepsSameOriginUntouched(t *testing.T) {
	reached := false
	h := CORS(CORSOptions{AllowedOrigins: []string{"https://app.example.com"}})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reached = true
			w.WriteHeader(http.StatusOK)
		}))

	t.Run("unknown origin", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/v1/items", nil)
		r.Header.Set("Origin", "https://evil.example.net")
		res := httptest.NewRecorder()
		h.ServeHTTP(res, r)

		if got := res.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("allow-origin = %q, want none", got)
		}
		if !reached {
			t.Error("a simple request from an unknown origin was blocked server-side, which CORS does not do")
		}
	})

	t.Run("same origin", func(t *testing.T) {
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/items", nil))
		if got := res.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("allow-origin = %q, want none", got)
		}
	})
}

func TestCORSWildcardNeverGrantsCredentials(t *testing.T) {
	h := CORS(CORSOptions{AllowedOrigins: []string{"*"}, AllowCredentials: true})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	r := httptest.NewRequest(http.MethodGet, "/v1/items", nil)
	r.Header.Set("Origin", "https://anywhere.example.net")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, r)

	if got := res.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("allow-origin = %q, want *", got)
	}
	if got := res.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("allow-credentials = %q, want none beside a wildcard origin", got)
	}
}

func TestTimeoutRendersTheEnvelopeWhenTheHandlerOverruns(t *testing.T) {
	captureDefaultLogger(t)

	released := make(chan struct{})
	h := Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-released
		// A write after the deadline must not reach the client.
		w.WriteHeader(http.StatusOK)
	}), Options{Timeout: 20 * time.Millisecond})

	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/items", nil))
	close(released)

	if res.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusGatewayTimeout)
	}
	p := decodeEnvelope(t, res)
	if p.Instance == "" {
		t.Error("the timeout envelope carries no request identifier")
	}
}

func TestTimeoutCancelsTheHandlerContext(t *testing.T) {
	captureDefaultLogger(t)

	observed := make(chan error, 1)
	h := Handler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		observed <- r.Context().Err()
	}), Options{Timeout: 20 * time.Millisecond})

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/items", nil))

	select {
	case err := <-observed:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("handler context error = %v, want %v", err, context.DeadlineExceeded)
		}
	case <-time.After(time.Second):
		t.Fatal("the handler context was never cancelled")
	}
}

func TestTimeoutLeavesACommittedResponseAlone(t *testing.T) {
	captureDefaultLogger(t)

	released := make(chan struct{})
	h := Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("streamed")); err != nil {
			t.Errorf("write the first chunk: %v", err)
		}
		<-released
	}), Options{Timeout: 20 * time.Millisecond})

	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/items", nil))
	close(released)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want the committed %d", res.Code, http.StatusOK)
	}
	if res.Body.String() != "streamed" {
		t.Fatalf("body = %q, want the committed body alone", res.Body.String())
	}
}

func TestTimeoutPropagatesAPanicToTheRecoveryStage(t *testing.T) {
	captureDefaultLogger(t)

	h := Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("inside the timeout goroutine")
	}), Options{Timeout: time.Second})

	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/items", nil))

	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusInternalServerError)
	}
	decodeEnvelope(t, res)
}

func TestRateLimitSpendsABurstThenRefills(t *testing.T) {
	captureDefaultLogger(t)

	now := time.Now()
	clock := func() time.Time { return now }
	h := Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), Options{RateLimit: &RateLimitOptions{Rate: 10, Burst: 2, Now: clock}})

	send := func() *httptest.ResponseRecorder {
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/items", nil))
		return res
	}

	for i := range 2 {
		if res := send(); res.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200 inside the burst", i, res.Code)
		}
	}

	res := send()
	if res.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d once the burst is spent", res.Code, http.StatusTooManyRequests)
	}
	if res.Header().Get("Retry-After") == "" {
		t.Error("the refusal carries no Retry-After header")
	}
	decodeEnvelope(t, res)

	// One tenth of a second refills exactly one token at ten per second.
	now = now.Add(100 * time.Millisecond)
	if res := send(); res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 after a refill", res.Code)
	}
}

func TestRateLimitBudgetsEachKeySeparately(t *testing.T) {
	captureDefaultLogger(t)

	h := Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), Options{RateLimit: &RateLimitOptions{
		Rate:  1,
		Burst: 1,
		Key:   func(r *http.Request) string { return r.Header.Get("X-Caller") },
	}})

	send := func(caller string) int {
		r := httptest.NewRequest(http.MethodGet, "/v1/items", nil)
		r.Header.Set("X-Caller", caller)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, r)
		return res.Code
	}

	if code := send("alice"); code != http.StatusOK {
		t.Fatalf("first alice request = %d, want 200", code)
	}
	if code := send("alice"); code != http.StatusTooManyRequests {
		t.Fatalf("second alice request = %d, want 429", code)
	}
	if code := send("bob"); code != http.StatusOK {
		t.Fatalf("first bob request = %d, want 200, because a budget is per key", code)
	}
}

func TestRateLimitEvictsIdleBuckets(t *testing.T) {
	now := time.Now()
	l := newLimiter(RateLimitOptions{
		Rate:        1,
		Burst:       1,
		Now:         func() time.Time { return now },
		IdleTimeout: time.Minute,
	})

	if ok, _ := l.allow("gone"); !ok {
		t.Fatal("the first request was refused")
	}
	now = now.Add(2 * time.Minute)
	if ok, _ := l.allow("present"); !ok {
		t.Fatal("the request after the idle period was refused")
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.buckets["gone"]; ok {
		t.Fatal("an idle bucket survived the sweep")
	}
	if _, ok := l.buckets["present"]; !ok {
		t.Fatal("the live bucket was swept")
	}
}

func TestPanicAfterTheDeadlineIsStillRecorded(t *testing.T) {
	logs := captureDefaultLogger(t)

	released := make(chan struct{})
	h := Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-released
		panic("late and unattended")
	}), Options{Timeout: 20 * time.Millisecond})

	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/items", nil))
	if res.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusGatewayTimeout)
	}
	close(released)

	// The handler goroutine outlives the response, so the record arrives after
	// the request is answered.
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(logs.text(), "handler panicked after the deadline") {
		if time.Now().After(deadline) {
			t.Fatalf("a panic after the deadline left no record; captured:\n%s", logs.text())
		}
		time.Sleep(time.Millisecond)
	}

	record := logs.find(t, func(r logRecord) bool { return r.Msg == "handler panicked after the deadline" })
	if !strings.Contains(record.Stack, "middleware_test.go") {
		t.Errorf("the record carries no stack from the panic site: %q", record.Stack)
	}
	if record.RequestID == "" {
		t.Error("the record names no request")
	}
	if body := res.Body.String(); strings.Contains(body, "unattended") {
		t.Errorf("the panic message reached the response body: %s", body)
	}
}
