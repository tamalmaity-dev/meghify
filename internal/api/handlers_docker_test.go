package api

import (
	"testing"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/db"
)

func TestDockerUsageCacheFresh(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

	if dockerUsageCacheFresh(time.Time{}, now) {
		t.Fatal("an unset measurement must not be fresh")
	}
	if dockerUsageCacheFresh(now.Add(-dockerUsageCacheTTL/2), now) != true {
		t.Fatal("a measurement inside the TTL should be reused")
	}
	if dockerUsageCacheFresh(now.Add(-dockerUsageCacheTTL), now) {
		t.Fatal("a measurement at the TTL boundary should be refreshed")
	}
	if dockerUsageCacheFresh(now.Add(-dockerUsageCacheTTL-time.Second), now) {
		t.Fatal("an old measurement should be refreshed")
	}
}

func TestValidateDockerCleanupSettings(t *testing.T) {
	valid := db.DefaultDockerCleanupSettings()
	if err := validateDockerCleanupSettings(&valid); err != nil {
		t.Fatalf("default settings rejected: %v", err)
	}

	empty := db.DockerCleanupSettings{}
	if err := validateDockerCleanupSettings(&empty); err != nil {
		t.Fatalf("empty settings should fall back to defaults: %v", err)
	}
	if empty.Schedule != "0 0 * * *" || empty.Trigger != db.CleanupTriggerSchedule ||
		empty.ApplicationImages != db.CleanupImagesKeep {
		t.Fatalf("defaults were not filled: %+v", empty)
	}

	tests := []struct {
		name   string
		mutate func(*db.DockerCleanupSettings)
	}{
		{"invalid cron", func(s *db.DockerCleanupSettings) { s.Schedule = "not a schedule" }},
		{"invalid trigger", func(s *db.DockerCleanupSettings) { s.Trigger = "always" }},
		{"threshold too low", func(s *db.DockerCleanupSettings) { s.ThresholdPercent = -1 }},
		{"threshold too high", func(s *db.DockerCleanupSettings) { s.ThresholdPercent = 100 }},
		{"invalid image mode", func(s *db.DockerCleanupSettings) { s.ApplicationImages = "delete" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := db.DefaultDockerCleanupSettings()
			test.mutate(&settings)
			if err := validateDockerCleanupSettings(&settings); err == nil {
				t.Fatalf("expected %s to be rejected", test.name)
			}
		})
	}
}
