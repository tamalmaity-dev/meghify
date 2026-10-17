package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tamalmaity-dev/meghify/internal/auth"
	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/github"
)

// --- sources CRUD ---

// gitHubAppView adds the computed "configured" flag to a stored app. Secrets
// never leave the API: the model marks them json:"-".
type gitHubAppView struct {
	*db.GitHubApp
	Configured bool `json:"configured"`
}

func gitHubAppViewOf(app *db.GitHubApp) *gitHubAppView {
	return &gitHubAppView{GitHubApp: app, Configured: app.Configured()}
}

func (s *Server) listGitHubApps(w http.ResponseWriter, r *http.Request) {
	apps, err := s.store.GitHub.ListGitHubApps(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]*gitHubAppView, 0, len(apps))
	for _, app := range apps {
		views = append(views, gitHubAppViewOf(app))
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) getGitHubApp(w http.ResponseWriter, r *http.Request) {
	app, err := s.store.GitHub.GitHubAppByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		mapError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gitHubAppViewOf(app))
}

type gitHubAppRequest struct {
	Name         string `json:"name"`
	Organization string `json:"organization"`
	BaseURL      string `json:"baseUrl"`
}

// createGitHubApp registers a placeholder source. The credentials arrive later,
// either from GitHub's manifest callback or from the manual setup form.
func (s *Server) createGitHubApp(w http.ResponseWriter, r *http.Request) {
	var req gitHubAppRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "app name is required")
		return
	}
	if len(name) > 60 {
		writeError(w, http.StatusBadRequest, "app name is too long")
		return
	}

	state, err := auth.RandomToken(24)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	app := &db.GitHubApp{
		ID:           db.NewID(),
		Name:         name,
		Organization: strings.TrimSpace(req.Organization),
		BaseURL:      strings.TrimRight(strings.TrimSpace(req.BaseURL), "/"),
		State:        state,
	}
	if err := s.store.GitHub.CreateGitHubApp(r.Context(), app); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r.Context(), r, "github.app.create", "github_app", app.ID, map[string]any{"name": app.Name})
	writeJSON(w, http.StatusCreated, gitHubAppViewOf(app))
}

