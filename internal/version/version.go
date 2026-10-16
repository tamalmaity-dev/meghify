// Package version carries build metadata stamped at link time.
package version

import (
	"fmt"
	"runtime"
)

// Populated with -ldflags "-X github.com/tamalmaity-dev/meghify/internal/version.Version=...".
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// String returns a human readable build identifier.
func String() string {
	return fmt.Sprintf("%s (commit %s, built %s, %s/%s, %s)",
		Version, Commit, Date, runtime.GOOS, runtime.GOARCH, runtime.Version())
}
