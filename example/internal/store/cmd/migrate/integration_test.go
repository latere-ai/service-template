//go:build integration

package main

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/example/reference-service/internal/testsupport"
)

// schemaCounter makes each schema name unique inside one test binary. The
// clock alone is not enough: two schemas created in the same nanosecond would
// collide.
var schemaCounter atomic.Int64

// The command is the only supported way to apply migrations, so it is
// exercised end to end against a real server rather than only through the
// package it calls.
func TestApplyRunsTheMigrationsAndIsIdempotent(t *testing.T) {
	dsn := schemaDSN(t)
	args := []string{"-dir", "../../../../migrations", "-dsn", dsn}

	var first bytes.Buffer
	if err := run(t.Context(), args, &first, func(string) string { return "" }); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(first.String(), "applied or already present") {
		t.Errorf("output %q does not report the result", first.String())
	}

	var second bytes.Buffer
	if err := run(t.Context(), args, &second, func(string) string { return "" }); err != nil {
		t.Fatalf("the second run failed: %v", err)
	}
}

// The connection string comes from DATABASE_URL when no flag carries it, which
// is how the deployment step passes it.
func TestApplyReadsTheConnectionStringFromTheEnvironment(t *testing.T) {
	dsn := schemaDSN(t)
	var out bytes.Buffer
	err := run(t.Context(), []string{"-dir", "../../../../migrations"}, &out,
		func(name string) string {
			if name == testsupport.Postgres.Env {
				return dsn
			}
			return ""
		})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
}

// schemaDSN creates an empty schema and returns a connection string pointing
// at it, so each test applies the migrations from nothing.
func schemaDSN(t *testing.T) string {
	t.Helper()
	dsn := testsupport.Require(t, testsupport.Postgres)

	name := fmt.Sprintf("migrate_cmd_%d_%d", time.Now().UnixNano(), schemaCounter.Add(1))
	conn, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatalf("connect to %s: %v", testsupport.Postgres.Name, err)
	}
	defer func() {
		if err := conn.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Errorf("close: %v", err)
		}
	}()
	if _, err := conn.Exec(t.Context(), "CREATE SCHEMA "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create the schema %s: %v", name, err)
	}
	t.Cleanup(func() {
		ctx := context.WithoutCancel(t.Context())
		c, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Errorf("connect to drop the schema %s: %v", name, err)
			return
		}
		defer func() {
			if err := c.Close(ctx); err != nil {
				t.Errorf("close: %v", err)
			}
		}()
		if _, err := c.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{name}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("drop the schema %s: %v", name, err)
		}
	})

	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" {
		return dsn + " search_path=" + name
	}
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()
	return u.String()
}
