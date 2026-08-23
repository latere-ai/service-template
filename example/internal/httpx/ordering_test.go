package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// callerKey carries the identity a stand-in authentication stage produces. The
// real one lives in the auth package; the chain only fixes where it runs.
type callerKeyType struct{}

var callerKey callerKeyType

// authStage refuses a request with no caller header and otherwise puts the
// caller in the context, which is the shape the chain's auth slot expects.
func authStage(t *testing.T) func(http.Handler) http.Handler {
	t.Helper()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller := r.Header.Get("X-Caller")
			if caller == "" {
				WriteError(w, r, New(http.StatusUnauthorized, "the request carries no credential"))
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey, caller)))
		})
	}
}

func TestPreflightIsAnsweredBeforeAuthenticationRefusesIt(t *testing.T) {
	captureDefaultLogger(t)

	h := Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a preflight reached the handler")
	}), Options{
		CORS: &CORSOptions{AllowedOrigins: []string{"https://app.example.com"}},
		Auth: authStage(t),
	})

	r := httptest.NewRequest(http.MethodOptions, "/v1/items", nil)
	r.Header.Set("Origin", "https://app.example.com")
	r.Header.Set("Access-Control-Request-Method", http.MethodPost)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, r)

	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d: a preflight carries no credential, so authentication must not see it",
			res.Code, http.StatusNoContent)
	}

	// The same route without the preflight headers is still authenticated.
	res = httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/v1/items", nil))
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d for a real request with no credential", res.Code, http.StatusUnauthorized)
	}
}

func TestRateLimitKeysOnTheIdentityAuthenticationProduced(t *testing.T) {
	captureDefaultLogger(t)

	var keyed []string
	h := Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), Options{
		Auth: authStage(t),
		RateLimit: &RateLimitOptions{
			Rate:  1,
			Burst: 1,
			Key: func(r *http.Request) string {
				caller, _ := r.Context().Value(callerKey).(string)
				keyed = append(keyed, caller)
				return caller
			},
		},
	})

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
		t.Fatalf("bob = %d, want 200: the budget belongs to the caller, not the address", code)
	}

	for i, k := range keyed {
		if k == "" {
			t.Fatalf("rate limit key %d was empty, so the limiter ran before authentication", i)
		}
	}
}

func TestUnauthenticatedRefusalCarriesTheEnvelopeAndTheRequestID(t *testing.T) {
	captureDefaultLogger(t)

	h := Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), Options{Auth: authStage(t)})

	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/items", nil))

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusUnauthorized)
	}
	p := decodeEnvelope(t, res)
	if p.Instance == "" || p.Instance != res.Header().Get(HeaderRequestID) {
		t.Fatalf("instance = %q, want the assigned request identifier %q", p.Instance, res.Header().Get(HeaderRequestID))
	}
}

func TestAccessLogCarriesTheRouteTheSpanStageResolved(t *testing.T) {
	logs := captureDefaultLogger(t)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/items/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := Handler(mux, Options{Router: mux})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/items/42", nil))

	record := logs.find(t, func(r logRecord) bool { return r.Msg == "request" })
	if want := "/v1/items/{id}"; record.Route != want {
		t.Fatalf("access log route = %q, want %q: the span stage resolves the route before the access log runs",
			record.Route, want)
	}
	if record.RequestID == "" {
		t.Error("the access log entry carries no request identifier")
	}
}
