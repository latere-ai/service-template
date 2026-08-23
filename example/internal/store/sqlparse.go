package store

import (
	"fmt"
	"strings"
)

// The DDL reader behind the compatibility check.
//
// It is deliberately narrow. It follows CREATE TABLE, ALTER TABLE, and DROP
// TABLE far enough to know which tables and columns exist, and it ignores
// everything else. A statement that carries DROP or RENAME and does not parse
// is an error rather than a pass, because the check exists to catch exactly
// those statements and a parser that shrugs at one it cannot read is worse
// than no check.

// schema is the set of tables and their columns as of some point in the
// migration sequence.
type schema struct {
	tables map[string]map[string]bool
}

func newSchema() *schema {
	return &schema{tables: make(map[string]map[string]bool)}
}

func (s *schema) hasTable(t string) bool {
	_, ok := s.tables[t]
	return ok
}

func (s *schema) hasColumn(t, c string) bool {
	cols, ok := s.tables[t]
	return ok && cols[c]
}

// has reports whether the schema holds the target of a removal, which is
// "table" or "table.column".
func (s *schema) has(target string) bool {
	if table, column, ok := strings.Cut(target, "."); ok {
		return s.hasColumn(table, column)
	}
	return s.hasTable(target)
}

// clone copies the schema, so a migration is checked against the schema as it
// stood before the migration ran.
func (s *schema) clone() *schema {
	out := &schema{tables: make(map[string]map[string]bool, len(s.tables))}
	for t, cols := range s.tables {
		copied := make(map[string]bool, len(cols))
		for c := range cols {
			copied[c] = true
		}
		out.tables[t] = copied
	}
	return out
}

// statements splits SQL into statements with comments removed. It follows
// string literals, quoted identifiers, and dollar-quoted bodies so a semicolon
// inside one is not a statement boundary.
func statements(sql string) ([]string, error) {
	var (
		out []string
		b   strings.Builder
	)
	r := []rune(sql)
	for i := 0; i < len(r); {
		switch {
		case r[i] == '-' && i+1 < len(r) && r[i+1] == '-':
			for i < len(r) && r[i] != '\n' {
				i++
			}
			b.WriteRune(' ')
		case r[i] == '/' && i+1 < len(r) && r[i+1] == '*':
			depth := 1
			i += 2
			for i < len(r) && depth > 0 {
				switch {
				case r[i] == '/' && i+1 < len(r) && r[i+1] == '*':
					depth++
					i += 2
				case r[i] == '*' && i+1 < len(r) && r[i+1] == '/':
					depth--
					i += 2
				default:
					i++
				}
			}
			if depth != 0 {
				return nil, fmt.Errorf("an opened block comment is never closed")
			}
			b.WriteRune(' ')
		case r[i] == '\'' || r[i] == '"':
			quote := r[i]
			b.WriteRune(r[i])
			i++
			for {
				if i >= len(r) {
					return nil, fmt.Errorf("an opened %q literal is never closed", string(quote))
				}
				if r[i] == quote {
					if i+1 < len(r) && r[i+1] == quote {
						b.WriteRune(r[i])
						b.WriteRune(r[i+1])
						i += 2
						continue
					}
					b.WriteRune(r[i])
					i++
					break
				}
				b.WriteRune(r[i])
				i++
			}
		case r[i] == '$':
			tag, ok := dollarTag(r, i)
			if !ok {
				b.WriteRune(r[i])
				i++
				continue
			}
			end := strings.Index(string(r[i+len(tag):]), tag)
			if end < 0 {
				return nil, fmt.Errorf("an opened %s body is never closed", tag)
			}
			stop := i + len(tag) + end + len(tag)
			b.WriteString(string(r[i:stop]))
			i = stop
		case r[i] == ';':
			out = appendStatement(out, b.String())
			b.Reset()
			i++
		default:
			b.WriteRune(r[i])
			i++
		}
	}
	return appendStatement(out, b.String()), nil
}

