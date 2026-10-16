package api

import (
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
)

// SPAHandler serves the compiled frontend from the embedded filesystem and falls
// back to index.html for client side routes.
//
// The web build output is embedded at compile time so the panel ships as a
// single binary with no static files to distribute.
type SPAHandler struct {
	filesystem http.FileSystem
	indexHTML  string
}

// NewSPAHandler builds the static file handler. The filesystem must contain
// index.html at its root, as Vite's build output does.
func NewSPAHandler(fsys fs.FS) (*SPAHandler, error) {
	index, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		return nil, fmt.Errorf("read index.html: %w", err)
	}

	return &SPAHandler{filesystem: http.FS(fsys), indexHTML: string(index)}, nil
}

// StaticHandler serves SPA assets. Requests that match a file return it;
// everything else receives index.html so the browser router can take over.
func (h *SPAHandler) StaticHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// hash-based cache headers let the browser keep assets forever.
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}

	cleaned := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	if cleaned == "." || cleaned == "/" {
		h.serveIndex(w, r)
		return
	}

	file, err := h.filesystem.Open(cleaned)
	if err != nil {
		h.serveIndex(w, r)
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || info.IsDir() {
		h.serveIndex(w, r)
		return
	}

	w.Header().Set("Content-Type", contentTypeFor(cleaned))
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, info.Name(), info.ModTime(), readSeekCloser{file})
}

func (h *SPAHandler) serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(h.indexHTML))
}

func (s *Server) serveSPA(w http.ResponseWriter, r *http.Request) {
	if s.spa == nil {
		writeError(w, http.StatusNotFound, "no frontend build is embedded")
		return
	}
	s.spa.StaticHandler(w, r)
}

// readSeekCloser adapts an http.File to an io.ReadSeeker for ServeContent.
type readSeekCloser struct {
	file interface {
		Read([]byte) (int, error)
	}
}

func (r readSeekCloser) Read(p []byte) (int, error) { return r.file.Read(p) }

func (r readSeekCloser) Seek(offset int64, whence int) (int64, error) {
	if seeker, ok := r.file.(interface {
		Seek(int64, int) (int64, error)
	}); ok {
		return seeker.Seek(offset, whence)
	}
	return 0, fmt.Errorf("seek unsupported")
}

func (r readSeekCloser) Close() error {
	if closer, ok := r.file.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

func contentTypeFor(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".webmanifest":
		return "application/manifest+json"
	default:
		return "application/octet-stream"
	}
}

// withSPA attaches the static handler to the router so unknown paths fall
// through to the SPA instead of a 404 JSON payload.
func (s *Server) withSPA(r chi.Router) {
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "endpoint not found")
			return
		}
		s.serveSPA(w, req)
	})
}
