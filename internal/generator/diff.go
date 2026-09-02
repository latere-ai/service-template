// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package generator

import (
	"fmt"
	"strings"
)

// diffContext is the number of unchanged lines printed around a change.
const diffContext = 3

// diffLineBudget caps the quadratic longest-common-subsequence table. A file
// larger than this reports the change without line detail, which keeps the
// check bounded on generated assets.
const diffLineBudget = 4000

type diffOp struct {
	kind byte // ' ' equal, '-' removed, '+' added
	text string
}

// UnifiedDiff renders the change from want to have in unified form. The old
// side is the template content and the new side is what the repository holds,
// so a reader sees what the local edit did.
func UnifiedDiff(path string, want, have []byte) string {
	a := splitLines(want)
	b := splitLines(have)
	header := fmt.Sprintf("--- template/%s\n+++ repository/%s\n", path, path)
	if len(a)+len(b) > diffLineBudget {
		return header + fmt.Sprintf("@@ %d template lines against %d repository lines @@\n", len(a), len(b))
	}
	ops := diffOps(a, b)
	hunks := hunks(ops)
	if len(hunks) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString(header)
	out.WriteString(hunks)
	return out.String()
}

func splitLines(data []byte) []string {
	s := string(data)
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

// diffOps returns the edit script from a to b using a longest common
// subsequence, which gives a stable, minimal-looking result.
func diffOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	table := make([][]int, n+1)
	for i := range table {
		table[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = table[i+1][j+1] + 1
				continue
			}
			table[i][j] = max(table[i+1][j], table[i][j+1])
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

// hunks renders the changed parts of an edit script with surrounding context.
func hunks(ops []diffOp) string {
	changed := false
	for _, op := range ops {
		if op.kind != ' ' {
			changed = true
			break
		}
	}
	if !changed {
		return ""
	}
	keep := make([]bool, len(ops))
	for i, op := range ops {
		if op.kind == ' ' {
			continue
		}
		lo := max(0, i-diffContext)
		hi := min(len(ops)-1, i+diffContext)
		for k := lo; k <= hi; k++ {
			keep[k] = true
		}
	}
	var out strings.Builder
	oldLine, newLine := 1, 1
	i := 0
	for i < len(ops) {
		if !keep[i] {
			if ops[i].kind != '+' {
				oldLine++
			}
			if ops[i].kind != '-' {
				newLine++
			}
			i++
			continue
		}
		start := i
		startOld, startNew := oldLine, newLine
		oldCount, newCount := 0, 0
		for i < len(ops) && keep[i] {
			if ops[i].kind != '+' {
				oldLine++
				oldCount++
			}
			if ops[i].kind != '-' {
				newLine++
				newCount++
			}
			i++
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", startOld, oldCount, startNew, newCount)
		for _, op := range ops[start:i] {
			out.WriteByte(op.kind)
			out.WriteString(op.text)
			out.WriteByte('\n')
		}
	}
	return out.String()
}
