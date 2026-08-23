package auth_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The gate: secret material is compared with a constant time function and
// never with == or !=. A variable time comparison returns after the first
// differing byte, so the time it takes measures how much of a guess was right,
// and a caller that can measure it can recover the secret byte by byte.
//
// The scanner reads the package source rather than its behaviour, because the
// defect is invisible at run time: both forms return the same answer.

// secretNames are the identifier fragments that mark a value as secret
// material. The match is on the rendered operand, lower cased, so
// "presentedSignature" and "a.APIKey" both match.
var secretNames = []string{
	"secret",
	"password",
	"passwd",
	"token",
	"credential",
	"signature",
	"hmac",
	"digest",
	"apikey",
	"key",
}

// constantTimeCalls are the comparisons that are safe by construction. A call
// to one of them is a comparison already performed, and its result is a plain
// integer or boolean.
var constantTimeCalls = []string{
	"hmac.Equal",
	"subtle.ConstantTimeCompare",
	"subtle.ConstantTimeEq",
	"subtle.ConstantTimeByteEq",
}

// secretComparisons returns the rendered == and != expressions in src that
// compare secret material. A comparison against a literal, for example an
// emptiness or a length check, is not one: it reveals nothing that the value's
// presence does not.
func secretComparisons(name string, src []byte) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		bin, ok := n.(*ast.BinaryExpr)
		if !ok || (bin.Op != token.EQL && bin.Op != token.NEQ) {
			return true
		}
		if isLiteral(bin.X) || isLiteral(bin.Y) {
			return true
		}
		if isConstantTimeCall(bin.X) || isConstantTimeCall(bin.Y) {
			return true
		}
		if namesSecret(bin.X) || namesSecret(bin.Y) {
			found = append(found, types.ExprString(bin))
		}
		return true
	})
	return found, nil
}

func isLiteral(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.BasicLit:
		return true
	case *ast.Ident:
		return v.Name == "nil"
	default:
		return false
	}
}

func isConstantTimeCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	return slices.Contains(constantTimeCalls, types.ExprString(call.Fun))
}

func namesSecret(e ast.Expr) bool {
	rendered := strings.ToLower(types.ExprString(e))
	for _, name := range secretNames {
		if strings.Contains(rendered, name) {
			return true
		}
	}
	return false
}

// TestSecretComparisonScannerReportsAVariableTimeComparison proves the gate
// fails when the defect it guards is present, and stays quiet for the forms
// that are allowed.
func TestSecretComparisonScannerReportsAVariableTimeComparison(t *testing.T) {
	path := filepath.Join("testdata", "secretcompare", "compare.go")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the fixture: %v", err)
	}
	found, err := secretComparisons(path, src)
	if err != nil {
		t.Fatalf("parse the fixture: %v", err)
	}
	want := []string{
		"token == expected",
		"apiKey != expected",
		"string(want) == presentedSignature",
	}
	if len(found) != len(want) {
		t.Fatalf("the scanner reported %v, want exactly %v", found, want)
	}
	for i, w := range want {
		if found[i] != w {
			t.Errorf("finding %d = %q, want %q", i, found[i], w)
		}
	}
}

// TestPackageComparesSecretsInConstantTime runs the scanner over the package's
// own source. Test files are excluded: a test compares fixtures it minted
// itself, and the rule protects the code that runs in production.
func TestPackageComparesSecretsInConstantTime(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list the package files: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("the scanner found no source files, so it asserts nothing")
	}
	scanned := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		found, err := secretComparisons(path, src)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		scanned++
		for _, expr := range found {
			t.Errorf("%s compares secret material with %q; use hmac.Equal or "+
				"subtle.ConstantTimeCompare", path, expr)
		}
	}
	if scanned == 0 {
		t.Fatal("every file was skipped, so the scanner asserts nothing")
	}
}
