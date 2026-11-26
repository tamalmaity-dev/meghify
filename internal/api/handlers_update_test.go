package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/db"
)

func TestFetchLatestRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v0.8.0","html_url":"https://example.test/release","published_at":"2026-01-01T00:00:00Z"}`))
	}))
	defer server.Close()

	previous := latestReleaseURL
	latestReleaseURL = server.URL
	defer func() { latestReleaseURL = previous }()

	release, err := fetchLatestRelease(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if release.TagName != "v0.8.0" {
		t.Fatalf("tag = %q, want v0.8.0", release.TagName)
	}
	if release.HTMLURL != "https://example.test/release" {
		t.Fatalf("url = %q", release.HTMLURL)
	}
}

func TestFetchLatestReleaseRejectsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer server.Close()

	previous := latestReleaseURL
	latestReleaseURL = server.URL
	defer func() { latestReleaseURL = previous }()

	if _, err := fetchLatestRelease(context.Background()); err == nil {
		t.Fatal("expected an error for a non-200 response")
	}
}

// --- apply update -----------------------------------------------------------

func resetUpdateTestState(t *testing.T) {
	t.Helper()
	clear := func() {
		updateCacheMu.Lock()
		updateCache = nil
		updateCacheAt = time.Time{}
		updateCacheMu.Unlock()

		updateStartedMu.Lock()
		updateStartedAt = time.Time{}
		updateStartedMu.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

func seedUpdateCache(status updateStatus) {
	updateCacheMu.Lock()
	updateCache = &status
	updateCacheAt = time.Now()
	updateCacheMu.Unlock()
}

func applyUpdateRequest(t *testing.T, server *Server) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/update/apply", nil)
	server.applyUpdate(recorder, request)
	return recorder
}

func TestApplyUpdateRejectsWhenUpToDate(t *testing.T) {
	resetUpdateTestState(t)
	seedUpdateCache(updateStatus{
		Current: "v0.7.2", Latest: "v0.7.2", Available: false,
		CheckedAt: time.Now().Format(time.RFC3339),
	})

	if got := applyUpdateRequest(t, &Server{}).Code; got != http.StatusConflict {
		t.Fatalf("status = %d, want %d", got, http.StatusConflict)
	}
}

func TestApplyUpdateRejectsWhileRunning(t *testing.T) {
	resetUpdateTestState(t)
	seedUpdateCache(updateStatus{
		Current: "v0.7.2", Latest: "v0.8.0", Available: true,
		CheckedAt: time.Now().Format(time.RFC3339),
	})
	markUpdateStarted()

	if got := applyUpdateRequest(t, &Server{}).Code; got != http.StatusConflict {
		t.Fatalf("status = %d, want %d", got, http.StatusConflict)
	}
}

func TestApplyUpdateRejectsInsideContainer(t *testing.T) {
	resetUpdateTestState(t)
	seedUpdateCache(updateStatus{
		Current: "v0.7.2", Latest: "v0.8.0", Available: true,
		CheckedAt: time.Now().Format(time.RFC3339),
	})

	previous := isContainer
	isContainer = func() bool { return true }
	t.Cleanup(func() { isContainer = previous })

	if got := applyUpdateRequest(t, &Server{}).Code; got != http.StatusConflict {
		t.Fatalf("status = %d, want %d", got, http.StatusConflict)
	}
}

func TestApplyUpdateStartsSystemdUnit(t *testing.T) {
	resetUpdateTestState(t)
	seedUpdateCache(updateStatus{
		Current: "v0.7.2", Latest: "v0.8.0", Available: true,
		CheckedAt: time.Now().Format(time.RFC3339),
	})

	unit := filepath.Join(t.TempDir(), "meghify.service")
	if err := os.WriteFile(unit, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(t.TempDir(), "meghify")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	previousContainer := isContainer
	previousLookPath := lookPath
	previousStart := startUpdateUnit
	previousUnit := meghifyUnitPath
	previousCLI := meghifyCLIPath
	t.Cleanup(func() {
		isContainer = previousContainer
		lookPath = previousLookPath
		startUpdateUnit = previousStart
		meghifyUnitPath = previousUnit
		meghifyCLIPath = previousCLI
	})

	isContainer = func() bool { return false }
	meghifyUnitPath = unit
	meghifyCLIPath = cli
	lookPath = func(name string) (string, error) {
		if name == "systemd-run" {
			return "/usr/bin/systemd-run", nil
		}
		return "", os.ErrNotExist
	}
	var startedWith string
	startUpdateUnit = func(path string) error {
		startedWith = path
		return nil
	}

	handle, err := db.OpenPath("file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "update.db")))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	server := &Server{store: db.OpenStore(handle), logger: slog.Default()}

	recorder := applyUpdateRequest(t, server)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusAccepted, recorder.Body.String())
	}
	if startedWith != cli {
		t.Fatalf("started %q, want %q", startedWith, cli)
	}
	if !updateInProgress() {
		t.Fatal("update was not marked as in progress")
	}
}

func TestIsNewerVersion(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.8.0", "v0.7.2", true},
		{"v0.7.2", "v0.7.2", false},
		{"v0.7.1", "v0.7.2", false},
		{"v0.10.0", "v0.9.0", true},
		{"v1.0.0", "v0.99.99", true},
		{"0.8.0", "0.7.2", true},
		{"v0.7.2-3-gabc123", "v0.7.2", false},
		{"v0.8.0", "dev", false},
		{"v0.8.0", "", false},
		{"dev", "v0.7.2", false},
	}
	for _, tc := range cases {
		if got := isNewerVersion(tc.latest, tc.current); got != tc.want {
			t.Errorf("isNewerVersion(%q, %q) = %v, want %v",
				tc.latest, tc.current, got, tc.want)
		}
	}
}

func TestVersionParts(t *testing.T) {
	cases := []struct {
		in   string
		want []int
		ok   bool
	}{
		{"v0.7.2", []int{0, 7, 2}, true},
		{"0.7.2", []int{0, 7, 2}, true},
		{"V1.2", []int{1, 2}, true},
		{"v0.7.2-3-gabc123", []int{0, 7, 2}, true},
		{"dev", nil, false},
		{"unknown", nil, false},
		{"", nil, false},
		{"release-candidate", nil, false},
	}
	for _, tc := range cases {
		got, ok := versionParts(tc.in)
		if ok != tc.ok {
			t.Errorf("versionParts(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("versionParts(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("versionParts(%q) = %v, want %v", tc.in, got, tc.want)
				break
			}
		}
	}
}
