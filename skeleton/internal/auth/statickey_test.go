package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example.com/service/internal/auth"
	"example.com/service/internal/auth/authtest"
)

// The configured service keys. Each failure mode has its own entry, so one
// case never depends on another's state.
const (
	liveSecret          = "live-service-key-0123456789"
	expiredSecret       = "expired-service-key-0123456789"
	otherAudienceSecret = "other-audience-key-0123456789"
	revokedSecret       = "revoked-service-key-0123456789"
	unissuedSecret      = "a-key-that-was-never-issued"
)

func newStaticKeys(t *testing.T) *auth.StaticKeyAuthenticator {
	t.Helper()
	return &auth.StaticKeyAuthenticator{
		Audience: testAudience,
		Now:      func() time.Time { return testClock },
		Keys: []auth.ServiceKey{
			{
				Subject:  "billing-worker",
				Secret:   liveSecret,
				Scopes:   []string{"orders:read", "invoices"},
				Audience: testAudience,
			},
			{
				Subject:  "retired-worker",
				Secret:   expiredSecret,
				Scopes:   []string{"orders:read"},
				Audience: testAudience,
				NotAfter: testClock.Add(-time.Hour),
			},
			{
				Subject:  "reporting-worker",
				Secret:   otherAudienceSecret,
				Scopes:   []string{"orders:read"},
				Audience: "another-api",
			},
			{
				Subject:  "leaked-worker",
				Secret:   revokedSecret,
				Scopes:   []string{"orders:read"},
				Audience: testAudience,
				Revoked:  true,
			},
		},
	}
}

func keyCredential(secret string) authtest.Credential {
	return func(r *http.Request) { r.Header.Set("Authorization", auth.ServiceKeyScheme+" "+secret) }
}

func TestStaticKeyAuthenticatorConformance(t *testing.T) {
	authtest.Suite{
		Name:              "StaticKeyAuthenticator",
		Authenticator:     newStaticKeys(t),
		Valid:             keyCredential(liveSecret),
		Subject:           "billing-worker",
		Kind:              auth.KindService,
		Scopes:            []string{"orders:read", "invoices:write"},
		Expired:           keyCredential(expiredSecret),
		WrongAudience:     keyCredential(otherAudienceSecret),
		UnknownKey:        keyCredential(unissuedSecret),
		Malformed:         func(r *http.Request) { r.Header.Set("Authorization", "Basic "+liveSecret) },
		Revoked:           keyCredential(revokedSecret),
		InsufficientScope: "orders:write",
	}.Run(t)
}

func authenticateKey(t *testing.T, a *auth.StaticKeyAuthenticator, header string) (*auth.Principal, error) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/v1/orders", nil)
	if header != "" {
		r.Header.Set("Authorization", header)
	}
	return a.Authenticate(context.Background(), r)
}

func TestStaticKeyAcceptsAnIssuedKey(t *testing.T) {
	p, err := authenticateKey(t, newStaticKeys(t), auth.ServiceKeyScheme+" "+liveSecret)
	if err != nil {
		t.Fatalf("an issued key was rejected: %v", err)
	}
	if p.Subject != "billing-worker" || p.Kind != auth.KindService {
		t.Fatalf("principal = %+v, want the billing worker as a service", p)
	}
	if !p.HasScope("invoices:write") {
		t.Errorf("the principal holding %v does not satisfy invoices:write", p.Scopes)
	}
}

// A key rotation holds two entries at once, and both are accepted until the
// old one is removed.
func TestStaticKeyAcceptsEveryConfiguredKeyDuringRotation(t *testing.T) {
	a := newStaticKeys(t)
	a.Keys = append(a.Keys, auth.ServiceKey{
		Subject:  "billing-worker",
		Secret:   "next-service-key-0123456789",
		Scopes:   []string{"orders:read"},
		Audience: testAudience,
	})
	for _, secret := range []string{liveSecret, "next-service-key-0123456789"} {
		if _, err := authenticateKey(t, a, auth.ServiceKeyScheme+" "+secret); err != nil {
			t.Errorf("the key %q was rejected during rotation: %v", secret, err)
		}
	}
}

