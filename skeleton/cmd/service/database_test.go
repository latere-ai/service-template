package main

import (
	"context"
	"errors"
	"testing"

	"example.com/service/internal/config"
)

// A scaffold runs without a database, and so do its tests. An absent connection
// string leaves the store unopened and registers nothing, rather than failing a
// start-up that has no database to reach.
func TestAnAbsentConnectionStringLeavesTheStoreClosed(t *testing.T) {
	a := newTestAssembly(t)
	a.cfg.DatabaseURL = config.Secret("")

	if err := connectStore(context.Background(), a); err != nil {
		t.Fatalf("connectStore: %v", err)
	}
	if len(a.components) != 0 || len(a.ready) != 0 {
		t.Errorf("registered %d components and %d checks with no connection string, want none",
			len(a.components), len(a.ready))
	}
}

// A connection string the pool cannot parse fails start-up. A service that
// cannot reach its store must not begin serving and report itself ready.
func TestAnUnusableConnectionStringFailsStartUp(t *testing.T) {
	a := newTestAssembly(t)
	a.cfg.DatabaseURL = config.Secret("this is not a connection string")

	if err := connectStore(context.Background(), a); err == nil {
		t.Fatal("an unusable connection string was accepted")
	}
}

// The serving path prefers the pooled connection string. The pooled string
// here is unusable and the direct one is absent, so start-up fails only if
// the pooled string is the one the store was opened with: were the direct
// string read first, the store would stay closed and start-up would pass.
func TestThePooledConnectionStringIsPreferred(t *testing.T) {
	a := newTestAssembly(t)
	a.cfg.DatabaseURL = config.Secret("")
	a.cfg.DatabasePoolURL = config.Secret("this is not a connection string")

	if err := connectStore(context.Background(), a); err == nil {
		t.Fatal("the pooled connection string was not the one opened")
	}
}

// fakeStore is an open store that needs no server. It records the calls the
// entry point makes on it.
type fakeStore struct {
	pingErr error
	pings   int
	closed  bool
}

func (f *fakeStore) Ping(context.Context) error { f.pings++; return f.pingErr }
func (f *fakeStore) Close()                     { f.closed = true }

// withFakeStore replaces the store the entry point opens for one test and
// returns the store and the connection string it was opened with.
func withFakeStore(t *testing.T) (*fakeStore, *string) {
	t.Helper()
	previous := openStore
	t.Cleanup(func() { openStore = previous })
	fake := &fakeStore{}
	var opened string
	openStore = func(_ context.Context, dsn string) (storeHandle, error) {
		opened = dsn
		return fake, nil
	}
	return fake, &opened
}

// An open store is a component of the process and a readiness check: it
// starts with the server, closes when the server stops, and its reachability
// is what readiness reports.
func TestAnOpenStoreRunsWithTheServerAndReportsReadiness(t *testing.T) {
	fake, opened := withFakeStore(t)
	a := newTestAssembly(t)
	a.cfg.DatabaseURL = config.Secret("postgres://direct")
	a.cfg.DatabasePoolURL = config.Secret("postgres://pooled")

	if err := connectStore(context.Background(), a); err != nil {
		t.Fatalf("connectStore: %v", err)
	}
	if *opened != "postgres://pooled" {
		t.Errorf("the store was opened with %q, want the pooled connection string", *opened)
	}
	if len(a.components) != 1 || a.components[0].Name != "store" {
		t.Fatalf("components = %+v, want the store", a.components)
	}
	if err := a.components[0].Start(context.Background()); err != nil {
		t.Errorf("the store component failed to start: %v", err)
	}
	if len(a.ready) != 1 || a.ready[0].name != "store" {
		t.Fatalf("readiness checks = %+v, want the store", a.ready)
	}
	fake.pingErr = errors.New("the server is unreachable")
	if err := a.ready[0].fn(context.Background()); !errors.Is(err, fake.pingErr) || fake.pings != 1 {
		t.Errorf("readiness did not report the store's reachability: err %v after %d pings", err, fake.pings)
	}
	if err := a.components[0].Stop(context.Background()); err != nil || !fake.closed {
		t.Errorf("stopping the store component did not close the store: err %v, closed %t", err, fake.closed)
	}
}

// Without a pooled connection string the serving path opens the direct one.
func TestTheDirectConnectionStringIsTheFallback(t *testing.T) {
	_, opened := withFakeStore(t)
	a := newTestAssembly(t)
	a.cfg.DatabaseURL = config.Secret("postgres://direct")
	a.cfg.DatabasePoolURL = config.Secret("")

	if err := connectStore(context.Background(), a); err != nil {
		t.Fatalf("connectStore: %v", err)
	}
	if *opened != "postgres://direct" {
		t.Errorf("the store was opened with %q, want the direct connection string", *opened)
	}
}
