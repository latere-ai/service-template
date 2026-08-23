package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func noEnv(string) string { return "" }

func TestCheckReadsTheShippedMigrations(t *testing.T) {
	var out bytes.Buffer
	if err := run(t.Context(), []string{"-check", "-dir", "../../../../migrations"}, &out, noEnv); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "backward compatible") {
		t.Errorf("output %q does not report the check result", out.String())
	}
}

func TestCheckRejectsAnIncompatibleMigration(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "0001_users.up.sql", "CREATE TABLE users (id bigint, email text);")
	write(t, dir, "0002_drop.up.sql", "ALTER TABLE users DROP COLUMN email;")

	var out bytes.Buffer
	err := run(t.Context(), []string{"-check", "-dir", dir}, &out, noEnv)
	if err == nil {
		t.Fatal("run accepted a migration that removes a column the previous release reads")
	}
	if !strings.Contains(err.Error(), "still reads") {
		t.Errorf("error %q does not explain the rejection", err)
	}
}

func TestApplyNeedsAConnectionString(t *testing.T) {
	var out bytes.Buffer
	err := run(t.Context(), []string{"-dir", "../../../../migrations"}, &out, noEnv)
	if err == nil {
		t.Fatal("run tried to apply with no connection string")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error %q does not name the variable to set", err)
	}
}

func TestUnknownFlagFails(t *testing.T) {
	var out bytes.Buffer
	if err := run(t.Context(), []string{"-nonsense"}, &out, noEnv); err == nil {
		t.Fatal("run accepted an unknown flag")
	}
}

func TestMissingDirectoryFails(t *testing.T) {
	var out bytes.Buffer
	err := run(t.Context(), []string{"-check", "-dir", filepath.Join(t.TempDir(), "absent")}, &out, noEnv)
	if err == nil {
		t.Fatal("run accepted a directory that does not exist")
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestPluralFollowsTheCount(t *testing.T) {
	cases := map[int]string{0: "migrations", 1: "migration", 2: "migrations"}
	for n, want := range cases {
		if got := plural(n); got != want {
			t.Errorf("plural(%d) = %q, want %q", n, got, want)
		}
	}
}
