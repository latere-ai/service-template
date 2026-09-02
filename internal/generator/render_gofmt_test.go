// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package generator

import (
	"go/format"
	"strings"
	"testing"
	"testing/fstest"
)

// The rewrite substitutes identifiers of a different width than the ones it
// replaces, and gofmt aligns a run of composite-literal values to the widest
// key in that run. A generator that only substitutes therefore emits source
// that gofmt would change, and the generated repository fails its own
// fmt-check gate before anyone has touched it.
func TestGeneratedGoIsFormattedAfterTheRewrite(t *testing.T) {
	// Alignment breaks only when a run mixes keys the rewrite touches with keys
	// it does not: the rewritten ones change width, the others do not, and the
	// padding gofmt computed for the old widths is now wrong. A run where every
	// key is rewritten by the same delta stays aligned, which is why this
	// fixture deliberately mixes the two.
	const src = `package main

var paths = map[string]string{
	"cmd/service":    ".",
	"internal/store": "internal/store",
	"internal/a":     "internal/a",
}
`
	if _, err := format.Source([]byte(src)); err != nil {
		t.Fatalf("fixture is not valid Go: %v", err)
	}

	skeleton := fstest.MapFS{"main.go": {Data: []byte(src)}}
	cfg := &Config{Module: "github.com/example/reference", Name: "a-much-longer-name"}

	out, err := Render(skeleton, Entry{Path: "main.go", Source: "main.go"}, cfg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(out), "cmd/a-much-longer-name") {
		t.Fatalf("rewrite did not run:\n%s", out)
	}

	want, err := format.Source(out)
	if err != nil {
		t.Fatalf("generated source is not valid Go: %v", err)
	}
	if string(out) != string(want) {
		t.Errorf("generated Go is not gofmt-clean; gofmt would rewrite it:\ngot:\n%s\nwant:\n%s", out, want)
	}
}

// A non-Go file must pass through untouched: running a Go formatter over a
// Makefile or a YAML document would corrupt it.
func TestNonGoFilesAreNotFormatted(t *testing.T) {
	const mk = "build:\n\tgo build ./...\n"
	skeleton := fstest.MapFS{"Makefile": {Data: []byte(mk)}}
	out, err := Render(skeleton, Entry{Path: "Makefile", Source: "Makefile"}, &Config{Module: "m", Name: "svc"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(out) != mk {
		t.Errorf("Makefile was altered:\ngot %q\nwant %q", out, mk)
	}
}
