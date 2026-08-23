package auth_test

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/reference-service/internal/auth"
	"github.com/example/reference-service/internal/auth/authtest"
)

const (
	testAudience = "orders-api"
	testIssuer   = "identity"
)

var (
	activeKey  = []byte("active-signing-key-0123456789")
	strangeKey = []byte("a-key-this-service-does-not-hold")
	testClock  = time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
)

func newBearer(t *testing.T) *auth.BearerAuthenticator {
	t.Helper()
	return &auth.BearerAuthenticator{
		Keys:     map[string][]byte{"k1": activeKey},
		Audience: testAudience,
		Issuer:   testIssuer,
		Now:      func() time.Time { return testClock },
		Revoked:  auth.RevokedSet("withdrawn-1"),
	}
}

// validClaims is the credential every bearer case starts from. Each case
// changes one field, so a rejection is attributable to that field alone.
func validClaims() auth.Claims {
	return auth.Claims{
		Subject:   "u1",
		Kind:      auth.KindUser,
		Audience:  testAudience,
		Issuer:    testIssuer,
		Scopes:    []string{"orders:read", "invoices"},
		KeyID:     "k1",
		TokenID:   "live-1",
		IssuedAt:  testClock.Add(-time.Hour).Unix(),
		ExpiresAt: testClock.Add(time.Hour).Unix(),
	}
}

func bearerCredential(t *testing.T, key []byte, c auth.Claims) authtest.Credential {
	t.Helper()
	token, err := auth.SignToken(key, c)
	if err != nil {
		t.Fatalf("sign the token: %v", err)
	}
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

func TestBearerAuthenticatorConformance(t *testing.T) {
	expired := validClaims()
	expired.ExpiresAt = testClock.Add(-time.Minute).Unix()
	wrongAudience := validClaims()
	wrongAudience.Audience = "another-api"
	unknownKey := validClaims()
	unknownKey.KeyID = "k9"
	revoked := validClaims()
	revoked.TokenID = "withdrawn-1"

	authtest.Suite{
		Name:          "BearerAuthenticator",
		Authenticator: newBearer(t),
		Valid:         bearerCredential(t, activeKey, validClaims()),
		Subject:       "u1",
		Kind:          auth.KindUser,
		// "invoices" is broader than the requirement below it, so the suite
		// also proves containment reaches the principal.
		Scopes:            []string{"orders:read", "invoices:write"},
		Expired:           bearerCredential(t, activeKey, expired),
		WrongAudience:     bearerCredential(t, activeKey, wrongAudience),
		UnknownKey:        bearerCredential(t, strangeKey, unknownKey),
		Malformed:         func(r *http.Request) { r.Header.Set("Authorization", "Bearer not-a-token") },
		Revoked:           bearerCredential(t, activeKey, revoked),
		InsufficientScope: "orders:write",
	}.Run(t)
}

// authenticateToken runs the verifier over one token string.
func authenticateToken(t *testing.T, a *auth.BearerAuthenticator, token string) (*auth.Principal, error) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	return a.Authenticate(context.Background(), r)
}

func signValid(t *testing.T, c auth.Claims) string {
	t.Helper()
	token, err := auth.SignToken(activeKey, c)
	if err != nil {
		t.Fatalf("sign the token: %v", err)
	}
	return token
}

func TestBearerRejectsMalformedTokens(t *testing.T) {
	good := signValid(t, validClaims())
	parts := strings.SplitN(good, ".", 3)
	cases := []struct {
		name  string
		token string
	}{
		{"no version", "abc.def"},
		{"wrong version", "v2." + parts[1] + "." + parts[2]},
		{"no signature part", "v1." + parts[1]},
		{"payload is not base64", "v1.!!!." + parts[2]},
		{"signature is not base64", "v1." + parts[1] + ".!!!"},
		{"payload is not JSON", "v1." + base64.RawURLEncoding.EncodeToString([]byte("plain")) + "." + parts[2]},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := authenticateToken(t, newBearer(t), c.token)
			if err == nil {
				t.Fatalf("the verifier accepted %q as %+v", c.token, p)
			}
			if !errors.Is(err, auth.ErrUnauthenticated) {
				t.Fatalf("error = %v, want an ErrUnauthenticated denial", err)
			}
		})
	}
}

// A payload rewritten after signing must not verify. This is the check that
// makes every claim below it trustworthy.
func TestBearerRejectsATamperedPayload(t *testing.T) {
	forged := validClaims()
	forged.Scopes = []string{"*"}
	token := signValid(t, validClaims())
	parts := strings.SplitN(token, ".", 3)
	tamperedPayload, err := auth.SignToken(activeKey, forged)
	if err != nil {
		t.Fatalf("sign the forged claims: %v", err)
	}
	swapped := "v1." + strings.SplitN(tamperedPayload, ".", 3)[1] + "." + parts[2]

	if p, err := authenticateToken(t, newBearer(t), swapped); err == nil {
		t.Fatalf("the verifier accepted a tampered payload as %+v holding %v", p, p.Scopes)
	}
}

