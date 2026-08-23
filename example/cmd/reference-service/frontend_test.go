package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/reference-service/internal/auth"
	"github.com/example/reference-service/internal/httpx"
)

// The shell is mounted at the lowest route precedence, so a client-side route
// that nothing registered is answered with the application document and not
// with a 404. Without this a hard load of a deep link fails while the same
// route works after in-browser navigation.
func TestTheShellAnswersAClientSideRoute(t *testing.T) {
	a := newTestAssembly(t)
	if err := serveShell(a); err != nil {
		t.Fatalf("serveShell: %v", err)
	}

	rec := httptest.NewRecorder()
	a.handler(true).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/orders", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Errorf("content type = %q, want an HTML document", rec.Header().Get("Content-Type"))
	}
}

// An application route still answers first. The shell is a fallback, not a
// catch-all that shadows the interface.
func TestAnApplicationRouteWinsOverTheShell(t *testing.T) {
	a := newTestAssembly(t)
	a.routes.HandleFunc(http.MethodGet, "/v1/status", auth.PublicPolicy(),
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	if err := serveShell(a); err != nil {
		t.Fatalf("serveShell: %v", err)
	}

	if rec := serveThrough(a, http.MethodGet, "/v1/status"); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

// An unmatched path under the interface prefix is an interface error and is
// answered with the envelope, not with markup. A typo in a client call has to
// read as a 404 a client can parse, not as an HTML document.
func TestAnUnmatchedInterfacePathIsNotAnsweredWithTheShell(t *testing.T) {
	a := newTestAssembly(t)
	if err := serveShell(a); err != nil {
		t.Fatalf("serveShell: %v", err)
	}

	target := httpx.Prefix(httpx.CurrentMajor) + "/orders/42"
	rec := serveThrough(a, http.MethodGet, target)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status for %s = %d, want %d", target, rec.Code, http.StatusNotFound)
	}
	if got := rec.Header().Get("Content-Type"); got != httpx.ProblemContentType {
		t.Errorf("content type = %q, want %q", got, httpx.ProblemContentType)
	}
}
