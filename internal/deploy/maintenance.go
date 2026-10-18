package deploy

// Storage maintenance for the Docker estate the panel owns.
//
// Three things grow without a bound otherwise: every build tags a fresh
// release image, the build cache inside the sandbox daemon (the
// meghify-build-cache volume) saves every layer a build produces, and failed
// or interrupted builds leave untagged leftovers. CleanupEngine ties the
// levers together; the deploy pipeline calls it after a release and the
// hourly GC job calls it on its schedule.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"time"

	units "github.com/docker/go-units"

	"github.com/tamalmaity-dev/meghify/internal/docker"
)

// CleanupResult reports what one cleanup pass reclaimed.
type CleanupResult struct {
	Reclaimed     int64 `json:"reclaimed"`
	ImagesRemoved int64 `json:"imagesRemoved"`
	// Sandbox says whether the build sandbox took part (it does not run in
	// some setups, e.g. BUILD_SANDBOX=off).
	Sandbox bool `json:"sandbox"`
}

// CleanupEngine reclaims Docker storage everywhere the panel is responsible
// for it: dangling images, application images past the retention count, the
// build cache — on the engine and inside the build sandbox. Failures are
// joined into the returned error while every remaining step still runs, so a
// sandbox hiccup cannot block an engine cleanup.
func CleanupEngine(ctx context.Context, client *docker.Client, protect map[string]struct{}, imageKeep int, cacheKeep int64) (CleanupResult, error) {
	var result CleanupResult
	if client == nil {
		return result, nil
	}

	var errs []error

	if bytes, err := client.PruneImages(ctx); err != nil {
		errs = append(errs, err)
	} else {
		result.Reclaimed += bytes
	}

	if bytes, removed, err := client.PruneImageRepos(ctx, imageKeep, protect); err != nil {
		errs = append(errs, err)
	} else {
		result.Reclaimed += bytes
		result.ImagesRemoved = int64(removed)
	}

	if cacheKeep > 0 {
		if bytes, err := client.BuildCachePruneKeep(ctx, cacheKeep); err != nil {
			errs = append(errs, err)
		} else {
			result.Reclaimed += bytes
		}
	}

	if bytes, ok, err := PruneSandbox(ctx, client, cacheKeep); err != nil {
		errs = append(errs, err)
	} else if ok {
		result.Reclaimed += bytes
		result.Sandbox = true
	}

	return result, errors.Join(errs...)
}

// sandboxManaged is the engine-side container name of the build sandbox.
func sandboxManaged() string { return docker.ManagedName(sandboxName) }

// SandboxUsage is the `docker system df` decomposition from inside the build
// sandbox's daemon. Total only summarises the four rows.
type SandboxUsage struct {
	Total      int64 `json:"total"`
	Images     int64 `json:"images"`
	Containers int64 `json:"containers"`
	Volumes    int64 `json:"volumes"`
	BuildCache int64 `json:"buildCache"`
}

// SandboxDiskUsage asks the sandbox daemon what it stores. ok is false when
// the sandbox is absent or down (BUILD_SANDBOX=off, sandbox stopped) — that
// is not an error, the caller just renders nothing for it.
func SandboxDiskUsage(ctx context.Context, client *docker.Client) (SandboxUsage, bool, error) {
	out, ok, err := execSandbox(ctx, client, "docker system df --format json")
	if err != nil || !ok {
		return SandboxUsage{}, false, err
	}

	var usage SandboxUsage
	for _, record := range scanJSONLines(out, sandboxDiskUsageRecord{}) {
		switch record.Type {
		case "Images":
			usage.Images = parseHumanSize(record.Size)
		case "Containers":
			usage.Containers = parseHumanSize(record.Size)
		case "Local Volumes":
			usage.Volumes = parseHumanSize(record.Size)
		case "Build Cache":
			usage.BuildCache = parseHumanSize(record.Size)
		}
	}
	usage.Total = usage.Images + usage.Containers + usage.Volumes + usage.BuildCache
	return usage, true, nil
}

