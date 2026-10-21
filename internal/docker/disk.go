package docker

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
)

// managedApplicationLabel is stamped on every image the deployment pipeline
// builds. Cleanup uses it to stay away from images an operator pulled
// themselves, even though they share the same engine and repository names.
const managedApplicationLabel = "meghify.application"

// DiskUsageReport breaks the engine's disk usage into the four categories the
// dashboard renders. Images is the engine's own accounting of unique layer
// bytes — the number `docker system df` reports, free of the double counting
// that summing every image would produce.
type DiskUsageReport struct {
	Total      int64 `json:"total"`
	Images     int64 `json:"images"`
	Containers int64 `json:"containers"`
	Volumes    int64 `json:"volumes"`
	BuildCache int64 `json:"buildCache"`
}

// UsageItem names one big object behind the report so operators can see what
// the categories are actually made of.
type UsageItem struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"` // image, container or volume
	Bytes int64  `json:"bytes"`
}

// DiskUsageDetail computes the category report and the largest containers,
// volumes and images. The engine walks its stores for this call, so it is
// priced like `docker system df` — fine for a dashboard, not for a hot path.
func (c *Client) DiskUsageDetail(ctx context.Context) (DiskUsageReport, []UsageItem, error) {
	usage, err := c.cli.DiskUsage(ctx, types.DiskUsageOptions{})
	if err != nil {
		return DiskUsageReport{}, nil, fmt.Errorf("disk usage: %w", err)
	}

	var report DiskUsageReport
	report.Images = usage.LayersSize

	var top []UsageItem
	for _, item := range usage.Containers {
		if item == nil || item.SizeRw <= 0 {
			continue
		}
		report.Containers += item.SizeRw
		top = append(top, UsageItem{Name: firstName(item.Names), Kind: "container", Bytes: item.SizeRw})
	}
	for _, item := range usage.Volumes {
		if item == nil || item.UsageData == nil || item.UsageData.Size <= 0 {
			continue
		}
		report.Volumes += item.UsageData.Size
		top = append(top, UsageItem{Name: item.Name, Kind: "volume", Bytes: item.UsageData.Size})
	}
	for _, item := range usage.BuildCache {
		if item != nil {
			report.BuildCache += item.Size
		}
	}
	// Images only feed the largest-items list: per-image numbers overlap
	// (layers are shared), so they never enter the category report.
	for _, item := range usage.Images {
		if item == nil || item.Size <= 0 || len(item.RepoTags) == 0 {
			continue
		}
		top = append(top, UsageItem{Name: item.RepoTags[0], Kind: "image", Bytes: item.Size})
	}

	report.Total = report.Images + report.Containers + report.Volumes + report.BuildCache

	sort.SliceStable(top, func(i, j int) bool { return top[i].Bytes > top[j].Bytes })
	if len(top) > 8 {
		top = top[:8]
	}
	return report, top, nil
}

// pruneCandidate is one removal: tags of one image inside one repository, plus
// the bytes its image carries. Removing the tags untags them from that
// repository; the engine only frees layers once no reference remains.
type pruneCandidate struct {
	ID    string
	Tags  []string
	Bytes int64
}

// PruneRepoExcept removes every image of one repository except the keepTag
// reference (in-use images refuse deletion and are skipped). Returns an
// estimated reclaimed byte count and the number of images removed.
func (c *Client) PruneRepoExcept(ctx context.Context, repo string, keepTag string) (int64, int, error) {
	if repo == "" {
		return 0, 0, nil
	}
	list, err := c.cli.ImageList(ctx, image.ListOptions{All: true})
	if err != nil {
		return 0, 0, fmt.Errorf("list images: %w", err)
	}

	reclaimed, removed := c.removeCandidates(ctx, selectRepoExceptions(list, repo, keepTag), nil)
	return reclaimed, removed, nil
}

