// Package resourceguard protects a host from runaway application containers.
//
// The guard is deliberately a backstop, not a replacement for cgroup limits:
// every application can carry its own CPU and memory ceiling, and those are
// enforced by Docker continuously. The guard adds sustained-usage detection for
// unconstrained (or badly constrained) applications and a host-memory emergency
// stop when the whole machine is under pressure.
package resourceguard

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/deploy"
	"github.com/tamalmaity-dev/meghify/internal/docker"
	"github.com/tamalmaity-dev/meghify/internal/realtime"
)

const (
	// A few samples per run are plenty for a self-hosted panel. Keeping the
	// worker count bounded avoids opening a burst of stats streams on a busy
	// engine while still making a 30-second pass practical for dozens of apps.
	sampleWorkers = 4

	// If the panel could not sample a node for this long, a streak is stale and
	// must not fire the moment the engine reconnects.
	metricStaleAfter = 2 * time.Minute

	// Stop gracefully so applications can flush work before the cgroup dies.
	stopTimeout = 20 * time.Second
)

// Engine is the subset of the Docker client the guard needs. Keeping it an
// interface makes the action flow testable without a Docker daemon.
type Engine interface {
	ListContainers(ctx context.Context, labelKey, labelValue string) ([]*docker.ContainerInfo, error)
	ContainerUsage(ctx context.Context, id string) (*docker.ContainerUsage, error)
	UpdateContainerResources(ctx context.Context, id string, cpuLimit float64, memoryLimit int64) error
	StopContainer(ctx context.Context, id string, timeout time.Duration) error
}

// ClientResolver returns an engine client for a node. "local" and the empty
// string mean the engine the panel itself runs on.
type ClientResolver func(ctx context.Context, nodeID string) (Engine, error)

// Options wire the guard to the panel's stores and event bus.
type Options struct {
	Store   *db.Store
	Resolve ClientResolver
	Hub     *realtime.Hub
	Logger  *slog.Logger
	Now     func() time.Time
}

// Guard samples managed application containers and applies the saved policy.
// It keeps only in-memory streaks; the durable record is the event history.
type Guard struct {
	store   *db.Store
	resolve ClientResolver
	hub     *realtime.Hub
	logger  *slog.Logger
	now     func() time.Time

	mu      sync.Mutex
	running bool
	apps    map[string]*appState
	nodes   map[string]*nodeState
}

type appState struct {
	ContainerID string
	CPU         thresholdStreak
	Memory      thresholdStreak
	Throttled   bool
	LastAction  time.Time
	LastSeen    time.Time
}

type nodeState struct {
	Host       thresholdStreak
	LastAction time.Time
}

// New builds a guard. A nil logger is replaced with slog.Default; a nil clock
// uses the wall clock. Tests can inject both.
func New(opts Options) *Guard {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Guard{
		store:   opts.Store,
		resolve: opts.Resolve,
		hub:     opts.Hub,
		logger:  logger,
		now:     now,
		apps:    map[string]*appState{},
		nodes:   map[string]*nodeState{},
	}
}

// RunScheduled performs one pass with a bounded context. The cron wrapper has
// a 30-second deadline; the guard stops a little earlier so its Docker calls
// fail cleanly rather than being cancelled mid-response.
func (g *Guard) RunScheduled() {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := g.Run(ctx); err != nil {
		g.logger.Warn("resource guard pass failed", "error", err)
	}
}