// PruneSandbox bounds the sandbox daemon's storage: its build cache is pruned
// back to keepCache bytes (most recently used records survive) and untagged
// images from failed builds go. keepCache <= 0 leaves the cache unbounded.
// ok is false when the sandbox is absent or stopped — callers treat that as
// "nothing to do" rather than an error.
func PruneSandbox(ctx context.Context, client *docker.Client, keepCache int64) (int64, bool, error) {
	var reclaimed int64
	ok := false

	if keepCache > 0 {
		out, reachable, err := execSandbox(ctx, client,
			"docker builder prune --force --keep-storage "+strconv.FormatInt(keepCache, 10))
		if err != nil {
			return 0, reachable, err
		}
		ok = reachable
		reclaimed += parseReclaimedSpace(out)
	}

	out, reachable, err := execSandbox(ctx, client, "docker image prune --force")
	if err != nil {
		return reclaimed, reachable, err
	}
	ok = ok || reachable
	reclaimed += parseReclaimedSpace(out)
	return reclaimed, ok, nil
}

// execSandbox runs one shell command inside the sandbox container, reading a
// bounded amount of its output. ok=false means the sandbox is absent or not
// running.
func execSandbox(ctx context.Context, client *docker.Client, command string) (string, bool, error) {
	info, err := client.ContainerByName(ctx, sandboxName)
	if err != nil {
		return "", false, err
	}
	if info == nil || info.State != "running" {
		return "", false, nil
	}

	timeout, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	result, err := client.RunExec(timeout, sandboxManaged(), []string{"sh", "-c", command}, nil)
	if err != nil {
		return "", true, fmt.Errorf("sandbox exec: %w", err)
	}

	var out bytes.Buffer
	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(&out, result.Output)
		result.Output.Close()
		copyDone <- copyErr
	}()
	select {
	case copyErr := <-copyDone:
		if copyErr != nil {
			return out.String(), true, copyErr
		}
	case <-timeout.Done():
		result.Output.Close()
		<-copyDone
		return out.String(), true, timeout.Err()
	}
	code, err := result.Wait(timeout)
	if err != nil {
		return out.String(), true, err
	}
	if code != 0 {
		return out.String(), true, fmt.Errorf("sandbox exec exited with code %d: %s", code, clip(out.String()))
	}
	return out.String(), true, nil
}

type sandboxDiskUsageRecord struct {
	Type        string `json:"Type"`
	Size        string `json:"Size"`
	Reclaimable string `json:"Reclaimable"`
	TotalCount  string `json:"TotalCount"`
	Active      string `json:"Active"`
}

// parseReclaimedSpace pulls the reclaimed byte count out of prune output. The
// CLI prints human sizes in plain mode ("Total reclaimed space: 32.5MB") and
// numeric counts with --format json; accept both.
func parseReclaimedSpace(out string) int64 {
	if match := reclaimJSON.FindStringSubmatch(out); match != nil {
		if n, err := strconv.ParseInt(match[1], 10, 64); err == nil {
			return n
		}
	}
	if match := reclaimText.FindStringSubmatch(out); match != nil {
		return parseHumanSize(match[1])
	}
	return 0
}

var (
	reclaimJSON = regexp.MustCompile(`"SpaceReclaimed"\s*:\s*(\d+)`)
	reclaimText = regexp.MustCompile(`(?i)reclaimed space[^0-9]*([0-9.]+\s*[A-Za-z]+)`)
)

// parseHumanSize reads docker CLI sizes ("4.559GB", "127kB") as bytes. The
// CLI renders decimal sizes, which is the base go-units' FromHumanSize uses.
func parseHumanSize(size string) int64 {
	n, err := units.FromHumanSize(size)
	if err != nil {
		return 0
	}
	return n
}

// scanJSONLines decodes newline-delimited JSON records into a typed slice.
func scanJSONLines[T any](out string, template T) []T {
	var records []T
	for line := range bytes.Lines([]byte(out)) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		record := template
		if err := json.Unmarshal(line, &record); err == nil {
			records = append(records, record)
		}
	}
	return records
}

func clip(s string) string {
	if len(s) <= 200 {
		return s
	}
	return "…" + s[len(s)-200:]
}
