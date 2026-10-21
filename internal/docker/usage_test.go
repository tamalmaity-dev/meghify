package docker

import (
	"testing"

	"github.com/docker/docker/api/types/container"
)

func TestUsageFromStats(t *testing.T) {
	stats := &container.StatsResponse{}
	stats.CPUStats.CPUUsage.TotalUsage = 1_500
	stats.PreCPUStats.CPUUsage.TotalUsage = 1_000
	stats.CPUStats.SystemUsage = 11_000
	stats.PreCPUStats.SystemUsage = 10_000
	stats.MemoryStats.Usage = 700
	stats.MemoryStats.Limit = 1_024
	stats.MemoryStats.Stats = map[string]uint64{"inactive_file": 200}

	usage := usageFromStats(stats)
	if usage.CPUPercent != 50 {
		t.Fatalf("CPUPercent = %v, want 50", usage.CPUPercent)
	}
	if usage.MemoryBytes != 500 {
		t.Fatalf("MemoryBytes = %d, want 500", usage.MemoryBytes)
	}
	if usage.MemoryLimit != 1_024 {
		t.Fatalf("MemoryLimit = %d, want 1024", usage.MemoryLimit)
	}
}

func TestUsageFromStatsUsesCGroupsV1CacheFallback(t *testing.T) {
	stats := &container.StatsResponse{}
	stats.MemoryStats.Usage = 900
	stats.MemoryStats.Stats = map[string]uint64{"cache": 400}
	usage := usageFromStats(stats)
	if usage.MemoryBytes != 500 {
		t.Fatalf("MemoryBytes = %d, want 500", usage.MemoryBytes)
	}
}

func TestUsageFromStatsIgnoresMissingOrNegativeDeltas(t *testing.T) {
	stats := &container.StatsResponse{}
	stats.CPUStats.CPUUsage.TotalUsage = 100
	stats.PreCPUStats.CPUUsage.TotalUsage = 200
	stats.CPUStats.SystemUsage = 1000
	stats.PreCPUStats.SystemUsage = 900
	if usage := usageFromStats(stats); usage.CPUPercent != 0 {
		t.Fatalf("CPUPercent = %v, want 0", usage.CPUPercent)
	}
	stats.CPUStats.CPUUsage.TotalUsage = 0
	stats.PreCPUStats.CPUUsage.TotalUsage = 0
	stats.CPUStats.SystemUsage = 0
	stats.PreCPUStats.SystemUsage = 0
	if usage := usageFromStats(stats); usage.CPUPercent != 0 {
		t.Fatalf("empty CPUPercent = %v, want 0", usage.CPUPercent)
	}
}
