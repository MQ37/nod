// Package version holds the build version (set with -ldflags -X, or taken from the module
// version when built with `go install ...@vX.Y.Z`).
package version

import "runtime/debug"

// Version is overridden at build time.
var Version = "dev"

// String returns the version to print: the ldflags value, else the module version from build
// info (go install), else "dev".
func String() string {
	if Version != "dev" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return Version
}
