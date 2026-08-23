package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/reference-service/internal/auth"
	"github.com/example/reference-service/internal/auth/authtest"
)

// junk sets an Authorization header the anonymous authenticator must not read.
func junk(value string) authtest.Credential {
	return func(r *http.Request) { r.Header.Set("Authorization", value) }
}

// The anonymous authenticator passes the same suite as the verifying
// implementations. It never returns an error, so every failure case is
// satisfied the other way the suite allows: an anonymous principal that holds
// no scopes and therefore grants nothing.
func TestAnonymousAuthenticatorConformance(t *testing.T) {
	authtest.Suite{
		Name:              "AnonymousAuthenticator",
		Authenticator:     auth.AnonymousAuthenticator{},
		Valid:             func(*http.Request) {},
		Subject:           "",
		Kind:              auth.KindAnonymous,
		Expired:           junk("Bearer expired.token.here"),
		WrongAudience:     junk("Bearer wrong.audience.here"),
		UnknownKey:        junk("Bearer unknown.key.here"),
		Malformed:         junk("Bearer not-a-token"),
		Revoked:           junk("Key revoked-service-key"),
		InsufficientScope: "orders:read",
	}.Run(t)
}

func TestAnonymousAuthenticatorIgnoresAnyCredential(t *testing.T) {
	for _, header := range []string{"", "Bearer anything", "Key anything"} {
		r := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		p, err := auth.AnonymousAuthenticator{}.Authenticate(context.Background(), r)
		if err != nil {
			t.Fatalf("the anonymous authenticator failed for header %q: %v", header, err)
		}
		if !p.IsAnonymous() || p.Subject != "" || len(p.Scopes) != 0 {
			t.Fatalf("principal = %+v for header %q, want the anonymous principal", p, header)
		}
	}
}
