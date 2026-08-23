package main

import (
	"context"
	"log/slog"

	"github.com/example/reference-service/internal/server"
	"github.com/example/reference-service/internal/store"
)

// This file is present when the repository selected the database feature. It
// assigns the seam the entry point calls, so the entry point itself never names
// the store package and compiles without it.
func init() { openDatabase = connectStore }

// connectStore opens the pool, reports it through readiness, and closes it on
// shutdown.
//
// Migrations are not applied here. Two replicas starting at once would race,
// and a start-up migration ties a schema change to rollout timing instead of to
// the deployment step that owns it. They run through the migrate command.
func connectStore(ctx context.Context, a *assembly) error {
	dsn := a.cfg.DatabaseURL.Reveal()
	if dsn == "" {
		// A scaffold runs without a database, and so do its tests. A service
		// that requires one states that by making the connection string a
		// required setting in internal/config.
		a.logger.Warn("no database connection string, the store is not opened")
		return nil
	}

	db, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}

	a.addComponent(server.Component{
		Name:  "store",
		Start: func(context.Context) error { return nil },
		Stop: func(context.Context) error {
			// Close blocks until every acquired connection is returned, so a
			// leaked acquisition shows up as a slow shutdown rather than as a
			// connection the server drops mid-query.
			db.Close()
			return nil
		},
	})
	// Readiness reports the pool, not the process: a database the service
	// cannot reach must take the replica out of the load balancer without
	// restarting it, which is what separates ready from live.
	a.addReadyCheck("store", db.Ping)

	slog.InfoContext(ctx, "store connected")
	return nil
}
