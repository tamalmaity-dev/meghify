package deploy

import (
	"context"
	"testing"
	"time"
)

func TestSandboxIdleExpired(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	if sandboxIdleExpired(time.Time{}, now, 15*time.Minute) {
		t.Fatal("an unused sandbox timestamp must not expire")
	}
	if sandboxIdleExpired(now.Add(-14*time.Minute), now, 15*time.Minute) {
		t.Fatal("a recent build must keep the sandbox alive")
	}
	if !sandboxIdleExpired(now.Add(-15*time.Minute), now, 15*time.Minute) {
		t.Fatal("a sandbox at the idle boundary should stop")
	}
}

func TestStopIdleSandboxesDisabled(t *testing.T) {
	p := &Pipeline{}
	stopped, err := p.StopIdleSandboxes(context.Background(), 0)
	if err != nil || stopped != 0 {
		t.Fatalf("disabled sweep = (%d, %v), want (0, nil)", stopped, err)
	}
}
