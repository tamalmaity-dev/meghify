//go:build !linux

package main

import "runtime"

// hostSampleNow has no implementation outside Linux. The panel targets Linux
// servers; a dev build on another OS simply records no samples.
func hostSampleNow() (hostSample, bool) {
	return hostSample{Arch: runtime.GOARCH}, false
}
