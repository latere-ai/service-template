// Package handler is the positive half of the lint fixture. Each function
// breaks exactly one rule the generated configuration enables, so a test can
// prove every linter fires rather than assuming the list is honoured.
package handler

import (
	"log/slog"
	"math/rand"
	"os"
)

// UncheckedError drops an error the caller has to handle. errcheck reports it.
func UncheckedError() {
	os.Remove("lint-fixture")
}

// DeadAssignment writes a value nothing reads. ineffassign reports it.
func DeadAssignment(n int) int {
	total := n
	total = 0
	return total
}

// StaleIdiom counts with the pre-range form. modernize reports it.
func StaleIdiom(n int) int {
	total := 0
	for i := 0; i < n; i++ {
		total += i
	}
	return total
}

// ForbiddenImport reaches for the superseded random number package. depguard
// reports the import.
func ForbiddenImport() int {
	return rand.Intn(2)
}

// ContextlessLog writes a record with no context, so it carries no trace or
// span identifier. sloglint reports it inside the request path.
func ContextlessLog() {
	slog.Info("handled")
}
