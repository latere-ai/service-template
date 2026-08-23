package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/reference-service/internal/auth"
)

func TestPrincipalContextRoundTrip(t *testing.T) {
	want := &auth.Principal{Subject: "u1", Kind: auth.KindUser, Scopes: []string{"orders"}}
	got, ok := auth.FromContext(auth.NewContext(context.Background(), want))
	if !ok || got != want {
		t.Fatalf("FromContext = (%+v, %v), want the stored principal", got, ok)
	}
}

// A handler that runs with no middleware must see no principal, because
// treating an absent principal as an anonymous one turns a wiring mistake into
// an open route.
func TestFromContextReportsAnAbsentPrincipal(t *testing.T) {
	if p, ok := auth.FromContext(context.Background()); ok {
		t.Fatalf("FromContext on a bare context returned %+v", p)
	}
	if p, ok := auth.FromContext(auth.NewContext(context.Background(), nil)); ok {
		t.Fatalf("FromContext on a nil principal returned %+v", p)
	}
}

func TestAnonymousPrincipalHoldsNothing(t *testing.T) {
	p := auth.AnonymousPrincipal()
	if !p.IsAnonymous() {
		t.Error("the anonymous principal does not report itself as anonymous")
	}
	if len(p.Scopes) != 0 {
		t.Errorf("the anonymous principal holds %v", p.Scopes)
	}
	if p.HasScope("orders:read") {
		t.Error("the anonymous principal satisfies orders:read")
	}
	if !(*auth.Principal)(nil).IsAnonymous() {
		t.Error("a nil principal does not report itself as anonymous")
	}
	if (*auth.Principal)(nil).HasScope("orders:read") {
		t.Error("a nil principal satisfies orders:read")
	}
}

func TestDenialClassesMapToStatuses(t *testing.T) {
	cases := []struct {
		err        error
		wantStatus int
		wantTitle  string
		wantOK     bool
	}{
		{auth.Unauthenticated("token expired"), http.StatusUnauthorized, "Unauthorized", true},
		{auth.Forbidden("scope missing"), http.StatusForbidden, "Forbidden", true},
		{errors.New("a database is unreachable"), 0, "", false},
		{nil, 0, "", false},
	}
	for _, c := range cases {
		status, title, ok := auth.PublicStatus(c.err)
		if status != c.wantStatus || title != c.wantTitle || ok != c.wantOK {
			t.Errorf("PublicStatus(%v) = (%d, %q, %v), want (%d, %q, %v)",
				c.err, status, title, ok, c.wantStatus, c.wantTitle, c.wantOK)
		}
	}
}

func TestDenialErrorCarriesTheReasonForTheLogOnly(t *testing.T) {
	err := auth.Unauthenticated("token expired at %s", "2026-01-01T00:00:00Z")
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("the denial %v is not an ErrUnauthenticated", err)
	}
	if !strings.Contains(err.Error(), "token expired at 2026-01-01T00:00:00Z") {
		t.Errorf("the denial reason %q does not name the failed check", err.Error())
	}
	rec := httptest.NewRecorder()
	auth.WriteDenial(rec, err)
	if strings.Contains(rec.Body.String(), "expired") {
		t.Errorf("the response body %q carries the reason", rec.Body.String())
	}
}

func TestWriteDenialRendersOneShapePerClass(t *testing.T) {
	cases := []struct {
		err        error
		wantStatus int
		wantBody   string
	}{
		{auth.Unauthenticated("unknown key id %q", "k9"), http.StatusUnauthorized,
			`{"title":"Unauthorized","status":401}`},
		{auth.Forbidden("missing scope %q", "orders:write"), http.StatusForbidden,
			`{"title":"Forbidden","status":403}`},
		{errors.New("the store is down"), http.StatusInternalServerError,
			`{"title":"Internal Server Error","status":500}`},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		auth.WriteDenial(rec, c.err)
		if rec.Code != c.wantStatus {
			t.Errorf("WriteDenial(%v) status = %d, want %d", c.err, rec.Code, c.wantStatus)
		}
		if got := rec.Body.String(); got != c.wantBody {
			t.Errorf("WriteDenial(%v) body = %s, want %s", c.err, got, c.wantBody)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
			t.Errorf("WriteDenial(%v) content type = %q", c.err, got)
		}
	}
}

func TestWriteDenialChallengesAnUnauthenticatedCaller(t *testing.T) {
	rec := httptest.NewRecorder()
	auth.WriteDenial(rec, auth.Unauthenticated("no header"))
	if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
		t.Errorf("WWW-Authenticate = %q, want %q", got, "Bearer")
	}
	rec = httptest.NewRecorder()
	auth.WriteDenial(rec, auth.Forbidden("no scope"))
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("a forbidden response challenges with %q; the caller is already identified", got)
	}
}

func TestBearerTokenReadsTheAuthorizationHeader(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
		wantOK bool
	}{
		{"exact scheme", "Bearer abc.def", "abc.def", true},
		{"lower case scheme", "bearer abc.def", "abc.def", true},
		{"mixed case scheme keeps the credential case", "BeArEr AbC.DeF", "AbC.DeF", true},
		{"padded credential", "Bearer   abc  ", "abc", true},
		{"no header", "", "", false},
		{"other scheme", "Basic abc", "", false},
		{"no credential", "Bearer ", "", false},
		{"scheme only", "Bearer", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
			if c.header != "" {
				r.Header.Set("Authorization", c.header)
			}
			got, err := auth.BearerToken(r, "Bearer")
			if c.wantOK {
				if err != nil {
					t.Fatalf("BearerToken(%q) failed: %v", c.header, err)
				}
				if got != c.want {
					t.Fatalf("BearerToken(%q) = %q, want %q", c.header, got, c.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("BearerToken(%q) = %q, want a denial", c.header, got)
			}
			if !errors.Is(err, auth.ErrUnauthenticated) {
				t.Fatalf("BearerToken(%q) = %v, want an ErrUnauthenticated denial", c.header, err)
			}
		})
	}
}

func TestAuthenticatorFuncAndAuthorizerFuncAdapt(t *testing.T) {
	want := &auth.Principal{Subject: "u1", Kind: auth.KindUser}
	var a auth.Authenticator = auth.AuthenticatorFunc(
		func(context.Context, *http.Request) (*auth.Principal, error) { return want, nil })
	got, err := a.Authenticate(context.Background(), httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil || got != want {
		t.Fatalf("AuthenticatorFunc = (%+v, %v), want the principal", got, err)
	}
	sentinel := auth.Forbidden("no")
	var z auth.Authorizer = auth.AuthorizerFunc(
		func(context.Context, *auth.Principal, string, string) error { return sentinel })
	if err := z.Authorize(context.Background(), want, "read", "orders"); !errors.Is(err, sentinel) {
		t.Fatalf("AuthorizerFunc = %v, want the sentinel", err)
	}
}