func (s *Server) deleteGitHubApp(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.store.GitHub.DeleteGitHubApp(r.Context(), id); err != nil {
		mapError(w, err)
		return
	}
	s.audit(r.Context(), r, "github.app.delete", "github_app", id, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// gitHubAppManifest builds the registration payload the browser POSTs to
// GitHub. It is also the payload the "Register With GitHub" button submits.
func (s *Server) gitHubAppManifest(w http.ResponseWriter, r *http.Request) {
	app, err := s.store.GitHub.GitHubAppByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		mapError(w, err)
		return
	}
	if app.Configured() {
		writeError(w, http.StatusConflict, "this GitHub App is already registered")
		return
	}

	// The state ties the callback back to this row; it survives a page reload
	// so the user can pick the flow back up after leaving.
	if app.State == "" {
		state, err := auth.RandomToken(24)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		app.State = state
		if err := s.store.GitHub.UpdateGitHubApp(r.Context(), app); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	base := app.BaseURL
	if base == "" {
		base = github.DefaultBaseURL
	}
	panel := strings.TrimRight(s.cfg.PublicURL, "/")
	webhookURL := panel + "/api/webhooks/github"

	action := base + "/settings/apps/new"
	if app.Organization != "" {
		action = fmt.Sprintf("%s/organizations/%s/settings/apps/new", base, url.PathEscape(app.Organization))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"postUrl":    action + "?state=" + url.QueryEscape(app.State),
		"manifest":   github.BuildManifest(panel, app.Name, webhookURL),
		"webhookUrl": webhookURL,
		"panelUrl":   panel,
	})
}

type gitHubCredentialsRequest struct {
	AppID         int64  `json:"appId"`
	Slug          string `json:"slug"`
	PrivateKey    string `json:"privateKey"`
	WebhookSecret string `json:"webhookSecret"`
	ClientID      string `json:"clientId"`
	ClientSecret  string `json:"clientSecret"`
}

// setGitHubAppCredentials stores manually created app credentials — the
// fallback when the automated manifest flow cannot be used.
func (s *Server) setGitHubAppCredentials(w http.ResponseWriter, r *http.Request) {
	app, err := s.store.GitHub.GitHubAppByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		mapError(w, err)
		return
	}

	var req gitHubCredentialsRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.AppID <= 0 {
		writeError(w, http.StatusBadRequest, "app id is required")
		return
	}
	if strings.TrimSpace(req.PrivateKey) == "" || strings.TrimSpace(req.WebhookSecret) == "" {
		writeError(w, http.StatusBadRequest, "private key and webhook secret are required")
		return
	}
	// Prove the key parses now, so a paste error surfaces here instead of on the
	// first deploy or repository listing.
	if _, err := github.SignAppJWT(req.AppID, req.PrivateKey, time.Now()); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	seal := func(value string) (string, error) {
		if strings.TrimSpace(value) == "" {
			return "", nil
		}
		return s.auth.Seal(value)
	}
	if app.PrivateKeyEnc, err = seal(req.PrivateKey); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if app.WebhookSecretEnc, err = seal(req.WebhookSecret); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if app.ClientSecretEnc, err = seal(req.ClientSecret); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	app.AppID = req.AppID
	app.ClientID = strings.TrimSpace(req.ClientID)
	app.Slug = strings.TrimSpace(req.Slug)
	app.State = ""
	if app.Slug != "" {
		base := app.BaseURL
		if base == "" {
			base = github.DefaultBaseURL
		}
		app.HTMLURL = base + "/apps/" + app.Slug
	}

	if err := s.store.GitHub.UpdateGitHubApp(r.Context(), app); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r.Context(), r, "github.app.credentials", "github_app", app.ID, nil)
	writeJSON(w, http.StatusOK, gitHubAppViewOf(app))
}

// --- manifest flow callbacks ---

// gitHubCallback is where GitHub redirects the browser after the user confirms
// a manifest. It exchanges the temporary code for the app credentials and then
// bounces the browser back to the Sources page.
func (s *Server) gitHubCallback(w http.ResponseWriter, r *http.Request) {
	redirect := func(query string) {
		http.Redirect(w, r, strings.TrimRight(s.cfg.PublicURL, "/")+"/sources"+query, http.StatusFound)
	}

	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" {
		redirect("?github=error&message=" + url.QueryEscape("GitHub did not return a setup code"))
		return
	}

	app, err := s.store.GitHub.GitHubAppByState(r.Context(), state)
	if err != nil {
		redirect("?github=error&message=" + url.QueryEscape("That setup link has expired — start again from Sources"))
		return
	}

	creds, err := github.NewClient(app.BaseURL).ParseManifest(r.Context(), code)
	if err != nil {
		s.logger.Warn("github manifest exchange failed", "app", app.ID, "error", err)
		redirect("?github=error&message=" + url.QueryEscape(err.Error()))
		return
	}

	seal := func(value string) (string, error) {
		if value == "" {
			return "", nil
		}
		return s.auth.Seal(value)
	}
	if app.PrivateKeyEnc, err = seal(creds.PEM); err != nil {
		redirect("?github=error&message=" + url.QueryEscape(err.Error()))
		return
	}
	if app.WebhookSecretEnc, err = seal(creds.WebhookSecret); err != nil {
		redirect("?github=error&message=" + url.QueryEscape(err.Error()))
		return
	}
	if app.ClientSecretEnc, err = seal(creds.ClientSecret); err != nil {
		redirect("?github=error&message=" + url.QueryEscape(err.Error()))
		return
	}

	app.AppID = creds.ID
	app.Slug = creds.Slug
	app.ClientID = creds.ClientID
	app.HTMLURL = creds.HTMLURL
	app.State = ""
	if err := s.store.GitHub.UpdateGitHubApp(r.Context(), app); err != nil {
		redirect("?github=error&message=" + url.QueryEscape(err.Error()))
		return
	}

	s.audit(r.Context(), r, "github.app.register", "github_app", app.ID, nil)
	redirect("?github=connected&message=" + url.QueryEscape(app.Name))
}