// Run executes one sampling and enforcement pass. Concurrent invocations are
// coalesced: a slow engine must not stack passes on top of each other.
func (g *Guard) Run(ctx context.Context) error {
	if g == nil || g.store == nil {
		return nil
	}
	g.mu.Lock()
	if g.running {
		g.mu.Unlock()
		return nil
	}
	g.running = true
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		g.running = false
		g.mu.Unlock()
	}()

	settings, err := g.store.ResourceGuard.Settings(ctx)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		g.mu.Lock()
		g.apps = map[string]*appState{}
		g.nodes = map[string]*nodeState{}
		g.mu.Unlock()
		return nil
	}

	apps, err := g.store.Applications.ListApplications(ctx)
	if err != nil {
		return fmt.Errorf("list applications for resource guard: %w", err)
	}
	nodes, err := g.store.Nodes.ListNodes(ctx)
	if err != nil {
		return fmt.Errorf("list nodes for resource guard: %w", err)
	}
	if g.resolve == nil {
		return nil
	}

	now := g.now().UTC()
	appsByNode := make(map[string][]*db.Application)
	for _, app := range apps {
		if app == nil {
			continue
		}
		appsByNode[normalizeNodeID(app.NodeID)] = append(appsByNode[normalizeNodeID(app.NodeID)], app)
	}

	targets := make([]*target, 0, len(apps))
	nodeMetrics := make(map[string]*db.NodeMetrics, len(nodes))
	seenApps := make(map[string]bool, len(apps))
	nodesWithTargets := make(map[string]bool, len(nodes))

	for _, node := range nodes {
		if node == nil {
			continue
		}
		nodeID := normalizeNodeID(node.ID)
		if len(appsByNode[nodeID]) == 0 {
			continue
		}
		client, err := g.resolve(ctx, nodeID)
		if err != nil || client == nil {
			g.logger.Debug("resource guard: engine unavailable", "node", nodeID, "error", err)
			continue
		}

		metric, err := g.store.Nodes.LatestMetric(ctx, nodeID)
		if err != nil {
			g.logger.Debug("resource guard: metric unavailable", "node", nodeID, "error", err)
		}
		nodeMetrics[nodeID] = metric

		containers, err := client.ListContainers(ctx, "", "")
		if err != nil {
			g.logger.Debug("resource guard: could not list containers", "node", nodeID, "error", err)
			continue
		}
		byName := make(map[string]*docker.ContainerInfo, len(containers))
		for _, container := range containers {
			if container != nil && container.State == "running" {
				byName[container.Name] = container
			}
		}

		nodeMemory := node.MemoryBytes
		if metric != nil && metric.MemTotal > 0 {
			nodeMemory = metric.MemTotal
		}
		for _, app := range appsByNode[nodeID] {
			if app == nil || !app.ResourceGuardEnabled {
				continue
			}
			finalName := docker.ManagedName(deploy.ContainerNameByID(app.ID))
			container := byName[finalName]
			if container == nil {
				continue
			}
			seenApps[app.ID] = true
			nodesWithTargets[nodeID] = true
			targets = append(targets, &target{
				app:        app,
				node:       node,
				nodeID:     nodeID,
				nodeMemory: nodeMemory,
				client:     client,
				container:  container,
			})
		}
	}

	g.sample(ctx, targets)

	targetsByNode := make(map[string][]*target)
	for _, t := range targets {
		targetsByNode[t.nodeID] = append(targetsByNode[t.nodeID], t)
		g.evaluateApp(ctx, settings, t, now)
	}
	g.evaluateHosts(ctx, settings, targetsByNode, nodeMetrics, now)

	g.mu.Lock()
	for id, state := range g.apps {
		if !seenApps[id] && now.Sub(state.LastSeen) > time.Hour {
			delete(g.apps, id)
		}
	}
	for id := range g.nodes {
		if !nodesWithTargets[id] {
			delete(g.nodes, id)
		}
	}
	g.mu.Unlock()
	return nil
}

// target is one running application container plus its engine and node facts.
type target struct {
	app        *db.Application
	node       *db.Node
	nodeID     string
	nodeMemory int64
	client     Engine
	container  *docker.ContainerInfo
	usage      *docker.ContainerUsage
	err        error
	stopped    bool
}

func (g *Guard) sample(ctx context.Context, targets []*target) {
	if len(targets) == 0 {
		return
	}
	sem := make(chan struct{}, sampleWorkers)
	var wg sync.WaitGroup
	for _, item := range targets {
		item := item
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			item.usage, item.err = item.client.ContainerUsage(ctx, item.container.ID)
		}()
	}
	wg.Wait()
}

