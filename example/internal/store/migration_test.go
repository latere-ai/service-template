package store

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func TestLoadOrdersByVersionAndDigestsTheFile(t *testing.T) {
	body := "CREATE TABLE a (id bigint);\n"
	fsys := fstest.MapFS{
		"0002_second.up.sql":     {Data: []byte("CREATE TABLE b (id bigint);\n")},
		"0001_first.up.sql":      {Data: []byte(body)},
		"0001_first.down.sql":    {Data: []byte("DROP TABLE a;\n")},
		"README.md":              {Data: []byte("not a migration\n")},
		"0010_tenth_step.up.sql": {Data: []byte("CREATE TABLE c (id bigint);\n")},
	}

	migs, err := Load(fsys)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []int64{1, 2, 10}
	if len(migs) != len(want) {
		t.Fatalf("Load returned %d migrations, want %d: %+v", len(migs), len(want), migs)
	}
	for i, v := range want {
		if migs[i].Version != v {
			t.Errorf("migration %d has version %d, want %d", i, migs[i].Version, v)
		}
	}
	if migs[0].Name != "first" {
		t.Errorf("name = %q, want %q", migs[0].Name, "first")
	}
	if migs[2].Name != "tenth_step" {
		t.Errorf("name = %q, want %q", migs[2].Name, "tenth_step")
	}
	sum := sha256.Sum256([]byte(body))
	if migs[0].Digest != hex.EncodeToString(sum[:]) {
		t.Errorf("digest = %q, want the SHA-256 of the file", migs[0].Digest)
	}
	if migs[0].SQL != body {
		t.Errorf("SQL = %q, want the file content", migs[0].SQL)
	}
}

func TestLoadNeverTreatsADownFileAsAMigration(t *testing.T) {
	fsys := fstest.MapFS{
		"0001_first.up.sql":   {Data: []byte("CREATE TABLE a (id bigint);\n")},
		"0001_first.down.sql": {Data: []byte("DROP TABLE a;\n")},
	}
	migs, err := Load(fsys)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(migs) != 1 {
		t.Fatalf("Load returned %d migrations, want 1", len(migs))
	}
	if strings.Contains(migs[0].SQL, "DROP") {
		t.Errorf("the down file was loaded: %q", migs[0].SQL)
	}
}

func TestLoadRejectsAmbiguousDirectories(t *testing.T) {
	cases := map[string]struct {
		files fstest.MapFS
		want  string
	}{
		"unnumbered file": {
			files: fstest.MapFS{"create_users.sql": {Data: []byte("SELECT 1;")}},
			want:  "<version>_<name>",
		},
		"no direction": {
			files: fstest.MapFS{"0001_users.sql": {Data: []byte("SELECT 1;")}},
			want:  "<version>_<name>",
		},
		"duplicate version": {
			files: fstest.MapFS{
				"0001_users.up.sql":    {Data: []byte("SELECT 1;")},
				"0001_accounts.up.sql": {Data: []byte("SELECT 2;")},
			},
			want: "already used by",
		},
		"down with no up": {
			files: fstest.MapFS{"0007_users.down.sql": {Data: []byte("SELECT 1;")}},
			want:  "there is no up migration",
		},
		"empty file": {
			files: fstest.MapFS{"0001_users.up.sql": {Data: []byte("   \n")}},
			want:  "the file is empty",
		},
		"uppercase name": {
			files: fstest.MapFS{"0001_Users.up.sql": {Data: []byte("SELECT 1;")}},
			want:  "<version>_<name>",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(tc.files)
			if err == nil {
				t.Fatalf("Load accepted %s", name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestLoadReportsAnUnreadableDirectory(t *testing.T) {
	if _, err := Load(os.DirFS(t.TempDir() + "/absent")); err == nil {
		t.Fatal("Load accepted a directory that does not exist")
	}
}

// The shipped migrations are part of the module, so they load and pass the
// compatibility check like any other set.
func TestShippedMigrationsLoadAndAreCompatible(t *testing.T) {
	migs, err := Load(os.DirFS("../../migrations"))
	if err != nil {
		t.Fatalf("load the shipped migrations: %v", err)
	}
	if len(migs) == 0 {
		t.Fatal("the migrations directory holds no forward migration")
	}
	if err := CheckCompatibility(migs); err != nil {
		t.Fatalf("the shipped migrations fail the compatibility check: %v", err)
	}
}
