// Package brand holds the product name and every name derived from it.
// Nothing else in the code spells the name out (ARCHITECTURE.md, change 1).
package brand

const (
	// Name is the product name as streamers see it.
	Name = "StreamGremlin"
	// Slug is the lower-case name used for binaries, Linux paths and formats.
	Slug = "streamgremlin"
	// EnvPrefix starts every environment variable the app reads.
	EnvPrefix = "STREAMGREMLIN_"

	// APIVersion is the major version of the local API.
	APIVersion = "1"
	// APIVersionHeader is sent on every API response and may be sent by clients.
	APIVersionHeader = Name + "-API-Version"

	// DefaultAPIAddr is where the engine listens unless told otherwise.
	// If it is taken, the engine falls back to a free loopback port.
	DefaultAPIAddr = "127.0.0.1:7430"
)

// Env returns the full name of an environment variable, for example
// Env("DATA_DIR") is "STREAMGREMLIN_DATA_DIR".
func Env(name string) string { return EnvPrefix + name }

// Format returns the versioned name of a file format, for example
// Format("workflow") is "streamgremlin.workflow/v1".
func Format(kind string) string { return Slug + "." + kind + "/v1" }
