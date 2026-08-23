package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

// fakeDB is an in-memory stand-in for the server. Several migrators share one,
// so the lock discipline and the tracking table are exercised the way two
// deployment steps would exercise them.
type fakeDB struct {
	mu   sync.Mutex
	cond *sync.Cond

	database string
	schema   string

	locked   map[int64]bool
	held     int
	maxHeld  int
	tracking bool

	applied []AppliedMigration
	ran     []string

	// failApply names the migration whose apply fails, which is how a
	// half-finished run is simulated.
	failApply int64
	// failTracking makes the tracking table creation fail.
	failTracking bool
	// failUnlock makes the release fail, which must reach the caller.
	failUnlock bool
}

func newFakeDB() *fakeDB {
	db := &fakeDB{database: "app", schema: "public", locked: map[int64]bool{}}
	db.cond = sync.NewCond(&db.mu)
	return db
}

// conn returns one connection to the database. Each run holds its own, which
// is what makes the advisory lock meaningful.
func (d *fakeDB) conn() *fakeConn { return &fakeConn{db: d} }

type fakeConn struct {
	db  *fakeDB
	key int64
}

func (c *fakeConn) LockKey(context.Context) (int64, error) {
	return advisoryKey(c.db.database, c.db.schema), nil
}

func (c *fakeConn) Lock(_ context.Context, key int64) error {
	d := c.db
	d.mu.Lock()
	defer d.mu.Unlock()
	for d.locked[key] {
		d.cond.Wait()
	}
	d.locked[key] = true
	c.key = key
	d.held++
	if d.held > d.maxHeld {
		d.maxHeld = d.held
	}
	return nil
}

func (c *fakeConn) Unlock(_ context.Context, key int64) error {
	d := c.db
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.locked[key] {
		return errors.New("the session does not hold the lock")
	}
	delete(d.locked, key)
	d.held--
	d.cond.Broadcast()
	if d.failUnlock {
		return errors.New("the connection is gone")
	}
	return nil
}

func (c *fakeConn) EnsureTracking(context.Context) error {
	d := c.db
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failTracking {
		return errors.New("permission denied")
	}
	d.tracking = true
	return nil
}

func (c *fakeConn) Applied(context.Context) ([]AppliedMigration, error) {
	d := c.db
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.tracking {
		return nil, errors.New("the tracking table does not exist")
	}
	return append([]AppliedMigration(nil), d.applied...), nil
}

func (c *fakeConn) Apply(_ context.Context, m Migration) error {
	d := c.db
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.locked[c.key] {
		return errors.New("apply ran without the lock")
	}
	if d.failApply == m.Version {
		return errors.New("syntax error at or near \"CRATE\"")
	}
	d.ran = append(d.ran, m.Path)
	d.applied = append(d.applied, AppliedMigration{Version: m.Version, Name: m.Name, Digest: m.Digest})
	return nil
}

