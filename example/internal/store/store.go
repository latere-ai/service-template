package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Options are the pool limits.
//
// Every field has a reason to exist. MaxConns caps what the service can demand
// of the server, which is shared with every other service. MaxConnLifetime
// retires connections so a failover or a changed authentication does not
// require a restart. AcquireTimeout bounds the wait for a slot, so a saturated
// pool fails a request quickly instead of queueing until the client gives up.
type Options struct {
	MaxConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
	AcquireTimeout  time.Duration
}

// DefaultOptions are the limits a service starts with. They are conservative
// on connection count, because the database is the shared resource and the
// service is the replicated one.
func DefaultOptions() Options {
	return Options{
		MaxConns:        10,
		MaxConnLifetime: 30 * time.Minute,
		MaxConnIdleTime: 5 * time.Minute,
		AcquireTimeout:  3 * time.Second,
	}
}

// validate rejects limits that would produce a pool with no usable behaviour.
func (o Options) validate() error {
	var problems []error
	if o.MaxConns < 1 {
		problems = append(problems, fmt.Errorf("MaxConns: must be at least 1, got %d", o.MaxConns))
	}
	if o.MaxConnLifetime <= 0 {
		problems = append(problems, fmt.Errorf("MaxConnLifetime: must be greater than zero, got %s", o.MaxConnLifetime))
	}
	if o.MaxConnIdleTime <= 0 {
		problems = append(problems, fmt.Errorf("MaxConnIdleTime: must be greater than zero, got %s", o.MaxConnIdleTime))
	}
	if o.AcquireTimeout <= 0 {
		problems = append(problems, fmt.Errorf("AcquireTimeout: must be greater than zero, got %s", o.AcquireTimeout))
	}
	return errors.Join(problems...)
}

// Store is the database handle the service holds. It owns one pool.
type Store struct {
	pool           *pgxpool.Pool
	acquireTimeout time.Duration
}

// Open connects the pool with the default limits. The connection string may
// override any of them with the pool_max_conns, pool_max_conn_lifetime, and
// pool_max_conn_idle_time parameters, so an operator can raise a limit without
// a release.
func Open(ctx context.Context, dsn string) (*Store, error) {
	return OpenWith(ctx, dsn, DefaultOptions())
}

// OpenWith connects the pool with explicit limits.
func OpenWith(ctx context.Context, dsn string, opts Options) (*Store, error) {
	if dsn == "" {
		return nil, errors.New("the database connection string is empty")
	}
	if err := opts.validate(); err != nil {
		return nil, err
	}
	cfg, err := poolConfig(dsn, opts)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open the database pool: %w", err)
	}
	s := &Store{pool: pool, acquireTimeout: opts.AcquireTimeout}
	if err := s.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// poolConfig parses the connection string and applies the limits the string
// did not set. It is separate from OpenWith so the precedence is testable
// without a server.
func poolConfig(dsn string, opts Options) (*pgxpool.Config, error) {
	given, err := poolParams(dsn)
	if err != nil {
		return nil, err
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse the database connection string: %w", err)
	}
	if !given["pool_max_conns"] {
		cfg.MaxConns = opts.MaxConns
	}
	if !given["pool_max_conn_lifetime"] {
		cfg.MaxConnLifetime = opts.MaxConnLifetime
	}
	if !given["pool_max_conn_idle_time"] {
		cfg.MaxConnIdleTime = opts.MaxConnIdleTime
	}
	return cfg, nil
}

// poolNames are the connection-string parameters that set a pool limit. The
// pool parser consumes them, so they are read before it runs to know which
// limits the operator set.
var poolNames = []string{"pool_max_conns", "pool_max_conn_lifetime", "pool_max_conn_idle_time"}

func poolParams(dsn string) (map[string]bool, error) {
	cc, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse the database connection string: %w", err)
	}
	given := make(map[string]bool, len(poolNames))
	for _, name := range poolNames {
		if _, ok := cc.RuntimeParams[name]; ok {
			given[name] = true
		}
	}
	return given, nil
}

