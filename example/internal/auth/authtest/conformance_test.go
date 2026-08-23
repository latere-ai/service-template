package authtest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/example/reference-service/internal/auth"
)

// recorder implements reporter and collects the failures instead of failing
// the test that drives the suite. It is how the suite itself is held to the
// rule it enforces: a gate that cannot fail proves nothing.
type recorder struct{ failures []string }

func (r *recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

func (r *recorder) joined() string { return strings.Join(r.failures, "\n") }

// authenticatorFunc adapts a function under test.
type authenticatorFunc func(r *http.Request) (*auth.Principal, error)

func (f authenticatorFunc) Authenticate(_ context.Context, r *http.Request) (*auth.Principal, error) {
	return f(r)
}

// honest is a correct reference implementation for the suite's own test: it
// accepts one credential and refuses everything else with a denial.
func honest() auth.Authenticator {
	return authenticatorFunc(func(r *http.Request) (*auth.Principal, error) {
		if r.Header.Get("Authorization") != "Key good" {
			return nil, auth.Unauthenticated("the credential %q is not accepted", r.Header.Get("Authorization"))
		}
		return &auth.Principal{Subject: "svc", Kind: auth.KindService, Scopes: []string{"orders:read"}}, nil
	})
}

func honestSuite() Suite {
	return Suite{
		Name:              "honest",
		Authenticator:     honest(),
		Valid:             func(r *http.Request) { r.Header.Set("Authorization", "Key good") },
		Subject:           "svc",
		Kind:              auth.KindService,
		Scopes:            []string{"orders:read"},
		Expired:           func(r *http.Request) { r.Header.Set("Authorization", "Key expired") },
		WrongAudience:     func(r *http.Request) { r.Header.Set("Authorization", "Key other-audience") },
		UnknownKey:        func(r *http.Request) { r.Header.Set("Authorization", "Key unknown") },
		Malformed:         func(r *http.Request) { r.Header.Set("Authorization", "garbage") },
		Revoked:           func(r *http.Request) { r.Header.Set("Authorization", "Key revoked") },
		InsufficientScope: "orders:write",
	}
}

func TestSuitePassesACorrectImplementation(t *testing.T) {
	r := &recorder{}
	honestSuite().run(r)
	if len(r.failures) != 0 {
		t.Fatalf("the suite failed a correct implementation:\n%s", r.joined())
	}
}

func TestSuiteRunsAsSubtests(t *testing.T) {
	honestSuite().Run(t)
}

func TestSuiteNamesEveryUnwrittenCase(t *testing.T) {
	r := &recorder{}
	Suite{Name: "empty"}.run(r)
	for _, want := range []string{
		"Authenticator is nil",
		"the Valid credential factory is nil",
		"Kind is empty",
		"InsufficientScope is empty",
		"the malformed credential factory is nil",
		"the expired credential factory is nil",
		"the wrong-audience credential factory is nil",
		"the unknown-key credential factory is nil",
		"the revoked credential factory is nil",
	} {
		if !strings.Contains(r.joined(), want) {
			t.Errorf("the suite did not report %q for an empty declaration:\n%s", want, r.joined())
		}
	}
}

// An implementation that admits every credential is the failure the suite
// exists to catch.
func TestSuiteFailsAnImplementationThatAcceptsEverything(t *testing.T) {
	s := honestSuite()
	s.Authenticator = authenticatorFunc(func(*http.Request) (*auth.Principal, error) {
		return &auth.Principal{Subject: "svc", Kind: auth.KindService, Scopes: []string{"*"}}, nil
	})
	r := &recorder{}
	s.run(r)
	for _, name := range []string{"missing", "malformed", "expired", "wrong-audience", "unknown-key", "revoked"} {
		if !strings.Contains(r.joined(), fmt.Sprintf("the %s credential was accepted", name)) {
			t.Errorf("the suite accepted the %s case:\n%s", name, r.joined())
		}
		if !strings.Contains(r.joined(), fmt.Sprintf("the %s credential reached the handler", name)) {
			t.Errorf("the suite did not report that the %s credential reached the handler:\n%s", name, r.joined())
		}
	}
	if !strings.Contains(r.joined(), "which was declared insufficient") {
		t.Errorf("the suite did not report the granted insufficient scope:\n%s", r.joined())
	}
}

// A rejection that returns the anonymous principal is allowed. A rejection
// that returns the anonymous principal holding scopes is not, because scopes
// are the privilege the rejection was supposed to withhold.
func TestSuiteFailsAnonymousWithScopes(t *testing.T) {
	s := honestSuite()
	s.Authenticator = authenticatorFunc(func(r *http.Request) (*auth.Principal, error) {
		if r.Header.Get("Authorization") == "Key good" {
			return &auth.Principal{Subject: "svc", Kind: auth.KindService, Scopes: []string{"orders:read"}}, nil
		}
		return &auth.Principal{Kind: auth.KindAnonymous, Scopes: []string{"orders:read"}}, nil
	})
	r := &recorder{}
	s.run(r)
	if !strings.Contains(r.joined(), "anonymous principal holding") {
		t.Fatalf("the suite allowed an anonymous principal with scopes:\n%s", r.joined())
	}
}

func TestSuiteFailsAnUnclassifiedRejection(t *testing.T) {
	s := honestSuite()
	s.Authenticator = authenticatorFunc(func(r *http.Request) (*auth.Principal, error) {
		if r.Header.Get("Authorization") == "Key good" {
			return &auth.Principal{Subject: "svc", Kind: auth.KindService, Scopes: []string{"orders:read"}}, nil
		}
		return nil, errors.New("no")
	})
	r := &recorder{}
	s.run(r)
	if !strings.Contains(r.joined(), "which is neither an ErrUnauthenticated nor an ErrForbidden denial") {
		t.Fatalf("the suite allowed an unclassified rejection:\n%s", r.joined())
	}
}

// A rejection that returns nothing at all admits the request through a guard
// that treats a nil principal as an anonymous one, so the suite reports it.
func TestSuiteFailsARejectionThatReturnsNothing(t *testing.T) {
	s := honestSuite()
	s.Authenticator = authenticatorFunc(func(r *http.Request) (*auth.Principal, error) {
		if r.Header.Get("Authorization") == "Key good" {
			return &auth.Principal{Subject: "svc", Kind: auth.KindService, Scopes: []string{"orders:read"}}, nil
		}
		return nil, nil
	})
	r := &recorder{}
	s.run(r)
	if !strings.Contains(r.joined(), "produced no principal and no error") {
		t.Fatalf("the suite allowed an empty result:\n%s", r.joined())
	}
}

// An implementation whose denials differ by class tells the caller which check
// failed: an expired credential answers 401 and an unknown key answers 403.
func TestSuiteFailsDenialsThatDiffer(t *testing.T) {
	s := honestSuite()
	s.Authenticator = authenticatorFunc(func(r *http.Request) (*auth.Principal, error) {
		switch r.Header.Get("Authorization") {
		case "Key good":
			return &auth.Principal{Subject: "svc", Kind: auth.KindService, Scopes: []string{"orders:read"}}, nil
		case "Key unknown":
			return nil, auth.Forbidden("unknown key")
		default:
			return nil, auth.Unauthenticated("rejected")
		}
	})
	r := &recorder{}
	s.run(r)
	if !strings.Contains(r.joined(), "the denial responses differ") {
		t.Fatalf("the suite allowed distinguishable denials:\n%s", r.joined())
	}
	if !strings.Contains(r.joined(), "unknown-key") {
		t.Fatalf("the report does not name the case that differs:\n%s", r.joined())
	}
}

func TestSuiteFailsAWrongPrincipal(t *testing.T) {
	cases := []struct {
		name      string
		principal *auth.Principal
		want      string
	}{
		{"wrong subject", &auth.Principal{Subject: "other", Kind: auth.KindService, Scopes: []string{"orders:read"}},
			"subject = \"other\""},
		{"wrong kind", &auth.Principal{Subject: "svc", Kind: auth.KindUser, Scopes: []string{"orders:read"}},
			"kind = \"user\""},
		{"missing scope", &auth.Principal{Subject: "svc", Kind: auth.KindService},
			"does not satisfy scope \"orders:read\""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := honestSuite()
			s.Authenticator = authenticatorFunc(func(r *http.Request) (*auth.Principal, error) {
				if r.Header.Get("Authorization") == "Key good" {
					return c.principal, nil
				}
				return nil, auth.Unauthenticated("rejected")
			})
			r := &recorder{}
			s.run(r)
			if !strings.Contains(r.joined(), c.want) {
				t.Fatalf("the suite did not report %q:\n%s", c.want, r.joined())
			}
		})
	}
}

// An implementation that refuses its own valid credential fails on the happy
// path, which is the one case a partial suite would still cover.
func TestSuiteFailsARejectedValidCredential(t *testing.T) {
	s := honestSuite()
	s.Valid = func(r *http.Request) { r.Header.Set("Authorization", "Key not-the-one") }
	r := &recorder{}
	s.run(r)
	if !strings.Contains(r.joined(), "the valid credential was rejected") {
		t.Fatalf("the suite passed an implementation that rejects its valid credential:\n%s", r.joined())
	}
}

// A one-segment insufficient scope has no action and resource pair, so the
// authorizer half of the check is skipped and the containment half still runs.
func TestSuiteAcceptsAOneSegmentInsufficientScope(t *testing.T) {
	s := honestSuite()
	s.InsufficientScope = "admin"
	r := &recorder{}
	s.run(r)
	if len(r.failures) != 0 {
		t.Fatalf("the suite failed on a one-segment insufficient scope:\n%s", r.joined())
	}
}
