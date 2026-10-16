//go:build web_assets

package webui

import (
	"embed"
	"io/fs"
)

// Assets holds the compiled frontend produced by the Vite build.
//
// This file is only compiled with -tags web_assets. The dist directory is the
// output target of the web build; it is created (empty) in the repository so
// `go build` works before the first frontend build.
//
//go:embed all:dist
var Assets embed.FS

// AssetsReady reports whether a real frontend build was embedded.
func AssetsReady() bool {
	entries, err := Assets.ReadDir("dist")
	return err == nil && len(entries) > 0
}

// Files returns the embedded frontend rooted at dist.
func Files() fs.FS {
	sub, err := fs.Sub(Assets, "dist")
	if err != nil {
		return nil
	}
	return sub
}