// selectRepoExceptions lists the images of one repository whose tags are all
// different from keepTag, together with the tags to remove. Pure and unit
// tested.
func selectRepoExceptions(images []image.Summary, repo string, keepTag string) []pruneCandidate {
	var victims []pruneCandidate
	for _, summary := range images {
		repoTags := make([]string, 0, len(summary.RepoTags))
		for _, tag := range summary.RepoTags {
			if repoOf(tag) == repo && tag != keepTag {
				repoTags = append(repoTags, tag)
			}
		}
		if len(summary.RepoTags) == 0 || len(repoTags) == 0 {
			continue
		}
		// Removing only this repository's tags is safe either way: references
		// outside it (other tags on the same image) keep the layers alive.
		victims = append(victims, pruneCandidate{ID: summary.ID, Tags: repoTags, Bytes: summary.Size})
	}
	return victims
}

// PruneManagedImages keeps the newest keep images of every application
// repository that still carries at least one tag (each build is tagged with a
// fresh stamp, so repositories otherwise grow forever) and removes the rest.
// A non-empty repo limits the sweep to that application repository. Only
// images stamped by this panel are considered, so operator-pulled images are
// never part of the sweep. Tags listed in protect always survive, and
// images a container uses refuse deletion — those failures are skipped, not
// fatal. Returns an estimated reclaimed byte count and the number of images
// removed.
func (c *Client) PruneManagedImages(ctx context.Context, repo string, keep int, protect map[string]struct{}) (int64, int, error) {
	if keep < 1 {
		keep = 1
	}
	list, err := c.cli.ImageList(ctx, image.ListOptions{All: true})
	if err != nil {
		return 0, 0, fmt.Errorf("list images: %w", err)
	}
	containers, err := c.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return 0, 0, fmt.Errorf("list containers: %w", err)
	}
	// Protect by image ID as well as tag: a dangling or retagged image can
	// still be the one a live or stopped container was created from.
	protectedIDs := make(map[string]struct{}, len(containers))
	for _, item := range containers {
		if item.ImageID != "" {
			protectedIDs[item.ImageID] = struct{}{}
		}
	}

	reclaimed, removed := c.removeCandidates(ctx, selectReposToPrune(selectManagedImages(list, repo), keep, protect), protectedIDs)
	return reclaimed, removed, nil
}

// selectManagedImages limits a retention sweep to images the panel built.
// Foreign images on the same engine (Traefik, Docker CLI, a developer's own
// builds) are deliberately excluded; the proxy and retired helper
// repositories have their own narrow sweeps. An empty repo means every
// application repository.
func selectManagedImages(images []image.Summary, repo string) []image.Summary {
	managed := make([]image.Summary, 0, len(images))
	for _, summary := range images {
		if summary.Labels[managedApplicationLabel] == "" {
			continue
		}
		if repo == "" || imageInRepo(summary, repo) {
			managed = append(managed, summary)
		}
	}
	return managed
}

func imageInRepo(summary image.Summary, repo string) bool {
	for _, tag := range summary.RepoTags {
		if repoOf(tag) == repo {
			return true
		}
	}
	return false
}

// removeCandidates deletes the tags of each victim. The engine refuses
// images referenced by any container; those candidates are left for a later
// pass, and protectedIDs catches the same case before the API call.
func (c *Client) removeCandidates(ctx context.Context, victims []pruneCandidate, protectedIDs map[string]struct{}) (int64, int) {
	reclaimed, removed := int64(0), 0
	for _, victim := range victims {
		if _, ok := protectedIDs[victim.ID]; ok {
			continue
		}
		failed := false
		for _, tag := range victim.Tags {
			if err := c.RemoveImage(ctx, tag); err != nil {
				// In use or engine refusal: leave the whole image for the
				// next sweep rather than half-removing it.
				failed = true
				break
			}
		}
		if failed {
			continue
		}
		removed++
		reclaimed += victim.Bytes
	}
	return reclaimed, removed
}

