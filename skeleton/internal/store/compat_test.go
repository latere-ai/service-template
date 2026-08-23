package store

import (
	"strings"
	"testing"
)

// seq turns SQL bodies into a migration sequence, numbered in the order given.
func seq(bodies ...string) []Migration {
	out := make([]Migration, 0, len(bodies))
	for i, b := range bodies {
		out = append(out, Migration{
			Version: int64(i + 1),
			Name:    "step",
			Path:    fileName(i + 1),
			SQL:     b,
		})
	}
	return out
}

func fileName(version int) string {
	return strings.Repeat("0", 4-len(itoa(version))) + itoa(version) + "_step.up.sql"
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

const createUsers = `CREATE TABLE users (
	id bigint PRIMARY KEY,
	email text NOT NULL,
	legacy_email text,
	CONSTRAINT users_email_key UNIQUE (email)
);`

func TestCompatibilityRejectsARemovalThePreviousReleaseReads(t *testing.T) {
	cases := map[string]struct {
		second string
		target string
	}{
		"drop column": {
			second: "ALTER TABLE users DROP COLUMN legacy_email;",
			target: `"users.legacy_email"`,
		},
		"drop column without the keyword": {
			second: "ALTER TABLE users DROP legacy_email;",
			target: `"users.legacy_email"`,
		},
		"drop column if exists": {
			second: "ALTER TABLE users DROP COLUMN IF EXISTS legacy_email CASCADE;",
			target: `"users.legacy_email"`,
		},
		"drop table": {
			second: "DROP TABLE users;",
			target: `"users"`,
		},
		"rename column": {
			second: "ALTER TABLE users RENAME COLUMN legacy_email TO old_email;",
			target: `"users.legacy_email"`,
		},
		"rename table": {
			second: "ALTER TABLE users RENAME TO accounts;",
			target: `"users"`,
		},
		"quoted identifier": {
			second: `ALTER TABLE "users" DROP COLUMN "legacy_email";`,
			target: `"users.legacy_email"`,
		},
		"one action among several": {
			second: "ALTER TABLE users ADD COLUMN nickname text, DROP COLUMN legacy_email;",
			target: `"users.legacy_email"`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := CheckCompatibility(seq(createUsers, tc.second))
			if err == nil {
				t.Fatalf("the check accepted %q", tc.second)
			}
			if !strings.Contains(err.Error(), tc.target) {
				t.Errorf("error %q does not name %s", err, tc.target)
			}
			if !strings.Contains(err.Error(), waiverPrefix) {
				t.Errorf("error %q does not say how to record the removal", err)
			}
		})
	}
}

func TestCompatibilityAcceptsAWaivedRemoval(t *testing.T) {
	second := `-- template:allow-drop users.legacy_email since=v1.4.0 reason=the last reader shipped in v1.4.0
ALTER TABLE users DROP COLUMN legacy_email;`
	if err := CheckCompatibility(seq(createUsers, second)); err != nil {
		t.Fatalf("the check rejected a waived removal: %v", err)
	}
}

func TestCompatibilityAcceptsNonDestructiveChange(t *testing.T) {
	cases := map[string]string{
		"add column":       "ALTER TABLE users ADD COLUMN nickname text;",
		"add constraint":   "ALTER TABLE users ADD CONSTRAINT users_email_ck CHECK (email <> '');",
		"drop constraint":  "ALTER TABLE users DROP CONSTRAINT users_email_key;",
		"drop default":     "ALTER TABLE users ALTER COLUMN email DROP DEFAULT;",
		"drop not null":    "ALTER TABLE users ALTER COLUMN email DROP NOT NULL;",
		"create index":     "CREATE INDEX users_email_idx ON users (email);",
		"drop index":       "DROP INDEX users_email_idx;",
		"insert":           "INSERT INTO users (id, email) VALUES (1, 'a@example.com');",
		"comment mentions": "-- this does not DROP COLUMN legacy_email\nALTER TABLE users ADD COLUMN nickname text;",
		"literal mentions": "INSERT INTO users (id, email) VALUES (1, 'DROP COLUMN legacy_email; oops');",
		"new table dropped in the same migration": "CREATE TABLE scratch (id bigint);\nDROP TABLE scratch;",
	}
	for name, second := range cases {
		t.Run(name, func(t *testing.T) {
			if err := CheckCompatibility(seq(createUsers, second)); err != nil {
				t.Fatalf("the check rejected %q: %v", second, err)
			}
		})
	}
}

