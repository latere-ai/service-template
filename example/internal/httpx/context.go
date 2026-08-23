package httpx

import (
	"context"
	"sync"
)

// contextKey is the unexported key type of this package. A distinct type keeps
// the value reachable only through the accessors below.
type contextKey int

const stateKey contextKey = iota

// requestState is what the stages learn about a request as it travels inward:
// the identifier the second stage assigns and the route the third stage
// resolves. It is held by pointer so an outer stage sees a value an inner
// stage filled in, which is what lets the outermost stage, recovery, name the
// request in a panic it renders.
//
// The fields are guarded because the timeout stage runs the handler on a
// second goroutine.
type requestState struct {
	mu    sync.RWMutex
	id    string
	route string
}

func (s *requestState) requestID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.id
}

func (s *requestState) setRequestID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.id = id
}

func (s *requestState) routePattern() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.route
}

func (s *requestState) setRoutePattern(route string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.route = route
}

// withState attaches request state, or returns the state already attached, so
// a stage used on its own still works and a stage inside the shared chain
// shares one record.
func withState(ctx context.Context) (context.Context, *requestState) {
	if s := stateFrom(ctx); s != nil {
		return ctx, s
	}
	s := &requestState{}
	return context.WithValue(ctx, stateKey, s), s
}

func stateFrom(ctx context.Context) *requestState {
	s, _ := ctx.Value(stateKey).(*requestState)
	return s
}

// RequestID reports the identifier assigned to the request, or the empty
// string outside a request. It is the value the envelope reports as
// `instance` and the access log reports as `request_id`.
func RequestID(ctx context.Context) string {
	if s := stateFrom(ctx); s != nil {
		return s.requestID()
	}
	return ""
}

// RoutePattern reports the registered route pattern the request matched, for
// example "/v1/items/{id}". Telemetry labels use the pattern and never the raw
// path, because a label carrying an identifier produces one time series per
// identifier.
func RoutePattern(ctx context.Context) string {
	if s := stateFrom(ctx); s != nil {
		return s.routePattern()
	}
	return ""
}
