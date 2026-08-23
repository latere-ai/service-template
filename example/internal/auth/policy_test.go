package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/reference-service/internal/auth"
)

// staticAuthenticator returns one fixed outcome, so a guard test states the
// identity result it wants without minting a credential.
type staticAuthenticator struct {
	principal *auth.Principal
	err       error
}

func (s staticAuthenticator) Authenticate(context.Context, *http.Request) (*auth.Principal, error) {
	return s.principal, s.err
}

func newGuard(a auth.Authenticator, logs *bytes.Buffer) *auth.Guard {
	return &auth.Guard{
		Authenticator: a,
		Logger:        slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

func recordingHandler(ran *bool, seen **auth.Principal) http.Handler {
	return http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		*ran = true
		p, _ := auth.FromContext(r.Context())
		*seen = p
	})
}

func TestPolicyDecided(t *testing.T) {
	cases := []struct {
		policy  auth.Policy
		decided bool
		text    string
	}{
		{auth.Policy{}, false, "undecided"},
		{auth.PublicPolicy(), true, "public"},
		{auth.Guarded("read", "orders"), true, "read orders"},
		{auth.Policy{Action: "read"}, false, "undecided"},
		{auth.Policy{Resource: "orders"}, false, "undecided"},
		{auth.Policy{Public: true, Action: "read", Resource: "orders"}, false, "undecided"},
	}
	for _, c := range cases {
		if got := c.policy.Decided(); got != c.decided {
			t.Errorf("Policy%+v.Decided() = %v, want %v", c.policy, got, c.decided)
		}
		if got := c.policy.String(); got != c.text {
			t.Errorf("Policy%+v.String() = %q, want %q", c.policy, got, c.text)
		}
	}
}

func TestRouteTableReportsRoutesWithNoDecision(t *testing.T) {
	table := auth.NewRouteTable(newGuard(staticAuthenticator{principal: auth.AnonymousPrincipal()}, &bytes.Buffer{}))
	table.HandleFunc(http.MethodGet, "/v1/health", auth.PublicPolicy(), func(http.ResponseWriter, *http.Request) {})
	table.HandleFunc(http.MethodGet, "/v1/orders", auth.Guarded("read", "orders"), func(http.ResponseWriter, *http.Request) {})
	if err := table.Validate(); err != nil {
		t.Fatalf("a table of decided routes failed validation: %v", err)
	}

	// The gate has to fail for the route it exists to catch.
	table.HandleFunc(http.MethodPost, "/v1/orders", auth.Policy{}, func(http.ResponseWriter, *http.Request) {})
	err := table.Validate()
	if err == nil {
		t.Fatal("a route with neither an authorization rule nor a public marker passed validation")
	}
	if !strings.Contains(err.Error(), "POST /v1/orders") {
		t.Fatalf("the failure %q does not name the undecided route", err)
	}
	if strings.Contains(err.Error(), "/v1/health") {
		t.Fatalf("the failure %q names a decided route", err)
	}
	if got := table.Routes(); len(got) != 3 {
		t.Fatalf("Routes() returned %d entries, want 3", len(got))
	}
}

