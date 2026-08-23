// Package store is the negative half of the scoped logging fixture. The
// request-path logging rule is anchored to the transport directories, so the
// same call that fails in a handler is silent here.
package store

import "log/slog"

// ContextlessLog writes a record with no context outside the request path.
func ContextlessLog() {
	slog.Info("stored")
}
