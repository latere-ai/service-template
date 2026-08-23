---
title: Authentication boundary: pluggable identity with no provider in the core
status: drafted
depends_on:
  - specs/010-http-api-surface.md
affects: [skeleton/internal/auth/, skeleton/internal/handler/]
created: 2026-08-23
author: changkun
trigger: foundation spec
---

# Authentication boundary

## Problem

A template that ships one identity provider forces every consumer to adopt it or
to cut the template open. A template that ships nothing leaves every consumer to
write its own middleware, and authentication written from scratch repeatedly is
where the security defects concentrate: a token compared with a non-constant
time comparison, a missing audience check, an expiry that is parsed and not
enforced.

## Scope

Layer 3. The identity interfaces, the middleware, the authorization primitive,
and the reference implementations.

## Design

### Interfaces, not a provider

The core defines two small interfaces:

```go
// Authenticator turns a request into a Principal or an error.
type Authenticator interface {
    Authenticate(ctx context.Context, r *http.Request) (*Principal, error)
}

// Authorizer decides whether a Principal may perform an action on a resource.
type Authorizer interface {
    Authorize(ctx context.Context, p *Principal, action, resource string) error
}
```

A `Principal` carries a subject, a type (user, service, or anonymous), a scope
set, and provider-specific claims. The handler layer sees only the `Principal`,
so switching provider changes one construction site.

### Reference implementations

The template ships three, each small and each tested against the shared
conformance suite: a bearer-token verifier for signed tokens, a static key
verifier for service-to-service calls, and an anonymous authenticator for
public routes. A consumer that needs a hosted identity provider implements the
interface and runs the same conformance suite.

### Conformance suite

One exported test suite runs against any implementation and covers the failure
modes that recur: an expired credential, a credential for the wrong audience, a
credential signed by an unknown key, a malformed header, a missing header, a
valid credential with insufficient scope, and a credential replayed after
revocation. A new implementation cannot pass by handling the happy path alone.

### Middleware and defaults

Routes are deny by default. A route is public only when it is explicitly
registered as public, so a new route added without an authorization decision
fails closed rather than open. A test enumerates the route table and fails when
a route carries neither an authorization rule nor a public marker.

Comparisons of secret material use constant-time functions. Authentication
failures return the error envelope with no detail about which check failed, and
log the specific reason server-side.

### Scope model

Scopes are strings with a documented grammar, and the authorization primitive
checks containment rather than equality, so a broader scope satisfies a narrower
requirement without a special case per route.

## Acceptance criteria

1. The core imports no identity-provider client library; a dependency test
   asserts it.
2. All three reference implementations pass the conformance suite.
3. A route registered with neither an authorization rule nor a public marker
   fails the route-table test.
4. An expired, wrong-audience, or unknown-key credential is rejected, and the
   response body reveals which check failed for none of them.
5. Secret comparison is constant time; a lint rule or test forbids `==` on
   secret material.
6. A principal with a broader scope satisfies a narrower requirement.

## Out of scope

User management, session storage, and login user interfaces.