// gitHubSetup receives the browser after the user installs the app on an
// account. It resolves which source the installation belongs to and records it.
func (s *Server) gitHubSetup(w http.ResponseWriter, r *http.Request) {
	redirect := func(query string) {
		http.Redirect(w, r, strings.TrimRight(s.cfg.PublicURL, "/")+"/sources"+query, http.StatusFound)
	}

	installationID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("installation_id")), 10, 64)
	if err != nil || installationID == 0 {
		redirect("?github=error&message=" + url.QueryEscape("GitHub did not return an installation id"))
		return
	}

	app, installation, err := s.installationOwner(r.Context(), r.URL.Query().Get("state"), installationID)
	if err != nil {
		s.logger.Warn("github setup: no app matched the installation", "installation", installationID, "error", err)
		redirect("?github=error&message=" + url.QueryEscape(err.Error()))
		return
	}

	if err := s.store.GitHub.UpsertGitHubInstallation(r.Context(), &db.GitHubInstallation{
		ID:                  db.NewID(),
		GitHubAppID:         app.ID,
		InstallationID:      installation.ID,
		AccountLogin:        installation.Account.Login,
		AccountType:         installation.Account.Type,
		RepositorySelection: installation.RepositorySelection,
	}); err != nil {
		redirect("?github=error&message=" + url.QueryEscape(err.Error()))
		return
	}

	redirect("?installed=" + url.QueryEscape(installation.Account.Login))
}

