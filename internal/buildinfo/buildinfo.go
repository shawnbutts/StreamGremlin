// Package buildinfo reports the version the binary was built as.
// Release builds set both variables with -ldflags -X.
package buildinfo

var (
	// Version is the release version, or "dev" for local builds.
	Version = "dev"
	// Commit is the short commit hash the binary was built from.
	Commit = "unknown"
)
