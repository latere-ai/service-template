package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/example/reference-service/internal/auth"
)

func TestScopeContainment(t *testing.T) {
	cases := []struct {
		held     string
		required string
		want     bool
	}{
		{"orders:read", "orders:read", true},
		{"orders", "orders:read", true},
		{"orders", "orders:read:own", true},
		{"*", "orders:read", true},
		{"orders:*", "orders:write", true},
		{"*:read", "orders:read", true},
		{"orders:read", "orders", false},
		{"orders:read", "orders:write", false},
		{"orders:read:own", "orders:read", false},
		{"invoices", "orders:read", false},
		{"orders:*", "invoices:read", false},
		{"", "orders:read", false},
		{"orders:read", "", false},
		{"", "", false},
		{"orders::read", "orders:read", false},
		{"orders:read", "orders:", false},
	}
	for _, c := range cases {
		if got := auth.Scope(c.held).Satisfies(c.required); got != c.want {
			t.Errorf("Scope(%q).Satisfies(%q) = %v, want %v", c.held, c.required, got, c.want)
		}
	}
}

func TestScopeSetSatisfiesFromAnyHeldScope(t *testing.T) {
	set := auth.ScopeSet{"invoices:read", "orders"}
	if !set.Satisfies("orders:write") {
		t.Errorf("the set %v does not satisfy %q, containment should cover it", set, "orders:write")
	}
	if set.Satisfies("payments:read") {
		t.Errorf("the set %v satisfies %q, which no held scope covers", set, "payments:read")
	}
	if (auth.ScopeSet(nil)).Satisfies("orders:read") {
		t.Error("an empty scope set satisfies a requirement")
	}
}

func TestScopeForRendersResourceThenAction(t *testing.T) {
	if got := auth.ScopeFor("read", "orders"); got != "orders:read" {
		t.Errorf("ScopeFor = %q, want %q", got, "orders:read")
	}
	if got := auth.ScopeFor("", "orders"); got != "" {
		t.Errorf("ScopeFor with no action = %q, want the empty scope", got)
	}
	if got := auth.ScopeFor("read", ""); got != "" {
		t.Errorf("ScopeFor with no resource = %q, want the empty scope", got)
	}
}

func TestScopeAuthorizerGrantsOnContainment(t *testing.T) {
	p := &auth.Principal{Subject: "u1", Kind: auth.KindUser, Scopes: []string{"orders"}}
	if err := (auth.ScopeAuthorizer{}).Authorize(context.Background(), p, "read", "orders"); err != nil {
		t.Errorf("a principal holding %v was denied orders:read: %v", p.Scopes, err)
	}
}

// A route that reached the authorizer with no action and no resource is an
// undecided route. The authorizer denies it, so the deny-by-default rule holds
// even when the static route table check was not run.
func TestScopeAuthorizerDeniesAnEmptyDecision(t *testing.T) {
	p := &auth.Principal{Subject: "u1", Kind: auth.KindUser, Scopes: []string{"*"}}
	for _, c := range []struct{ action, resource string }{
		{"", ""},
		{"read", ""},
		{"", "orders"},
	} {
		err := (auth.ScopeAuthorizer{}).Authorize(context.Background(), p, c.action, c.resource)
		if err == nil {
			t.Errorf("Authorize(action=%q, resource=%q) allowed a principal holding the wildcard scope",
				c.action, c.resource)
			continue
		}
		if !errors.Is(err, auth.ErrForbidden) {
			t.Errorf("Authorize(action=%q, resource=%q) = %v, want an ErrForbidden denial",
				c.action, c.resource, err)
		}
	}
}

func TestScopeAuthorizerDeniesNilAndAnonymousPrincipals(t *testing.T) {
	for _, p := range []*auth.Principal{nil, auth.AnonymousPrincipal()} {
		if err := (auth.ScopeAuthorizer{}).Authorize(context.Background(), p, "read", "orders"); err == nil {
			t.Errorf("Authorize allowed principal %+v", p)
		}
	}
}