func (g *Guard) evaluateApp(ctx context.Context, settings *db.ResourceGuardSettings, t *target, now time.Time) {
	state := g.stateFor(t)
	state.LastSeen = now
	if !t.app.ResourceGuardEnabled {
		state.reset()
		return
	}
	if t.app.Status == db.StatusDeploying || inGrace(t.container, now, settings.GraceSeconds) {
		state.reset()
		return
	}
	if t.err != nil || t.usage == nil {
		if t.err != nil {
			g.logger.Debug("resource guard: container stats failed",
				"application", t.app.ID, "container", t.container.ID, "error", t.err)
		}
		state.reset()
		return
	}

	if settings.CPUEnabled {
		threshold := t.app.ResourceGuardCPUPercent
		if threshold <= 0 {
			threshold = settings.CPUPercent
		}
		action := effectiveAction(t.app.ResourceGuardCPUAction, settings.CPUAction)
		if action == "" {
			action = db.GuardActionNotify
		}
		over := float64(threshold) > 0 && t.usage.CPUPercent >= float64(threshold)
		if state.CPU.observe(now, over, time.Duration(settings.CPUDurationSeconds)*time.Second) {
			if g.inCooldown(state.LastAction, now, settings.CooldownSeconds) {
				// Do not consume the streak while the cooldown is active. The
				// next sustained window can act once the cooldown expires.
				state.CPU.set(false)
			} else {
				throttlePercent := settings.CPUThrottlePercent
				if threshold > 0 && throttlePercent >= threshold {
					// A per-app threshold can be lower than the panel's default
					// throttle level. In that case cap at half the threshold so
					// the action actually relieves the pressure it detected.
					throttlePercent = threshold / 2
					if throttlePercent < 1 {
						throttlePercent = 1
					}
				}
				cores := throttleCores(t.node.CPUs, throttlePercent, t.app.CPULimit)
				g.act(ctx, t, state, db.GuardMetricCPU, action,
					t.usage.CPUPercent, threshold, settings.CPUDurationSeconds,
					0, 0, cores, now)
				if t.stopped {
					return
				}
			}
		}
	}

	if settings.MemoryEnabled {
		threshold := t.app.ResourceGuardMemoryPercent
		if threshold <= 0 {
			threshold = settings.MemoryPercent
		}
		limit := t.app.MemoryLimit
		if limit <= 0 {
			limit = t.nodeMemory
		}
		observed := percentOf(t.usage.MemoryBytes, limit)
		action := effectiveAction(t.app.ResourceGuardMemoryAction, settings.MemoryAction)
		if action == db.GuardActionThrottle {
			// Memory cannot be throttled safely; notify is the useful fallback.
			action = db.GuardActionNotify
		}
		if action == "" {
			action = db.GuardActionStop
		}
		over := limit > 0 && observed >= float64(threshold)
		if state.Memory.observe(now, over, time.Duration(settings.MemoryDurationSeconds)*time.Second) {
			if g.inCooldown(state.LastAction, now, settings.CooldownSeconds) {
				state.Memory.set(false)
			} else {
				g.act(ctx, t, state, db.GuardMetricMemory, action,
					observed, threshold, settings.MemoryDurationSeconds,
					t.usage.MemoryBytes, limit, 0, now)
			}
		}
	}
}

