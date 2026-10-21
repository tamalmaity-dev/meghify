package main

import (
	"context"
	"testing"

	"github.com/tamalmaity-dev/meghify/internal/db"
)

func TestScheduleDockerCleanupEnqueuesOnceAndHonoursDisabled(t *testing.T) {
	handle, err := db.OpenPath("file:" + t.TempDir() + "/test.db?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	store := db.OpenStore(handle)
	ctx := context.Background()

	settings, err := store.DockerCleanup.Settings(ctx)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	settings.Enabled = true
	settings.Schedule = "* * * * *"
	if err := store.DockerCleanup.SaveSettings(ctx, settings); err != nil {
		t.Fatalf("save: %v", err)
	}

	scheduleDockerCleanup(ctx, store, nil)
	jobs, err := store.Jobs.Jobs(ctx, db.JobPending, 10)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Kind != db.JobDockerCleanup {
		t.Fatalf("jobs = %+v, want one docker_cleanup", jobs)
	}

	// The minute tick runs repeatedly while a cleanup is pending; the unique
	// enqueue must collapse those ticks into one job.
	scheduleDockerCleanup(ctx, store, nil)
	jobs, err = store.Jobs.Jobs(ctx, db.JobPending, 10)
	if err != nil {
		t.Fatalf("list jobs after second tick: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("second tick created a duplicate: %+v", jobs)
	}

	if err := store.Jobs.Complete(ctx, jobs[0].ID); err != nil {
		t.Fatalf("complete: %v", err)
	}
	settings.Enabled = false
	if err := store.DockerCleanup.SaveSettings(ctx, settings); err != nil {
		t.Fatalf("disable: %v", err)
	}
	scheduleDockerCleanup(ctx, store, nil)
	jobs, err = store.Jobs.Jobs(ctx, db.JobPending, 10)
	if err != nil {
		t.Fatalf("list jobs after disable: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("disabled schedule still enqueued a job: %+v", jobs)
	}
}
