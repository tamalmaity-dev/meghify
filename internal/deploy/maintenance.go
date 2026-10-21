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

	"github.com/docker/docker/api/types/image"
	units "github.com/docker/go-units"

	"github.com/tamalmaity-dev/meghify/internal/docker"
)

// retiredHelperRepos are images the panel itself used before switching to its
// own build helper. They are safe to drop when no container references them.
var retiredHelperRepos = []string{"coollabsio/coolify-helper"}

// CleanupResult reports what one cleanup pass reclaimed.
type CleanupResult struct {
	Reclaimed     int64 `json:"reclaimed"`
	ImagesRemoved int64 `json:"imagesRemoved"`
	// OrphanedContainers counts application containers whose database row is
	// gone. They are removed before the image sweep so their old image can be
	// reclaimed in the same pass.
	OrphanedContainers int64 `json:"orphanedContainers,omitempty"`
	VolumesRemoved     int64 `json:"volumesRemoved,omitempty"`
	NetworksRemoved    int64 `json:"networksRemoved,omitempty"`
	// Sandbox says whether the build sandbox took part (it does not run in
	// some setups, e.g. BUILD_SANDBOX=off).
	Sandbox bool `json:"sandbox"`
}

// CleanupOptions selects the resources one cleanup pass may reclaim. The
// default (zero) keeps operator resources and only performs the panel's
// normal dangling-image, retention, helper and cache maintenance.
type CleanupOptions struct {
	Protect     map[string]struct{}
	KnownApps   map[string]struct{}
	ImageKeep   int
	CacheKeep   int64
	HelperImage string
	ProxyImage  string

	// UnusedVolumes and UnusedNetworks are opt-in because they can remove
	// data from stopped containers. Prune methods are label-scoped, so only
	// panel-created volumes and networks participate.
	UnusedVolumes  bool
	UnusedNetworks bool
}

