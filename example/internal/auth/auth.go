// Package auth is the identity boundary. It defines what an authenticated
// caller is, the two interfaces every identity source implements, and the
// denial vocabulary the HTTP layer renders.
//
// The package holds no identity-provider client. A provider is reached through
// an Authenticator written in the consumer repository, so switching provider
// changes one construction site and nothing else. Three reference
// implementations ship here: a signed bearer token verifier, a static service
// key verifier, and an anonymous authenticator for public routes.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Principal is the authenticated caller. It is the only identity value the
// handler layer sees, so a provider-specific token shape never reaches a
// handler.
type Principal struct {
	// Subject identifies the caller inside its provider, for example a user
	// identifier or a service name. It is stable for the life of the caller.
	Subject string
	// Kind is KindUser, KindService, or KindAnonymous.
	Kind string
	// Scopes are the permissions the credential carries. Containment decides
	// whether a scope satisfies a requirement, so a broader scope covers a
	// narrower one.
	Scopes []string
	// Claims carries provider-specific values a handler may read. It is nil
	// when the credential carried none.
	Claims map[string]any
}

// Principal kinds. The set is closed: an identity is a person, a machine, or
// no one at all.
const (
	KindUser      = "user"
	KindService   = "service"
	KindAnonymous = "anonymous"
)

// AnonymousPrincipal is the caller of a public route: no subject, no scopes,
// and therefore no authorization decision it can satisfy.
func AnonymousPrincipal() *Principal {
	return &Principal{Subject: "", Kind: KindAnonymous}
}

// IsAnonymous reports whether p is absent or carries the anonymous kind.
func (p *Principal) IsAnonymous() bool { return p == nil || p.Kind == KindAnonymous }

// HasScope reports whether any scope the principal holds contains required.
// An anonymous principal holds none, so it satisfies nothing.
func (p *Principal) HasScope(required string) bool {
	if p == nil {
		return false
	}
	return ScopeSet(p.Scopes).Satisfies(required)
}

// Authenticator turns a request into a Principal or an error. An
// implementation reads whatever credential its provider uses and returns a
// DenialError describing, for the log only, which check failed.
type Authenticator interface {
	Authenticate(ctx context.Context, r *http.Request) (*Principal, error)
}

// Authorizer decides whether a Principal may perform an action on a resource.
// It returns nil to allow and an error to deny.
type Authorizer interface {
	Authorize(ctx context.Context, p *Principal, action, resource string) error
}

// AuthenticatorFunc adapts a function to Authenticator.
type AuthenticatorFunc func(ctx context.Context, r *http.Request) (*Principal, error)

// Authenticate calls f.
func (f AuthenticatorFunc) Authenticate(ctx context.Context, r *http.Request) (*Principal, error) {
	return f(ctx, r)
}

// AuthorizerFunc adapts a function to Authorizer.
type AuthorizerFunc func(ctx context.Context, p *Principal, action, resource string) error

// Authorize calls f.
func (f AuthorizerFunc) Authorize(ctx context.Context, p *Principal, action, resource string) error {
	return f(ctx, p, action, resource)
}

// The two denial classes. Every rejection wraps one of them, so the HTTP layer
// maps a denial to a status without knowing which implementation produced it.
var (
	// ErrUnauthenticated means no usable credential was presented. It maps to
	// 401.
	ErrUnauthenticated = errors.New("unauthenticated")
	// ErrForbidden means the credential is valid and the action is not
	// permitted. It maps to 403.
	ErrForbidden = errors.New("forbidden")
)

// The response text for each denial class. The text is fixed and carries no
// reason, because the reason states which check failed and telling an
// unauthenticated caller that distinguishes an expired credential from an
// unknown key.
const (
	unauthenticatedTitle = "Unauthorized"
	forbiddenTitle       = "Forbidden"
)

// DenialError is a rejection with a server-side reason. Reason names the check
// that failed and belongs in a log record. The response carries the status and
// the fixed title from PublicStatus and never the reason.
type DenialError struct {
	// Reason names the failed check, for example "credential expired".
	Reason string
	// Class is ErrUnauthenticated or ErrForbidden.
	Class error
}

// Error renders the reason for a log record. It is never a response body.
func (e *DenialError) Error() string {
	return fmt.Sprintf("%s: %s", e.Class, e.Reason)
}

// Unwrap exposes the denial class to errors.Is.
func (e *DenialError) Unwrap() error { return e.Class }

// Unauthenticated builds a 401 denial. The arguments format the log reason.
func Unauthenticated(format string, args ...any) *DenialError {
	return &DenialError{Reason: fmt.Sprintf(format, args...), Class: ErrUnauthenticated}
}

// Forbidden builds a 403 denial. The arguments format the log reason.
func Forbidden(format string, args ...any) *DenialError {
	return &DenialError{Reason: fmt.Sprintf(format, args...), Class: ErrForbidden}
}

// PublicStatus reports the response status and title for a denial, and whether
// err is a denial at all. An error that is neither class is not an identity
// failure and belongs to whatever layer produced it.
func PublicStatus(err error) (status int, title string, ok bool) {
	switch {
	case errors.Is(err, ErrUnauthenticated):
		return http.StatusUnauthorized, unauthenticatedTitle, true
	case errors.Is(err, ErrForbidden):
		return http.StatusForbidden, forbiddenTitle, true
	default:
		return 0, "", false
	}
}

// contextKey is unexported so no other package can place a Principal in a
// context this package reads.
type contextKey struct{}

// NewContext returns a context carrying p.
func NewContext(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, p)
}

// FromContext returns the principal a middleware placed in ctx. The second
// result is false when no middleware ran, which a handler must treat as a
// denial rather than as an anonymous caller.
func FromContext(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(contextKey{}).(*Principal)
	return p, ok && p != nil
}

// BearerToken extracts the credential from an Authorization header with the
// given scheme, for example "Bearer". The scheme match is case insensitive
// because RFC 7235 defines it that way, while the credential itself is
// returned untouched.
func BearerToken(r *http.Request, scheme string) (string, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", Unauthenticated("no Authorization header")
	}
	value, found := strings.CutPrefix(header, scheme+" ")
	if !found {
		lower := strings.ToLower(header)
		value, found = strings.CutPrefix(lower, strings.ToLower(scheme)+" ")
		if found {
			// The prefix matched case insensitively, so cut the original at
			// the same offset to keep the credential's own case.
			value = header[len(scheme)+1:]
		}
	}
	if !found {
		return "", Unauthenticated("Authorization header is not the %s scheme", scheme)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", Unauthenticated("%s credential is empty", scheme)
	}
	return value, nil
}