// selectReposToPrune decides which tags a retention sweep removes. Every
// repository groups its images newest first; each image past the keep count
// has its tags in that repository removed — unless any of the image's tags is
// protected. Pure and unit tested: fake summaries here, real engine only
// through the callers.
func selectReposToPrune(images []image.Summary, keep int, protect map[string]struct{}) []pruneCandidate {
	if keep < 1 {
		keep = 1
	}

	type resident struct {
		repo    string
		summary image.Summary
	}
	perRepo := map[string]map[string]resident{}
	for _, summary := range images {
		if len(summary.RepoTags) == 0 {
			continue // dangling layers are the job of PruneImages
		}
		for _, tag := range summary.RepoTags {
			repo := repoOf(tag)
			if repo == "" {
				continue
			}
			if perRepo[repo] == nil {
				perRepo[repo] = map[string]resident{}
			}
			perRepo[repo][summary.ID] = resident{repo: repo, summary: summary}
		}
	}

	var victims []pruneCandidate
	for _, residents := range perRepo {
		group := make([]resident, 0, len(residents))
		for _, entry := range residents {
			group = append(group, entry)
		}
		sort.SliceStable(group, func(i, j int) bool { return group[i].summary.Created > group[j].summary.Created })

		for index, entry := range group {
			if index < keep {
				continue
			}
			if protected(entry.summary, protect) {
				continue
			}
			candidate := pruneCandidate{ID: entry.summary.ID, Bytes: entry.summary.Size}
			// Untag every reference in this repository — an image tagged twice
			// under the same repo keeps layers alive through the second tag.
			for _, tag := range entry.summary.RepoTags {
				if repoOf(tag) == entry.repo {
					candidate.Tags = append(candidate.Tags, tag)
				}
			}
			victims = append(victims, candidate)
		}
	}
	return victims
}

// repoOf returns the repository part of a full image reference. The tag
// separator is the last colon beyond the last slash, so registries with ports
// (registry:5000/app:tag) keep their host in the repository name.
func repoOf(tag string) string {
	if tag == "" || tag == "<none>" {
		return ""
	}
	if colon := strings.LastIndex(tag, ":"); colon > strings.LastIndex(tag, "/") {
		return tag[:colon]
	}
	return tag
}

// RepoOf exports the repository/holder split of a full image reference for
// callers outside the package; see repoOf.
func RepoOf(tag string) string { return repoOf(tag) }

func protected(summary image.Summary, protect map[string]struct{}) bool {
	for _, tag := range summary.RepoTags {
		if _, ok := protect[tag]; ok {
			return true
		}
	}
	return false
}

// BuildCachePruneKeep prunes the build cache back to a ceiling, in bytes,
// keeping the most recently used records first. Engines without API 1.48 use
// the deprecated keep-storage parameter; both mean "target this much space".
func (c *Client) BuildCachePruneKeep(ctx context.Context, keep int64) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	options := build.CachePruneOptions{All: true}
	if c.apiVersionAtLeast("1.48") {
		options.MaxUsedSpace = keep
	} else {
		options.KeepStorage = keep
	}
	report, err := c.cli.BuildCachePrune(ctx, options)
	if err != nil {
		return 0, fmt.Errorf("prune build cache: %w", err)
	}
	return int64(report.SpaceReclaimed), nil
}

// apiVersionAtLeast compares the negotiated API version with a major.minor
// floor.
func (c *Client) apiVersionAtLeast(want string) bool {
	major, minor, ok := splitVersion(c.cli.ClientVersion())
	if !ok {
		return false
	}
	wMajor, wMinor, ok := splitVersion(want)
	if !ok {
		return false
	}
	return major > wMajor || (major == wMajor && minor >= wMinor)
}

func splitVersion(version string) (int, int, bool) {
	parts := strings.SplitN(strings.TrimPrefix(version, "v"), ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}
