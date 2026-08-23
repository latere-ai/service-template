package store

import (
	"strings"
	"testing"
)

func TestApplyFoldsTheStatementsIntoTheSchema(t *testing.T) {
	sc := newSchema()
	stmts := []string{
		`CREATE UNLOGGED TABLE IF NOT EXISTS public.widgets (
			id bigint PRIMARY KEY,
			name text,
			PRIMARY KEY (id)
		)`,
		"ALTER TABLE ONLY widgets ADD COLUMN colour text",
		"ALTER TABLE widgets ADD weight numeric",
		"ALTER TABLE widgets ADD COLUMN IF NOT EXISTS height numeric",
		"ALTER TABLE parts ADD COLUMN label text",
		"CREATE TABLE archive AS SELECT * FROM widgets",
		"CREATE INDEX widgets_name_idx ON widgets (name)",
		"CREATE VIEW active_widgets AS SELECT * FROM widgets",
		"COMMENT ON TABLE widgets IS 'a comment'",
	}
	for _, stmt := range stmts {
		if _, err := apply(sc, stmt); err != nil {
			t.Fatalf("apply(%q): %v", stmt, err)
		}
	}

	for _, want := range []string{
		"widgets", "widgets.id", "widgets.name", "widgets.colour",
		"widgets.weight", "widgets.height", "parts.label", "archive",
	} {
		if !sc.has(want) {
			t.Errorf("the schema does not hold %q", want)
		}
	}
	if sc.has("widgets.primary") {
		t.Error("a table constraint was folded in as a column")
	}
}

func TestApplyRemovesWhatTheStatementRemoves(t *testing.T) {
	sc := newSchema()
	setup := []string{
		"CREATE TABLE widgets (id bigint, name text, colour text)",
		"CREATE TABLE parts (id bigint)",
		"CREATE TABLE scrap (id bigint)",
	}
	for _, stmt := range setup {
		if _, err := apply(sc, stmt); err != nil {
			t.Fatalf("apply(%q): %v", stmt, err)
		}
	}

	cases := []struct {
		stmt string
		want []string
	}{
		{"ALTER TABLE widgets DROP COLUMN colour CASCADE", []string{"widgets.colour"}},
		{"ALTER TABLE widgets RENAME COLUMN name TO title", []string{"widgets.name"}},
		{"ALTER TABLE widgets RENAME TO gadgets", []string{"widgets"}},
		{"DROP TABLE IF EXISTS parts, scrap CASCADE", []string{"parts", "scrap"}},
		{"DROP TABLE absent", nil},
		{"ALTER TABLE gadgets DROP CONSTRAINT gadgets_pkey", nil},
		{"ALTER TABLE gadgets RENAME CONSTRAINT a TO b", nil},
		{"ALTER TABLE gadgets ALTER COLUMN title DROP NOT NULL", nil},
		{"ALTER TABLE gadgets SET SCHEMA reporting", nil},
	}
	for _, tc := range cases {
		got, err := apply(sc, tc.stmt)
		if err != nil {
			t.Fatalf("apply(%q): %v", tc.stmt, err)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("apply(%q) reported %v, want %v", tc.stmt, got, tc.want)
		}
		for i, w := range tc.want {
			if got[i].target != w {
				t.Errorf("apply(%q) removal %d is %q, want %q", tc.stmt, i, got[i].target, w)
			}
		}
	}
	if !sc.has("gadgets.title") {
		t.Error("the renamed column is not in the schema under its new name")
	}
	if sc.has("widgets") {
		t.Error("the renamed table is still in the schema under its old name")
	}
}

func TestApplyRejectsDDLItCannotResolve(t *testing.T) {
	cases := map[string]string{
		"table with no name":  "CREATE TABLE",
		"alter with no table": "ALTER TABLE",
		"drop with no column": "ALTER TABLE widgets DROP",
		"rename with no to":   "ALTER TABLE widgets RENAME COLUMN name",
		"rename to nothing":   "ALTER TABLE widgets RENAME TO",
		"unknown drop":        "DROP MATERIALIZED VIEW widgets_summary",
	}
	for name, stmt := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := apply(newSchema(), stmt); err == nil {
				t.Fatalf("apply accepted %q", stmt)
			}
		})
	}
}

func TestApplyIgnoresAnEmptyStatement(t *testing.T) {
	got, err := apply(newSchema(), "   ")
	if err != nil || got != nil {
		t.Fatalf("apply(blank) = %v, %v, want nothing", got, err)
	}
}

func TestTokenizeKeepsQuotedTextWhole(t *testing.T) {
	tokens := tokenize(`INSERT INTO "odd name" VALUES ('it''s', "a""b"), ($1)`)
	joined := strings.Join(tokens, "|")
	for _, want := range []string{`"odd name"`, `'it''s'`, `"a""b"`, `$1`} {
		if !strings.Contains(joined, want) {
			t.Errorf("tokens %q do not hold %q", joined, want)
		}
	}
}

func TestStatementsKeepsAPlaceholderOutOfTheDollarQuoteRule(t *testing.T) {
	stmts, err := statements("UPDATE widgets SET name = $1 WHERE id = $2;")
	if err != nil {
		t.Fatalf("statements: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("statements returned %d, want 1: %q", len(stmts), stmts)
	}
}

func TestStatementsHandlesNestedBlockComments(t *testing.T) {
	stmts, err := statements("/* outer /* inner */ still a comment */ SELECT 1;")
	if err != nil {
		t.Fatalf("statements: %v", err)
	}
	if len(stmts) != 1 || !strings.Contains(stmts[0], "SELECT 1") {
		t.Fatalf("statements returned %q, want the single SELECT", stmts)
	}
}

func TestUpperIsSafeOutsideTheTokenRun(t *testing.T) {
	tokens := []string{"ALTER"}
	if got := upper(tokens, 5); got != "" {
		t.Errorf("upper past the end = %q, want the empty string", got)
	}
	if got := upper(tokens, -1); got != "" {
		t.Errorf("upper before the start = %q, want the empty string", got)
	}
}

func TestMatchingReportsTheEndOfAnUnbalancedRun(t *testing.T) {
	tokens := []string{"(", "a", ",", "b"}
	if got := matching(tokens, 0); got != len(tokens) {
		t.Errorf("matching = %d, want %d for an unclosed parenthesis", got, len(tokens))
	}
}

func TestSplitTopLevelIgnoresCommasInsideParentheses(t *testing.T) {
	got := splitTopLevel(tokenize("a numeric(10, 2), b text, CHECK (x IN (1, 2))"))
	if len(got) != 3 {
		t.Fatalf("splitTopLevel returned %d elements, want 3: %v", len(got), got)
	}
}

func TestSchemaCloneIsIndependent(t *testing.T) {
	sc := newSchema()
	if _, err := apply(sc, "CREATE TABLE widgets (id bigint)"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	clone := sc.clone()
	if _, err := apply(sc, "ALTER TABLE widgets ADD COLUMN name text"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if clone.has("widgets.name") {
		t.Error("the clone changed with the schema it was copied from")
	}
	if !clone.has("widgets.id") {
		t.Error("the clone lost a column")
	}
}