func (g *Guard) evaluateHosts(
	ctx context.Context,
	settings *db.ResourceGuardSettings,
	targetsByNode map[string][]*target,
	nodeMetrics map[string]*db.NodeMetrics,
	now time.Time,
) {
	if !settings.HostMemoryEnabled {
		return
	}
	for nodeID, targets := range targetsByNode {
		metric := nodeMetrics[nodeID]
		if metric == nil || metric.MemTotal <= 0 {
			continue
		}
		if now.Sub(metric.RecordedAt) > metricStaleAfter {
			if state := g.nodes[nodeID]; state != nil {
				state.Host.set(false)
			}
			continue
		}
		state := g.nodeStateFor(nodeID)
		hostPercent := percentOf(metric.MemUsed, metric.MemTotal)
		over := hostPercent >= float64(settings.HostMemoryPercent)
		if !state.Host.observe(now, over, time.Duration(settings.HostMemoryDurationSecs)*time.Second) {
			continue
		}
		if g.inCooldown(state.LastAction, now, settings.CooldownSeconds) {
			state.Host.set(false)
			continue
		}

		candidate := largestMemory(targets, now, settings.GraceSeconds)
		if candidate == nil {
			state.Host.set(false)
			continue
		}
		candidateState := g.stateFor(candidate)
		if g.inCooldown(candidateState.LastAction, now, settings.CooldownSeconds) {
			state.Host.set(false)
			continue
		}
		g.act(ctx, candidate, candidateState, db.GuardMetricHostMemory,
			db.GuardActionStop, hostPercent, settings.HostMemoryPercent,
			settings.HostMemoryDurationSecs, candidate.usage.MemoryBytes, metric.MemTotal, 0, now)
		state.LastAction = now
		// Start a fresh streak: if memory stays critical, the next pass may
		// need to stop another application, but not on every 30-second tick.
		state.Host.set(false)
	}
}

func (g *Guard) act(
	ctx context.Context,
	t *target,
	state *appState,
	metric, action string,
	observed float64,
	threshold, durationSeconds int,
	usageBytes, limitBytes int64,
	cores float64,
	now time.Time,
) {
	state.LastAction = now
	event := &db.ResourceGuardEvent{
		ID:               db.NewID(),
		ApplicationID:    t.app.ID,
		ApplicationName:  t.app.Name,
		NodeID:           t.nodeID,
		ContainerID:      t.container.ID,
		ContainerName:    t.container.Name,
		Metric:           metric,
		Action:           action,
		Status:           db.GuardStatusSuccess,
		ObservedPercent:  round1(observed),
		ThresholdPercent: threshold,
		DurationSeconds:  durationSeconds,
		UsageBytes:       usageBytes,
		LimitBytes:       limitBytes,
		CreatedAt:        now,
	}

	switch action {
	case db.GuardActionNotify:
		event.Message = notifyMessage(metric, observed, threshold, durationSeconds, limitBytes)
	case db.GuardActionThrottle:
		if state.Throttled {
			return
		}
		if cores <= 0 {
			event.Status = db.GuardStatusFailed
			event.Error = "calculated throttle limit is zero"
			event.Message = notifyMessage(metric, observed, threshold, durationSeconds, limitBytes)
			state.CPU.set(false)
			break
		}
		if err := t.client.UpdateContainerResources(ctx, t.container.ID, cores, t.app.MemoryLimit); err != nil {
			event.Status = db.GuardStatusFailed
			event.Error = err.Error()
			event.Message = fmt.Sprintf("CPU stayed above %d%% for %s; throttling failed",
				threshold, humanDuration(durationSeconds))
			state.CPU.set(false)
			break
		}
		state.Throttled = true
		event.Message = fmt.Sprintf("CPU stayed above %d%% of host capacity for %s; capped at %.2f CPU",
			threshold, humanDuration(durationSeconds), cores)
	case db.GuardActionStop:
		if err := t.client.StopContainer(ctx, t.container.ID, stopTimeout); err != nil {
			event.Status = db.GuardStatusFailed
			event.Error = err.Error()
			event.Message = fmt.Sprintf("%s threshold exceeded; stopping the container failed",
				metricLabel(metric))
			state.CPU.set(false)
			state.Memory.set(false)
			break
		}
		t.stopped = true
		// A later manual start is a new incident. The cooldown still prevents
		// immediate action, but the streaks must not remain latched forever.
		state.CPU.set(false)
		state.Memory.set(false)
		if err := g.store.Applications.PatchApplicationStatus(ctx, t.app.ID, db.StatusStopped); err != nil {
			g.logger.Warn("resource guard: container stopped but status update failed",
				"application", t.app.ID, "error", err)
		}
		t.app.Status = db.StatusStopped
		event.Message = stopMessage(metric, observed, threshold, durationSeconds, t.app.Name, usageBytes)
	default:
		return
	}

	if err := g.store.ResourceGuard.AddEvent(ctx, event); err != nil {
		g.logger.Warn("resource guard: could not record event", "application", t.app.ID, "error", err)
	}
	level := slog.LevelInfo
	if event.Status == db.GuardStatusFailed {
		level = slog.LevelWarn
	}
	g.logger.Log(ctx, level, "resource guard action",
		"application", t.app.ID,
		"container", t.container.ID,
		"metric", metric,
		"action", action,
		"status", event.Status,
		"observed", event.ObservedPercent,
		"error", event.Error)
	if g.hub != nil {
		payload := map[string]any{
			"applicationId":   t.app.ID,
			"event":           "resource_guard",
			"metric":          metric,
			"action":          action,
			"status":          event.Status,
			"observedPercent": event.ObservedPercent,
			"message":         event.Message,
			"at":              now,
		}
		if action == db.GuardActionStop && event.Status == db.GuardStatusSuccess {
			payload["applicationStatus"] = db.StatusStopped
		}
		g.hub.BroadcastApplication(t.app.ID, payload)
	}
}

