package docker

import (
	"testing"
	"time"

	"github.com/docker/docker/api/types/image"
)

func summary(id string, repo string, tag string, created time.Time, size int64) image.Summary {
	return image.Summary{
		ID:       id,
		RepoTags: []string{repo + ":" + tag},
		Created:  int64(created.Unix()),
		Size:     size,
	}
}

// Each build tags a fresh stamp, so the repository grows one image per build:
// keep must retain the newest and drop older ones, oldest first.
func TestSelectReposToPruneKeepsNewest(t *testing.T) {
	base := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	images := []image.Summary{
		summary("id-old", "meghify.local/app", "abcd1234-100", base.Add(-2*time.Hour), 900<<20),
		summary("id-mid", "meghify.local/app", "abcd1234-200", base.Add(-time.Hour), 900<<20),
		summary("id-new", "meghify.local/app", "abcd1234-300", base, 900<<20),
	}

	victims := selectReposToPrune(images, 2, nil)
	if len(victims) != 1 || victims[0].Tags[0] != "meghify.local/app:abcd1234-100" {
		t.Fatalf("victims = %+v, want only the oldest tag", victims)
	}
	if victims[0].Bytes != 900<<20 {
		t.Errorf("victim bytes = %d, want the image size", victims[0].Bytes)
	}
}

// Protected tags never die, even when they fall outside the keep window: a
// running container still serves from them. A protected image is also skipped
// (not a victim) when the keep window has already passed over it.
func TestSelectReposToPruneProtectsTags(t *testing.T) {
	base := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	images := []image.Summary{
		summary("id-old", "meghify.local/app", "x-100", base.Add(-8*time.Hour), 1),
		summary("id-serving", "meghify.local/app", "y-200", base.Add(-5*time.Hour), 2),
		summary("id-new", "meghify.local/app", "z-300", base, 3),
	}
	protect := map[string]struct{}{"meghify.local/app:y-200": {}}

	victims := selectReposToPrune(images, 2, protect)
	if len(victims) != 1 || victims[0].Tags[0] != "meghify.local/app:x-100" {
		t.Fatalf("victims = %+v, want only x-100 (y-200 protected, z-300 inside the window)", victims)
	}

	// With keep=1 the newest image owns the window; the protected middle image
	// must still not become a victim, while the oldest one does.
	victims = selectReposToPrune(images, 1, protect)
	if len(victims) != 1 || victims[0].Tags[0] != "meghify.local/app:x-100" {
		t.Fatalf("victims = %+v, want only x-100 again", victims)
	}
}

// Dangling images carry no tags and belong to a dedicated prune, never the
// retention sweep.
func TestSelectReposToPruneIgnoresDangling(t *testing.T) {
	base := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	images := []image.Summary{
		{ID: "id-dangling", Created: int64(base.Add(-time.Hour).Unix()), Size: 42},
		summary("id-one", "meghify.local/app", "a-1", base, 1),
	}
	if victims := selectReposToPrune(images, 1, nil); len(victims) != 0 {
		t.Fatalf("victims = %+v, want none", victims)
	}
}

// Two tags on one image inside one repository remove together; tags across
// different repositories are decided per repository.
func TestSelectReposToPruneGroupsByRepository(t *testing.T) {
	base := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	double := summary("id-double", "meghify.local/app", "t-1", base, 7)
	double.RepoTags = []string{"meghify.local/app:t-1", "meghify.local/app:t-1b"}

	images := []image.Summary{
		double,
		summary("id-old", "meghify.local/app", "t-0", base.Add(-time.Hour), 5),
	}

	victims := selectReposToPrune(images, 1, nil)
	if len(victims) != 1 {
		t.Fatalf("victims = %+v, want the single old image", victims)
	}
	if len(victims[0].Tags) != 1 || victims[0].Tags[0] != "meghify.local/app:t-0" {
		t.Fatalf("victim tags = %v, want the old tag only", victims[0].Tags)
	}
}

// Repositories keep separate retention: trimming one application leaves the
// other's images alone when they are within its own keep window.
func TestSelectReposToPruneSeparatesApplications(t *testing.T) {
	base := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	images := []image.Summary{
		summary("id-a-old", "meghify.local/app-a", "s-1", base.Add(-time.Hour), 1),
		summary("id-b-old", "meghify.local/app-b", "s-1", base.Add(-time.Hour), 2),
		summary("id-b-new", "meghify.local/app-b", "s-2", base, 3),
	}

	victims := selectReposToPrune(images, 1, nil)
	if len(victims) != 1 {
		t.Fatalf("victims = %+v, want exactly one", victims)
	}
	// app-a's only image is inside its own keep window; app-b's older one is
	// not.
	if victims[0].Tags[0] != "meghify.local/app-b:s-1" {
		t.Fatalf("victim tag = %v, want app-b's old image", victims[0].Tags)
	}
}

