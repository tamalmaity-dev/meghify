// Package github talks to GitHub's REST API on behalf of the panel.
//
// One Client represents one GitHub host — github.com, or a GitHub Enterprise
// Server instance. Everything the panel does against GitHub goes through here:
// exchanging a manifest code, listing installations, minting installation
// tokens and fetching repository lists. Pointing a client at a stub server is
// how this package is tested.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the public GitHub host.
	DefaultBaseURL = "https://github.com"
	// DefaultAPIURL is the public REST endpoint. GitHub Enterprise Server
	// serves its API under <base>/api/v3 instead.
	DefaultAPIURL = "https://api.github.com"

	apiVersion = "2022-11-28"
	userAgent  = "meghify"
)

// Client is a REST client for one GitHub host.
type Client struct {
	BaseURL string
	APIURL  string
	HTTP    *http.Client
}

// NewClient builds a client for a host. An empty or github.com base URL maps to
// the public API; anything else is treated as GitHub Enterprise Server.
func NewClient(baseURL string) *Client {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	api := DefaultAPIURL
	if !strings.EqualFold(base, DefaultBaseURL) {
		api = base + "/api/v3"
	}
	return &Client{BaseURL: base, APIURL: api, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// AppCredentials is what GitHub returns when a manifest code is exchanged for
// the app it created.
type AppCredentials struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	PEM           string `json:"pem"`
	WebhookSecret string `json:"webhook_secret"`
	HTMLURL       string `json:"html_url"`
}

// Installation is one account that installed the app.
type Installation struct {
	ID                  int64  `json:"id"`
	RepositorySelection string `json:"repository_selection"`
	Account             struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"account"`
}

// Repository is one repository an installation can see.
type Repository struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	Archived      bool   `json:"archived"`
	DefaultBranch string `json:"default_branch"`
	CloneURL      string `json:"clone_url"`
	HTMLURL       string `json:"html_url"`
	UpdatedAt     string `json:"updated_at"`
}

// RepositoryList is GitHub's paginated repository payload.
type RepositoryList struct {
	TotalCount   int          `json:"total_count"`
	Repositories []Repository `json:"repositories"`
}

// APIError is a non-2xx GitHub response, carrying GitHub's own message.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	message := e.Body
	var parsed struct {
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(e.Body), &parsed) == nil && parsed.Message != "" {
		message = parsed.Message
	}
	return fmt.Sprintf("github responded %d: %s", e.Status, message)
}

// ParseManifest exchanges the temporary code GitHub appends to the manifest
// redirect for the credentials of the app it just created.
func (c *Client) ParseManifest(ctx context.Context, code string) (*AppCredentials, error) {
	var out AppCredentials
	endpoint := c.APIURL + "/app-manifests/" + url.PathEscape(code) + "/conversions"
	if err := c.do(ctx, http.MethodPost, endpoint, "", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListInstallations returns every account that installed the app.
func (c *Client) ListInstallations(ctx context.Context, jwt string) ([]Installation, error) {
	var out []Installation
	if err := c.do(ctx, http.MethodGet, c.APIURL+"/app/installations?per_page=100", jwt, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetInstallation fetches one installation by id.
func (c *Client) GetInstallation(ctx context.Context, jwt string, installationID int64) (*Installation, error) {
	var out Installation
	endpoint := fmt.Sprintf("%s/app/installations/%d", c.APIURL, installationID)
	if err := c.do(ctx, http.MethodGet, endpoint, jwt, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListRepositories lists the repositories an installation can see, newest
// first. GitHub caps one page at 100 entries; TotalCount carries the full count.
func (c *Client) ListRepositories(ctx context.Context, token string) (*RepositoryList, error) {
	var out RepositoryList
	if err := c.do(ctx, http.MethodGet,
		c.APIURL+"/installation/repositories?per_page=100&sort=updated", token, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// do performs one JSON request. token is either an app JWT or an installation
// token; an empty token sends the request anonymously (manifest conversion).
func (c *Client) do(ctx context.Context, method, endpoint, token string, body, dst any) error {
	var reader io.Reader
	if body != nil {
		blob, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(blob)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &APIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(payload))}
	}
	if dst == nil {
		return nil
	}
	if err := json.Unmarshal(payload, dst); err != nil {
		return fmt.Errorf("decode github response: %w", err)
	}
	return nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}
