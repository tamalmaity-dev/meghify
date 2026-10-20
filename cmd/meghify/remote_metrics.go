package main

import (
	"context"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/docker"
	"github.com/tamalmaity-dev/meghify/internal/node"
)

// Remote metric collection is deliberately slower than the local 15s sample:
// every sample is an SSH command, and a large fleet does not need per-second
// CPU charts. The guard also keeps the queue job and cron from sampling twice.
const remoteMetricsInterval = time.Minute

var (
	remoteMetricsMu      sync.Mutex
	lastRemoteMetrics    time.Time
	remoteMetricsRunning bool
)

// collectRemoteMetrics samples every registered remote server over its SSH
// connection and stores a normal node_metrics row. It runs after the local
// sample so a slow or unreachable node cannot delay the panel's own series.
func collectRemoteMetrics(
	ctx context.Context,
	store *db.Store,
	registry *node.Registry,
	resolve func(context.Context, string) (*docker.Client, error),
	logger *slog.Logger,
) {
	if registry == nil || store == nil {
		return
	}
	remoteMetricsMu.Lock()
	if remoteMetricsRunning || time.Since(lastRemoteMetrics) < remoteMetricsInterval {
		remoteMetricsMu.Unlock()
		return
	}
	remoteMetricsRunning = true
	lastRemoteMetrics = time.Now()
	remoteMetricsMu.Unlock()
	defer func() {
		remoteMetricsMu.Lock()
		remoteMetricsRunning = false
		remoteMetricsMu.Unlock()
	}()

	nodes, err := store.Nodes.ListNodes(ctx)
	if err != nil {
		logger.Warn("could not list servers for metrics", "error", err)
		return
	}
	for _, n := range nodes {
		if n.ID == "" || n.ID == db.LocalNodeID {
			continue
		}
		nodeCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		collected := collectRemoteNodeMetrics(nodeCtx, store, registry, resolve, n, logger)
		cancel()
		if !collected {
			continue
		}
	}
}

func collectRemoteNodeMetrics(
	ctx context.Context,
	store *db.Store,
	registry *node.Registry,
	resolve func(context.Context, string) (*docker.Client, error),
	n *db.Node,
	logger *slog.Logger,
) bool {
	dialer, err := registry.Dialer(ctx, n)
	if err != nil {
		logger.Debug("remote metrics: ssh unavailable", "node", n.ID, "error", err)
		return false
	}
	sample, err := remoteSample(dialer, ctx)
	if err != nil {
		logger.Debug("remote metrics: sample failed", "node", n.ID, "error", err)
		return false
	}

	// Engine facts are richer and agree with what deployments see. They are
	// optional: a running SSH host with a down engine still reports resource
	// usage, and the Server page shows the engine error instead of blanks.
	osName := valueOr(sample.OS, n.OS)
	kernel := valueOr(sample.Kernel, n.Kernel)
	arch := valueOr(sample.Arch, n.Arch)
	if arch == "unknown" {
		arch = n.Arch
	}
	cpus, memory := sample.CPUs, sample.MemTotal
	if cpus <= 0 {
		cpus = n.CPUs
	}
	if memory <= 0 {
		memory = n.MemoryBytes
	}
	dockerVersion := n.DockerVersion
	if resolve != nil {
		if c, err := resolve(ctx, n.ID); err == nil && c != nil {
			if info, err := c.Info(ctx); err == nil {
				osName = valueOr(info.OperatingSystem, osName)
				kernel = valueOr(info.KernelVersion, kernel)
				arch = valueOr(info.Architecture, arch)
				if info.NCPU > 0 {
					cpus = info.NCPU
				}
				if info.MemTotal > 0 {
					memory = info.MemTotal
				}
				dockerVersion = info.ServerVersion
			}
		}
	}

	var bootTime *time.Time
	if !sample.BootTime.IsZero() {
		bootTime = &sample.BootTime
	}
	now := time.Now().UTC()
	if err := store.Nodes.UpdateNodeFacts(ctx, n.ID, osName, kernel, arch, dockerVersion, cpus, memory, bootTime, now); err != nil {
		logger.Warn("remote metrics: could not update server facts", "node", n.ID, "error", err)
	}
	if err := store.Nodes.UpdateNodeStatus(ctx, n.ID, db.NodeOnline, "", now); err != nil {
		logger.Warn("remote metrics: could not mark server online", "node", n.ID, "error", err)
	}
	if err := store.Nodes.InsertMetric(ctx, &db.NodeMetrics{
		ID:         db.NewID(),
		NodeID:     n.ID,
		CPUPercent: math.Round(sample.CPUPercent*10) / 10,
		MemUsed:    sample.MemUsed,
		MemTotal:   sample.MemTotal,
		DiskUsed:   sample.DiskUsed,
		DiskTotal:  sample.DiskTotal,
		NetRx:      sample.NetRx,
		NetTx:      sample.NetTx,
		RecordedAt: now,
	}); err != nil {
		logger.Warn("remote metrics: could not store sample", "node", n.ID, "error", err)
		return false
	}
	return true
}

