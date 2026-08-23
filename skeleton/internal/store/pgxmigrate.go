package store

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/jackc/pgx/v5"
)

// TrackingTable is the table that records which migrations were applied and
// with which digest. It is created unqualified, so it lands in the first
// schema of the connection's search_path, beside the objects the migrations
// create.
const TrackingTable = "schema_migrations"

// pgxMigrator runs the migration steps on one dedicated connection.
//
// One connection is a requirement, not an optimization. A PostgreSQL session
// advisory lock belongs to the session that took it, so taking it on a pooled
// connection and releasing it on another leaves the lock held until the first
// session ends.
type pgxMigrator struct {
	conn *pgx.Conn
}

// connectMigrator opens the dedicated connection for one apply run.
func connectMigrator(ctx context.Context, dsn string) (*pgxMigrator, error) {
	if dsn == "" {
		return nil, errors.New("the database connection string is empty")
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect for migrations: %w", err)
	}
	return &pgxMigrator{conn: conn}, nil
}

// close ends the connection. A close failure after a successful run does not
// change the applied state, so it is reported and not returned.
func (p *pgxMigrator) close(ctx context.Context) {
	// The connection is closed on a best-effort basis: the run is over, and
	// the process exits next.
	_ = p.conn.Close(ctx)
}

// LockKey derives the advisory lock key from the database and the schema the
// connection writes into. Advisory locks are database wide, so a key per
// schema lets migrations for two schemas in one database run at the same time
// while two runs against one schema serialize.
func (p *pgxMigrator) LockKey(ctx context.Context) (int64, error) {
	var (
		database string
		schema   *string
	)
	err := p.conn.QueryRow(ctx, "SELECT current_database(), current_schema()").Scan(&database, &schema)
	if err != nil {
		return 0, err
	}
	if schema == nil {
		return 0, errors.New("the connection has no current schema; the search_path names no schema that exists")
	}
	return advisoryKey(database, *schema), nil
}

// advisoryKey hashes the target into the int64 the lock functions take.
func advisoryKey(database, schema string) int64 {
	h := fnv.New64a()
	// A hash write never fails, and the value is fully determined by the
	// input, so the error return carries no information here.
	_, _ = h.Write([]byte("migrate\x00" + database + "\x00" + schema))
	return int64(h.Sum64())
}

// Lock waits for the lock. It blocks rather than failing fast, because a
// second deployment step that arrives during the first must apply nothing and
// still report success.
func (p *pgxMigrator) Lock(ctx context.Context, key int64) error {
	_, err := p.conn.Exec(ctx, "SELECT pg_advisory_lock($1)", key)
	return err
}

// Unlock releases the lock and fails when the session did not hold it, which
// would mean the lock was taken on another connection.
func (p *pgxMigrator) Unlock(ctx context.Context, key int64) error {
	var released bool
	if err := p.conn.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", key).Scan(&released); err != nil {
		return err
	}
	if !released {
		return fmt.Errorf("the session did not hold the advisory lock %d", key)
	}
	return nil
}

// EnsureTracking creates the tracking table when it is absent.
func (p *pgxMigrator) EnsureTracking(ctx context.Context) error {
	_, err := p.conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS `+TrackingTable+` (
	version    bigint PRIMARY KEY,
	name       text NOT NULL,
	digest     text NOT NULL,
	applied_at timestamptz NOT NULL DEFAULT now()
)`)
	return err
}

// Applied reads the tracking table in version order.
func (p *pgxMigrator) Applied(ctx context.Context) ([]AppliedMigration, error) {
	rows, err := p.conn.Query(ctx, `SELECT version, name, digest FROM `+TrackingTable+` ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AppliedMigration
	for rows.Next() {
		var a AppliedMigration
		if err := rows.Scan(&a.Version, &a.Name, &a.Digest); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Apply runs one migration and records it in the same transaction, so a
// migration that fails part way leaves neither schema change nor tracking row.
func (p *pgxMigrator) Apply(ctx context.Context, m Migration) error {
	tx, err := p.conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		// Rollback after a successful commit returns pgx.ErrTxClosed, which
		// carries no information, so the result is dropped deliberately.
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, m.SQL); err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO `+TrackingTable+` (version, name, digest) VALUES ($1, $2, $3)`,
		m.Version, m.Name, m.Digest)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
