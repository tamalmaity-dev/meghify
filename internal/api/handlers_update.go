package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/version"
)

// The panel checks GitHub for the newest release so Settings can tell the
// operator an upgrade exists. The check is cached: a self-hosted panel must not
// spend the unauthenticated GitHub API budget (60 requests/hour per address) on
// every Settings page load.
const (
	updateCheckTTL = time.Hour
	updateErrorTTL = 5 * time.Minute
	// A manual refresh still respects this floor so the button cannot hammer
	// the GitHub API.
	updateForceFloor = time.Minute
)

// A variable so tests can point the check at a local server.
var latestReleaseURL = "https://api.github.com/repos/tamalmaity-dev/meghify/releases/latest"

// updateStatus is the shape Settings consumes. Errors are reported inside the
// body (with HTTP 200) so a network failure never breaks the page.
type updateStatus struct {
	Current     string `json:"current"`
	Latest      string `json:"latest,omitempty"`
	Available   bool   `json:"available"`
	ReleaseURL  string `json:"releaseUrl,omitempty"`
	PublishedAt string `json:"publishedAt,omitempty"`
	CheckedAt   string `json:"checkedAt"`
	Error       string `json:"error,omitempty"`
	// CanApply is true when this install can update itself from the panel
	// (systemd + the meghify CLI). Docker installs update from the host.
	CanApply           bool   `json:"canApply"`
	ApplyBlockedReason string `json:"applyBlockedReason,omitempty"`
}

