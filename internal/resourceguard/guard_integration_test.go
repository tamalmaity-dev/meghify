package resourceguard

import (
	"context"
	"testing"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/deploy"
	"github.com/tamalmaity-dev/meghify/internal/docker"
)

type fakeEngine struct {
	containers []*docker.ContainerInfo
	usage      *docker.ContainerUsage
	stopped    bool
	updated    bool
	cpu        float64
	memory     int64
}

func (f *fakeEngine) ListContainers(context.Context, string, string) ([]*docker.ContainerInfo, error) {
	return f.containers, nil
}

func (f *fakeEngine) ContainerUsage(context.Context, string) (*docker.ContainerUsage, error) {
	return f.usage, nil
}

func (f *fakeEngine) UpdateContainerResources(_ context.Context, _ string, cpuLimit float64, memoryLimit int64) error {
	f.updated = true
	f.cpu = cpuLimit
	f.memory = memoryLimit
	return nil
}

func (f *fakeEngine) StopContainer(context.Context, string, time.Duration) error {
	f.stopped = true
	return nil
}

func guardTestApp(t *testing.T, store *db.Store, memoryLimit int64) *db.Application {
	t.Helper()
	ctx := context.Background()
	project := &db.Project{ID: db.NewID(), Name: "guard"}
	if err := store.Projects.CreateProject(ctx, project); err != nil {
		t.Fatalf("project: %v", err)
	}
	env := &db.Environment{ID: db.NewID(), ProjectID: project.ID, Name: "production"}
	if err := store.Projects.CreateEnvironment(ctx, env); err != nil {
		t.Fatalf("environment: %v", err)
	}
	app := &db.Application{
		ID:                   db.NewID(),
		EnvironmentID:        env.ID,
		Name:                 "worker",
		Port:                 3000,
		Status:               db.StatusRunning,
		MemoryLimit:          memoryLimit,
		ResourceGuardEnabled: true,
	}
	if err := store.Applications.CreateApplication(ctx, app); err != nil {
		t.Fatalf("application: %v", err)
	}
	return app
}

