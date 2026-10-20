package deploy

import (
	"errors"
	"path"
	"path/filepath"
	"strings"
)

// ErrUnsafePath reports a caller-supplied path that would escape a build
// workspace or point at the panel's filesystem.
var ErrUnsafePath = errors.New("path must stay inside the repository")

// CleanRelativePath normalises a repository-relative path supplied by the API.
//
// The panel accepts the friendly leading slash used by the UI ("/apps/web"),
// but the result is always a slash-separated relative path. Absolute paths,
// drive letters, backslashes, NUL bytes and ".." components are rejected: this
// value is later joined to a workspace by both the host fallback and the helper
// container, so accepting it unchecked would allow deleting or copying "/".
func CleanRelativePath(value string, allowRoot bool) (string, error) {
	value = strings.TrimSpace(value)
	if strings.ContainsRune(value, 0) || strings.ContainsAny(value, "\r\n") {
		return "", ErrUnsafePath
	}
	if strings.Contains(value, "\\") {
		return "", ErrUnsafePath
	}
	// Windows drive letters and UNC-ish prefixes are never repository-relative.
	if len(value) >= 2 && value[1] == ':' {
		return "", ErrUnsafePath
	}
	trimmed := strings.TrimPrefix(value, "/")
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == "/" {
		if allowRoot {
			return "", nil
		}
		return "", errors.New("path is required")
	}
	if path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", ErrUnsafePath
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == ".." {
			return "", ErrUnsafePath
		}
	}
	return cleaned, nil
}

// JoinRelative joins a validated repository-relative path to a workspace root
// and verifies the result cannot escape that root.
func JoinRelative(root, relative string) (string, error) {
	cleaned, err := CleanRelativePath(relative, true)
	if err != nil {
		return "", err
	}
	target := root
	if cleaned != "" {
		target = filepath.Join(root, filepath.FromSlash(cleaned))
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", ErrUnsafePath
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrUnsafePath
	}
	return target, nil
}
