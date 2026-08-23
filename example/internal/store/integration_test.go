//go:build integration

package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/example/reference-service/internal/testsupport"
)

// schemaCounter makes each schema name unique inside one test binary. The
// clock alone is not enough: two schemas created in the same nanosecond would
// collide.
var schemaCounter atomic.Int64

// The integration tier never skips itself in CI. testsupport.Require fails and
// names the dependency when TEST_DEPENDENCY_MODE is required, so a run without
// a database reports the missing database instead of reporting success.
func databaseURL(t *testing.T) string {
	t.Helper()
	return testsupport.Require(t, testsupport.Postgres)
}

// freshSchema creates an empty schema and returns a connection string that
// points at it. Each test gets its own, so tests build their schema from the
// migration files and still run in parallel.
func freshSchema(t *testing.T) string {
	t.Helper()
	dsn := databaseURL(t)

	name := fmt.Sprintf("migrate_test_%d_%d", time.Now().UnixNano(), schemaCounter.Add(1))
	admin, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatalf("connect to %s: %v", testsupport.Postgres.Name, err)
	}
	defer func() {
		if err := admin.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Errorf("close the administrative connection: %v", err)
		}
	}()

	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create the schema %s: %v", name, err)
	}
	t.Cleanup(func() {
		ctx := context.WithoutCancel(t.Context())
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Errorf("connect to drop the schema %s: %v", name, err)
			return
		}
		defer func() {
			if err := conn.Close(ctx); err != nil {
				t.Errorf("close the cleanup connection: %v", err)
			}
		}()
		if _, err := conn.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{name}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("drop the schema %s: %v", name, err)
		}
	})
	return withSearchPath(t, dsn, name)
}

// withSearchPath points a connection string at one schema. The migration
// runner creates its tracking table unqualified, so the tracking table lands
// in the same schema as the objects the migrations create.
func withSearchPath(t *testing.T, dsn, schema string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" {
		return dsn + " search_path=" + schema
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

func appliedVersions(t *testing.T, dsn string) []int64 {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() {
		if err := conn.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Errorf("close: %v", err)
		}
	}()

	rows, err := conn.Query(t.Context(), "SELECT version FROM "+TrackingTable+" ORDER BY version")
	if err != nil {
		t.Fatalf("read %s: %v", TrackingTable, err)
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read %s: %v", TrackingTable, err)
	}
	return out
}

// The shipped migrations are the source of the test schema. A migration the
// server rejects fails here rather than in a deployment.
func TestMigrateBuildsTheSchemaFromTheFiles(t *testing.T) {
	t.Parallel()
	dsn := freshSchema(t)
	files := os.DirFS("../../migrations")

	if err := Migrate(t.Context(), dsn, files); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	migs, err := Load(files)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	applied := appliedVersions(t, dsn)
	if len(applied) != len(migs) {
		t.Fatalf("%d migrations are recorded, want %d", len(applied), len(migs))
	}

	// The second run is a no-op: the tracking table already holds every
	// version, so nothing is applied and the call still succeeds.
	if err := Migrate(t.Context(), dsn, files); err != nil {
		t.Fatalf("the second Migrate failed: %v", err)
	}
	if again := appliedVersions(t, dsn); len(again) != len(applied) {
		t.Fatalf("the second run recorded %d migrations, want %d", len(again), len(applied))
	}
}

func TestMigrateRejectsAnEditedAppliedMigrationOnTheServer(t *testing.T) {
	t.Parallel()
	dsn := freshSchema(t)
	original := fstest.MapFS{
		"0001_widgets.up.sql": {Data: []byte("CREATE TABLE widgets (id bigint PRIMARY KEY);")},
	}
	if err := Migrate(t.Context(), dsn, original); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	edited := fstest.MapFS{
		"0001_widgets.up.sql": {Data: []byte("CREATE TABLE widgets (id bigint PRIMARY KEY, name text);")},
	}
	var mismatch *DigestMismatchError
	if err := Migrate(t.Context(), dsn, edited); !errors.As(err, &mismatch) {
		t.Fatalf("Migrate returned %v, want a DigestMismatchError", err)
	}
}

// A migration the server rejects fails the run, and the tracking table records
// nothing for it, so the next run retries the same migration.
func TestABrokenMigrationFailsTheRun(t *testing.T) {
	t.Parallel()
	dsn := freshSchema(t)
	files := fstest.MapFS{
		"0001_widgets.up.sql": {Data: []byte("CREATE TABLE widgets (id bigint PRIMARY KEY);")},
		"0002_broken.up.sql":  {Data: []byte("CRATE TABLE oops (id bigint);")},
	}
	err := Migrate(t.Context(), dsn, files)
	if err == nil {
		t.Fatal("Migrate reported success for a migration the server rejects")
	}
	if !strings.Contains(err.Error(), "0002_broken.up.sql") {
		t.Errorf("error %q does not name the failing migration", err)
	}
	applied := appliedVersions(t, dsn)
	if len(applied) != 1 || applied[0] != 1 {
		t.Errorf("the tracking table holds %v, want only version 1", applied)
	}
}

