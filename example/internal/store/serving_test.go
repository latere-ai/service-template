package store

import (
	"io/fs"
	"os"
	"path"
	"strings"
	"testing"
)

// The serving process must not apply migrations. Two replicas starting at once
// would race, and a start-up migration ties the schema change to rollout
// timing instead of to the deployment step that owns it.
//
// The rule is a gate rather than a comment, because the tempting call is one
// line in main and nothing else would report it.

// migrationCalls returns the files under root that reach the migration
// runner, and the number of Go files it read.
func migrationCalls(root string) (found []string, inspected int, err error) {
	err = fs.WalkDir(os.DirFS(root), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		data, err := os.ReadFile(path.Join(root, p))
		if err != nil {
			return err
		}
		inspected++
		for _, call := range []string{"store.Migrate(", "store.Load("} {
			if strings.Contains(string(data), call) {
				found = append(found, path.Join(root, p)+": "+call)
			}
		}
		return nil
	})
	return found, inspected, err
}

func TestTheServingProcessDoesNotApplyMigrations(t *testing.T) {
	const root = "../../cmd"
	found, inspected, err := migrationCalls(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	if inspected == 0 {
		// A gate that inspects nothing cannot fail, so an empty walk is the
		// failure rather than a pass.
		t.Fatalf("no Go file was inspected under %s", root)
	}
	for _, f := range found {
		t.Errorf("%s applies migrations; they are their own deployment step, "+
			"run through internal/store/cmd/migrate", f)
	}
}

// The gate is proved against a fixture that breaks the rule, because a gate
// nobody has seen fail is a gate nobody knows works.
func TestTheServingGateFailsOnACallInMain(t *testing.T) {
	found, inspected, err := migrationCalls("testdata/serving")
	if err != nil {
		t.Fatalf("read the fixture: %v", err)
	}
	if inspected == 0 {
		t.Fatal("the fixture holds no Go file")
	}
	if len(found) == 0 {
		t.Fatal("the gate passed a main that calls store.Migrate")
	}
}
