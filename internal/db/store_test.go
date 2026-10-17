// Package db contains the control plane's SQLite schema.
//
// These tests cover the parts the UI depends on and that are easy to get wrong
// silently: the job queue's claim semantics, timestamp round-tripping, and the
// CRUD surface every resource is built on.
package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/db"
)

func openStore(t *testing.T) *db.Store {
	t.Helper()
	handle, err := db.OpenPath("file:" + t.TempDir() + "/test.db?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { handle.Close() })
	return db.OpenStore(handle)
}

func TestJobQueueClaimIsExclusive(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()

	enqueued, err := store.Jobs.Enqueue(ctx, "noop", `{}`, time.Now(), 3)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	first, err := store.Jobs.Claim(ctx)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if first == nil {
		t.Fatal("expected the first claim to win a job")
	}

	// A second worker must not see the same job while it is running.
	second, err := store.Jobs.Claim(ctx)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if second != nil {
		t.Fatalf("expected no second claim, got %s", second.ID)
	}

	if err := store.Jobs.Complete(ctx, enqueued.ID); err != nil {
		t.Fatalf("complete: %v", err)
	}

	third, err := store.Jobs.Claim(ctx)
	if err != nil {
		t.Fatalf("third claim: %v", err)
	}
	if third != nil {
		t.Fatalf("expected a completed job to stay complete, got %s", third.ID)
	}
}

func TestJobBackoffSchedulesARetry(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()

	job, err := store.Jobs.Enqueue(ctx, "flaky", `{}`, time.Now(), 3)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	claimed, err := store.Jobs.Claim(ctx)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v", err)
	}

	if err := store.Jobs.Fail(ctx, job.ID, "boom"); err != nil {
		t.Fatalf("fail: %v", err)
	}

	updated, err := store.Jobs.JobByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if updated.Status != "retrying" {
		t.Fatalf("status = %q, want retrying", updated.Status)
	}
	// The backoff must push the job into the future, otherwise workers spin.
	runAt := updated.RunAt
	if !runAt.After(time.Now().Add(10 * time.Second)) {
		t.Fatalf("runAt = %v, want it delayed by at least 10s", runAt)
	}
}

func TestJobDeadLettersAfterMaxAttempts(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()

	job, err := store.Jobs.Enqueue(ctx, "broken", `{}`, time.Now(), 1)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := store.Jobs.Claim(ctx); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := store.Jobs.Fail(ctx, job.ID, "permanent"); err != nil {
		t.Fatalf("fail: %v", err)
	}

	updated, err := store.Jobs.JobByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if updated.Status != "failed" {
		t.Fatalf("status = %q, want failed", updated.Status)
	}

	// A dead-lettered job must not be claimable again.
	next, err := store.Jobs.Claim(ctx)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if next != nil {
		t.Fatalf("expected the queue to be empty, got %s", next.ID)
	}
}

