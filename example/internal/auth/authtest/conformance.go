// Package authtest holds the conformance suite every Authenticator must pass.
//
// The suite exists because authentication defects concentrate in the paths a
// happy-path test never reaches: a credential whose expiry is parsed and not
// enforced, an audience that is carried and not checked, a signature verified
// against any key the caller names. An implementation written in a consumer
// repository runs the same suite as the three reference implementations, so a
// new identity source is held to the same failures.
package authtest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/example/reference-service/internal/auth"
)

// Credential prepares a request so it carries one credential. A factory that
// does nothing produces a request with no credential at all.
type Credential func(r *http.Request)

// Suite describes one implementation under test. Every credential factory is
// required: an implementation cannot pass the suite by handling the valid case
// and leaving the rest unwritten.
type Suite struct {
	// Name identifies the implementation in the test output.
	Name string
	// Authenticator is the implementation under test.
	Authenticator auth.Authenticator

	// Valid presents a credential the implementation accepts.
	Valid Credential
	// Subject, Kind, and Scopes are what Valid must produce. Scopes are
	// checked by containment, so a broader held scope satisfies each one.
	Subject string
	Kind    string
	Scopes  []string

	// Expired presents a credential past its expiry.
	Expired Credential
	// WrongAudience presents a credential minted for another service.
	WrongAudience Credential
	// UnknownKey presents a credential signed or issued under a key the
	// verifier does not hold.
	UnknownKey Credential
	// Malformed presents a credential the format cannot parse.
	Malformed Credential
	// Revoked presents a credential withdrawn before its expiry.
	Revoked Credential

	// InsufficientScope is a scope the valid credential must not satisfy. It
	// proves the scope check is a check and not a formality.
	InsufficientScope string
}

// Run executes the suite as subtests of t.
func (s Suite) Run(t *testing.T) {
	t.Helper()
	for _, c := range s.checks() {
		t.Run(c.name, func(t *testing.T) { c.run(t) })
	}
}

// reporter is the part of *testing.T the suite uses. It is an interface so the
// suite's own test can drive it with a recorder and assert that a broken
// implementation fails. Only Errorf is used, never Fatalf, because a check
// must report and return rather than stop the goroutine it runs on.
type reporter interface {
	Helper()
	Errorf(format string, args ...any)
}

// run executes every check against one reporter, in suite order.
func (s Suite) run(r reporter) {
	for _, c := range s.checks() {
		c.run(r)
	}
}

type check struct {
	name string
	run  func(r reporter)
}

// rejection is one credential the implementation must refuse.
type rejection struct {
	name string
	cred Credential
}

// rejections lists the failure modes in a fixed order, so the report of a
// missing factory names the same case every run.
func (s Suite) rejections() []rejection {
	return []rejection{
		{"missing", func(*http.Request) {}},
		{"malformed", s.Malformed},
		{"expired", s.Expired},
		{"wrong-audience", s.WrongAudience},
		{"unknown-key", s.UnknownKey},
		{"revoked", s.Revoked},
	}
}

func (s Suite) checks() []check {
	checks := []check{
		{"declaration", s.checkDeclaration},
		{"valid-credential", s.checkValid},
		{"insufficient-scope", s.checkInsufficientScope},
	}
	for _, rej := range s.rejections() {
		checks = append(checks, check{rej.name, func(r reporter) { s.checkRejected(r, rej) }})
	}
	checks = append(checks, check{"denials-are-indistinguishable", s.checkDenialsIdentical})
	return checks
}

// checkDeclaration fails the suite when the implementation left a case
// unwritten. Naming the field is what stops a partial suite from reporting
// success.
func (s Suite) checkDeclaration(r reporter) {
	r.Helper()
	if s.Authenticator == nil {
		r.Errorf("conformance suite %q: Authenticator is nil", s.Name)
	}
	if s.Valid == nil {
		r.Errorf("conformance suite %q: the Valid credential factory is nil", s.Name)
	}
	if s.Kind == "" {
		r.Errorf("conformance suite %q: Kind is empty, state the principal kind Valid produces", s.Name)
	}
	if s.InsufficientScope == "" {
		r.Errorf("conformance suite %q: InsufficientScope is empty, state a scope the valid credential must not satisfy", s.Name)
	}
	for _, rej := range s.rejections() {
		if rej.cred == nil {
			r.Errorf("conformance suite %q: the %s credential factory is nil, every failure mode is required",
				s.Name, rej.name)
		}
	}
}

// checkValid asserts the accepted credential produces the declared principal.
func (s Suite) checkValid(r reporter) {
	r.Helper()
	if s.Authenticator == nil || s.Valid == nil {
		return
	}
	p, err := s.authenticate(s.Valid)
	if err != nil {
		r.Errorf("conformance suite %q: the valid credential was rejected: %v", s.Name, err)
		return
	}
	if p == nil {
		r.Errorf("conformance suite %q: the valid credential produced no principal and no error", s.Name)
		return
	}
	if p.Subject != s.Subject {
		r.Errorf("conformance suite %q: subject = %q, want %q", s.Name, p.Subject, s.Subject)
	}
	if p.Kind != s.Kind {
		r.Errorf("conformance suite %q: kind = %q, want %q", s.Name, p.Kind, s.Kind)
	}
	for _, want := range s.Scopes {
		if !p.HasScope(want) {
			r.Errorf("conformance suite %q: the principal does not satisfy scope %q, it holds %v",
				s.Name, want, p.Scopes)
		}
	}
}