// The static check is one half. A route with no decision must also deny at
// request time, because a table that is never validated would otherwise serve
// it.
func TestUndecidedRouteDeniesAtRequestTime(t *testing.T) {
	var logs bytes.Buffer
	guard := newGuard(staticAuthenticator{
		principal: &auth.Principal{Subject: "u1", Kind: auth.KindUser, Scopes: []string{"*"}},
	}, &logs)
	table := auth.NewRouteTable(guard)
	ran := false
	table.HandleFunc(http.MethodPost, "/v1/orders", auth.Policy{}, func(http.ResponseWriter, *http.Request) { ran = true })

	rec := httptest.NewRecorder()
	table.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/orders", nil))
	if ran {
		t.Fatal("the handler of an undecided route ran")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if !strings.Contains(logs.String(), "carries no authorization decision") {
		t.Fatalf("the log %q does not state why the route was denied", logs.String())
	}
}

func TestGuardedRouteAdmitsASufficientScope(t *testing.T) {
	want := &auth.Principal{Subject: "u1", Kind: auth.KindUser, Scopes: []string{"orders"}}
	var logs bytes.Buffer
	ran := false
	var seen *auth.Principal
	handler := newGuard(staticAuthenticator{principal: want}, &logs).
		Protect(auth.Guarded("write", "orders"), recordingHandler(&ran, &seen))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/orders", nil))
	if !ran {
		t.Fatalf("the handler did not run, status = %d body = %s", rec.Code, rec.Body)
	}
	if seen != want {
		t.Fatalf("the handler saw principal %+v, want %+v", seen, want)
	}
}

func TestGuardedRouteRefusesAnInsufficientScope(t *testing.T) {
	var logs bytes.Buffer
	ran := false
	var seen *auth.Principal
	principal := &auth.Principal{Subject: "u1", Kind: auth.KindUser, Scopes: []string{"orders:read"}}
	handler := newGuard(staticAuthenticator{principal: principal}, &logs).
		Protect(auth.Guarded("write", "orders"), recordingHandler(&ran, &seen))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/orders", nil))
	if ran {
		t.Fatal("the handler ran for a principal without the scope")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if strings.Contains(rec.Body.String(), "orders:write") {
		t.Fatalf("the response %s names the missing scope", rec.Body)
	}
	if !strings.Contains(logs.String(), "orders:write") {
		t.Fatalf("the log %q does not name the missing scope", logs.String())
	}
}

// The reason belongs in the log and the log alone. This asserts both halves at
// once: the record carries it, the response does not.
func TestDenialReasonIsLoggedAndNotReturned(t *testing.T) {
	var logs bytes.Buffer
	handler := newGuard(staticAuthenticator{err: auth.Unauthenticated("token expired at 2026-01-01")}, &logs).
		Protect(auth.Guarded("read", "orders"), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/orders", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &record); err != nil {
		t.Fatalf("the log record does not parse: %v (%q)", err, logs.String())
	}
	reason, _ := record["reason"].(string)
	if !strings.Contains(reason, "token expired at 2026-01-01") {
		t.Fatalf("the logged reason %q does not name the failed check", reason)
	}
	if path, _ := record["path"].(string); path != "/v1/orders" {
		t.Fatalf("the log record path = %q, want the request path", path)
	}
	if strings.Contains(rec.Body.String(), "expired") {
		t.Fatalf("the response %s carries the reason", rec.Body)
	}
}

func TestPublicRouteAdmitsACallerWithNoCredential(t *testing.T) {
	var logs bytes.Buffer
	ran := false
	var seen *auth.Principal
	handler := newGuard(auth.AnonymousAuthenticator{}, &logs).
		Protect(auth.PublicPolicy(), recordingHandler(&ran, &seen))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	if !ran {
		t.Fatalf("a public route denied a caller with no credential, status = %d", rec.Code)
	}
	if !seen.IsAnonymous() {
		t.Fatalf("the handler saw %+v, want the anonymous principal", seen)
	}
}

// A public route with an identity source still admits a caller whose
// credential fails, and records the rejection, because a broken client on a
// public route is a support question and not an outage.
func TestPublicRouteAdmitsARejectedCredentialAsAnonymous(t *testing.T) {
	var logs bytes.Buffer
	ran := false
	var seen *auth.Principal
	handler := newGuard(staticAuthenticator{err: auth.Unauthenticated("signature does not verify")}, &logs).
		Protect(auth.PublicPolicy(), recordingHandler(&ran, &seen))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	if !ran {
		t.Fatalf("a public route denied a caller carrying a bad credential, status = %d", rec.Code)
	}
	if !seen.IsAnonymous() {
		t.Fatalf("the handler saw %+v, want the anonymous principal", seen)
	}
	if !strings.Contains(logs.String(), "signature does not verify") {
		t.Fatalf("the log %q does not record the rejected credential", logs.String())
	}
}

// A public route carries the authenticated principal when the credential is
// good, so a handler can vary its response for a known caller without a second
// route.
func TestPublicRouteCarriesAnAcceptedPrincipal(t *testing.T) {
	want := &auth.Principal{Subject: "u1", Kind: auth.KindUser, Scopes: []string{"orders:read"}}
	ran := false
	var seen *auth.Principal
	handler := newGuard(staticAuthenticator{principal: want}, &bytes.Buffer{}).
		Protect(auth.PublicPolicy(), recordingHandler(&ran, &seen))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	if !ran || seen != want {
		t.Fatalf("the handler saw %+v, want %+v", seen, want)
	}
}

func TestGuardFailsClosedOnAMisconfiguration(t *testing.T) {
	cases := []struct {
		name  string
		guard *auth.Guard
	}{
		{"no authenticator", &auth.Guard{}},
		{"authenticator returns nothing", &auth.Guard{Authenticator: staticAuthenticator{}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var logs bytes.Buffer
			c.guard.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
			ran := false
			handler := c.guard.ProtectFunc(auth.Guarded("read", "orders"),
				func(http.ResponseWriter, *http.Request) { ran = true })
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/orders", nil))
			if ran {
				t.Fatal("the handler ran")
			}
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestGuardUsesTheConfiguredDenialWriter(t *testing.T) {
	var written error
	guard := &auth.Guard{
		Authenticator: staticAuthenticator{err: auth.Unauthenticated("no header")},
		Logger:        slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
		OnDeny: func(w http.ResponseWriter, _ *http.Request, err error) {
			written = err
			w.WriteHeader(http.StatusTeapot)
		},
	}
	rec := httptest.NewRecorder()
	guard.Protect(auth.Guarded("read", "orders"), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/orders", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, the configured writer did not run", rec.Code)
	}
	if !errors.Is(written, auth.ErrUnauthenticated) {
		t.Fatalf("the writer received %v, want the denial", written)
	}
}

// A service with its own decision model supplies an Authorizer, and the guard
// asks it instead of the scope rule.
func TestGuardUsesTheConfiguredAuthorizer(t *testing.T) {
	principal := &auth.Principal{Subject: "u1", Kind: auth.KindUser}
	var asked [2]string
	guard := newGuard(staticAuthenticator{principal: principal}, &bytes.Buffer{})
	guard.Authorizer = auth.AuthorizerFunc(func(_ context.Context, p *auth.Principal, action, resource string) error {
		asked = [2]string{action, resource}
		if p.Subject == "u1" {
			return nil
		}
		return auth.Forbidden("not u1")
	})
	ran := false
	var seen *auth.Principal
	rec := httptest.NewRecorder()
	guard.Protect(auth.Guarded("write", "orders"), recordingHandler(&ran, &seen)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/orders", nil))
	if !ran {
		t.Fatalf("the configured authorizer allowed the request and the handler did not run, status = %d", rec.Code)
	}
	if asked != [2]string{"write", "orders"} {
		t.Fatalf("the authorizer was asked for %v, want the route's action and resource", asked)
	}
}

func TestRouteStringNamesMethodPatternAndPolicy(t *testing.T) {
	r := auth.Route{Method: http.MethodGet, Pattern: "/v1/orders", Policy: auth.Guarded("read", "orders")}
	if got := r.String(); got != "GET /v1/orders (read orders)" {
		t.Fatalf("Route.String() = %q", got)
	}
}

// A table built without a guard still denies, so a caller that forgot to pass
// one does not get an open service.
func TestRouteTableWithNoGuardDenies(t *testing.T) {
	table := auth.NewRouteTable(nil)
	ran := false
	table.HandleFunc(http.MethodGet, "/v1/orders", auth.Guarded("read", "orders"),
		func(http.ResponseWriter, *http.Request) { ran = true })
	rec := httptest.NewRecorder()
	table.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/orders", nil))
	if ran || rec.Code != http.StatusUnauthorized {
		t.Fatalf("ran = %v, status = %d, want a denial", ran, rec.Code)
	}
}