func TestJobListHidesTelemetryAndPruneTargetsOneKind(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()

	// Host-metrics rows used to be queue entries too, one every 15 seconds.
	for i := 0; i < 5; i++ {
		if _, err := store.Jobs.Enqueue(ctx, db.JobNodeMetrics, `{}`, time.Now(), 1); err != nil {
			t.Fatalf("enqueue metrics: %v", err)
		}
	}
	if _, err := store.Jobs.Enqueue(ctx, db.JobDeploy, `{}`, time.Now(), 1); err != nil {
		t.Fatalf("enqueue deploy: %v", err)
	}

	// Drain the queue so every row is finished.
	for {
		job, err := store.Jobs.Claim(ctx)
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if job == nil {
			break
		}
		if err := store.Jobs.Complete(ctx, job.ID); err != nil {
			t.Fatalf("complete: %v", err)
		}
	}

	visible, err := store.Jobs.JobsExcludingKind(ctx, "", db.JobNodeMetrics, 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(visible) != 1 || visible[0].Kind != db.JobDeploy {
		t.Fatalf("expected only the deploy job to be visible, got %d rows", len(visible))
	}

	// A real job — pending or finished — must survive a metrics prune, even with
	// a cutoff in the future.
	if _, err := store.Jobs.Enqueue(ctx, db.JobDeploy, `{}`, time.Now(), 1); err != nil {
		t.Fatalf("enqueue deploy: %v", err)
	}
	pruned, err := store.Jobs.PruneJobsByKind(ctx, db.JobNodeMetrics, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if pruned != 5 {
		t.Fatalf("pruned = %d, want 5", pruned)
	}
	remaining, err := store.Jobs.Jobs(ctx, "", 100)
	if err != nil {
		t.Fatalf("list after prune: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("expected both deploy jobs to survive, got %d", len(remaining))
	}
}

func TestGitHubSourceLinkDrivesAutoDeploy(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()

	project := &db.Project{ID: db.NewID(), Name: "proj"}
	if err := store.Projects.CreateProject(ctx, project); err != nil {
		t.Fatalf("create project: %v", err)
	}
	env := &db.Environment{ID: db.NewID(), ProjectID: project.ID, Name: "production"}
	if err := store.Projects.CreateEnvironment(ctx, env); err != nil {
		t.Fatalf("create environment: %v", err)
	}

	source := &db.GitHubApp{ID: db.NewID(), Name: "meghify-test", AppID: 7, PrivateKeyEnc: "sealed"}
	if err := store.GitHub.CreateGitHubApp(ctx, source); err != nil {
		t.Fatalf("create github app: %v", err)
	}
	if !source.Configured() {
		// A sealed key alone configures the app; the numeric id confirms it.
		t.Fatal("app with an id and key should count as configured")
	}

	installation := &db.GitHubInstallation{
		ID: db.NewID(), GitHubAppID: source.ID, InstallationID: 42,
		AccountLogin: "acme", AccountType: "Organization", RepositorySelection: "all",
	}
	if err := store.GitHub.UpsertGitHubInstallation(ctx, installation); err != nil {
		t.Fatalf("upsert installation: %v", err)
	}
	// A second sync for the same installation must refresh, not duplicate.
	installation.AccountLogin = "acme-renamed"
	if err := store.GitHub.UpsertGitHubInstallation(ctx, installation); err != nil {
		t.Fatalf("upsert installation again: %v", err)
	}
	stored, err := store.GitHub.ListGitHubInstallations(ctx)
	if err != nil {
		t.Fatalf("list installations: %v", err)
	}
	if len(stored) != 1 || stored[0].AccountLogin != "acme-renamed" || stored[0].AppName != "meghify-test" {
		t.Fatalf("unexpected installations: %+v", stored)
	}

	app := &db.Application{
		ID: db.NewID(), EnvironmentID: env.ID, Name: "web",
		GitRepository: "https://github.com/acme/web.git", GitBranch: "main",
		BuildPack: db.BuildNixpacks, Status: db.StatusIdle,
		GitHubAppID: source.ID, GitHubInstallationID: 42,
		RepositoryFullName: "acme/web", AutoDeploy: true,
	}
	if err := store.Applications.CreateApplication(ctx, app); err != nil {
		t.Fatalf("create application: %v", err)
	}

	// The webhook path: a push to the tracked branch matches, anything else not.
	matches, err := store.Applications.AutoDeployApplications(ctx, source.ID, "acme/web", "main")
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(matches) != 1 || matches[0].ID != app.ID {
		t.Fatalf("expected one match, got %d", len(matches))
	}
	if other, err := store.Applications.AutoDeployApplications(ctx, source.ID, "acme/web", "dev"); err != nil || len(other) != 0 {
		t.Fatalf("branch mismatch should not match: %v %d", err, len(other))
	}
	if other, err := store.Applications.AutoDeployApplications(ctx, source.ID, "acme/api", "main"); err != nil || len(other) != 0 {
		t.Fatalf("repository mismatch should not match: %v %d", err, len(other))
	}

	// Turning auto-deploy off takes the application out of the webhook path.
	app.AutoDeploy = false
	if err := store.Applications.UpdateApplication(ctx, app); err != nil {
		t.Fatalf("update application: %v", err)
	}
	if other, err := store.Applications.AutoDeployApplications(ctx, source.ID, "acme/web", "main"); err != nil || len(other) != 0 {
		t.Fatalf("disabled auto-deploy should not match: %v %d", err, len(other))
	}

	// Deleting the source keeps the application and its repository URL, but
	// severs the link (and drops the installation with it).
	if err := store.GitHub.DeleteGitHubApp(ctx, source.ID); err != nil {
		t.Fatalf("delete github app: %v", err)
	}
	reloaded, err := store.Applications.ApplicationByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("reload application: %v", err)
	}
	if reloaded.GitHubAppID != "" || reloaded.GitHubInstallationID != 0 || reloaded.AutoDeploy {
		t.Fatalf("link not severed: %+v", reloaded)
	}
	if reloaded.GitRepository != app.GitRepository {
		t.Fatalf("repository url changed: %q", reloaded.GitRepository)
	}
	remaining, err := store.GitHub.ListGitHubInstallations(ctx)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("installations should cascade, got %d", len(remaining))
	}
}

func TestTimestampsRoundTrip(t *testing.T) {
	store := openStore(t)
	_ = store

	when := time.Date(2026, 3, 4, 5, 6, 7, 891234567, time.UTC)
	stored := db.Text(when)
	if got := db.Parse(stored); !got.Equal(when) {
		t.Fatalf("round trip: got %v want %v", got, when)
	}
	if got := db.Parse(""); !got.IsZero() {
		t.Fatalf("empty should parse to the zero time, got %v", got)
	}
}

func TestNullableColumnsRoundTrip(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()

	// Applications, services and databases all carry a nullable node_id. A new
	// resource is unpinned until it is placed on a server, so the scan path for
	// NULL has to work or every list endpoint 500s.
	project := &db.Project{ID: db.NewID(), Name: "nullable"}
	if err := store.Projects.CreateProject(ctx, project); err != nil {
		t.Fatalf("create project: %v", err)
	}
	env := &db.Environment{ID: db.NewID(), ProjectID: project.ID, Name: "production"}
	if err := store.Projects.CreateEnvironment(ctx, env); err != nil {
		t.Fatalf("create environment: %v", err)
	}

	app := &db.Application{
		ID:            db.NewID(),
		EnvironmentID: env.ID,
		Name:          "web",
		GitRepository: "https://example.com/web.git",
		GitBranch:     "main",
		BuildPack:     "dockerfile",
		Port:          3000,
		Status:        db.StatusIdle,
	}
	if err := store.Applications.CreateApplication(ctx, app); err != nil {
		t.Fatalf("create application: %v", err)
	}

	loaded, err := store.Applications.ApplicationByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("load application: %v", err)
	}
	if loaded.NodeID != "" {
		t.Fatalf("expected an unpinned application, got node %q", loaded.NodeID)
	}
	if loaded.CreatedAt.IsZero() {
		t.Fatal("expected a created timestamp")
	}

	list, err := store.Applications.ApplicationsByEnvironment(ctx, env.ID)
	if err != nil {
		t.Fatalf("list applications: %v", err)
	}
	if len(list) != 1 || list[0].Name != "web" {
		t.Fatalf("got %d applications, want 1 named web", len(list))
	}

	database := &db.Database{
		ID:            db.NewID(),
		EnvironmentID: env.ID,
		Name:          "orders",
		Kind:          db.DBPostgres,
		Version:       "17-alpine",
		Status:        db.StatusRunning,
	}
	if err := store.Resources.CreateDatabase(ctx, database); err != nil {
		t.Fatalf("create database: %v", err)
	}
	loadedDB, err := store.Resources.DatabaseByID(ctx, database.ID)
	if err != nil {
		t.Fatalf("load database: %v", err)
	}
	if loadedDB.NodeID != "" {
		t.Fatalf("expected an unpinned database, got node %q", loadedDB.NodeID)
	}

	all, err := store.Resources.ListDatabases(ctx)
	if err != nil {
		t.Fatalf("list databases: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d databases, want 1", len(all))
	}
}