// checkInsufficientScope asserts a valid credential is still refused for an
// action its scopes do not cover.
func (s Suite) checkInsufficientScope(r reporter) {
	r.Helper()
	if s.Authenticator == nil || s.Valid == nil || s.InsufficientScope == "" {
		return
	}
	p, err := s.authenticate(s.Valid)
	if err != nil || p == nil {
		// checkValid already reported this.
		return
	}
	if p.HasScope(s.InsufficientScope) {
		r.Errorf("conformance suite %q: the principal satisfies %q, which was declared insufficient",
			s.Name, s.InsufficientScope)
	}
	// ScopeFor renders "<resource>:<action>", so the declared scope splits
	// back into that pair.
	resource, action, ok := strings.Cut(s.InsufficientScope, auth.ScopeSeparator)
	if !ok {
		// A one-segment scope names no action and resource pair to check.
		return
	}
	if err := (auth.ScopeAuthorizer{}).Authorize(context.Background(), p, action, resource); err == nil {
		r.Errorf("conformance suite %q: the authorizer permitted %q on %q for a principal holding %v",
			s.Name, action, resource, p.Scopes)
	} else if !errors.Is(err, auth.ErrForbidden) {
		r.Errorf("conformance suite %q: the scope denial is %v, want an ErrForbidden denial", s.Name, err)
	}
}

// checkRejected asserts a failing credential grants nothing. An implementation
// satisfies it either by returning a denial, or by returning the anonymous
// principal, which holds no scopes and therefore passes no authorization
// decision.
func (s Suite) checkRejected(r reporter, rej rejection) {
	r.Helper()
	if s.Authenticator == nil || rej.cred == nil {
		return
	}
	p, err := s.authenticate(rej.cred)
	if err != nil {
		if _, _, ok := auth.PublicStatus(err); !ok {
			r.Errorf("conformance suite %q: the %s credential was refused with %v, which is neither an "+
				"ErrUnauthenticated nor an ErrForbidden denial", s.Name, rej.name, err)
		}
		return
	}
	switch {
	case p == nil:
		r.Errorf("conformance suite %q: the %s credential produced no principal and no error",
			s.Name, rej.name)
	case !p.IsAnonymous():
		r.Errorf("conformance suite %q: the %s credential was accepted as %q of kind %q",
			s.Name, rej.name, p.Subject, p.Kind)
	case len(p.Scopes) > 0:
		r.Errorf("conformance suite %q: the %s credential produced an anonymous principal holding %v",
			s.Name, rej.name, p.Scopes)
	}
}

// checkDenialsIdentical asserts the responses to every refused credential are
// byte identical. A caller that can tell an expired credential from an unknown
// key by the response has been told which check failed, which is what the
// fixed envelope prevents. It also asserts the reason reaches the log, because
// a denial nobody can diagnose server-side is unoperable.
func (s Suite) checkDenialsIdentical(r reporter) {
	r.Helper()
	if s.Authenticator == nil {
		return
	}
	var logs bytes.Buffer
	guard := &auth.Guard{
		Authenticator: s.Authenticator,
		Logger:        slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	reached := false
	handler := guard.Protect(auth.Guarded("read", "conformance"),
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	seen := map[string][]string{}
	for _, rej := range s.rejections() {
		if rej.cred == nil {
			continue
		}
		logs.Reset()
		reached = false
		req := httptest.NewRequest(http.MethodGet, "/v1/conformance", nil)
		rej.cred(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if reached {
			r.Errorf("conformance suite %q: the %s credential reached the handler", s.Name, rej.name)
			continue
		}
		if logs.Len() == 0 {
			r.Errorf("conformance suite %q: the %s denial logged nothing server-side", s.Name, rej.name)
		}
		dump := dumpResponse(rec)
		seen[dump] = append(seen[dump], rej.name)
	}
	if len(seen) <= 1 {
		return
	}
	shapes := make([]string, 0, len(seen))
	for dump, names := range seen {
		sort.Strings(names)
		shapes = append(shapes, fmt.Sprintf("%s => %s", strings.Join(names, ","), dump))
	}
	sort.Strings(shapes)
	r.Errorf("conformance suite %q: the denial responses differ, so a caller learns which check failed:\n%s",
		s.Name, strings.Join(shapes, "\n"))
}

// authenticate runs the implementation against a request carrying cred.
func (s Suite) authenticate(cred Credential) (*auth.Principal, error) {
	req := httptest.NewRequest(http.MethodGet, "/v1/conformance", nil)
	cred(req)
	return s.Authenticator.Authenticate(req.Context(), req)
}

// dumpResponse renders a response as one comparable string: the status, every
// header, and the body.
func dumpResponse(rec *httptest.ResponseRecorder) string {
	result := rec.Result()
	// The recorder's body is already in memory, so closing it cannot fail and
	// nothing is read from the network.
	defer func() { _ = result.Body.Close() }()
	names := make([]string, 0, len(result.Header))
	for name := range result.Header {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, "status=%d", result.StatusCode)
	for _, name := range names {
		fmt.Fprintf(&b, " %s=%q", name, strings.Join(result.Header.Values(name), ","))
	}
	fmt.Fprintf(&b, " body=%q", rec.Body.String())
	return b.String()
}
