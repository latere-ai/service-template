// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package generator

import (
	"fmt"
	"strconv"
	"strings"
)

// The generator reads three hand-written or machine-written YAML documents:
// the consumer declaration, the lock file, and the manifest fragments. All
// three use a fixed, small subset of YAML: block mappings, block sequences,
// flow sequences of scalars, and plain or quoted scalars. Parsing that subset
// here keeps the command free of third-party dependencies, and keeps error
// messages anchored to a file name and a line number, which is what a person
// editing a fragment by hand needs.

type nodeKind int

const (
	kindScalar nodeKind = iota
	kindMapping
	kindSequence
)

// node is one value in a parsed document. A mapping keeps its keys in source
// order so that emitted documents are stable.
type node struct {
	kind  nodeKind
	line  int
	str   string
	keys  []string
	vals  []*node
	items []*node
}

// get returns the value for a mapping key, or nil when the key is absent or
// the node is not a mapping.
func (n *node) get(key string) *node {
	if n == nil || n.kind != kindMapping {
		return nil
	}
	for i, k := range n.keys {
		if k == key {
			return n.vals[i]
		}
	}
	return nil
}

// yamlError carries the file and line so a person can find the problem.
type yamlError struct {
	file string
	line int
	msg  string
}

func (e *yamlError) Error() string {
	if e.line > 0 {
		return fmt.Sprintf("%s:%d: %s", e.file, e.line, e.msg)
	}
	return fmt.Sprintf("%s: %s", e.file, e.msg)
}

func errAt(file string, line int, format string, args ...any) error {
	return &yamlError{file: file, line: line, msg: fmt.Sprintf(format, args...)}
}

// srcLine is one significant line: blanks and comment-only lines are dropped
// before parsing so the recursive descent never has to skip.
type srcLine struct {
	num    int
	indent int
	text   string
}

// parseYAML parses the supported subset and returns the document root. An
// empty document yields an empty mapping.
func parseYAML(file string, data []byte) (*node, error) {
	lines, err := scanLines(file, string(data))
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return &node{kind: kindMapping}, nil
	}
	if lines[0].indent != 0 {
		return nil, errAt(file, lines[0].num, "document must start at column 1")
	}
	n, next, err := parseBlock(file, lines, 0, 0)
	if err != nil {
		return nil, err
	}
	if next != len(lines) {
		return nil, errAt(file, lines[next].num, "unexpected indentation")
	}
	return n, nil
}

func scanLines(file, data string) ([]srcLine, error) {
	var out []srcLine
	for i, raw := range strings.Split(data, "\n") {
		num := i + 1
		lead := len(raw) - len(strings.TrimLeft(raw, " "))
		if strings.ContainsRune(raw[:lead], '\t') || strings.HasPrefix(strings.TrimLeft(raw, " "), "\t") {
			return nil, errAt(file, num, "tab indentation is not supported, use spaces")
		}
		text := strings.TrimRight(stripComment(raw), " \t\r")
		if strings.TrimSpace(text) == "" {
			continue
		}
		if text == "---" {
			continue
		}
		out = append(out, srcLine{num: num, indent: lead, text: strings.TrimLeft(text, " ")})
	}
	return out, nil
}

// stripComment removes a trailing comment. A '#' inside a quoted scalar is
// part of the value, so quoting state is tracked.
func stripComment(s string) string {
	var inSingle, inDouble bool
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
				return s[:i]
			}
		}
	}
	return s
}

// parseBlock parses every line at the given indentation as one mapping or one
// sequence, and returns the index of the first line that belongs to an
// enclosing block.
func parseBlock(file string, lines []srcLine, i, indent int) (*node, int, error) {
	if strings.HasPrefix(lines[i].text, "- ") || lines[i].text == "-" {
		return parseSequence(file, lines, i, indent)
	}
	return parseMapping(file, lines, i, indent)
}

func parseSequence(file string, lines []srcLine, i, indent int) (*node, int, error) {
	seq := &node{kind: kindSequence, line: lines[i].num}
	for i < len(lines) && lines[i].indent == indent {
		text := lines[i].text
		if !strings.HasPrefix(text, "- ") && text != "-" {
			// A line at the sequence's own column that is not an item closes
			// the sequence, which is how a sequence written level with its key
			// ends and the next key begins.
			break
		}
		rest := strings.TrimSpace(strings.TrimPrefix(text, "-"))
		switch {
		case rest == "":
			if i+1 >= len(lines) || lines[i+1].indent <= indent {
				return nil, 0, errAt(file, lines[i].num, "sequence item has no value")
			}
			child, next, err := parseBlock(file, lines, i+1, lines[i+1].indent)
			if err != nil {
				return nil, 0, err
			}
			seq.items = append(seq.items, child)
			i = next
		case isMappingStart(rest):
			// An inline "- key: value" opens a mapping whose remaining keys
			// follow on later lines, indented past the dash.
			inner := make([]srcLine, 0, len(lines)-i)
			inner = append(inner, srcLine{num: lines[i].num, indent: indent + 2, text: rest})
			j := i + 1
			for j < len(lines) && lines[j].indent > indent {
				inner = append(inner, lines[j])
				j++
			}
			child, next, err := parseMapping(file, inner, 0, indent+2)
			if err != nil {
				return nil, 0, err
			}
			if next != len(inner) {
				return nil, 0, errAt(file, inner[next].num, "unexpected indentation inside a sequence item")
			}
			seq.items = append(seq.items, child)
			i = j
		default:
			seq.items = append(seq.items, scalarNode(lines[i].num, rest))
			i++
		}
	}
	return seq, i, nil
}

