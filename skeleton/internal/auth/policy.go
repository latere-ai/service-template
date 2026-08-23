package auth

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
)

// Policy is the authorization decision attached to one route. The zero value
// is undecided: it is neither public nor guarded, and both the route table
// check and the request path treat it as a denial. A route therefore fails
// closed when it is added without a decision.
type Policy struct {
	// Public marks a route reachable without a credential. A public route
	// still receives a Principal in its context: the authenticated one when a
	// credential was presented and accepted, the anonymous one otherwise.
	Public bool
	// Action is the verb the route performs, for example "read".
	Action string
	// Resource is the noun the route acts on, for example "orders".
	Resource string
}

// PublicPolicy marks a route as reachable without a credential.
func PublicPolicy() Policy { return Policy{Public: true} }

// Guarded requires the caller to be authenticated and to pass the authorizer
// for action on resource.
func Guarded(action, resource string) Policy {
	return Policy{Action: action, Resource: resource}
}

// Decided reports whether the policy states a decision. An undecided policy is
// the zero value, and a policy that is both public and guarded is undecided
// too, because the two statements contradict each other.
func (p Policy) Decided() bool {
	if p.Public {
		return p.Action == "" && p.Resource == ""
	}
	return p.Action != "" && p.Resource != ""
}

// String renders the policy for a route table report.
func (p Policy) String() string {
	switch {
	case p.Public && p.Action == "" && p.Resource == "":
		return "public"
	case p.Action != "" && p.Resource != "" && !p.Public:
		return p.Action + " " + p.Resource
	default:
		return "undecided"
	}
}

// Guard applies a Policy to a handler: it authenticates the request, places
// the Principal in the context, and asks the Authorizer for the decision.
//
// A denial writes the fixed response for its class and logs the reason. The
// two are deliberately separate: the reason names the check that failed, and
// naming it in the response would tell a caller whether a credential is
// expired, unknown, or merely revoked.
type Guard struct {
	// Authenticator reads the credential. A nil Authenticator denies every
	// request, because a guard that cannot identify a caller must not admit
	// one.
	Authenticator Authenticator
	// Authorizer decides the action. Nil means ScopeAuthorizer.
	Authorizer Authorizer
	// Logger records denial reasons. Nil means slog.Default.
	Logger *slog.Logger
	// OnDeny writes the denial response. Nil means WriteDenial, which renders
	// the class title and status only. A service that owns an error envelope
	// sets this to its own writer, which maps the denial with
	// errors.Is(err, ErrUnauthenticated) and PublicStatus.
	OnDeny func(w http.ResponseWriter, r *http.Request, err error)
}

// Protect wraps h with the policy. It is the only way a route acquires its
// decision, so an unwrapped handler is a route with no decision at all.
func (g *Guard) Protect(p Policy, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !p.Decided() {
			g.deny(w, r, Forbidden("route %s %s carries no authorization decision", r.Method, r.URL.Path))
			return
		}
		principal, err := g.authenticate(r)
		if err != nil {
			if !p.Public {
				g.deny(w, r, err)
				return
			}
			// A public route accepts a caller with no usable credential. The
			// rejection is still logged, because a failing credential on a
			// public route is how a broken client shows up.
			g.logger().DebugContext(r.Context(), "credential rejected on a public route",
				slog.String("method", r.Method), slog.String("path", r.URL.Path),
				slog.String("reason", err.Error()))
			principal = AnonymousPrincipal()
		}
		if !p.Public {
			if err := g.authorizer().Authorize(r.Context(), principal, p.Action, p.Resource); err != nil {
				g.deny(w, r, err)
				return
			}
		}
		h.ServeHTTP(w, r.WithContext(NewContext(r.Context(), principal)))
	})
}

// ProtectFunc is Protect for a handler function.
func (g *Guard) ProtectFunc(p Policy, h http.HandlerFunc) http.Handler { return g.Protect(p, h) }

func (g *Guard) authenticate(r *http.Request) (*Principal, error) {
	if g.Authenticator == nil {
		return nil, Unauthenticated("no authenticator is configured")
	}
	principal, err := g.Authenticator.Authenticate(r.Context(), r)
	if err != nil {
		return nil, err
	}
	if principal == nil {
		return nil, Unauthenticated("the authenticator returned no principal")
	}
	return principal, nil
}

func (g *Guard) authorizer() Authorizer {
	if g.Authorizer == nil {
		return ScopeAuthorizer{}
	}
	return g.Authorizer
}

