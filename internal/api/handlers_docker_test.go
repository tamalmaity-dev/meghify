package api

import (
	"testing"
	"time"
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