type githubRelease struct {
	TagName     string `json:"tag_name"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
}

var (
	updateCacheMu sync.Mutex
	updateCache   *updateStatus
	updateCacheAt time.Time
)

// updateCheck returns the cached or freshly fetched release status. force=1
// bypasses the hourly cache, but never more than once a minute.
func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") == "1"
	status := s.checkForUpdate(r.Context(), force)
	// Install mode is host state, not release state, so it is added to the
	// response rather than cached with the GitHub answer.
	status.ApplyBlockedReason = updateBlockedReason()
	status.CanApply = status.Available && status.ApplyBlockedReason == ""
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) checkForUpdate(ctx context.Context, force bool) updateStatus {
	current := version.Version
	now := time.Now().UTC()

	updateCacheMu.Lock()
	if updateCache != nil {
		ttl := updateCheckTTL
		if updateCache.Error != "" {
			ttl = updateErrorTTL
		}
		fresh := time.Since(updateCacheAt) < ttl
		if fresh && (!force || time.Since(updateCacheAt) < updateForceFloor) {
			cached := *updateCache
			updateCacheMu.Unlock()
			return cached
		}
	}
	updateCacheMu.Unlock()

	status := updateStatus{Current: current, CheckedAt: now.Format(time.RFC3339)}
	release, err := fetchLatestRelease(ctx)
	if err != nil {
		status.Error = err.Error()
		cacheUpdate(&status, now)
		return status
	}

	status.Latest = release.TagName
	status.ReleaseURL = release.HTMLURL
	status.PublishedAt = release.PublishedAt
	status.Available = isNewerVersion(release.TagName, current)
	cacheUpdate(&status, now)
	return status
}

func cacheUpdate(status *updateStatus, at time.Time) {
	updateCacheMu.Lock()
	updateCache = status
	updateCacheAt = at
	updateCacheMu.Unlock()
}

func fetchLatestRelease(ctx context.Context) (*githubRelease, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "meghify/"+version.Version)

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github: %s", resp.Status)
	}

	var release githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	if strings.TrimSpace(release.TagName) == "" {
		return nil, errors.New("github: release has no tag")
	}
	return &release, nil
}

// isNewerVersion reports whether latest is a later release than current.
// Development builds (dev/unknown/empty) are never considered outdated.
func isNewerVersion(latest, current string) bool {
	latestParts, ok := versionParts(latest)
	if !ok {
		return false
	}
	currentParts, ok := versionParts(current)
	if !ok {
		return false
	}
	for i := 0; i < 3; i++ {
		a, b := 0, 0
		if i < len(latestParts) {
			a = latestParts[i]
		}
		if i < len(currentParts) {
			b = currentParts[i]
		}
		if a != b {
			return a > b
		}
	}
	return false
}

// versionParts extracts the numeric dotted prefix from a tag: "v0.7.2" and
// "v0.7.2-3-gabc123" both become [0, 7, 2]. "dev" is not a version.
func versionParts(v string) ([]int, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	if v == "" || v == "dev" || v == "unknown" {
		return nil, false
	}

	end := 0
	for end < len(v) {
		c := v[end]
		if (c < '0' || c > '9') && c != '.' {
			break
		}
		end++
	}
	v = strings.Trim(v[:end], ".")
	if v == "" {
		return nil, false
	}

	fields := strings.Split(v, ".")
	parts := make([]int, 0, len(fields))
	for _, field := range fields {
		n, err := strconv.Atoi(field)
		if err != nil {
			return nil, false
		}
		parts = append(parts, n)
	}
	if len(parts) == 0 {
		return nil, false
	}
	return parts, true
}

// --- applying an update -----------------------------------------------------

const updateLockout = 10 * time.Minute

// Injected so tests can avoid touching systemd or the host filesystem.
var (
	updateStartedMu sync.Mutex
	updateStartedAt time.Time

	meghifyUnitPath = "/etc/systemd/system/meghify.service"
	meghifyCLIPath  = "/usr/local/bin/meghify"

	isContainer     = runningInContainer
	lookPath        = exec.LookPath
	startUpdateUnit = runSystemdUpdate
)

// applyUpdate starts the installed `meghify update` CLI in a transient systemd
// unit and returns immediately. The CLI downloads the release, swaps the
// binary, restarts the panel and health-checks it, rolling back on failure.
//
// The transient unit is essential: the panel service runs with the default
// KillMode=control-group, so a plain child process would be killed by the
// `systemctl restart meghify` the CLI performs. systemd-run puts the updater in
// its own cgroup, where the restart cannot reach it.
func (s *Server) applyUpdate(w http.ResponseWriter, r *http.Request) {
	if updateInProgress() {
		writeError(w, http.StatusConflict, "an update is already in progress")
		return
	}

	status := s.checkForUpdate(r.Context(), false)
	if status.Error != "" {
		writeError(w, http.StatusBadGateway, "could not check for updates: "+status.Error)
		return
	}
	if !status.Available {
		writeError(w, http.StatusConflict, "this panel is already on the latest release")
		return
	}
	if reason := updateBlockedReason(); reason != "" {
		writeError(w, http.StatusConflict, reason)
		return
	}
	cli, err := meghifyCLI()
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err := startUpdateUnit(cli); err != nil {
		writeError(w, http.StatusInternalServerError, "could not start the update: "+err.Error())
		return
	}

	markUpdateStarted()
	s.audit(r.Context(), r, "update.apply", "panel", "panel", map[string]any{
		"from": status.Current,
		"to":   status.Latest,
	})
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":  "started",
		"from":    status.Current,
		"to":      status.Latest,
		"message": "Updating to " + status.Latest + ". The panel restarts when the new binary is in place.",
	})
}

// updateBlockedReason explains why the panel cannot update itself. Docker
// installs update from the host because the panel container is distroless and
// has no systemd; an unknown layout must not guess.
func updateBlockedReason() string {
	if isContainer() {
		return "this panel runs in Docker; update from the host with `meghify update`"
	}
	if _, err := os.Stat(meghifyUnitPath); err != nil {
		return "no systemd unit was found; update from a shell with `meghify update`"
	}
	if _, err := lookPath("systemd-run"); err != nil {
		return "systemd-run is unavailable; update from a shell with `meghify update`"
	}
	return ""
}

func meghifyCLI() (string, error) {
	if info, err := os.Stat(meghifyCLIPath); err == nil && !info.IsDir() {
		return meghifyCLIPath, nil
	}
	path, err := lookPath("meghify")
	if err != nil {
		return "", errors.New("the meghify CLI is not installed; run `meghify update` from a shell")
	}
	return path, nil
}

func runningInContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return true
	}
	return false
}

// runSystemdUpdate starts a transient unit. --no-block returns as soon as the
// unit is queued; --collect removes it once it exits.
func runSystemdUpdate(cli string) error {
	cmd := exec.Command("systemd-run",
		"--unit=meghify-update",
		"--collect",
		"--no-block",
		"--description=Meghify panel update",
		cli, "update")
	return cmd.Run()
}

func updateInProgress() bool {
	updateStartedMu.Lock()
	defer updateStartedMu.Unlock()
	return !updateStartedAt.IsZero() && time.Since(updateStartedAt) < updateLockout
}

func markUpdateStarted() {
	updateStartedMu.Lock()
	updateStartedAt = time.Now()
	updateStartedMu.Unlock()
}