func (g *Guard) logger() *slog.Logger {
	if g.Logger == nil {
		return slog.Default()
	}
	return g.Logger
}

// deny logs the reason and writes the reason-free response.
func (g *Guard) deny(w http.ResponseWriter, r *http.Request, err error) {
	status, title, _ := PublicStatus(err)
	g.logger().WarnContext(r.Context(), "request denied",
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Int("status", status),
		slog.String("title", title),
		slog.String("reason", err.Error()))
	if g.OnDeny != nil {
		g.OnDeny(w, r, err)
		return
	}
	WriteDenial(w, err)
}

// WriteDenial writes the response for a denial: the status and the title of
// its class, and nothing else. Every rejection of the same class produces
// byte-identical output, so a caller cannot tell which check failed by
// comparing responses. An error that is not a denial becomes a 500 with no
// body detail, because only this package's classes describe an identity
// failure.
func WriteDenial(w http.ResponseWriter, err error) {
	status, title, ok := PublicStatus(err)
	if !ok {
		status, title = http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError)
	}
	body, marshalErr := json.Marshal(struct {
		Title  string `json:"title"`
		Status int    `json:"status"`
	}{Title: title, Status: status})
	if marshalErr != nil {
		// A struct of two scalars cannot fail to marshal; the status alone
		// still denies if it somehow does.
		http.Error(w, title, status)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	w.WriteHeader(status)
	// The response is already committed, so a write failure has no remedy
	// beyond the log the server's access middleware records.
	_, _ = w.Write(body)
}

// Route is one entry of the route table: the request it matches and the
// decision it carries.
type Route struct {
	Method  string
	Pattern string
	Policy  Policy
}

// String renders the route for a report.
func (r Route) String() string { return fmt.Sprintf("%s %s (%s)", r.Method, r.Pattern, r.Policy) }

// RouteTable registers routes with their policies and keeps the list for the
// deny-by-default test. Registration is the only way to add a route, so every
// route in the table has a policy recorded next to it, decided or not.
type RouteTable struct {
	guard  *Guard
	mux    *http.ServeMux
	routes []Route
}

// NewRouteTable returns a table whose routes are wrapped by g.
func NewRouteTable(g *Guard) *RouteTable {
	if g == nil {
		g = &Guard{}
	}
	return &RouteTable{guard: g, mux: http.NewServeMux()}
}

// Handle registers h for method and pattern under p. An undecided policy is
// registered and denies at request time, so the route table test reports it
// rather than the route quietly not existing.
func (t *RouteTable) Handle(method, pattern string, p Policy, h http.Handler) {
	t.routes = append(t.routes, Route{Method: method, Pattern: pattern, Policy: p})
	t.mux.Handle(strings.TrimSpace(method+" "+pattern), t.guard.Protect(p, h))
}

// HandleFunc is Handle for a handler function.
func (t *RouteTable) HandleFunc(method, pattern string, p Policy, h http.HandlerFunc) {
	t.Handle(method, pattern, p, h)
}

// Routes returns the registered routes in registration order.
func (t *RouteTable) Routes() []Route {
	out := make([]Route, len(t.routes))
	copy(out, t.routes)
	return out
}

// Validate reports the routes that carry no decision. It is what the route
// table test calls: a route added without a policy, or marked both public and
// guarded, names itself here instead of shipping open.
func (t *RouteTable) Validate() error {
	var undecided []string
	for _, r := range t.routes {
		if !r.Policy.Decided() {
			undecided = append(undecided, r.Method+" "+r.Pattern)
		}
	}
	if len(undecided) == 0 {
		return nil
	}
	sort.Strings(undecided)
	return fmt.Errorf("routes with neither an authorization rule nor a public marker: %s",
		strings.Join(undecided, ", "))
}

// ServeHTTP dispatches to the registered handler.
func (t *RouteTable) ServeHTTP(w http.ResponseWriter, r *http.Request) { t.mux.ServeHTTP(w, r) }

// Handler reports the guarded handler a request matches and the pattern it
// matched, without serving it. The pattern is empty when no route claims the
// request.
//
// It exists because the transport layer labels a span and a metric with the
// route pattern, and only the router that registered the pattern knows it. A
// label that falls back to the request path produces one time series per
// identifier in the path.
func (t *RouteTable) Handler(r *http.Request) (http.Handler, string) {
	return t.mux.Handler(r)
}