func TestStaticKeyRejectionsNameTheFailedCheckInTheReasonOnly(t *testing.T) {
	cases := []struct {
		name   string
		header string
		reason string
	}{
		{"no header", "", "no Authorization header"},
		{"wrong scheme", "Bearer " + liveSecret, "not the Key scheme"},
		{"empty credential", auth.ServiceKeyScheme + " ", "credential is empty"},
		{"unissued key", auth.ServiceKeyScheme + " " + unissuedSecret, "no configured service key matches"},
		{"expired key", auth.ServiceKeyScheme + " " + expiredSecret, "expired at"},
		{"other audience", auth.ServiceKeyScheme + " " + otherAudienceSecret, "not \"orders-api\""},
		{"revoked key", auth.ServiceKeyScheme + " " + revokedSecret, "is revoked"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := authenticateKey(t, newStaticKeys(t), c.header)
			if err == nil {
				t.Fatalf("the header %q was accepted as %+v", c.header, p)
			}
			if !errors.Is(err, auth.ErrUnauthenticated) {
				t.Fatalf("error = %v, want an ErrUnauthenticated denial", err)
			}
			if !strings.Contains(err.Error(), c.reason) {
				t.Fatalf("reason = %q, want it to name %q", err, c.reason)
			}
			rec := httptest.NewRecorder()
			auth.WriteDenial(rec, err)
			if strings.Contains(rec.Body.String(), liveSecret) || strings.Contains(rec.Body.String(), "key") {
				t.Fatalf("the response %s describes the credential", rec.Body)
			}
		})
	}
}

// A verifier with no audience accepts a key issued for another service.
func TestStaticKeyRequiresAConfiguredAudience(t *testing.T) {
	a := newStaticKeys(t)
	a.Audience = ""
	_, err := authenticateKey(t, a, auth.ServiceKeyScheme+" "+liveSecret)
	if err == nil || !strings.Contains(err.Error(), "no audience configured") {
		t.Fatalf("error = %v, want a rejection naming the missing audience", err)
	}
}

func TestStaticKeyWithNoKeysRejectsEverything(t *testing.T) {
	a := &auth.StaticKeyAuthenticator{Audience: testAudience}
	if p, err := authenticateKey(t, a, auth.ServiceKeyScheme+" "+liveSecret); err == nil {
		t.Fatalf("a verifier holding no keys accepted a credential as %+v", p)
	}
}

// A key with no expiry is rotated by deployment, so the clock must not
// withdraw it.
func TestStaticKeyWithNoExpiryOutlivesTheClock(t *testing.T) {
	a := newStaticKeys(t)
	a.Now = func() time.Time { return testClock.Add(10 * 365 * 24 * time.Hour) }
	if _, err := authenticateKey(t, a, auth.ServiceKeyScheme+" "+liveSecret); err != nil {
		t.Fatalf("a key with no expiry was rejected ten years on: %v", err)
	}
	if _, err := authenticateKey(t, a, auth.ServiceKeyScheme+" "+expiredSecret); err == nil {
		t.Fatal("a key past its expiry was accepted")
	}
}

// The default clock is the wall clock, so a verifier that configures none
// still enforces expiry.
func TestStaticKeyDefaultsToTheWallClock(t *testing.T) {
	a := newStaticKeys(t)
	a.Now = nil
	a.Keys = []auth.ServiceKey{{
		Subject:  "billing-worker",
		Secret:   liveSecret,
		Audience: testAudience,
		NotAfter: time.Now().Add(-time.Minute),
	}}
	if _, err := authenticateKey(t, a, auth.ServiceKeyScheme+" "+liveSecret); err == nil {
		t.Fatal("a key that expired a minute ago was accepted")
	}
}
