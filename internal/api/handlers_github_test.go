package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tamalmaity-dev/meghify/internal/github"
)

// The repository picker reads camelCase fields; GitHub's payload is
// snake_case. This guards the translation so repository names never render
// blank in the picker again.
func TestGitHubRepositoryListView(t *testing.T) {
	list := &github.RepositoryList{
		TotalCount: 2,
		Repositories: []github.Repository{
			{
				ID: 5, FullName: "acme/web", Private: true, DefaultBranch: "main",
				CloneURL:  "https://github.com/acme/web.git",
				HTMLURL:   "https://github.com/acme/web",
				UpdatedAt: "2026-01-01T00:00:00Z",
			},
			{ID: 6, FullName: "acme/api"},
		},
	}
	blob, err := json.Marshal(gitHubRepositoryListViewOf(list))
	if err != nil {
		t.Fatal(err)
	}
	got := string(blob)
	for _, want := range []string{
		`"totalCount":2`,
		`"fullName":"acme/web"`,
		`"defaultBranch":"main"`,
		`"cloneUrl":"https://github.com/acme/web.git"`,
		`"private":true`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("response missing %s: %s", want, got)
		}
	}
	if strings.Contains(got, "full_name") || strings.Contains(got, "clone_url") {
		t.Fatalf("github wire field names leaked to the browser: %s", got)
	}

	empty, err := json.Marshal(gitHubRepositoryListViewOf(&github.RepositoryList{}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(empty), `"repositories":[]`) {
		t.Fatalf("empty list should serialize as [], got %s", empty)
	}
}