// ResetApplication clears the in-memory streak and throttle state for one
// application. The API calls this when saved CPU/memory limits are applied or
// the per-application guard policy changes, so the next pass evaluates the new
// configuration from a clean slate.
func (g *Guard) ResetApplication(appID string) {
	if g == nil || appID == "" {
		return
	}
	g.mu.Lock()
	delete(g.apps, appID)
	g.mu.Unlock()
}

// ResetAll clears every streak after the panel-wide policy changes.
func (g *Guard) ResetAll() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.apps = map[string]*appState{}
	g.nodes = map[string]*nodeState{}
	g.mu.Unlock()
}

func (g *Guard) stateFor(t *target) *appState {
	g.mu.Lock()
	defer g.mu.Unlock()
	state := g.apps[t.app.ID]
	if state == nil || state.ContainerID != t.container.ID {
		previous := time.Time{}
		if state != nil {
			previous = state.LastAction
		}
		state = &appState{ContainerID: t.container.ID, LastAction: previous}
		g.apps[t.app.ID] = state
	}
	return state
}

func (g *Guard) nodeStateFor(nodeID string) *nodeState {
	g.mu.Lock()
	defer g.mu.Unlock()
	state := g.nodes[nodeID]
	if state == nil {
		state = &nodeState{}
		g.nodes[nodeID] = state
	}
	return state
}

func (s *appState) reset() {
	s.CPU.set(false)
	s.Memory.set(false)
}

func (g *Guard) inCooldown(last, now time.Time, seconds int) bool {
	if last.IsZero() || seconds <= 0 {
		return false
	}
	return now.Sub(last) < time.Duration(seconds)*time.Second
}

// thresholdStreak turns consecutive over-threshold samples into a single
// event after the configured duration. A dip below the threshold resets it.
type thresholdStreak struct {
	since time.Time
	fired bool
}

func (s *thresholdStreak) observe(now time.Time, over bool, duration time.Duration) bool {
	if !over {
		s.set(false)
		return false
	}
	if s.since.IsZero() {
		s.since = now
	}
	if s.fired {
		return false
	}
	if duration <= 0 || now.Sub(s.since) >= duration {
		s.fired = true
		return true
	}
	return false
}

func (s *thresholdStreak) set(over bool) {
	if !over {
		s.since = time.Time{}
		s.fired = false
	}
}

func effectiveAction(override, fallback string) string {
	switch override {
	case db.GuardActionNotify, db.GuardActionThrottle, db.GuardActionStop:
		return override
	}
	switch fallback {
	case db.GuardActionNotify, db.GuardActionThrottle, db.GuardActionStop:
		return fallback
	}
	return db.GuardActionNotify
}

func throttleCores(hostCPUs, throttlePercent int, appLimit float64) float64 {
	if hostCPUs <= 0 {
		hostCPUs = 1
	}
	if throttlePercent <= 0 {
		throttlePercent = 50
	}
	cores := float64(hostCPUs) * float64(throttlePercent) / 100
	if cores < 0.05 {
		cores = 0.05
	}
	if appLimit > 0 && appLimit < cores {
		// Preserve an already tighter application limit exactly instead of
		// rounding a small value down to zero (which Docker would treat as
		// "unlimited").
		if appLimit < 0.001 {
			return 0.001
		}
		return appLimit
	}
	return math.Round(cores*100) / 100
}

