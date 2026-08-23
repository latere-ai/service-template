package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
)

// AppliedMigration is one row of the tracking table.
type AppliedMigration struct {
	Version int64
	Name    string
	Digest  string
}

// Result reports what one apply run did.
type Result struct {
	// Applied lists the migrations this run executed, in order.
	Applied []Migration
	// AlreadyApplied counts the migrations the tracking table already held, so
	// a second run over the same set reports zero applied and the full count
	// here.
	AlreadyApplied int
}

// migrator is the database access the apply algorithm needs. It exists so the
// ordering, the digest comparison, and the lock discipline are exercised
// without a server, while the SQL behind them stays in one adapter.
type migrator interface {
	// LockKey identifies the schema being migrated. Advisory locks are
	// database wide, so the key is derived from the target schema and two
	// runs against different schemas do not serialize against each other.
	LockKey(ctx context.Context) (int64, error)
	Lock(ctx context.Context, key int64) error
	Unlock(ctx context.Context, key int64) error
	EnsureTracking(ctx context.Context) error
	Applied(ctx context.Context) ([]AppliedMigration, error)
	Apply(ctx context.Context, m Migration) error
}

// DigestMismatchError reports a migration file that changed after it was
// applied. The applied database state no longer matches the file, so the file
// is not a description of the schema any more.
type DigestMismatchError struct {
	Version  int64
	Path     string
	Recorded string
	Found    string
}

func (e *DigestMismatchError) Error() string {
	return fmt.Sprintf(
		"migration %d (%s) was applied with digest %s and now hashes to %s; "+
			"an applied migration is immutable, so write a new migration instead",
		e.Version, e.Path, e.Recorded, e.Found)
}

// MissingMigrationError reports a migration the database applied and the
// migrations directory no longer holds.
type MissingMigrationError struct {
	Version int64
	Name    string
}

func (e *MissingMigrationError) Error() string {
	return fmt.Sprintf("migration %d (%s) is applied in the database and absent from the migrations directory",
		e.Version, e.Name)
}

// OutOfOrderError reports a new migration numbered below one already applied.
// Applying it would produce a schema no other database can reproduce, because
// the order of application would differ.
type OutOfOrderError struct {
	Version     int64
	Path        string
	LastApplied int64
}

func (e *OutOfOrderError) Error() string {
	return fmt.Sprintf("migration %d (%s) is not applied and sits below the applied migration %d; "+
		"renumber it above %d",
		e.Version, e.Path, e.LastApplied, e.LastApplied)
}

// Migrate applies the pending migrations in fsys to the database named by dsn.
//
// It runs as its own step before the new code starts. A serving process must
// never call it: two replicas starting together would race, and a start-up
// migration binds a schema change to rollout timing.
func Migrate(ctx context.Context, dsn string, fsys fs.FS) error {
	migs, err := Load(fsys)
	if err != nil {
		return err
	}
	if err := CheckCompatibility(migs); err != nil {
		return err
	}
	conn, err := connectMigrator(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.close(ctx)

	_, err = run(ctx, conn, migs)
	return err
}

// run is the apply algorithm. The lock is taken before the tracking table is
// read, so a concurrent run waits and then sees the rows the first run wrote
// rather than applying the same migration twice.
func run(ctx context.Context, m migrator, migs []Migration) (result Result, err error) {
	key, err := m.LockKey(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("identify the migration lock: %w", err)
	}
	if err := m.Lock(ctx, key); err != nil {
		return Result{}, fmt.Errorf("take the migration lock: %w", err)
	}
	defer func() {
		// The lock is released explicitly rather than left to session end, so
		// a long-lived connection does not hold it. A failed release is
		// reported, because the next run would block on it.
		if uerr := m.Unlock(ctx, key); uerr != nil {
			err = errors.Join(err, fmt.Errorf("release the migration lock: %w", uerr))
		}
	}()

	if err := m.EnsureTracking(ctx); err != nil {
		return Result{}, fmt.Errorf("create the migration tracking table: %w", err)
	}
	applied, err := m.Applied(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("read the applied migrations: %w", err)
	}

	pending, err := plan(migs, applied)
	if err != nil {
		return Result{}, err
	}
	result.AlreadyApplied = len(applied)
	for _, mig := range pending {
		if err := m.Apply(ctx, mig); err != nil {
			return result, fmt.Errorf("apply %s: %w", mig.Path, err)
		}
		result.Applied = append(result.Applied, mig)
	}
	return result, nil
}

// plan compares the files with the tracking table and returns the migrations
// still to apply, in order. It rejects an applied migration whose file changed
// or disappeared, and a new migration numbered below one already applied.
func plan(migs []Migration, applied []AppliedMigration) ([]Migration, error) {
	byVersion := make(map[int64]Migration, len(migs))
	for _, m := range migs {
		byVersion[m.Version] = m
	}

	var (
		problems []error
		last     int64
		done     = make(map[int64]bool, len(applied))
	)
	for _, a := range applied {
		done[a.Version] = true
		if a.Version > last {
			last = a.Version
		}
		file, ok := byVersion[a.Version]
		if !ok {
			problems = append(problems, &MissingMigrationError{Version: a.Version, Name: a.Name})
			continue
		}
		if file.Digest != a.Digest {
			problems = append(problems, &DigestMismatchError{
				Version:  a.Version,
				Path:     file.Path,
				Recorded: a.Digest,
				Found:    file.Digest,
			})
		}
	}

	var pending []Migration
	for _, m := range migs {
		if done[m.Version] {
			continue
		}
		if m.Version < last {
			problems = append(problems, &OutOfOrderError{Version: m.Version, Path: m.Path, LastApplied: last})
			continue
		}
		pending = append(pending, m)
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].Version < pending[j].Version })
	return pending, nil
}