func parseMapping(file string, lines []srcLine, i, indent int) (*node, int, error) {
	m := &node{kind: kindMapping, line: lines[i].num}
	for i < len(lines) && lines[i].indent == indent {
		key, rest, ok := splitKey(lines[i].text)
		if !ok {
			return nil, 0, errAt(file, lines[i].num, "expected \"key: value\", found %q", lines[i].text)
		}
		if m.get(key) != nil {
			return nil, 0, errAt(file, lines[i].num, "duplicate key %q", key)
		}
		rest = strings.TrimSpace(rest)
		switch {
		case rest != "":
			v, err := parseInline(file, lines[i].num, rest)
			if err != nil {
				return nil, 0, err
			}
			m.keys = append(m.keys, key)
			m.vals = append(m.vals, v)
			i++
		case i+1 < len(lines) && lines[i+1].indent > indent:
			child, next, err := parseBlock(file, lines, i+1, lines[i+1].indent)
			if err != nil {
				return nil, 0, err
			}
			m.keys = append(m.keys, key)
			m.vals = append(m.vals, child)
			i = next
		case i+1 < len(lines) && lines[i+1].indent == indent && strings.HasPrefix(lines[i+1].text, "- "):
			// A sequence written at the same column as its key.
			child, next, err := parseSequence(file, lines, i+1, indent)
			if err != nil {
				return nil, 0, err
			}
			m.keys = append(m.keys, key)
			m.vals = append(m.vals, child)
			i = next
		default:
			m.keys = append(m.keys, key)
			m.vals = append(m.vals, scalarNode(lines[i].num, ""))
			i++
		}
	}
	return m, i, nil
}

// isMappingStart reports whether a sequence item body opens a mapping rather
// than holding a plain scalar.
func isMappingStart(s string) bool {
	_, _, ok := splitKey(s)
	return ok
}

// splitKey separates "key: value" or "key:" from its body. A key holds only
// unquoted identifier characters, which is all the schemas use.
func splitKey(s string) (key, rest string, ok bool) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ':' {
			if i == 0 {
				return "", "", false
			}
			if i+1 < len(s) && s[i+1] != ' ' {
				return "", "", false
			}
			return s[:i], s[i+1:], true
		}
		if !isKeyChar(c) {
			return "", "", false
		}
	}
	return "", "", false
}

func isKeyChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '_' || c == '-' || c == '.' || c == '/'
}

// parseInline handles a value written on the same line as its key: a flow
// sequence or a scalar.
func parseInline(file string, line int, s string) (*node, error) {
	if strings.HasPrefix(s, "[") {
		if !strings.HasSuffix(s, "]") {
			return nil, errAt(file, line, "unterminated flow sequence")
		}
		body := strings.TrimSpace(s[1 : len(s)-1])
		seq := &node{kind: kindSequence, line: line}
		if body == "" {
			return seq, nil
		}
		for part := range strings.SplitSeq(body, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				return nil, errAt(file, line, "empty item in flow sequence")
			}
			seq.items = append(seq.items, scalarNode(line, part))
		}
		return seq, nil
	}
	if strings.HasPrefix(s, "{") {
		return nil, errAt(file, line, "flow mappings are not supported")
	}
	return scalarNode(line, s), nil
}

func scalarNode(line int, raw string) *node {
	return &node{kind: kindScalar, line: line, str: unquote(raw)}
}

func unquote(s string) string {
	if len(s) >= 2 {
		if s[0] == '\'' && s[len(s)-1] == '\'' {
			return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
		}
		if s[0] == '"' && s[len(s)-1] == '"' {
			if out, err := strconv.Unquote(s); err == nil {
				return out
			}
			return s[1 : len(s)-1]
		}
	}
	return s
}

// scalar reads a mapping key as a string.
func (n *node) scalar(file, key string) (string, error) {
	v := n.get(key)
	if v == nil {
		return "", nil
	}
	if v.kind != kindScalar {
		return "", errAt(file, v.line, "%q must be a scalar", key)
	}
	return v.str, nil
}

func (n *node) boolean(file, key string) (bool, error) {
	v := n.get(key)
	if v == nil {
		return false, nil
	}
	if v.kind != kindScalar {
		return false, errAt(file, v.line, "%q must be true or false", key)
	}
	switch strings.ToLower(v.str) {
	case "true", "yes", "on":
		return true, nil
	case "false", "no", "off", "":
		return false, nil
	}
	return false, errAt(file, v.line, "%q must be true or false, found %q", key, v.str)
}

func (n *node) integer(file, key string, def int) (int, error) {
	v := n.get(key)
	if v == nil {
		return def, nil
	}
	if v.kind != kindScalar {
		return 0, errAt(file, v.line, "%q must be a whole number", key)
	}
	i, err := strconv.Atoi(strings.TrimSpace(v.str))
	if err != nil {
		return 0, errAt(file, v.line, "%q must be a whole number, found %q", key, v.str)
	}
	return i, nil
}

func (n *node) strings(file, key string) ([]string, error) {
	v := n.get(key)
	if v == nil {
		return nil, nil
	}
	if v.kind != kindSequence {
		return nil, errAt(file, v.line, "%q must be a list", key)
	}
	out := make([]string, 0, len(v.items))
	for _, it := range v.items {
		if it.kind != kindScalar {
			return nil, errAt(file, it.line, "%q must be a list of plain values", key)
		}
		out = append(out, it.str)
	}
	return out, nil
}
