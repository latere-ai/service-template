package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// The bearer token format is a signed, self-describing string:
//
//	v1.<base64url(claims JSON)>.<base64url(HMAC-SHA256)>
//
// The signature covers the version prefix and the encoded claims, so the key
// identifier inside the claims cannot be swapped without invalidating it. The
// format exists so the template ships a working signed-token verifier without
// a token library in the dependency set. A consumer that needs a standard
// token format writes an Authenticator for it and runs the same conformance
// suite.
const (
	// FormatVersion prefixes every token this package issues and accepts. A
	// format change raises it, and an old token then fails as malformed
	// instead of being read under new rules.
	FormatVersion = "v1"
	// BearerScheme is the Authorization scheme the verifier reads.
	BearerScheme = "Bearer"
)

// Claims is the signed payload of a bearer token.
type Claims struct {
	// Subject identifies the caller.
	Subject string `json:"sub"`
	// Kind is KindUser or KindService.
	Kind string `json:"knd,omitempty"`
	// Audience names the service the token is for. A token minted for another
	// service is rejected, which is what stops a token from one deployment
	// being replayed against another.
	Audience string `json:"aud"`
	// Issuer names the minting authority. An empty Issuer on the verifier
	// accepts any.
	Issuer string `json:"iss,omitempty"`
	// Scopes are the permissions the token grants.
	Scopes []string `json:"scp,omitempty"`
	// KeyID selects the signing key. An identifier the verifier does not hold
	// is rejected.
	KeyID string `json:"kid"`
	// TokenID identifies this token for revocation.
	TokenID string `json:"jti,omitempty"`
	// IssuedAt and ExpiresAt are Unix seconds. A token with no expiry is
	// rejected, because a credential that never expires cannot be rotated.
	IssuedAt  int64 `json:"iat,omitempty"`
	ExpiresAt int64 `json:"exp"`
	// Extra carries provider-specific values and reaches the handler as
	// Principal.Claims.
	Extra map[string]any `json:"ext,omitempty"`
}

// SignToken renders and signs a token. It is used by the service that mints
// credentials and by tests; verification never needs it.
func SignToken(key []byte, c Claims) (string, error) {
	if len(key) == 0 {
		return "", Unauthenticated("no signing key for key id %q", c.KeyID)
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	signed := FormatVersion + "." + base64.RawURLEncoding.EncodeToString(payload)
	return signed + "." + base64.RawURLEncoding.EncodeToString(sign(key, signed)), nil
}

func sign(key []byte, signed string) []byte {
	mac := hmac.New(sha256.New, key)
	// hash.Hash never returns an error from Write.
	_, _ = mac.Write([]byte(signed))
	return mac.Sum(nil)
}

// BearerAuthenticator verifies a signed bearer token from the Authorization
// header.
type BearerAuthenticator struct {
	// Keys maps a key identifier to its signing secret. Several entries let a
	// key be rotated: the new key signs while the old key still verifies.
	Keys map[string][]byte
	// Audience is the value a token must carry. It is required, because a
	// verifier that accepts any audience accepts a token minted for a
	// different service.
	Audience string
	// Issuer, when set, is the value a token must carry.
	Issuer string
	// Leeway absorbs clock skew between the minting service and this one.
	Leeway time.Duration
	// Now reads the clock. Nil means time.Now.
	Now func() time.Time
	// Revoked reports whether a token identifier was withdrawn before it
	// expired. Nil means no token is revoked.
	Revoked func(ctx context.Context, tokenID string) (bool, error)
}

// Authenticate implements Authenticator.
func (a *BearerAuthenticator) Authenticate(ctx context.Context, r *http.Request) (*Principal, error) {
	raw, err := BearerToken(r, BearerScheme)
	if err != nil {
		return nil, err
	}
	claims, err := a.verify(raw)
	if err != nil {
		return nil, err
	}
	if err := a.checkClaims(ctx, claims); err != nil {
		return nil, err
	}
	kind := claims.Kind
	if kind == "" {
		kind = KindUser
	}
	return &Principal{
		Subject: claims.Subject,
		Kind:    kind,
		Scopes:  claims.Scopes,
		Claims:  claims.Extra,
	}, nil
}

// verify checks the shape and the signature. The signature is checked before
// any claim, so a forged token is never read for its contents.
func (a *BearerAuthenticator) verify(raw string) (*Claims, error) {
	version, rest, ok := strings.Cut(raw, ".")
	if !ok || version != FormatVersion {
		return nil, Unauthenticated("token is not %s format", FormatVersion)
	}
	encoded, signature, ok := strings.Cut(rest, ".")
	if !ok {
		return nil, Unauthenticated("token has no signature part")
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, Unauthenticated("token payload is not base64url: %v", err)
	}
	presented, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return nil, Unauthenticated("token signature is not base64url: %v", err)
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, Unauthenticated("token payload is not valid JSON: %v", err)
	}
	key, held := a.Keys[claims.KeyID]
	if !held || len(key) == 0 {
		return nil, Unauthenticated("no verification key for key id %q", claims.KeyID)
	}
	// hmac.Equal is constant time, so a caller cannot search for a valid
	// signature by measuring how long a comparison takes.
	if !hmac.Equal(presented, sign(key, version+"."+encoded)) {
		return nil, Unauthenticated("token signature does not verify under key id %q", claims.KeyID)
	}
	return &claims, nil
}

// checkClaims enforces expiry, audience, issuer, and revocation.
func (a *BearerAuthenticator) checkClaims(ctx context.Context, c *Claims) error {
	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}
	if c.ExpiresAt == 0 {
		return Unauthenticated("token carries no expiry")
	}
	if now.Add(-a.Leeway).After(time.Unix(c.ExpiresAt, 0)) {
		return Unauthenticated("token expired at %s", time.Unix(c.ExpiresAt, 0).UTC().Format(time.RFC3339))
	}
	if a.Audience == "" {
		return Unauthenticated("the verifier has no audience configured")
	}
	if c.Audience != a.Audience {
		return Unauthenticated("token audience %q is not %q", c.Audience, a.Audience)
	}
	if a.Issuer != "" && c.Issuer != a.Issuer {
		return Unauthenticated("token issuer %q is not %q", c.Issuer, a.Issuer)
	}
	if a.Revoked == nil {
		return nil
	}
	revoked, err := a.Revoked(ctx, c.TokenID)
	if err != nil {
		return Unauthenticated("revocation lookup for token id %q failed: %v", c.TokenID, err)
	}
	if revoked {
		return Unauthenticated("token id %q is revoked", c.TokenID)
	}
	return nil
}

// RevokedSet returns a revocation check backed by a fixed set of token
// identifiers. It suits a short deny list held in configuration; a longer list
// belongs behind a store lookup with the same signature.
func RevokedSet(tokenIDs ...string) func(context.Context, string) (bool, error) {
	set := make(map[string]struct{}, len(tokenIDs))
	for _, id := range tokenIDs {
		set[id] = struct{}{}
	}
	return func(_ context.Context, id string) (bool, error) {
		_, found := set[id]
		return found, nil
	}
}