// remoteSample reads the same /proc sources as hostSampleNow, but through one
// SSH round trip. The script prints section markers so parsing does not depend
// on the order in which kernel versions list fields.
func remoteSample(dialer *node.Dialer, ctx context.Context) (hostSample, error) {
	const script = `set -e
echo "@cpu1"; grep '^cpu ' /proc/stat
echo "@mem"; cat /proc/meminfo
echo "@disk"; df -kP /
echo "@net"; cat /proc/net/dev
echo "@uptime"; cat /proc/uptime
echo "@kernel"; uname -r
echo "@os"; grep '^PRETTY_NAME=' /etc/os-release || true
echo "@cpus"; grep -c '^processor' /proc/cpuinfo
sleep 0.25
echo "@cpu2"; grep '^cpu ' /proc/stat
`
	var output strings.Builder
	if err := dialer.Run(ctx, script, func(chunk string) {
		if output.Len() < 256*1024 {
			output.WriteString(chunk)
		}
	}); err != nil {
		return hostSample{}, err
	}

	sections := map[string][]string{}
	current := ""
	for _, line := range strings.Split(output.String(), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "@") {
			current = strings.TrimPrefix(line, "@")
			continue
		}
		if current != "" {
			sections[current] = append(sections[current], line)
		}
	}

	sample := hostSample{Arch: "unknown"}
	if line := firstLine(sections["kernel"]); line != "" {
		sample.Kernel = line
	}
	for _, line := range sections["os"] {
		if name, value, ok := strings.Cut(line, "="); ok && name == "PRETTY_NAME" {
			sample.OS = strings.Trim(value, `"`)
		}
	}
	if cpus, err := strconv.Atoi(strings.TrimSpace(firstLine(sections["cpus"]))); err == nil && cpus > 0 {
		sample.CPUs = cpus
	}

	if idle1, total1, ok1 := parseProcCPU(firstLine(sections["cpu1"])); ok1 {
		if idle2, total2, ok2 := parseProcCPU(firstLine(sections["cpu2"])); ok2 && total2 > total1 {
			deltaTotal := total2 - total1
			deltaIdle := idle2 - idle1
			sample.CPUPercent = float64(deltaTotal-deltaIdle) / float64(deltaTotal) * 100
		}
	}

	var memTotal, memAvailable uint64
	for _, line := range sections["mem"] {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			memTotal = value * 1024
		case "MemAvailable:":
			memAvailable = value * 1024
		}
	}
	if memTotal > 0 {
		if memAvailable == 0 || memAvailable > memTotal {
			memAvailable = memTotal
		}
		sample.MemTotal = int64(memTotal)
		sample.MemUsed = int64(memTotal - memAvailable)
	}

	for _, line := range sections["disk"] {
		fields := strings.Fields(line)
		// df -kP columns: filesystem, 1024-blocks, used, available, ...
		if len(fields) < 3 {
			continue
		}
		totalKB, err1 := strconv.ParseInt(fields[1], 10, 64)
		usedKB, err2 := strconv.ParseInt(fields[2], 10, 64)
		if err1 == nil && err2 == nil && totalKB > 0 {
			sample.DiskTotal = totalKB * 1024
			sample.DiskUsed = usedKB * 1024
			break
		}
	}

	for _, line := range sections["net"] {
		name, rest, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(name) == "lo" {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 9 {
			continue
		}
		rx, err1 := strconv.ParseInt(fields[0], 10, 64)
		tx, err2 := strconv.ParseInt(fields[8], 10, 64)
		if err1 == nil && err2 == nil {
			sample.NetRx += rx
			sample.NetTx += tx
		}
	}

	if fields := strings.Fields(firstLine(sections["uptime"])); len(fields) > 0 {
		if uptime, err := strconv.ParseFloat(fields[0], 64); err == nil && uptime >= 0 {
			sample.BootTime = time.Now().Add(-time.Duration(uptime * float64(time.Second))).UTC()
		}
	}
	return sample, nil
}

func firstLine(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[0])
}

func parseProcCPU(line string) (idle, total uint64, ok bool) {
	if !strings.HasPrefix(line, "cpu ") {
		return 0, 0, false
	}
	for i, field := range strings.Fields(line)[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		total += value
		if i == 3 || i == 4 {
			idle += value
		}
	}
	return idle, total, total > 0
}