// dollarTag reports the dollar-quote tag starting at i, for example "$$" or
// "$body$".
func dollarTag(r []rune, i int) (string, bool) {
	for j := i + 1; j < len(r); j++ {
		if r[j] == '$' {
			return string(r[i : j+1]), true
		}
		if !isIdentRune(r[j]) {
			return "", false
		}
	}
	return "", false
}

func isIdentRune(c rune) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func appendStatement(out []string, s string) []string {
	if t := strings.TrimSpace(s); t != "" {
		out = append(out, t)
	}
	return out
}

// tokenize splits a statement into words, quoted identifiers, string literals,
// and the punctuation the parser needs.
func tokenize(stmt string) []string {
	var out []string
	r := []rune(stmt)
	for i := 0; i < len(r); {
		switch r[i] {
		case ' ', '\t', '\n', '\r':
			i++
		case '(', ')', ',':
			out = append(out, string(r[i]))
			i++
		case '\'', '"':
			quote := r[i]
			start := i
			i++
			for i < len(r) {
				if r[i] == quote {
					if i+1 < len(r) && r[i+1] == quote {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			out = append(out, string(r[start:i]))
		default:
			start := i
			for i < len(r) && !strings.ContainsRune(" \t\n\r(),", r[i]) && r[i] != '\'' && r[i] != '"' {
				i++
			}
			out = append(out, string(r[start:i]))
		}
	}
	return out
}

// normalizeIdent folds an identifier the way PostgreSQL does: an unquoted name
// lowercases, a quoted name keeps its case. A schema qualification is dropped,
// because the migration runner works inside one schema.
func normalizeIdent(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 && !strings.HasPrefix(s, `"`) {
		s = s[i+1:]
	}
	if len(s) >= 2 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		return strings.ReplaceAll(s[1:len(s)-1], `""`, `"`)
	}
	return strings.ToLower(s)
}

func upper(tokens []string, i int) string {
	if i < 0 || i >= len(tokens) {
		return ""
	}
	return strings.ToUpper(tokens[i])
}

// constraintHeads are the words that open a table constraint in a CREATE TABLE
// body, which is how a constraint is told apart from a column definition.
var constraintHeads = map[string]bool{
	"CONSTRAINT": true, "PRIMARY": true, "UNIQUE": true, "FOREIGN": true,
	"CHECK": true, "EXCLUDE": true, "LIKE": true, "PERIOD": true,
}

// addConstraintHeads are the words that follow ADD when the action adds a
// constraint rather than a column.
var addConstraintHeads = map[string]bool{
	"CONSTRAINT": true, "PRIMARY": true, "UNIQUE": true, "FOREIGN": true,
	"CHECK": true, "EXCLUDE": true, "GENERATED": true,
}

// apply folds one statement into the schema and reports what it removes.
func apply(sc *schema, stmt string) ([]removal, error) {
	tokens := tokenize(stmt)
	if len(tokens) == 0 {
		return nil, nil
	}
	switch {
	case upper(tokens, 0) == "CREATE" && tableWord(tokens) > 0:
		return nil, createTable(sc, tokens)
	case upper(tokens, 0) == "DROP" && upper(tokens, 1) == "TABLE":
		return dropTable(sc, tokens)
	case upper(tokens, 0) == "ALTER" && upper(tokens, 1) == "TABLE":
		return alterTable(sc, tokens)
	case upper(tokens, 0) == "DROP":
		// An index or a trigger is not a name a reader selects, so removing
		// one is not a compatibility break. Every other DROP reaches
		// unreadable below and fails, which is the intended default.
		if object := upper(tokens, 1); object == "INDEX" || object == "TRIGGER" {
			return nil, nil
		}
		return nil, unreadable(stmt)
	default:
		return nil, unreadable(stmt)
	}
}

// unreadable rejects a statement that carries a destructive keyword the parser
// could not resolve, and passes everything else.
func unreadable(stmt string) error {
	for _, t := range tokenize(stmt) {
		switch strings.ToUpper(t) {
		case "DROP", "RENAME":
			return fmt.Errorf(
				"the compatibility check cannot read this statement, and it removes or renames something: %s",
				summarize(stmt))
		}
	}
	return nil
}

// summarize shortens a statement for a failure message.
func summarize(stmt string) string {
	s := strings.Join(strings.Fields(stmt), " ")
	if len(s) > 120 {
		return s[:117] + "..."
	}
	return s
}

// tableWord reports the index of TABLE in a CREATE statement, allowing the
// modifiers that may sit between, for example CREATE UNLOGGED TABLE.
func tableWord(tokens []string) int {
	for i := 1; i < len(tokens) && i <= 3; i++ {
		switch upper(tokens, i) {
		case "TABLE":
			return i
		case "UNLOGGED", "TEMP", "TEMPORARY", "GLOBAL", "LOCAL":
		default:
			return -1
		}
	}
	return -1
}

func createTable(sc *schema, tokens []string) error {
	i := tableWord(tokens) + 1
	if upper(tokens, i) == "IF" && upper(tokens, i+1) == "NOT" && upper(tokens, i+2) == "EXISTS" {
		i += 3
	}
	if i >= len(tokens) {
		return fmt.Errorf("a CREATE TABLE statement names no table")
	}
	table := normalizeIdent(tokens[i])
	i++
	cols := make(map[string]bool)
	if i < len(tokens) && tokens[i] == "(" {
		for _, element := range splitTopLevel(tokens[i+1 : matching(tokens, i)]) {
			if len(element) == 0 || constraintHeads[strings.ToUpper(element[0])] {
				continue
			}
			cols[normalizeIdent(element[0])] = true
		}
	}
	sc.tables[table] = cols
	return nil
}

func dropTable(sc *schema, tokens []string) ([]removal, error) {
	i := 2
	if upper(tokens, i) == "IF" && upper(tokens, i+1) == "EXISTS" {
		i += 2
	}
	var out []removal
	for ; i < len(tokens); i++ {
		switch upper(tokens, i) {
		case ",":
			continue
		case "CASCADE", "RESTRICT":
			continue
		}
		table := normalizeIdent(tokens[i])
		if sc.hasTable(table) {
			out = append(out, removal{target: table, what: "DROP TABLE"})
		}
		delete(sc.tables, table)
	}
	return out, nil
}

func alterTable(sc *schema, tokens []string) ([]removal, error) {
	i := 2
	if upper(tokens, i) == "IF" && upper(tokens, i+1) == "EXISTS" {
		i += 2
	}
	if upper(tokens, i) == "ONLY" {
		i++
	}
	if i >= len(tokens) {
		return nil, fmt.Errorf("an ALTER TABLE statement names no table")
	}
	table := normalizeIdent(tokens[i])
	i++

	var out []removal
	for _, action := range splitTopLevel(tokens[i:]) {
		rs, err := alterAction(sc, table, action)
		if err != nil {
			return nil, err
		}
		out = append(out, rs...)
	}
	return out, nil
}

func alterAction(sc *schema, table string, action []string) ([]removal, error) {
	if len(action) == 0 {
		return nil, nil
	}
	switch strings.ToUpper(action[0]) {
	case "ADD":
		addColumn(sc, table, action)
		return nil, nil
	case "DROP":
		return dropColumn(sc, table, action)
	case "RENAME":
		return renameAction(sc, table, action)
	case "ALTER":
		return nil, alterColumnAction(action)
	case "SET", "RESET", "OWNER", "ENABLE", "DISABLE", "VALIDATE",
		"CLUSTER", "ATTACH", "DETACH", "INHERIT", "NO", "REPLICA", "FORCE":
		// These change storage, ownership, or constraint state. None of them
		// removes a name a reader selects.
		return nil, unreadable(strings.Join(action, " "))
	default:
		return nil, unreadable(strings.Join(action, " "))
	}
}

// columnPropertyDrops are the column properties an ALTER COLUMN action may
// drop. Each changes how a value is written, not whether the column can be
// read, so none of them is a compatibility break.
var columnPropertyDrops = map[string]bool{
	"DEFAULT": true, "NOT": true, "IDENTITY": true, "EXPRESSION": true,
}

// alterColumnAction accepts the ALTER COLUMN forms that change a column in
// place. A DROP inside one is safe only for the properties above, so anything
// else is rejected rather than assumed harmless.
func alterColumnAction(action []string) error {
	for i, t := range action {
		switch strings.ToUpper(t) {
		case "DROP":
			if !columnPropertyDrops[upper(action, i+1)] {
				return fmt.Errorf("an ALTER COLUMN action drops something the check cannot resolve: %s",
					summarize(strings.Join(action, " ")))
			}
		case "RENAME":
			return fmt.Errorf("an ALTER COLUMN action renames something the check cannot resolve: %s",
				summarize(strings.Join(action, " ")))
		}
	}
	return nil
}

func addColumn(sc *schema, table string, action []string) {
	i := 1
	if upper(action, i) == "COLUMN" {
		i++
	} else if addConstraintHeads[upper(action, i)] {
		return
	}
	if upper(action, i) == "IF" && upper(action, i+1) == "NOT" && upper(action, i+2) == "EXISTS" {
		i += 3
	}
	if i >= len(action) {
		return
	}
	if sc.tables[table] == nil {
		sc.tables[table] = make(map[string]bool)
	}
	sc.tables[table][normalizeIdent(action[i])] = true
}

func dropColumn(sc *schema, table string, action []string) ([]removal, error) {
	i := 1
	switch upper(action, i) {
	case "CONSTRAINT":
		return nil, nil
	case "COLUMN":
		i++
	case "DEFAULT", "NOT", "EXPRESSION", "IDENTITY":
		return nil, nil
	}
	if upper(action, i) == "IF" && upper(action, i+1) == "EXISTS" {
		i += 2
	}
	if i >= len(action) {
		return nil, fmt.Errorf("a DROP action names no column: %s", summarize(strings.Join(action, " ")))
	}
	column := normalizeIdent(action[i])
	var out []removal
	if sc.hasColumn(table, column) {
		out = append(out, removal{target: table + "." + column, what: "DROP COLUMN"})
	}
	delete(sc.tables[table], column)
	return out, nil
}

func renameAction(sc *schema, table string, action []string) ([]removal, error) {
	i := 1
	switch upper(action, i) {
	case "CONSTRAINT":
		return nil, nil
	case "TO":
		if i+1 >= len(action) {
			return nil, fmt.Errorf("a RENAME TO action names no table")
		}
		to := normalizeIdent(action[i+1])
		var out []removal
		if sc.hasTable(table) {
			out = append(out, removal{target: table, what: "ALTER TABLE ... RENAME TO"})
		}
		sc.tables[to] = sc.tables[table]
		delete(sc.tables, table)
		return out, nil
	case "COLUMN":
		i++
	}
	if i+2 >= len(action) || upper(action, i+1) != "TO" {
		return nil, fmt.Errorf("a RENAME action is not readable: %s", summarize(strings.Join(action, " ")))
	}
	from, to := normalizeIdent(action[i]), normalizeIdent(action[i+2])
	var out []removal
	if sc.hasColumn(table, from) {
		out = append(out, removal{target: table + "." + from, what: "ALTER TABLE ... RENAME COLUMN"})
	}
	if sc.tables[table] == nil {
		sc.tables[table] = make(map[string]bool)
	}
	delete(sc.tables[table], from)
	sc.tables[table][to] = true
	return out, nil
}

// matching returns the index of the parenthesis closing the one at open.
func matching(tokens []string, open int) int {
	depth := 0
	for i := open; i < len(tokens); i++ {
		switch tokens[i] {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(tokens)
}

// splitTopLevel splits a token run on commas that sit outside parentheses.
func splitTopLevel(tokens []string) [][]string {
	var (
		out   [][]string
		cur   []string
		depth int
	)
	for _, t := range tokens {
		switch t {
		case "(":
			depth++
		case ")":
			depth--
		case ",":
			if depth == 0 {
				out = append(out, cur)
				cur = nil
				continue
			}
		}
		cur = append(cur, t)
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}