// CleanupEngine reclaims Docker storage everywhere the panel is responsible
// for it: dangling images, application images past the retention count, the
// build cache — on the engine and inside the build sandbox — and superseded
// helper and proxy image versions. Failures are joined into the returned error
// while every remaining step still runs, so a sandbox hiccup cannot block an
// engine cleanup.
func CleanupEngine(ctx context.Context, client *docker.Client, opts CleanupOptions) (CleanupResult, error) {
	var result CleanupResult
	if client == nil {
		return result, nil
	}

	var errs []error

	// A deleted application can leave a stopped container behind when the
	// engine was unreachable during teardown. That container protects its old
	// image from the retention sweep forever. Remove both before considering
	// retention; a nil map means the caller could not read the application
	// list, so this pass is skipped rather than treating every app as gone.
	if opts.KnownApps != nil {
		containers, images, err := PruneOrphanedApplications(ctx, client, opts.KnownApps, opts.Protect)
		result.OrphanedContainers = int64(containers)
		result.ImagesRemoved += int64(images)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if bytes, err := client.PruneImages(ctx); err != nil {
		errs = append(errs, err)
	} else {
		result.Reclaimed += bytes
	}

	if bytes, removed, err := client.PruneManagedImages(ctx, "", opts.ImageKeep, opts.Protect); err != nil {
		errs = append(errs, err)
	} else {
		result.Reclaimed += bytes
		result.ImagesRemoved += int64(removed)
	}

	// Older helper versions are obsolete the moment the panel upgrades; the
	// repository is entirely the panel's, so every other tag can go. The
	// in-use one is protected and the engine refuses deletions under live
	// containers anyway.
	pruneRepo := func(repo, keep string) {
		if repo == "" {
			return
		}
		bytes, removed, err := client.PruneRepoExcept(ctx, repo, keep)
		if err != nil {
			errs = append(errs, err)
			return
		}
		result.Reclaimed += bytes
		result.ImagesRemoved += int64(removed)
	}
	currentHelperRepo := docker.RepoOf(opts.HelperImage)
	pruneRepo(currentHelperRepo, opts.HelperImage)
	// Releases before v0.3.1 used Coolify's helper image. Its layers live in a
	// different repository, so the sweep above never reclaimed them. Removing
	// the tags is safe even on a host that also runs Coolify: the engine
	// refuses to remove an image referenced by a running or stopped container.
	for _, repo := range retiredHelperRepos {
		if repo != currentHelperRepo {
			pruneRepo(repo, "")
		}
	}
	// The proxy repository is shared with upstream tags, so keep the exact
	// configured reference. Upgrading from traefik:v3.3 to v3.7 is the common
	// case: the old layer set is no longer referenced and can go.
	if proxyRepo := docker.RepoOf(opts.ProxyImage); proxyRepo != "" && proxyRepo != currentHelperRepo {
		pruneRepo(proxyRepo, opts.ProxyImage)
	}

	if opts.UnusedVolumes {
		if bytes, removed, err := client.PruneVolumes(ctx); err != nil {
			errs = append(errs, err)
		} else {
			result.Reclaimed += bytes
			result.VolumesRemoved = int64(removed)
		}
	}
	if opts.UnusedNetworks {
		if removed, err := client.PruneNetworks(ctx); err != nil {
			errs = append(errs, err)
		} else {
			result.NetworksRemoved = int64(removed)
		}
	}

	if opts.CacheKeep > 0 {
		if bytes, err := client.BuildCachePruneKeep(ctx, opts.CacheKeep); err != nil {
			errs = append(errs, err)
		} else {
			result.Reclaimed += bytes
		}
	}

	if bytes, ok, err := PruneSandbox(ctx, client, opts.CacheKeep); err != nil {
		errs = append(errs, err)
	} else if ok {
		result.Reclaimed += bytes
		result.Sandbox = true
	}

	return result, errors.Join(errs...)
}

// PruneOrphanedApplications removes application containers and images whose
// meghify.application label points at a record that no longer exists. It is
// intentionally bounded to panel-labelled resources: unrelated engine state is
// never touched. Passing a nil known set would be dangerous, so callers that
// cannot read the database skip the pass entirely.
func PruneOrphanedApplications(ctx context.Context, client *docker.Client, known map[string]struct{}, protect map[string]struct{}) (int, int, error) {
	if client == nil || known == nil {
		return 0, 0, nil
	}
	var errs []error
	removedContainers := 0
	removedImages := 0

	containers, err := client.ListContainers(ctx, "", "")
	if err != nil {
		errs = append(errs, fmt.Errorf("list application containers: %w", err))
	} else {
		for _, container := range selectOrphanedContainers(containers, known) {
			_ = client.StopContainer(ctx, container.ID, 20*time.Second)
			if err := client.RemoveContainer(ctx, container.ID); err != nil {
				errs = append(errs, fmt.Errorf("remove orphaned container %s: %w", container.Name, err))
				continue
			}
			if protect != nil && container.Image != "" {
				delete(protect, container.Image)
			}
			removedContainers++
		}
	}

	images, err := client.ListImages(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("list application images: %w", err))
		return removedContainers, removedImages, errors.Join(errs...)
	}
	for _, summary := range selectOrphanedImages(images, known) {
		refs := summary.RepoTags
		if len(refs) == 0 {
			refs = []string{summary.ID}
		}
		ok := true
		for _, ref := range refs {
			if err := client.RemoveImage(ctx, ref); err != nil {
				ok = false
			}
		}
		if ok {
			removedImages++
		}
	}
	return removedContainers, removedImages, errors.Join(errs...)
}

func selectOrphanedContainers(containers []*docker.ContainerInfo, known map[string]struct{}) []*docker.ContainerInfo {
	out := make([]*docker.ContainerInfo, 0)
	for _, container := range containers {
		appID := container.Labels["meghify.application"]
		if appID == "" {
			continue
		}
		if _, ok := known[appID]; ok {
			continue
		}
		out = append(out, container)
	}
	return out
}

func selectOrphanedImages(images []image.Summary, known map[string]struct{}) []image.Summary {
	out := make([]image.Summary, 0)
	for _, summary := range images {
		appID := summary.Labels["meghify.application"]
		if appID == "" {
			continue
		}
		if _, ok := known[appID]; ok {
			continue
		}
		out = append(out, summary)
	}
	return out
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