// A token signed with the wrong secret under a key identifier the verifier
// does hold must fail on the signature, not on the lookup.
func TestBearerRejectsAWrongSignatureUnderAKnownKeyID(t *testing.T) {
	token, err := auth.SignToken(strangeKey, validClaims())
	if err != nil {
		t.Fatalf("sign the token: %v", err)
	}
	_, err = authenticateToken(t, newBearer(t), token)
	if err == nil {
		t.Fatal("the verifier accepted a token signed with another key")
	}
	if !strings.Contains(err.Error(), "signature does not verify") {
		t.Fatalf("reason = %q, want the signature check", err)
	}
}

func TestBearerRejectsATokenWithNoExpiry(t *testing.T) {
	c := validClaims()
	c.ExpiresAt = 0
	_, err := authenticateToken(t, newBearer(t), signValid(t, c))
	if err == nil || !strings.Contains(err.Error(), "no expiry") {
		t.Fatalf("error = %v, want a rejection naming the missing expiry", err)
	}
}

func TestBearerLeewayAbsorbsClockSkew(t *testing.T) {
	c := validClaims()
	c.ExpiresAt = testClock.Add(-20 * time.Second).Unix()
	token := signValid(t, c)

	a := newBearer(t)
	if _, err := authenticateToken(t, a, token); err == nil {
		t.Fatal("a token 20 seconds past expiry was accepted with no leeway")
	}
	a.Leeway = time.Minute
	if _, err := authenticateToken(t, a, token); err != nil {
		t.Fatalf("a token inside the leeway was rejected: %v", err)
	}
}

// A verifier with no audience accepts a token minted for any service, so it
// refuses to run at all.
func TestBearerRequiresAConfiguredAudience(t *testing.T) {
	a := newBearer(t)
	a.Audience = ""
	_, err := authenticateToken(t, a, signValid(t, validClaims()))
	if err == nil || !strings.Contains(err.Error(), "no audience configured") {
		t.Fatalf("error = %v, want a rejection naming the missing audience", err)
	}
}

func TestBearerChecksTheIssuerWhenOneIsConfigured(t *testing.T) {
	c := validClaims()
	c.Issuer = "someone-else"
	if _, err := authenticateToken(t, newBearer(t), signValid(t, c)); err == nil {
		t.Fatal("a token from another issuer was accepted")
	}
	a := newBearer(t)
	a.Issuer = ""
	if _, err := authenticateToken(t, a, signValid(t, c)); err != nil {
		t.Fatalf("a verifier with no configured issuer rejected the token: %v", err)
	}
}

func TestBearerFailsClosedWhenRevocationCannotBeChecked(t *testing.T) {
	a := newBearer(t)
	a.Revoked = func(context.Context, string) (bool, error) {
		return false, errors.New("the revocation store is unreachable")
	}
	_, err := authenticateToken(t, a, signValid(t, validClaims()))
	if err == nil {
		t.Fatal("a token was accepted while revocation could not be checked")
	}
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("error = %v, want an ErrUnauthenticated denial", err)
	}
}

func TestBearerCarriesKindAndExtraClaims(t *testing.T) {
	c := validClaims()
	c.Kind = auth.KindService
	c.Extra = map[string]any{"tenant": "acme"}
	p, err := authenticateToken(t, newBearer(t), signValid(t, c))
	if err != nil {
		t.Fatalf("the token was rejected: %v", err)
	}
	if p.Kind != auth.KindService {
		t.Errorf("kind = %q, want %q", p.Kind, auth.KindService)
	}
	if p.Claims["tenant"] != "acme" {
		t.Errorf("claims = %v, want the tenant claim", p.Claims)
	}

	c.Kind = ""
	p, err = authenticateToken(t, newBearer(t), signValid(t, c))
	if err != nil {
		t.Fatalf("the token was rejected: %v", err)
	}
	if p.Kind != auth.KindUser {
		t.Errorf("kind = %q, want %q for a token that states none", p.Kind, auth.KindUser)
	}
}

func TestSignTokenNeedsAKey(t *testing.T) {
	if _, err := auth.SignToken(nil, validClaims()); err == nil {
		t.Fatal("a token was signed with no key")
	}
}

func TestSignTokenReportsClaimsThatCannotBeEncoded(t *testing.T) {
	c := validClaims()
	c.Extra = map[string]any{"channel": make(chan int)}
	if _, err := auth.SignToken(activeKey, c); err == nil {
		t.Fatal("claims that JSON cannot encode were signed")
	}
}

func TestRevokedSetReportsMembership(t *testing.T) {
	revoked := auth.RevokedSet("a", "b")
	for _, c := range []struct {
		id   string
		want bool
	}{{"a", true}, {"b", true}, {"c", false}, {"", false}} {
		got, err := revoked(context.Background(), c.id)
		if err != nil {
			t.Fatalf("the revocation lookup failed: %v", err)
		}
		if got != c.want {
			t.Errorf("RevokedSet(%q) = %v, want %v", c.id, got, c.want)
		}
	}
}
