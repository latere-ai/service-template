package auth

import (
	"context"
	"strings"
)

// Scope grammar.
//
//	scope   := segment (":" segment)*
//	segment := [a-z0-9_.-]+ | "*"
//
// A scope names a capability from the general to the specific, for example
// "orders:read". Authorization checks containment and not equality, so one
// held scope covers every requirement below it:
//
//	held        satisfies       does not satisfy
//	"*"         "orders:read"   nothing
//	"orders"    "orders:read"   "invoices:read"
//	"orders:*"  "orders:write"  "orders"
//
// Containment lets a broad credential answer a narrow route without a special
// case per route, and it keeps the reverse closed: a narrow scope never
// satisfies a broader requirement.
const (
	// ScopeSeparator divides a scope into segments.
	ScopeSeparator = ":"
	// ScopeWildcard is the segment that matches any one segment.
	ScopeWildcard = "*"
)

// Scope is one permission string.
type Scope string

// Satisfies reports whether s covers required. An empty scope on either side
// satisfies nothing, so a route that forgot to state its requirement denies
// rather than admits.
func (s Scope) Satisfies(required string) bool {
	if s == "" || required == "" {
		return false
	}
	held := strings.Split(string(s), ScopeSeparator)
	want := strings.Split(required, ScopeSeparator)
	if len(held) > len(want) {
		// A more specific scope never covers a broader requirement.
		return false
	}
	for i, seg := range held {
		if seg == "" || want[i] == "" {
			return false
		}
		if seg == ScopeWildcard {
			continue
		}
		if seg != want[i] {
			return false
		}
	}
	return true
}

// ScopeSet is the scope list a principal holds.
type ScopeSet []string

// Satisfies reports whether any held scope covers required.
func (set ScopeSet) Satisfies(required string) bool {
	for _, held := range set {
		if Scope(held).Satisfies(required) {
			return true
		}
	}
	return false
}

// ScopeFor is the scope an action on a resource requires. It is the mapping
// ScopeAuthorizer uses, and it is exported so a route table test can state the
// scope a route needs without duplicating the format.
func ScopeFor(action, resource string) string {
	if action == "" || resource == "" {
		return ""
	}
	return resource + ScopeSeparator + action
}

// ScopeAuthorizer permits an action on a resource when the principal holds a
// scope containing "<resource>:<action>". An action or a resource that is
// empty produces no scope and therefore denies, which is what makes an
// undecided route fail closed even if it reaches the authorizer.
type ScopeAuthorizer struct{}

// Authorize implements Authorizer.
func (ScopeAuthorizer) Authorize(_ context.Context, p *Principal, action, resource string) error {
	required := ScopeFor(action, resource)
	if required == "" {
		return Forbidden("no scope is defined for action %q on resource %q", action, resource)
	}
	if !p.HasScope(required) {
		return Forbidden("principal %q lacks scope %q", subjectOf(p), required)
	}
	return nil
}

// subjectOf names a principal in a log reason without dereferencing nil.
func subjectOf(p *Principal) string {
	if p == nil {
		return "<none>"
	}
	if p.Subject == "" {
		return "<" + p.Kind + ">"
	}
	return p.Subject
}