func TestCompatibilityFailsClosedOnAStatementItCannotRead(t *testing.T) {
	cases := map[string]string{
		"drop view":            "DROP VIEW active_users;",
		"unknown alter action": "ALTER TABLE users ALTER COLUMN email DROP SOMETHING;",
		"rename inside alter":  "ALTER TABLE users ALTER COLUMN email RENAME TO mail;",
		"drop schema":          "DROP SCHEMA reporting CASCADE;",
	}
	for name, second := range cases {
		t.Run(name, func(t *testing.T) {
			err := CheckCompatibility(seq(createUsers, second))
			if err == nil {
				t.Fatalf("the check accepted an unreadable statement: %q", second)
			}
			if !strings.Contains(err.Error(), "cannot") && !strings.Contains(err.Error(), "resolve") {
				t.Errorf("error %q does not say the statement could not be read", err)
			}
		})
	}
}

func TestCompatibilityRejectsAStaleOrMalformedWaiver(t *testing.T) {
	cases := map[string]struct {
		second string
		want   string
	}{
		"waiver matches nothing": {
			second: "-- template:allow-drop users.nickname since=v1.4.0 reason=no reader\n" +
				"ALTER TABLE users ADD COLUMN nickname text;",
			want: "matches no removal",
		},
		"waiver without a reason": {
			second: "-- template:allow-drop users.legacy_email since=v1.4.0\n" +
				"ALTER TABLE users DROP COLUMN legacy_email;",
			want: "a waiver is written",
		},
		"waiver without a release": {
			second: "-- template:allow-drop users.legacy_email reason=no reader\n" +
				"ALTER TABLE users DROP COLUMN legacy_email;",
			want: "a waiver is written",
		},
		"waiver for another target": {
			second: "-- template:allow-drop users.email since=v1.4.0 reason=no reader\n" +
				"ALTER TABLE users DROP COLUMN legacy_email;",
			want: "still reads",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := CheckCompatibility(seq(createUsers, tc.second))
			if err == nil {
				t.Fatalf("the check accepted %s", name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestCompatibilityFollowsTheThreeReleaseRename(t *testing.T) {
	// Release one adds the new column, release two backfills, release three
	// removes the old column with the waiver that records why it is safe.
	err := CheckCompatibility(seq(
		createUsers,
		"ALTER TABLE users ADD COLUMN email_address text;",
		"UPDATE users SET email_address = email;",
		"-- template:allow-drop users.email since=v2.0.0 reason=every reader moved to email_address in v2.0.0\n"+
			"ALTER TABLE users DROP COLUMN email;",
	))
	if err != nil {
		t.Fatalf("the three-release rename was rejected: %v", err)
	}
}

func TestStatementsSplitsOnRealBoundaries(t *testing.T) {
	sql := `-- a comment with a ; semicolon
CREATE TABLE a (id bigint); /* block ; comment */
INSERT INTO a VALUES (1);
INSERT INTO b VALUES ('semi;colon');`
	stmts, err := statements(sql)
	if err != nil {
		t.Fatalf("statements: %v", err)
	}
	if len(stmts) != 3 {
		t.Fatalf("statements returned %d, want 3: %q", len(stmts), stmts)
	}
	if strings.Contains(stmts[0], "comment") {
		t.Errorf("the comment survived: %q", stmts[0])
	}
}

func TestStatementsRejectsUnterminatedText(t *testing.T) {
	cases := map[string]string{
		"open literal":       "INSERT INTO a VALUES ('x);",
		"open block comment": "/* never closed\nCREATE TABLE a (id bigint);",
		"open dollar body":   "CREATE FUNCTION f() RETURNS int AS $body$ SELECT 1;",
	}
	for name, sql := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := statements(sql); err == nil {
				t.Fatalf("statements accepted %s", name)
			}
		})
	}
}

func TestStatementsKeepsADollarQuotedBodyWhole(t *testing.T) {
	sql := `CREATE FUNCTION f() RETURNS int AS $body$ BEGIN RETURN 1; END $body$ LANGUAGE plpgsql;`
	stmts, err := statements(sql)
	if err != nil {
		t.Fatalf("statements: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("statements returned %d, want 1: %q", len(stmts), stmts)
	}
}

func TestNormalizeIdent(t *testing.T) {
	cases := map[string]string{
		"Users":          "users",
		"public.Users":   "users",
		`"Users"`:        "Users",
		`"a""b"`:         `a"b`,
		"UPPER_CASE_COL": "upper_case_col",
	}
	for in, want := range cases {
		if got := normalizeIdent(in); got != want {
			t.Errorf("normalizeIdent(%q) = %q, want %q", in, got, want)
		}
	}
}