func largestMemory(targets []*target, now time.Time, graceSeconds int) *target {
	candidates := make([]*target, 0, len(targets))
	for _, t := range targets {
		if t == nil || t.stopped || !t.app.ResourceGuardEnabled || t.usage == nil || t.usage.MemoryBytes <= 0 {
			continue
		}
		if inGrace(t.container, now, graceSeconds) {
			continue
		}
		candidates = append(candidates, t)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].usage.MemoryBytes > candidates[j].usage.MemoryBytes
	})
	if len(candidates) == 0 {
		return nil
	}
	return candidates[0]
}

func inGrace(container *docker.ContainerInfo, now time.Time, graceSeconds int) bool {
	if graceSeconds <= 0 || container == nil || container.Created <= 0 {
		return false
	}
	created := time.Unix(container.Created, 0)
	return now.Sub(created) < time.Duration(graceSeconds)*time.Second
}

func normalizeNodeID(nodeID string) string {
	if nodeID == "" || nodeID == "local" {
		return db.LocalNodeID
	}
	return nodeID
}

func percentOf(value, total int64) float64 {
	if total <= 0 || value <= 0 {
		return 0
	}
	return float64(value) / float64(total) * 100
}

func round1(value float64) float64 {
	return math.Round(value*10) / 10
}

func metricLabel(metric string) string {
	switch metric {
	case db.GuardMetricCPU:
		return "CPU"
	case db.GuardMetricHostMemory:
		return "Host memory"
	default:
		return "Memory"
	}
}

func notifyMessage(metric string, observed float64, threshold, durationSeconds int, limitBytes int64) string {
	switch metric {
	case db.GuardMetricCPU:
		return fmt.Sprintf("CPU stayed above %d%% of host capacity for %s (observed %.1f%%)",
			threshold, humanDuration(durationSeconds), observed)
	case db.GuardMetricHostMemory:
		return fmt.Sprintf("Host memory stayed above %d%% for %s (observed %.1f%%)",
			threshold, humanDuration(durationSeconds), observed)
	default:
		if limitBytes > 0 {
			return fmt.Sprintf("Memory stayed above %d%% of the %s limit for %s (observed %.1f%%)",
				threshold, formatBytes(limitBytes), humanDuration(durationSeconds), observed)
		}
		return fmt.Sprintf("Memory stayed above %d%% for %s (observed %.1f%%)",
			threshold, humanDuration(durationSeconds), observed)
	}
}

func stopMessage(metric string, observed float64, threshold, durationSeconds int, appName string, usageBytes int64) string {
	switch metric {
	case db.GuardMetricHostMemory:
		return fmt.Sprintf("Host memory stayed above %d%% for %s; stopped %s (using %s)",
			threshold, humanDuration(durationSeconds), appName, formatBytes(usageBytes))
	default:
		return fmt.Sprintf("%s stayed above %d%% for %s (observed %.1f%%); container stopped",
			metricLabel(metric), threshold, humanDuration(durationSeconds), observed)
	}
}

func humanDuration(seconds int) string {
	if seconds <= 0 {
		return "a sustained period"
	}
	if seconds%3600 == 0 {
		hours := seconds / 3600
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	}
	if seconds%60 == 0 {
		minutes := seconds / 60
		if minutes == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", minutes)
	}
	return fmt.Sprintf("%d seconds", seconds)
}

func formatBytes(bytes int64) string {
	if bytes <= 0 {
		return "0 B"
	}
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
	)
	switch {
	case bytes >= gib:
		return fmt.Sprintf("%.1f GB", float64(bytes)/gib)
	case bytes >= mib:
		return fmt.Sprintf("%.1f MB", float64(bytes)/mib)
	case bytes >= kib:
		return fmt.Sprintf("%.1f KB", float64(bytes)/kib)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
