package resourceguard

import (
	"testing"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/docker"
)

func TestThresholdStreakRequiresContinuousDurationAndFiresOnce(t *testing.T) {
	start := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	var streak thresholdStreak

	if streak.observe(start, true, 10*time.Minute) {
		t.Fatal("fired immediately")
	}
	if streak.observe(start.Add(5*time.Minute), true, 10*time.Minute) {
		t.Fatal("fired before the duration elapsed")
	}
	if !streak.observe(start.Add(10*time.Minute), true, 10*time.Minute) {
		t.Fatal("did not fire after the duration elapsed")
	}
	if streak.observe(start.Add(11*time.Minute), true, 10*time.Minute) {
		t.Fatal("fired twice for the same incident")
	}
	if streak.observe(start.Add(12*time.Minute), false, 10*time.Minute) {
		t.Fatal("a below-threshold sample must not fire")
	}
	if streak.observe(start.Add(30*time.Minute), true, 10*time.Minute) {
		t.Fatal("a new incident fired before its own duration elapsed")
	}
	if !streak.observe(start.Add(40*time.Minute), true, 10*time.Minute) {
		t.Fatal("a new incident did not fire after its own duration")
	}
}

func TestThresholdStreakResetsOnDip(t *testing.T) {
	start := time.Now().UTC()
	var streak thresholdStreak
	streak.observe(start, true, 5*time.Minute)
	streak.observe(start.Add(3*time.Minute), false, 5*time.Minute)
	if streak.since != (time.Time{}) || streak.fired {
		t.Fatal("a dip did not reset the streak")
	}
	if streak.observe(start.Add(6*time.Minute), true, 5*time.Minute) {
		t.Fatal("reset incident fired from the old start time")
	}
}

func TestEffectiveActionPrefersPerAppOverride(t *testing.T) {
	if got := effectiveAction(db.GuardActionStop, db.GuardActionThrottle); got != db.GuardActionStop {
		t.Fatalf("override = %q", got)
	}
	if got := effectiveAction("", db.GuardActionThrottle); got != db.GuardActionThrottle {
		t.Fatalf("inherited = %q", got)
	}
	if got := effectiveAction("bad", ""); got != db.GuardActionNotify {
		t.Fatalf("fallback = %q", got)
	}
}

func TestThrottleCoresHonoursHostAndApplicationLimit(t *testing.T) {
	if got := throttleCores(4, 50, 0); got != 2 {
		t.Fatalf("4 CPUs at 50%% = %v, want 2", got)
	}
	if got := throttleCores(4, 50, 0.5); got != 0.5 {
		t.Fatalf("existing lower limit = %v, want 0.5", got)
	}
	if got := throttleCores(0, 50, 0); got != 0.5 {
		t.Fatalf("unknown CPU count = %v, want 0.5", got)
	}
	if got := throttleCores(4, 50, 0.001); got != 0.001 {
		t.Fatalf("tiny existing limit = %v, want 0.001", got)
	}
}

func TestLargestMemorySkipsOptedOutGraceAndStopped(t *testing.T) {
	now := time.Now().UTC()
	old := &docker.ContainerInfo{ID: "old", Created: now.Add(-time.Hour).Unix()}
	targets := []*target{
		{app: &db.Application{ID: "opted-out", ResourceGuardEnabled: false}, container: old,
			usage: &docker.ContainerUsage{MemoryBytes: 4 << 30}},
		{app: &db.Application{ID: "grace", ResourceGuardEnabled: true}, container: &docker.ContainerInfo{ID: "new", Created: now.Unix()},
			usage: &docker.ContainerUsage{MemoryBytes: 3 << 30}},
		{app: &db.Application{ID: "stopped", ResourceGuardEnabled: true}, container: old, stopped: true,
			usage: &docker.ContainerUsage{MemoryBytes: 2 << 30}},
		{app: &db.Application{ID: "running", ResourceGuardEnabled: true}, container: old,
			usage: &docker.ContainerUsage{MemoryBytes: 1 << 30}},
	}
	got := largestMemory(targets, now, 300)
	if got == nil || got.app.ID != "running" {
		t.Fatalf("candidate = %+v, want the running protected application", got)
	}
}

func TestStateForCarriesCooldownAcrossContainerReplacement(t *testing.T) {
	guard := New(Options{})
	first := &target{
		app:       &db.Application{ID: "app-1"},
		container: &docker.ContainerInfo{ID: "container-1"},
	}
	state := guard.stateFor(first)
	actionAt := time.Now().Add(-5 * time.Minute)
	state.LastAction = actionAt

	second := &target{
		app:       &db.Application{ID: "app-1"},
		container: &docker.ContainerInfo{ID: "container-2"},
	}
	next := guard.stateFor(second)
	if next.ContainerID != "container-2" {
		t.Fatalf("container id = %q", next.ContainerID)
	}
	if !next.LastAction.Equal(actionAt) {
		t.Fatal("cooldown timestamp was not carried across a redeploy")
	}

	guard.ResetApplication("app-1")
	if _, ok := guard.apps["app-1"]; ok {
		t.Fatal("ResetApplication did not clear the state")
	}
}

func TestResetAllClearsStates(t *testing.T) {
	guard := New(Options{})
	guard.apps["app"] = &appState{ContainerID: "container"}
	guard.nodes["node"] = &nodeState{}
	guard.ResetAll()
	if len(guard.apps) != 0 || len(guard.nodes) != 0 {
		t.Fatal("ResetAll left cached state behind")
	}
}

func TestPercentOfAndDurationMessages(t *testing.T) {
	if got := percentOf(512, 1024); got != 50 {
		t.Fatalf("percentOf = %v", got)
	}
	if got := percentOf(1, 0); got != 0 {
		t.Fatalf("zero total = %v", got)
	}
	if got := humanDuration(600); got != "10 minutes" {
		t.Fatalf("humanDuration = %q", got)
	}
	if got := humanDuration(3600); got != "1 hour" {
		t.Fatalf("humanDuration hour = %q", got)
	}
}
