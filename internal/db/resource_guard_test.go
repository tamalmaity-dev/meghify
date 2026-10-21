package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/db"
)

func TestResourceGuardSettingsRoundTrip(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()

	settings, err := store.ResourceGuard.Settings(ctx)
	if err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if !settings.Enabled || settings.CPUAction != db.GuardActionThrottle ||
		settings.MemoryAction != db.GuardActionStop || settings.CPUPercent != 90 {
		t.Fatalf("unexpected defaults: %+v", settings)
	}

	settings.Enabled = false
	settings.CPUAction = db.GuardActionStop
	settings.CPUPercent = 75
	settings.CPUThrottlePercent = 40
	settings.MemoryPercent = 80
	settings.HostMemoryPercent = 92
	settings.GraceSeconds = 120
	settings.CooldownSeconds = 60
	if err := store.ResourceGuard.SaveSettings(ctx, settings); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := store.ResourceGuard.Settings(ctx)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if loaded.Enabled || loaded.CPUAction != db.GuardActionStop ||
		loaded.CPUPercent != 75 || loaded.CPUThrottlePercent != 40 ||
		loaded.MemoryPercent != 80 || loaded.HostMemoryPercent != 92 ||
		loaded.GraceSeconds != 120 || loaded.CooldownSeconds != 60 {
		t.Fatalf("round trip mismatch: %+v", loaded)
	}
	if loaded.UpdatedAt.IsZero() {
		t.Fatal("updatedAt was not populated")
	}
}

func TestResourceGuardEventsAreNewestFirstAndFilterable(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	for i, appID := range []string{"app-a", "app-b", "app-a"} {
		event := &db.ResourceGuardEvent{
			ID:               db.NewID(),
			ApplicationID:    appID,
			ApplicationName:  appID,
			NodeID:           db.LocalNodeID,
			Metric:           db.GuardMetricCPU,
			Action:           db.GuardActionNotify,
			Status:           db.GuardStatusSuccess,
			ObservedPercent:  91.5,
			ThresholdPercent: 90,
			DurationSeconds:  600,
			Message:          "test",
			CreatedAt:        now.Add(time.Duration(i) * time.Second),
		}
		if err := store.ResourceGuard.AddEvent(ctx, event); err != nil {
			t.Fatalf("add event %d: %v", i, err)
		}
	}

	all, err := store.ResourceGuard.Events(ctx, "", 10)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 3 || all[0].ApplicationID != "app-a" {
		t.Fatalf("unexpected order: %+v", all)
	}

	filtered, err := store.ResourceGuard.Events(ctx, "app-a", 10)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("filtered length = %d, want 2", len(filtered))
	}
}

func TestResourceGuardEventPrune(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	old := &db.ResourceGuardEvent{
		ID: db.NewID(), ApplicationID: "old", Metric: db.GuardMetricMemory,
		Action: db.GuardActionStop, Status: db.GuardStatusSuccess,
		CreatedAt: time.Now().UTC().Add(-100 * 24 * time.Hour),
	}
	recent := &db.ResourceGuardEvent{
		ID: db.NewID(), ApplicationID: "recent", Metric: db.GuardMetricMemory,
		Action: db.GuardActionStop, Status: db.GuardStatusSuccess,
		CreatedAt: time.Now().UTC(),
	}
	for _, event := range []*db.ResourceGuardEvent{old, recent} {
		if err := store.ResourceGuard.AddEvent(ctx, event); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	if _, err := store.ResourceGuard.PruneEvents(ctx, time.Now().AddDate(0, 0, -90)); err != nil {
		t.Fatalf("prune: %v", err)
	}
	events, err := store.ResourceGuard.Events(ctx, "", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 1 || events[0].ApplicationID != "recent" {
		t.Fatalf("events after prune = %+v", events)
	}
}

func TestApplicationResourceGuardDefaultsAndRoundTrip(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()

	project := &db.Project{ID: db.NewID(), Name: "guard"}
	if err := store.Projects.CreateProject(ctx, project); err != nil {
		t.Fatalf("create project: %v", err)
	}
	env := &db.Environment{ID: db.NewID(), ProjectID: project.ID, Name: "production"}
	if err := store.Projects.CreateEnvironment(ctx, env); err != nil {
		t.Fatalf("create environment: %v", err)
	}
	app := &db.Application{
		ID:                   db.NewID(),
		EnvironmentID:        env.ID,
		Name:                 "api",
		Port:                 3000,
		Status:               db.StatusIdle,
		ResourceGuardEnabled: true,
	}
	if err := store.Applications.CreateApplication(ctx, app); err != nil {
		t.Fatalf("create application: %v", err)
	}

	loaded, err := store.Applications.ApplicationByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !loaded.ResourceGuardEnabled {
		t.Fatal("new applications must be protected by default")
	}
	if loaded.ResourceGuardCPUAction != "" || loaded.ResourceGuardMemoryAction != "" {
		t.Fatal("new applications should inherit the panel actions")
	}

	loaded.ResourceGuardEnabled = false
	loaded.ResourceGuardCPUAction = db.GuardActionStop
	loaded.ResourceGuardMemoryAction = db.GuardActionNotify
	loaded.ResourceGuardCPUPercent = 80
	loaded.ResourceGuardMemoryPercent = 70
	if err := store.Applications.UpdateApplication(ctx, loaded); err != nil {
		t.Fatalf("update: %v", err)
	}
	reloaded, err := store.Applications.ApplicationByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.ResourceGuardEnabled || reloaded.ResourceGuardCPUAction != db.GuardActionStop ||
		reloaded.ResourceGuardMemoryAction != db.GuardActionNotify ||
		reloaded.ResourceGuardCPUPercent != 80 || reloaded.ResourceGuardMemoryPercent != 70 {
		t.Fatalf("guard override round trip mismatch: %+v", reloaded)
	}
}
