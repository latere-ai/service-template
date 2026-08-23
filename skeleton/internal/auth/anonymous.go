package auth

import (
	"context"
	"net/http"
)

// AnonymousAuthenticator identifies every caller as the anonymous principal.
// It is what a public route uses when the service has no notion of an
// optional credential: the request is admitted, the principal carries no
// subject and no scopes, and every authorization decision it is asked to
// satisfy therefore fails.
//
// It is never a fallback for a guarded route. A guarded route with this
// authenticator admits the request and is refused by the authorizer, which is
// a denial, not an escalation.
type AnonymousAuthenticator struct{}

// Authenticate implements Authenticator. It ignores the request, including any
// credential it carries, because reading a credential it cannot verify is how
// an anonymous path turns into an unchecked one.
func (AnonymousAuthenticator) Authenticate(_ context.Context, _ *http.Request) (*Principal, error) {
	return AnonymousPrincipal(), nil
}
