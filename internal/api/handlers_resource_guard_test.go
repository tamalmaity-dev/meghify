package api

import (
	"testing"

	"github.com/tamalmaity-dev/meghify/internal/db"
)

func TestValidateResourceGuardSettings(t *testing.T) {
	good := db.DefaultResourceGuardSettings()
	if err := validateResourceGuardSettings(&good); err != nil {
		t.Fatalf("defaults should be valid: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*db.ResourceGuardSettings)
	}{
		{"bad cpu action", func(s *db.ResourceGuardSettings) { s.CPUAction = "restart" }},
		{"throttle above threshold", func(s *db.ResourceGuardSettings) {
			s.CPUAction = db.GuardActionThrottle
			s.CPUThrottlePercent = s.CPUPercent
		}},
		{"cpu threshold out of range", func(s *db.ResourceGuardSettings) { s.CPUPercent = 101 }},
		{"zero cpu duration", func(s *db.ResourceGuardSettings) { s.CPUDurationSeconds = 1 }},
		{"memory throttle unsupported", func(s *db.ResourceGuardSettings) { s.MemoryAction = db.GuardActionThrottle }},
		{"memory threshold out of range", func(s *db.ResourceGuardSettings) { s.MemoryPercent = 101 }},
		{"negative grace", func(s *db.ResourceGuardSettings) { s.GraceSeconds = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := db.DefaultResourceGuardSettings()
			tc.mutate(&settings)
			if err := validateResourceGuardSettings(&settings); err == nil {
				t.Fatalf("expected a validation error for %s", tc.name)
			}
		})
	}
}

func TestValidateGuardActions(t *testing.T) {
	for _, action := range []string{"", db.GuardActionNotify, db.GuardActionThrottle, db.GuardActionStop} {
		if err := validateGuardCPUAction(action); err != nil {
			t.Fatalf("CPU action %q: %v", action, err)
		}
	}
	if err := validateGuardCPUAction("restart"); err == nil {
		t.Fatal("unexpected CPU action accepted")
	}
	for _, action := range []string{"", db.GuardActionNotify, db.GuardActionStop} {
		if err := validateGuardMemoryAction(action); err != nil {
			t.Fatalf("memory action %q: %v", action, err)
		}
	}
	if err := validateGuardMemoryAction(db.GuardActionThrottle); err == nil {
		t.Fatal("memory throttle should not be accepted")
	}
}