func guardTestStore(t *testing.T) *db.Store {
	t.Helper()
	handle, err := db.OpenPath("file:" + t.TempDir() + "/guard.db?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	store := db.OpenStore(handle)
	ctx := context.Background()
	if err := store.Nodes.EnsureLocalNode(ctx); err != nil {
		t.Fatalf("local node: %v", err)
	}
	if err := store.Nodes.UpdateLocalNodeFacts(ctx, "", "", "", "", 4, 8<<30, nil); err != nil {
		t.Fatalf("node facts: %v", err)
	}
	if err := store.Nodes.InsertMetric(ctx, &db.NodeMetrics{
		ID:         db.NewID(),
		NodeID:     db.LocalNodeID,
		MemUsed:    1 << 30,
		MemTotal:   8 << 30,
		RecordedAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("metric: %v", err)
	}
	return store
}

func runGuardTwice(t *testing.T, guard *Guard, now *time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := guard.Run(ctx); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	*now = now.Add(2 * time.Second)
	if err := guard.Run(ctx); err != nil {
		t.Fatalf("second pass: %v", err)
	}
}

func TestGuardStopsOnSustainedMemory(t *testing.T) {
	store := guardTestStore(t)
	app := guardTestApp(t, store, 1000)
	now := time.Now().UTC()
	engine := &fakeEngine{
		containers: []*docker.ContainerInfo{{
			ID:      "container-1",
			Name:    docker.ManagedName(deploy.ContainerNameByID(app.ID)),
			State:   "running",
			Created: now.Add(-time.Hour).Unix(),
		}},
		usage: &docker.ContainerUsage{MemoryBytes: 950, MemoryLimit: 1000},
	}
	settings, err := store.ResourceGuard.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	settings.Enabled = true
	settings.CPUEnabled = false
	settings.MemoryEnabled = true
	settings.MemoryPercent = 90
	settings.MemoryDurationSeconds = 1
	settings.MemoryAction = db.GuardActionStop
	settings.HostMemoryEnabled = false
	settings.GraceSeconds = 0
	settings.CooldownSeconds = 0
	if err := store.ResourceGuard.SaveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}

	guard := New(Options{
		Store: store,
		Resolve: func(context.Context, string) (Engine, error) {
			return engine, nil
		},
		Now: func() time.Time { return now },
	})
	runGuardTwice(t, guard, &now)
	if !engine.stopped {
		t.Fatal("memory guard did not stop the container")
	}
	loaded, err := store.Applications.ApplicationByID(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != db.StatusStopped {
		t.Fatalf("application status = %q, want stopped", loaded.Status)
	}
	events, err := store.ResourceGuard.Events(context.Background(), app.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Metric != db.GuardMetricMemory ||
		events[0].Action != db.GuardActionStop || events[0].Status != db.GuardStatusSuccess {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestGuardStopsLargestAppOnHostMemoryEmergency(t *testing.T) {
	store := guardTestStore(t)
	app := guardTestApp(t, store, 0)
	now := time.Now().UTC()
	engine := &fakeEngine{
		containers: []*docker.ContainerInfo{{
			ID:      "container-1",
			Name:    docker.ManagedName(deploy.ContainerNameByID(app.ID)),
			State:   "running",
			Created: now.Add(-time.Hour).Unix(),
		}},
		usage: &docker.ContainerUsage{MemoryBytes: 7 << 30},
	}
	// The host is at 97.5% (7.8 GiB of 8 GiB), above the emergency threshold.
	if err := store.Nodes.InsertMetric(context.Background(), &db.NodeMetrics{
		ID:         db.NewID(),
		NodeID:     db.LocalNodeID,
		MemUsed:    (7 << 30) + (800 << 20),
		MemTotal:   8 << 30,
		RecordedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	settings, err := store.ResourceGuard.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	settings.Enabled = true
	settings.CPUEnabled = false
	settings.MemoryEnabled = false
	settings.HostMemoryEnabled = true
	settings.HostMemoryPercent = 95
	settings.HostMemoryDurationSecs = 1
	settings.GraceSeconds = 0
	settings.CooldownSeconds = 0
	if err := store.ResourceGuard.SaveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}

	guard := New(Options{
		Store: store,
		Resolve: func(context.Context, string) (Engine, error) {
			return engine, nil
		},
		Now: func() time.Time { return now },
	})
	runGuardTwice(t, guard, &now)
	if !engine.stopped {
		t.Fatal("host memory emergency did not stop the largest application")
	}
	events, err := store.ResourceGuard.Events(context.Background(), app.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Metric != db.GuardMetricHostMemory ||
		events[0].Action != db.GuardActionStop || events[0].Status != db.GuardStatusSuccess {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestGuardThrottlesSustainedCPU(t *testing.T) {
	store := guardTestStore(t)
	app := guardTestApp(t, store, 0)
	now := time.Now().UTC()
	engine := &fakeEngine{
		containers: []*docker.ContainerInfo{{
			ID:      "container-1",
			Name:    docker.ManagedName(deploy.ContainerNameByID(app.ID)),
			State:   "running",
			Created: now.Add(-time.Hour).Unix(),
		}},
		usage: &docker.ContainerUsage{CPUPercent: 20},
	}
	settings, err := store.ResourceGuard.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	settings.Enabled = true
	settings.CPUEnabled = true
	settings.CPUPercent = 10
	settings.CPUDurationSeconds = 1
	settings.CPUAction = db.GuardActionThrottle
	settings.CPUThrottlePercent = 5
	settings.MemoryEnabled = false
	settings.HostMemoryEnabled = false
	settings.GraceSeconds = 0
	settings.CooldownSeconds = 0
	if err := store.ResourceGuard.SaveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}

	guard := New(Options{
		Store: store,
		Resolve: func(context.Context, string) (Engine, error) {
			return engine, nil
		},
		Now: func() time.Time { return now },
	})
	runGuardTwice(t, guard, &now)
	if !engine.updated {
		t.Fatal("CPU guard did not update the container")
	}
	if engine.cpu != 0.2 {
		t.Fatalf("throttle CPU = %v, want 0.2 cores (5%% of 4 CPUs)", engine.cpu)
	}
	events, err := store.ResourceGuard.Events(context.Background(), app.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Metric != db.GuardMetricCPU ||
		events[0].Action != db.GuardActionThrottle || events[0].Status != db.GuardStatusSuccess {
		t.Fatalf("unexpected events: %+v", events)
	}
}
