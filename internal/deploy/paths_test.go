package deploy

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanRelativePathAcceptsRepositoryPaths(t *testing.T) {
	cases := map[string]string{
		"":            "",
		"/":           "",
		"/apps/web/":  "apps/web",
		"apps/api":    "apps/api",
		"./apps/api":  "apps/api",
		"/apps/./web": "apps/web",
	}
	for input, want := range cases {
		got, err := CleanRelativePath(input, true)
		if err != nil {
			t.Fatalf("CleanRelativePath(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("CleanRelativePath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCleanRelativePathRejectsTraversal(t *testing.T) {
	for _, input := range []string{
		"..",
		"../etc",
		"apps/../../etc",
		"apps/../../..",
		" C:\\Windows ",
		`apps\web`,
		"apps\x00web",
		"apps\nweb",
	} {
		if _, err := CleanRelativePath(input, true); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("CleanRelativePath(%q) error = %v, want ErrUnsafePath", input, err)
		}
	}
	if _, err := CleanRelativePath("", false); err == nil {
		t.Fatal("empty path should be rejected when a root is not allowed")
	}
}

func TestJoinRelativeStaysUnderRoot(t *testing.T) {
	root := t.TempDir()
	target, err := JoinRelative(root, "/apps/web")
	if err != nil {
		t.Fatalf("JoinRelative: %v", err)
	}
	want := filepath.Join(root, "apps", "web")
	if target != want {
		t.Fatalf("JoinRelative = %q, want %q", target, want)
	}
	if _, err := JoinRelative(root, "../../../etc"); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("traversal should fail, got %v", err)
	}
	if !strings.HasPrefix(target, filepath.Clean(root)+string(filepath.Separator)) {
		t.Fatalf("target %q escaped root %q", target, root)
	}
}