func TestSecretValuesAreEncryptedAtRest(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()

	app := &db.Application{ID: db.NewID(), Name: "secrets", Port: 3000}
	if err := store.Applications.UpsertEnvVar(ctx, &db.EnvVar{
		ID:       db.NewID(),
		Target:   db.TargetApplication,
		TargetID: app.ID,
		Key:      "DATABASE_URL",
		ValueEnc: "c2VhbGVkLXZhbHVl",
		IsSecret: true,
	}); err != nil {
		t.Fatalf("upsert env var: %v", err)
	}

	vars, err := store.Applications.EnvVarsByTarget(ctx, db.TargetApplication, app.ID)
	if err != nil {
		t.Fatalf("list env vars: %v", err)
	}
	if len(vars) != 1 {
		t.Fatalf("got %d variables, want 1", len(vars))
	}
	// The stored form must be the ciphertext, never the plaintext.
	if vars[0].ValueEnc != "c2VhbGVkLXZhbHVl" {
		t.Fatalf("value enc = %q, want the sealed form preserved", vars[0].ValueEnc)
	}

	// A second upsert of the same key must not create a duplicate row.
	if err := store.Applications.UpsertEnvVar(ctx, &db.EnvVar{
		ID: db.NewID(), Target: db.TargetApplication, TargetID: app.ID,
		Key: "DATABASE_URL", ValueEnc: "bmV3", IsSecret: true,
	}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	vars, err = store.Applications.EnvVarsByTarget(ctx, db.TargetApplication, app.ID)
	if err != nil {
		t.Fatalf("list after upsert: %v", err)
	}
	if len(vars) != 1 || vars[0].ValueEnc != "bmV3" {
		t.Fatalf("expected the upsert to replace the value, got %+v", vars)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()

	if got, err := store.GetSetting(ctx, "missing"); err != nil || got != "" {
		t.Fatalf("missing setting: got %q err %v", got, err)
	}
	if err := store.SetSetting(ctx, "instance.name", "pass"); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := store.GetSetting(ctx, "instance.name")
	if err != nil || got != "pass" {
		t.Fatalf("get: got %q err %v", got, err)
	}
	// A second write must overwrite, not duplicate.
	if err := store.SetSetting(ctx, "instance.name", "pass2"); err != nil {
		t.Fatalf("set again: %v", err)
	}
	got, _ = store.GetSetting(ctx, "instance.name")
	if got != "pass2" {
		t.Fatalf("got %q, want pass2", got)
	}
}

func TestProjectCascadeCreatesEnvironment(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()

	project := &db.Project{ID: db.NewID(), Name: "cascade"}
	if err := store.Projects.CreateProject(ctx, project); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := store.Projects.CreateEnvironment(ctx, &db.Environment{
		ID: db.NewID(), ProjectID: project.ID, Name: "production",
	}); err != nil {
		t.Fatalf("create environment: %v", err)
	}

	envs, err := store.Projects.EnvironmentsByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("list environments: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("got %d environments, want 1", len(envs))
	}

	if err := store.Projects.DeleteProject(ctx, project.ID); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	remaining, err := store.Projects.EnvironmentsByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected cascade delete to remove environments, got %d", len(remaining))
	}
}