func twoMigrations(t *testing.T) []Migration {
	t.Helper()
	migs, err := Load(fstest.MapFS{
		"0001_first.up.sql":  {Data: []byte("CREATE TABLE a (id bigint);\n")},
		"0002_second.up.sql": {Data: []byte("CREATE TABLE b (id bigint);\n")},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return migs
}

func TestRunAppliesEveryPendingMigrationInOrder(t *testing.T) {
	db := newFakeDB()
	migs := twoMigrations(t)

	result, err := run(t.Context(), db.conn(), migs)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(result.Applied) != 2 || result.AlreadyApplied != 0 {
		t.Fatalf("result = %d applied, %d already applied, want 2 and 0",
			len(result.Applied), result.AlreadyApplied)
	}
	if db.ran[0] != "0001_first.up.sql" || db.ran[1] != "0002_second.up.sql" {
		t.Errorf("applied in the order %v, want ascending version order", db.ran)
	}
	if db.locked[advisoryKey("app", "public")] {
		t.Error("the advisory lock is still held after a successful run")
	}
}

func TestRunAppliedTwiceChangesNothing(t *testing.T) {
	db := newFakeDB()
	migs := twoMigrations(t)

	if _, err := run(t.Context(), db.conn(), migs); err != nil {
		t.Fatalf("first run: %v", err)
	}
	result, err := run(t.Context(), db.conn(), migs)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(result.Applied) != 0 {
		t.Errorf("the second run applied %d migrations, want 0", len(result.Applied))
	}
	if result.AlreadyApplied != 2 {
		t.Errorf("AlreadyApplied = %d, want 2", result.AlreadyApplied)
	}
	if len(db.ran) != 2 {
		t.Errorf("the server ran %d migrations over two runs, want 2: %v", len(db.ran), db.ran)
	}
}

func TestRunRejectsAnEditedAppliedMigration(t *testing.T) {
	db := newFakeDB()
	migs := twoMigrations(t)
	if _, err := run(t.Context(), db.conn(), migs); err != nil {
		t.Fatalf("first run: %v", err)
	}

	edited, err := Load(fstest.MapFS{
		"0001_first.up.sql":  {Data: []byte("CREATE TABLE a (id bigint, extra text);\n")},
		"0002_second.up.sql": {Data: []byte("CREATE TABLE b (id bigint);\n")},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	_, err = run(t.Context(), db.conn(), edited)
	var mismatch *DigestMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("run returned %v, want a DigestMismatchError", err)
	}
	if mismatch.Version != 1 || mismatch.Path != "0001_first.up.sql" {
		t.Errorf("the mismatch names version %d in %q, want 1 in 0001_first.up.sql",
			mismatch.Version, mismatch.Path)
	}
	if mismatch.Recorded == mismatch.Found {
		t.Error("the mismatch reports the same digest twice")
	}
	if db.locked[advisoryKey("app", "public")] {
		t.Error("the advisory lock is still held after a rejected run")
	}
}

func TestRunRejectsAnAppliedMigrationThatDisappeared(t *testing.T) {
	db := newFakeDB()
	if _, err := run(t.Context(), db.conn(), twoMigrations(t)); err != nil {
		t.Fatalf("first run: %v", err)
	}

	only, err := Load(fstest.MapFS{"0001_first.up.sql": {Data: []byte("CREATE TABLE a (id bigint);\n")}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var missing *MissingMigrationError
	if _, err := run(t.Context(), db.conn(), only); !errors.As(err, &missing) {
		t.Fatalf("run returned %v, want a MissingMigrationError", err)
	}
	if missing.Version != 2 {
		t.Errorf("the error names version %d, want 2", missing.Version)
	}
}

func TestRunRejectsAMigrationNumberedBelowAnAppliedOne(t *testing.T) {
	// A branch merged with a stale number produces this shape: version 3 is
	// applied and version 2 arrives afterwards. Applying it would give this
	// database a schema no other database can reproduce.
	migs, err := Load(fstest.MapFS{
		"0002_second.up.sql": {Data: []byte("CREATE TABLE b (id bigint);\n")},
		"0003_third.up.sql":  {Data: []byte("CREATE TABLE c (id bigint);\n")},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	db := newFakeDB()
	db.tracking = true
	db.applied = []AppliedMigration{{Version: 3, Name: "third", Digest: migs[1].Digest}}

	var outOfOrder *OutOfOrderError
	if _, err := run(t.Context(), db.conn(), migs); !errors.As(err, &outOfOrder) {
		t.Fatalf("run returned %v, want an OutOfOrderError", err)
	}
	if outOfOrder.Version != 2 || outOfOrder.LastApplied != 3 {
		t.Errorf("the error names version %d below %d, want 2 below 3",
			outOfOrder.Version, outOfOrder.LastApplied)
	}
	if len(db.ran) != 0 {
		t.Errorf("the server ran %v, want nothing", db.ran)
	}
}

func TestConcurrentRunsSerializeAndBothSucceed(t *testing.T) {
	db := newFakeDB()
	migs := twoMigrations(t)

	const runners = 4
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		fails []error
		total int
	)
	start := make(chan struct{})
	for range runners {
		wg.Go(func() {
			<-start
			result, err := run(context.Background(), db.conn(), migs)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				fails = append(fails, err)
				return
			}
			total += len(result.Applied)
		})
	}
	close(start)
	wg.Wait()

	if len(fails) != 0 {
		t.Fatalf("%d of %d concurrent runs failed: %v", len(fails), runners, fails)
	}
	if db.maxHeld != 1 {
		t.Errorf("%d runs held the lock at once, want 1", db.maxHeld)
	}
	if total != len(migs) {
		t.Errorf("%d migrations were applied across the runs, want %d", total, len(migs))
	}
	if len(db.ran) != len(migs) {
		t.Errorf("the server ran %d migrations, want %d: %v", len(db.ran), len(migs), db.ran)
	}
}

func TestRunReleasesTheLockWhenAMigrationFails(t *testing.T) {
	db := newFakeDB()
	db.failApply = 2
	migs := twoMigrations(t)

	result, err := run(t.Context(), db.conn(), migs)
	if err == nil {
		t.Fatal("run reported success for a failing migration")
	}
	if !strings.Contains(err.Error(), "0002_second.up.sql") {
		t.Errorf("error %q does not name the failing migration", err)
	}
	if len(result.Applied) != 1 {
		t.Errorf("the run reports %d applied, want the one that succeeded", len(result.Applied))
	}
	if db.locked[advisoryKey("app", "public")] {
		t.Error("the advisory lock is still held after a failed migration")
	}
	// A later run applies what is left, which is only true because the failed
	// run recorded nothing for the migration it did not finish.
	db.failApply = 0
	next, err := run(t.Context(), db.conn(), migs)
	if err != nil {
		t.Fatalf("the retry failed: %v", err)
	}
	if len(next.Applied) != 1 {
		t.Errorf("the retry applied %d migrations, want 1", len(next.Applied))
	}
}

func TestRunReportsAFailureFromEachStep(t *testing.T) {
	migs := twoMigrations(t)

	t.Run("tracking table", func(t *testing.T) {
		db := newFakeDB()
		db.failTracking = true
		_, err := run(t.Context(), db.conn(), migs)
		if err == nil || !strings.Contains(err.Error(), "tracking table") {
			t.Fatalf("run returned %v, want a tracking table failure", err)
		}
		if db.held != 0 {
			t.Error("the lock is still held")
		}
	})

	t.Run("release", func(t *testing.T) {
		db := newFakeDB()
		db.failUnlock = true
		_, err := run(t.Context(), db.conn(), migs)
		if err == nil || !strings.Contains(err.Error(), "release the migration lock") {
			t.Fatalf("run returned %v, want the release failure", err)
		}
	})
}

func TestAdvisoryKeyIsPerSchema(t *testing.T) {
	a := advisoryKey("app", "public")
	if a != advisoryKey("app", "public") {
		t.Error("the key is not stable for one target")
	}
	if a == advisoryKey("app", "test_1") {
		t.Error("two schemas in one database share a lock key, so their migrations serialize")
	}
	if a == advisoryKey("other", "public") {
		t.Error("two databases share a lock key")
	}
}

func TestMigrateChecksTheFilesBeforeItConnects(t *testing.T) {
	cases := map[string]struct {
		files fstest.MapFS
		want  string
	}{
		"unreadable directory": {
			files: fstest.MapFS{"users.sql": {Data: []byte("SELECT 1;")}},
			want:  "<version>_<name>",
		},
		"incompatible migration": {
			files: fstest.MapFS{
				"0001_users.up.sql": {Data: []byte("CREATE TABLE users (id bigint, email text);")},
				"0002_drop.up.sql":  {Data: []byte("ALTER TABLE users DROP COLUMN email;")},
			},
			want: "still reads",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// The connection string is deliberately unusable: a failure that
			// names it would mean the file checks did not run first.
			err := Migrate(t.Context(), "postgres://user@127.0.0.1:1/db", tc.files)
			if err == nil {
				t.Fatalf("Migrate accepted %s", name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestMigrateRejectsAnEmptyConnectionString(t *testing.T) {
	err := Migrate(t.Context(), "", fstest.MapFS{
		"0001_users.up.sql": {Data: []byte("CREATE TABLE users (id bigint);")},
	})
	if err == nil || !strings.Contains(err.Error(), "connection string is empty") {
		t.Fatalf("Migrate returned %v, want the empty connection string failure", err)
	}
}

// The three rejections a person reads in a deployment log. Each names the
// migration and says what to do, because the reader is usually mid-release.
func TestTheRejectionMessagesNameTheMigration(t *testing.T) {
	cases := map[string]struct {
		err  error
		want []string
	}{
		"digest": {
			err:  &DigestMismatchError{Version: 7, Path: "0007_users.up.sql", Recorded: "aaa", Found: "bbb"},
			want: []string{"0007_users.up.sql", "aaa", "bbb", "write a new migration"},
		},
		"missing": {
			err:  &MissingMigrationError{Version: 7, Name: "users"},
			want: []string{"7", "users", "absent from the migrations directory"},
		},
		"out of order": {
			err:  &OutOfOrderError{Version: 2, Path: "0002_users.up.sql", LastApplied: 9},
			want: []string{"0002_users.up.sql", "9", "renumber"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			msg := tc.err.Error()
			for _, want := range tc.want {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q does not hold %q", msg, want)
				}
			}
		})
	}
}
