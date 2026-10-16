// Package webui exposes the compiled frontend to the HTTP server.
//
// The build has two modes. By default the panel serves the API only, which is
// what the dev workflow uses. Building with the web_assets tag after running the
// frontend build embeds the compiled SPA into the binary, so production runs as a
// single executable with no static files to distribute.
package webui

import (
	"errors"
	"io/fs"
)

// ErrNoAssets is returned when the binary was built without the web_assets tag.
var ErrNoAssets = errors.New("no embedded frontend assets")

// assets is the embedded filesystem, or a hand-written empty one. See the build
// tagged files in this package for the two implementations.
var assets fs.FS = emptyFS{}

type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) { return nil, fs.ErrNotExist }

func (emptyFS) ReadDir(string) ([]fs.DirEntry, error) { return nil, fs.ErrNotExist }

func (emptyFS) ReadFile(string) ([]byte, error) { return nil, fs.ErrNotExist }
