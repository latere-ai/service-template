package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"time"
)

// ServiceKeyScheme is the Authorization scheme the static key verifier reads.
// It is distinct from Bearer so a service key is never mistaken for a signed
// token, and a token presented under the wrong scheme fails as malformed.
const ServiceKeyScheme = "Key"

// ServiceKey is one issued service credential. The secret is held in memory
// and compared in constant time; it is never logged and never returned in a
// response.
type ServiceKey struct {
	// Subject names the calling service, for example "billing-worker".
	Subject string
	// Secret is the credential the caller presents.
	Secret string
	// Scopes are the permissions the credential grants.
	Scopes []string
	// Audience names the service the credential is for. It must equal the
	// verifier's audience, so a key issued for one service is refused by
	// another that happens to hold the same list.
	Audience string
	// NotAfter withdraws the credential at a time. The zero value never
	// expires, which suits a key rotated by deployment.
	NotAfter time.Time
	// Revoked withdraws the credential immediately, ahead of its removal from
	// the configured list.
	Revoked bool
}

// StaticKeyAuthenticator verifies a shared secret presented by another
// service. It suits machine callers inside one trust boundary, where a signed
// token needs an issuer that does not exist yet.
type StaticKeyAuthenticator struct {
	// Keys is the issued list. Several entries let a key be rotated: the new
	// key is added, callers move, and the old key is removed.
	Keys []ServiceKey
	// Audience is the value a key must carry. It is required, for the reason
	// the bearer verifier requires one.
	Audience string
	// Now reads the clock. Nil means time.Now.
	Now func() time.Time
}

// Authenticate implements Authenticator.
func (a *StaticKeyAuthenticator) Authenticate(_ context.Context, r *http.Request) (*Principal, error) {
	presented, err := BearerToken(r, ServiceKeyScheme)
	if err != nil {
		return nil, err
	}
	matched, found := a.match(presented)
	if !found {
		return nil, Unauthenticated("no configured service key matches the presented credential")
	}
	if a.Audience == "" {
		return nil, Unauthenticated("the verifier has no audience configured")
	}
	if matched.Audience != a.Audience {
		return nil, Unauthenticated("service key for %q has audience %q, not %q",
			matched.Subject, matched.Audience, a.Audience)
	}
	if matched.Revoked {
		return nil, Unauthenticated("service key for %q is revoked", matched.Subject)
	}
	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}
	if !matched.NotAfter.IsZero() && now.After(matched.NotAfter) {
		return nil, Unauthenticated("service key for %q expired at %s",
			matched.Subject, matched.NotAfter.UTC().Format(time.RFC3339))
	}
	return &Principal{Subject: matched.Subject, Kind: KindService, Scopes: matched.Scopes}, nil
}

// match finds the key the caller presented. Every entry is compared, and each
// comparison is constant time over a fixed-width digest, so neither the value
// of a secret nor its length leaks through the time the loop takes.
func (a *StaticKeyAuthenticator) match(presented string) (ServiceKey, bool) {
	sum := sha256.Sum256([]byte(presented))
	var (
		found ServiceKey
		hits  int
	)
	for _, k := range a.Keys {
		candidate := sha256.Sum256([]byte(k.Secret))
		if subtle.ConstantTimeCompare(sum[:], candidate[:]) == 1 {
			if hits == 0 {
				found = k
			}
			hits++
		}
	}
	return found, hits > 0
}
