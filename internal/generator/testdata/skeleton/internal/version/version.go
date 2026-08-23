// Package version reports what was built.
package version

// Module is the module path of this service.
const Module = "example.com/service"

// Build describes one build.
type Build struct{ Version, Commit, BuildTime, AssetHash string }

// Info returns the build stamped into the binary.
func Info() Build { return Build{} }