// Two deployment steps that overlap serialize on the advisory lock. Both
// report success, and each migration runs once.
func TestConcurrentApplyRunsSerialize(t *testing.T) {
	t.Parallel()
	dsn := freshSchema(t)
	files := fstest.MapFS{
		"0001_widgets.up.sql": {Data: []byte("CREATE TABLE widgets (id bigint PRIMARY KEY);")},
		"0002_parts.up.sql":   {Data: []byte("CREATE TABLE parts (id bigint PRIMARY KEY);")},
	}

	const runners = 4
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	start := make(chan struct{})
	for range runners {
		wg.Go(func() {
			<-start
			if err := Migrate(context.Background(), dsn, files); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}
	close(start)
	wg.Wait()

	if len(errs) != 0 {
		t.Fatalf("%d of %d concurrent runs failed: %v", len(errs), runners, errs)
	}
	if applied := appliedVersions(t, dsn); len(applied) != 2 {
		t.Fatalf("the tracking table holds %v, want exactly two versions", applied)
	}
}

// A cancelled query must not cost a pool slot. The pool would otherwise shrink
// with every client that disconnects early, until nothing can acquire.
func TestACancelledQueryReleasesItsPoolSlot(t *testing.T) {
	t.Parallel()
	dsn := freshSchema(t)

	opts := DefaultOptions()
	opts.MaxConns = 2
	opts.AcquireTimeout = 5 * time.Second
	s, err := OpenWith(t.Context(), dsn, opts)
	if err != nil {
		t.Fatalf("OpenWith: %v", err)
	}
	defer s.Close()

	for range 3 {
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		_, err := s.Exec(ctx, "SELECT pg_sleep(30)")
		cancel()
		if err == nil {
			t.Fatal("the query returned before its context expired")
		}
	}

	deadline := time.Now().Add(10 * time.Second)
	for s.Stat().AcquiredConns() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if acquired := s.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("%d connections are still acquired after the queries were cancelled", acquired)
	}

	// The pool is usable again, which is what the counter is a proxy for.
	var one int
	if err := s.QueryRow(t.Context(), "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("the pool cannot serve a query after the cancellations: %v", err)
	}
	if one != 1 {
		t.Fatalf("SELECT 1 returned %d", one)
	}
}

// The acquisition timeout is what turns a saturated pool into a fast failure
// instead of an unbounded queue.
func TestAcquireFailsWhenThePoolIsSaturated(t *testing.T) {
	t.Parallel()
	dsn := freshSchema(t)

	opts := DefaultOptions()
	opts.MaxConns = 1
	opts.AcquireTimeout = 200 * time.Millisecond
	s, err := OpenWith(t.Context(), dsn, opts)
	if err != nil {
		t.Fatalf("OpenWith: %v", err)
	}
	defer s.Close()

	held, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	started := time.Now()
	_, err = s.Acquire(t.Context())
	if err == nil {
		t.Fatal("Acquire succeeded with every connection in use")
	}
	if !strings.Contains(err.Error(), "no free connection") {
		t.Errorf("error %q does not report the saturated pool", err)
	}
	if waited := time.Since(started); waited > 2*time.Second {
		t.Errorf("Acquire waited %s, want about %s", waited, opts.AcquireTimeout)
	}
	held.Release()

	again, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatalf("Acquire failed after the connection was released: %v", err)
	}
	// The connection goes back before the pool closes: a pool with an
	// outstanding connection blocks in Close until it is released.
	again.Release()
}

// The query entry points a service uses, exercised against the schema the
// migrations build. Each one must return its connection to the pool, which the
// counter at the end proves.
func TestTheQueryEntryPointsReleaseTheirConnections(t *testing.T) {
	t.Parallel()
	dsn := freshSchema(t)
	if err := Migrate(t.Context(), dsn, os.DirFS("../../migrations")); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	opts := DefaultOptions()
	opts.MaxConns = 2
	s, err := OpenWith(t.Context(), dsn, opts)
	if err != nil {
		t.Fatalf("OpenWith: %v", err)
	}
	defer s.Close()

	if _, err := s.Exec(t.Context(),
		"INSERT INTO users (email) VALUES ($1)", "first@example.com"); err != nil {
		t.Fatalf("Exec: %v", err)
	}

	tx, err := s.Begin(t.Context())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := tx.Exec(t.Context(),
		"INSERT INTO users (email) VALUES ($1)", "second@example.com"); err != nil {
		t.Fatalf("Exec in the transaction: %v", err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	rows, err := s.Query(t.Context(), "SELECT email FROM users ORDER BY id")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	var emails []string
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		emails = append(emails, email)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(emails) != 1 || emails[0] != "first@example.com" {
		t.Fatalf("the table holds %v, want only the committed row", emails)
	}

	var email string
	err = s.QueryRow(t.Context(), "SELECT email FROM users WHERE email = $1", "absent").Scan(&email)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("QueryRow for an absent row returned %v, want pgx.ErrNoRows", err)
	}
	if err := s.QueryRow(t.Context(),
		"SELECT email FROM users ORDER BY id").Scan(&email); err != nil {
		t.Fatalf("QueryRow: %v", err)
	}

	if _, err := s.Query(t.Context(), "SELECT nonexistent FROM users"); err == nil {
		t.Fatal("Query accepted a column that does not exist")
	}
	if err := s.QueryRow(t.Context(), "SELECT nonexistent FROM users").Scan(&email); err == nil {
		t.Fatal("QueryRow accepted a column that does not exist")
	}

	if s.Pool() == nil {
		t.Fatal("Pool returned nothing")
	}
	if acquired := s.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("%d connections are still acquired after every call returned", acquired)
	}
}

// A pool that cannot reach the server fails at Open rather than at the first
// request, so a misconfigured deployment stops at start-up.
func TestOpenFailsWhenTheServerIsUnreachable(t *testing.T) {
	t.Parallel()
	// The port is reserved and never served, so the connection is refused
	// rather than left hanging.
	_, err := Open(t.Context(), "postgres://user:pass@127.0.0.1:1/app")
	if err == nil {
		t.Fatal("Open reported success against an unreachable server")
	}
	if !strings.Contains(err.Error(), "reach the database") {
		t.Errorf("error %q does not say the server could not be reached", err)
	}
}
