//go:build !web_assets

package webui

import "io/fs"

// AssetsReady reports whether the binary carries a compiled frontend.
func AssetsReady() bool { return false }

// Files returns the embedded frontend, falling back to an empty filesystem when
// the binary was built without the web_assets tag.
func Files() fs.FS { return emptyFS{} }