// Pool exposes the underlying pool for the pgx features this type does not
// wrap, for example CopyFrom and batches.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Close returns every connection to the server. It blocks until every
// acquired connection is released, so a caller that leaks one blocks shutdown.
func (s *Store) Close() {
	s.pool.Close()
}

// Ping proves the pool can reach the server. It is the readiness check the
// server registers for the database.
func (s *Store) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.acquireTimeout)
	defer cancel()
	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("reach the database: %w", err)
	}
	return nil
}

// Acquire takes a connection from the pool, bounded by the acquisition
// timeout. The caller releases it.
//
// The timeout applies to the wait for a slot only. The caller's context still
// bounds the work done on the connection, so a slow query and a saturated pool
// produce different failures.
func (s *Store) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	acquireCtx, cancel := context.WithTimeout(ctx, s.acquireTimeout)
	defer cancel()

	conn, err := s.pool.Acquire(acquireCtx)
	if err != nil {
		if acquireCtx.Err() != nil && ctx.Err() == nil {
			return nil, fmt.Errorf("the database pool has no free connection after %s "+
				"(%d of %d in use): %w", s.acquireTimeout,
				s.pool.Stat().AcquiredConns(), s.pool.Stat().MaxConns(), err)
		}
		return nil, err
	}
	return conn, nil
}

// Exec runs a statement that returns no rows.
func (s *Store) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	conn, err := s.Acquire(ctx)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	defer conn.Release()
	return conn.Exec(ctx, sql, args...)
}

// Query runs a query. The rows hold a pooled connection until they are
// closed, and closing them releases it.
func (s *Store) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	conn, err := s.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		conn.Release()
		return nil, err
	}
	return &pooledRows{Rows: rows, conn: conn}, nil
}

// QueryRow runs a query that returns at most one row. Scanning it releases the
// connection, and a query that returns nothing reports pgx.ErrNoRows.
func (s *Store) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	rows, err := s.Query(ctx, sql, args...)
	if err != nil {
		return errorRow{err: err}
	}
	return &singleRow{rows: rows}
}

// Begin starts a transaction. Commit or Rollback returns the connection to the
// pool, so every transaction needs one of them.
func (s *Store) Begin(ctx context.Context) (pgx.Tx, error) {
	acquireCtx, cancel := context.WithTimeout(ctx, s.acquireTimeout)
	defer cancel()
	return s.pool.Begin(acquireCtx)
}

// Stat reports the pool counters, which the readiness endpoint and the metrics
// exporter read.
func (s *Store) Stat() *pgxpool.Stat { return s.pool.Stat() }

// pooledRows releases the connection the rows were read on. Without it a
// caller that closes the rows leaks a pool slot, which is the failure the
// acquisition timeout would then report on every later query.
type pooledRows struct {
	pgx.Rows
	conn     *pgxpool.Conn
	released bool
}

func (r *pooledRows) Close() {
	r.Rows.Close()
	if !r.released {
		r.released = true
		r.conn.Release()
	}
}

// singleRow reads at most one row and closes the rows, which is what makes
// QueryRow safe to call without a matching Close.
type singleRow struct {
	rows pgx.Rows
}

func (r *singleRow) Scan(dest ...any) error {
	defer r.rows.Close()
	if !r.rows.Next() {
		if err := r.rows.Err(); err != nil {
			return err
		}
		return pgx.ErrNoRows
	}
	if err := r.rows.Scan(dest...); err != nil {
		return err
	}
	r.rows.Close()
	return r.rows.Err()
}

// errorRow carries an acquisition failure to the caller's Scan, so QueryRow
// keeps the single-value signature pgx defines.
type errorRow struct {
	err error
}

func (r errorRow) Scan(...any) error { return r.err }
