package main

import (
	"context"
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