// A registry with a port keeps its host in the repository name.
func TestRepoOfWithPortedRegistry(t *testing.T) {
	cases := map[string]string{
		"meghify.local/app:tag-1":              "meghify.local/app",
		"registry.example:5000/team/app:tag-1": "registry.example:5000/team/app",
		"app":                                  "app",
		"meghify.local/app":                    "meghify.local/app",
		"":                                     "",
		"<none>":                               "",
	}
	for tag, want := range cases {
		if got := repoOf(tag); got != want {
			t.Errorf("repoOf(%q) = %q, want %q", tag, got, want)
		}
	}
}

func TestSplitVersion(t *testing.T) {
	if major, minor, ok := splitVersion("1.51"); !ok || major != 1 || minor != 51 {
		t.Fatalf("splitVersion(1.51) = %d/%d/%t", major, minor, ok)
	}
	if _, _, ok := splitVersion("v1.48"); !ok {
		t.Fatal("leading v should be tolerated")
	}
	if _, _, ok := splitVersion("garbage"); ok {
		t.Fatal("garbage must not parse")
	}
}

// Every release leaves its predecessor's helper image behind; a cleanup pass
// keeps only the helper the running panel actually uses.
func TestSelectRepoExceptionsKeepsCurrentHelper(t *testing.T) {
	base := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	images := []image.Summary{
		summary("id-v1", "ghcr.io/tamalmaity-dev/meghify-helper", "v0.6.1", base.Add(-2*time.Hour), 276<<20),
		summary("id-v2", "ghcr.io/tamalmaity-dev/meghify-helper", "v0.6.2", base.Add(-time.Hour), 276<<20),
		summary("id-v3", "ghcr.io/tamalmaity-dev/meghify-helper", "v0.6.3", base, 276<<20),
	}

	victims := selectRepoExceptions(images, "ghcr.io/tamalmaity-dev/meghify-helper", "ghcr.io/tamalmaity-dev/meghify-helper:v0.6.3")
	if len(victims) != 2 {
		t.Fatalf("victims = %+v, want the two old helpers", victims)
	}
	for _, victim := range victims {
		if victim.Tags[0] == "ghcr.io/tamalmaity-dev/meghify-helper:v0.6.3" {
			t.Fatalf("current helper %v must survive", victim.Tags)
		}
		if victim.Bytes != 276<<20 {
			t.Errorf("victim bytes = %d, want the image size", victim.Bytes)
		}
	}
}

// Before v0.3.1 the panel used Coolify's helper image. Cleanup removes its
// tags too, while still refusing anything a container references.
func TestSelectRepoExceptionsFindsRetiredCoolifyHelper(t *testing.T) {
	base := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	images := []image.Summary{
		summary("id-coolify", "coollabsio/coolify-helper", "1.0.17", base.Add(-48*time.Hour), 621<<20),
		summary("id-helper", "ghcr.io/tamalmaity-dev/meghify-helper", "v0.7.0", base, 276<<20),
		summary("id-app", "meghify.local/roopsa-web", "bf50f017-1", base, 2<<30),
	}

	victims := selectRepoExceptions(images, "coollabsio/coolify-helper", "")
	if len(victims) != 1 || victims[0].Tags[0] != "coollabsio/coolify-helper:1.0.17" {
		t.Fatalf("victims = %+v, want only the retired Coolify helper", victims)
	}
}

// The helper sweep only touches the helper repository: application release
// images, the sandbox image and other registries are out of scope.
func TestSelectRepoExceptionsScopedToRepository(t *testing.T) {
	base := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	images := []image.Summary{
		summary("id-helper", "ghcr.io/tamalmaity-dev/meghify-helper", "v0.6.1", base, 9),
		summary("id-app", "meghify.local/roopsa-web", "bf50f017-1", base, 2<<30),
		summary("id-dind", "docker", "29-dind", base, 441<<20),
		{ID: "id-dangling", Created: int64(base.Unix()), Size: 7},
	}

	if victims := selectRepoExceptions(images, "ghcr.io/tamalmaity-dev/meghify-helper", "ghcr.io/tamalmaity-dev/meghify-helper:v0.6.3"); len(victims) != 1 || victims[0].Tags[0] != "ghcr.io/tamalmaity-dev/meghify-helper:v0.6.1" {
		t.Fatalf("victims = %+v, want only the stray old helper", victims)
	}
}

// An image tagged with the kept reference and an extra tag in the same
// repository loses only the extra tag; the kept reference holds the layers.
func TestSelectRepoExceptionsUntanglesSharedImage(t *testing.T) {
	base := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	shared := summary("id-shared", "ghcr.io/tamalmaity-dev/meghify-helper", "v0.6.3", base, 5)
	shared.RepoTags = []string{
		"ghcr.io/tamalmaity-dev/meghify-helper:v0.6.3",
		"ghcr.io/tamalmaity-dev/meghify-helper:latest",
	}

	victims := selectRepoExceptions([]image.Summary{shared}, "ghcr.io/tamalmaity-dev/meghify-helper", "ghcr.io/tamalmaity-dev/meghify-helper:v0.6.3")
	if len(victims) != 1 || len(victims[0].Tags) != 1 || victims[0].Tags[0] != "ghcr.io/tamalmaity-dev/meghify-helper:latest" {
		t.Fatalf("victims = %+v, want only the :latest tag removed", victims)
	}
}