// installationOwner finds the configured app that manages an installation. The
// panel's install links carry the source's id as the state parameter, so a
// direct lookup resolves the common case; the manifest token and probing every
// configured app cover installations started from GitHub's own "Install" pages.
func (s *Server) installationOwner(ctx context.Context, state string, installationID int64) (*db.GitHubApp, *github.Installation, error) {
	if state != "" {
		if app, err := s.store.GitHub.GitHubAppByID(ctx, state); err == nil && app.Configured() {
			if installation, err := s.installationDetail(ctx, app, installationID); err == nil {
				return app, installation, nil
			}
		}
	}
	if app, err := s.store.GitHub.GitHubAppByState(ctx, state); err == nil && app.Configured() {
		if installation, err := s.installationDetail(ctx, app, installationID); err == nil {
			return app, installation, nil
		}
	}

	apps, err := s.store.GitHub.ListGitHubApps(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, app := range apps {
		if !app.Configured() {
			continue
		}
		if installation, err := s.installationDetail(ctx, app, installationID); err == nil {
			return app, installation, nil
		}
	}
	return nil, nil, errors.New("no configured GitHub App manages this installation yet")
}

// installationDetail asks GitHub for one installation using the app's own
// credentials, which doubles as proof that the installation belongs to it.
func (s *Server) installationDetail(ctx context.Context, app *db.GitHubApp, installationID int64) (*github.Installation, error) {
	key, err := s.auth.Open(app.PrivateKeyEnc)
	if err != nil {
		return nil, err
	}
	jwt, err := github.SignAppJWT(app.AppID, key, time.Now())
	if err != nil {
		return nil, err
	}
	return github.NewClient(app.BaseURL).GetInstallation(ctx, jwt, installationID)
}

// --- installations and repositories ---

func (s *Server) listGitHubInstallations(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.GitHub.ListGitHubInstallations(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// syncGitHubInstallations refreshes every configured app's installation list
// from GitHub. It also picks up installations created before the panel was
// listening (for example when the user installs straight from GitHub).
func (s *Server) syncGitHubInstallations(w http.ResponseWriter, r *http.Request) {
	apps, err := s.store.GitHub.ListGitHubApps(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, app := range apps {
		if !app.Configured() {
			continue
		}
		if err := s.syncAppInstallations(r.Context(), app); err != nil {
			s.logger.Warn("github installation sync failed", "app", app.Name, "error", err)
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	}
	s.listGitHubInstallations(w, r)
}

// syncGitHubAppInstallations refreshes one app and returns its installations.
func (s *Server) syncGitHubAppInstallations(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	app, err := s.store.GitHub.GitHubAppByID(r.Context(), id)
	if err != nil {
		mapError(w, err)
		return
	}
	if !app.Configured() {
		writeError(w, http.StatusConflict, "finish setting this GitHub App up first")
		return
	}
	if err := s.syncAppInstallations(r.Context(), app); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	all, err := s.store.GitHub.ListGitHubInstallations(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	mine := make([]*db.GitHubInstallation, 0, len(all))
	for _, installation := range all {
		if installation.GitHubAppID == id {
			mine = append(mine, installation)
		}
	}
	writeJSON(w, http.StatusOK, mine)
}

func (s *Server) syncAppInstallations(ctx context.Context, app *db.GitHubApp) error {
	key, err := s.auth.Open(app.PrivateKeyEnc)
	if err != nil {
		return err
	}
	jwt, err := github.SignAppJWT(app.AppID, key, time.Now())
	if err != nil {
		return err
	}
	installations, err := github.NewClient(app.BaseURL).ListInstallations(ctx, jwt)
	if err != nil {
		return err
	}
	for _, installation := range installations {
		if err := s.store.GitHub.UpsertGitHubInstallation(ctx, &db.GitHubInstallation{
			ID:                  db.NewID(),
			GitHubAppID:         app.ID,
			InstallationID:      installation.ID,
			AccountLogin:        installation.Account.Login,
			AccountType:         installation.Account.Type,
			RepositorySelection: installation.RepositorySelection,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) listGitHubRepositories(w http.ResponseWriter, r *http.Request) {
	installationID, err := strconv.ParseInt(chi.URLParam(r, "installationId"), 10, 64)
	if err != nil || installationID == 0 {
		writeError(w, http.StatusBadRequest, "invalid installation id")
		return
	}

	installation, err := s.store.GitHub.GitHubInstallationByInstallationID(r.Context(), installationID)
	if err != nil {
		mapError(w, err)
		return
	}
	app, err := s.store.GitHub.GitHubAppByID(r.Context(), installation.GitHubAppID)
	if err != nil {
		mapError(w, err)
		return
	}

	key, err := s.auth.Open(app.PrivateKeyEnc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jwt, err := github.SignAppJWT(app.AppID, key, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	client := github.NewClient(app.BaseURL)
	token, err := client.InstallationToken(r.Context(), jwt, installationID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	repositories, err := client.ListRepositories(r.Context(), token)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, repositories)
}

// --- webhook ---

// gitHubWebhook receives GitHub App deliveries. It authenticates with the
// X-Hub-Signature-256 header — an HMAC of the raw body keyed with the app's
// webhook secret — instead of a session cookie, so it lives outside the
// authenticated API group.
func (s *Server) gitHubWebhook(w http.ResponseWriter, r *http.Request) {
	event := r.Header.Get("X-GitHub-Event")
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read payload")
		return
	}

	// Every app webhook names the app it was delivered for; without it there is
	// nothing to verify against.
	targetID, _ := strconv.ParseInt(strings.TrimSpace(r.Header.Get("X-GitHub-Hook-Installation-Target-ID")), 10, 64)
	if targetID == 0 {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "reason": "not an app webhook"})
		return
	}

	app, err := s.store.GitHub.GitHubAppByAppID(r.Context(), targetID)
	if err != nil {
		// A delivery for an app this panel does not know: acknowledge so GitHub
		// stops retrying, but do nothing.
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "reason": "unknown app"})
		return
	}

	secret, err := s.auth.Open(app.WebhookSecretEnc)
	if err != nil || !github.VerifyWebhookSignature(secret, body, r.Header.Get("X-Hub-Signature-256")) {
		writeError(w, http.StatusUnauthorized, "invalid webhook signature")
		return
	}

	switch event {
	case "ping":
		writeJSON(w, http.StatusOK, map[string]string{"status": "pong"})
	case "push":
		s.handleGitHubPush(w, r, app, body)
	case "installation":
		s.handleGitHubInstallationEvent(w, r, app, body)
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "event": event})
	}
}

type gitHubPushEvent struct {
	Ref   string `json:"ref"`
	After string `json:"after"`
	Repo  struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	HeadCommit struct {
		Message string `json:"message"`
	} `json:"head_commit"`
}

// handleGitHubPush redeploys every application that tracks the pushed branch.
func (s *Server) handleGitHubPush(w http.ResponseWriter, r *http.Request, app *db.GitHubApp, body []byte) {
	var event gitHubPushEvent
	if err := json.Unmarshal(body, &event); err != nil {
		writeError(w, http.StatusBadRequest, "could not parse push payload")
		return
	}

	branch := strings.TrimPrefix(event.Ref, "refs/heads/")
	if branch == event.Ref {
		// Tag or other ref: nothing tracks it.
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "queued": 0})
		return
	}

	applications, err := s.store.Applications.AutoDeployApplications(r.Context(), app.ID, event.Repo.FullName, branch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	queued := []string{}
	for _, application := range applications {
		message := firstLine(event.HeadCommit.Message)
		_, err := s.queueDeployment(r.Context(), application, db.TriggerWebhook, false, event.After, message)
		switch {
		case errors.Is(err, errDeploymentRunning):
			// A build is already running; the next push will redeploy.
		case err != nil:
			s.logger.Error("webhook deploy failed", "application", application.Name, "error", err)
		default:
			queued = append(queued, application.Name)
		}
	}

	if len(queued) > 0 {
		s.logger.Info("github push queued deployments",
			"repository", event.Repo.FullName, "branch", branch, "applications", queued)
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "queued": len(queued)})
}

type gitHubInstallationEvent struct {
	Action       string `json:"action"`
	Installation struct {
		ID                  int64  `json:"id"`
		RepositorySelection string `json:"repository_selection"`
		Account             struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
	} `json:"installation"`
}

// handleGitHubInstallationEvent keeps the stored installation list in step when
// an account installs, uninstalls or reconfigures the app directly on GitHub.
func (s *Server) handleGitHubInstallationEvent(w http.ResponseWriter, r *http.Request, app *db.GitHubApp, body []byte) {
	var event gitHubInstallationEvent
	if err := json.Unmarshal(body, &event); err != nil {
		writeError(w, http.StatusBadRequest, "could not parse installation payload")
		return
	}

	switch event.Action {
	case "created", "new_permissions_accepted":
		err := s.store.GitHub.UpsertGitHubInstallation(r.Context(), &db.GitHubInstallation{
			ID:                  db.NewID(),
			GitHubAppID:         app.ID,
			InstallationID:      event.Installation.ID,
			AccountLogin:        event.Installation.Account.Login,
			AccountType:         event.Installation.Account.Type,
			RepositorySelection: event.Installation.RepositorySelection,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	case "deleted":
		if err := s.store.GitHub.DeleteGitHubInstallation(r.Context(), event.Installation.ID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// firstLine keeps a commit message from flooding a deployment row.
func firstLine(message string) string {
	message = strings.TrimSpace(message)
	if index := strings.IndexByte(message, '\n'); index >= 0 {
		message = message[:index]
	}
	if len(message) > 200 {
		message = message[:200]
	}
	return message
}
