package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/docker"
)

// The build sandbox is an isolated, resource-limited Docker-in-Docker daemon.
// Build containers run inside its cgroup, so no matter what a repository does
// during a build (npm ci, next build, runaway compilers), the panel, the
// reverse proxy and every running application keep their share of the server.
//
// It also replaces the mounted host socket: the build never talks to the
// engine that runs production containers.
const (
	// sandboxName is the logical name; the engine-side container carries the
	// panel's managed prefix (docker.ManagedName).
	sandboxName        = "buildd"
	sandboxNetworkName = "meghify-build"
	sandboxVolumeName  = "meghify-build-cache"
	sandboxPort        = 2375
)

// sandboxInfo tells the build helper how to reach the sandbox.
type sandboxInfo struct {
	Endpoint string // DOCKER_HOST for the helper
	Network  string
	CPU      float64
	Memory   int64
}

// ensureSandbox provisions (or restarts) the build daemon and waits until it
// accepts Docker API calls. It is idempotent and safe to call per deployment.
func (p *Pipeline) ensureSandbox(ctx context.Context, sink io.Writer) (*sandboxInfo, error) {
	if p.client == nil || p.sandboxImage == "" {
		return nil, errors.New("build sandbox is not configured")
	}
	p.markSandboxUsed()
	if _, err := p.client.EnsureNetwork(ctx, sandboxNetworkName, false); err != nil {
		return nil, fmt.Errorf("sandbox network: %w", err)
	}

	container, err := p.client.ContainerByName(ctx, sandboxName)
	if err != nil {
		return nil, err
	}
	dockerName := docker.ManagedName(sandboxName)
	// Explicit BUILD_CPU_LIMIT / BUILD_MEMORY_LIMIT values are enforced by
	// recreating the daemon when a running sandbox no longer matches them;
	// the build cache volume survives the recreation. Auto limits (half of
	// this node) are only applied at creation so a host resize never restarts
	// the daemon under a running build.
	if container != nil && p.sandboxLimitDrift(ctx, container.ID) {
		sink.Write([]byte("[*] reapplying the build sandbox limits (the build cache is kept)\n"))
		_ = p.client.StopContainer(ctx, container.ID, 10*time.Second)
		_ = p.client.RemoveContainer(ctx, container.ID)
		container = nil
	}
	if container == nil {
		if ok, _ := p.client.ImageExists(ctx, p.sandboxImage); !ok {
			sink.Write([]byte("[*] pulling the build sandbox image " + p.sandboxImage + "\n"))
			if err := p.client.PullImageRetry(ctx, p.sandboxImage, sink); err != nil {
				return nil, fmt.Errorf("pull sandbox image: %w", err)
			}
		}
		cpu, memory := p.sandboxLimits(ctx)
		id, err := p.client.CreateContainer(ctx, &docker.ContainerSpec{
			Name:  sandboxName,
			Image: p.sandboxImage,
			// DOCKER_TLS_CERTDIR= disables the entrypoint's TLS: the daemon is
			// only reachable from the private build network.
			Cmd:           []string{"--host=tcp://0.0.0.0:" + strconv.Itoa(sandboxPort), "--host=unix:///var/run/docker.sock"},
			Env:           []string{"DOCKER_TLS_CERTDIR="},
			Labels:        map[string]string{"meghify.role": "build-sandbox"},
			Volumes:       []docker.Mount{{Source: sandboxVolumeName, Target: "/var/lib/docker"}},
			Networks:      []string{sandboxNetworkName},
			RestartPolicy: "unless-stopped",
			Privileged:    true,
			ShmSize:       512 << 20,
			MemoryLimit:   memory,
			CPULimit:      cpu,
		})
		if err != nil {
			return nil, fmt.Errorf("create sandbox: %w", err)
		}
		if err := p.client.StartContainer(ctx, id); err != nil {
			_ = p.client.RemoveContainer(ctx, id)
			return nil, fmt.Errorf("start sandbox: %w", err)
		}
		sink.Write([]byte("[*] build sandbox started: " + sandboxLimitText(cpu, memory) + " reserved for builds\n"))
	} else if container.State != "running" {
		if err := p.client.StartContainer(ctx, container.ID); err != nil {
			return nil, fmt.Errorf("restart sandbox: %w", err)
		}
	}

	if err := p.waitSandbox(ctx, sink, dockerName, 120*time.Second); err != nil {
		return nil, err
	}
	p.markSandboxUsed()
	return &sandboxInfo{
		Endpoint: "tcp://" + dockerName + ":" + strconv.Itoa(sandboxPort),
		Network:  sandboxNetworkName,
	}, nil
}

// markSandboxUsed records that a deployment needs the sandbox. The timestamp
// is shared by every per-node pipeline view; the idle sweep reads it.
func (p *Pipeline) markSandboxUsed() {
	if p == nil || p.mu == nil {
		return
	}
	nodeID := p.nodeID
	if nodeID == "" {
		nodeID = "local"
	}
	p.mu.Lock()
	if p.sandboxLastUsed == nil {
		p.sandboxLastUsed = map[string]time.Time{}
	}
	p.sandboxLastUsed[nodeID] = time.Now()
	p.mu.Unlock()
}

// StopAllSandboxes stops every build sandbox on every node. Startup calls this
// after interrupted deployments are reconciled: no build is running yet, and
// the next deployment starts the daemon again on demand.
func (p *Pipeline) StopAllSandboxes(ctx context.Context) (int, error) {
	return p.stopSandboxes(ctx, 0, true)
}

// StopIdleSandboxes stops sandboxes that have been unused for idle. Set idle
// to zero to disable the sweep. The build cache volume is never touched.
func (p *Pipeline) StopIdleSandboxes(ctx context.Context, idle time.Duration) (int, error) {
	if idle <= 0 {
		return 0, nil
	}
	return p.stopSandboxes(ctx, idle, false)
}

func (p *Pipeline) stopSandboxes(ctx context.Context, idle time.Duration, all bool) (int, error) {
	if p == nil || p.store == nil || p.sandboxMode == "off" {
		return 0, nil
	}
	p.mu.Lock()
	if len(p.running) > 0 {
		p.mu.Unlock()
		return 0, nil
	}
	p.mu.Unlock()

	nodes, err := p.store.Nodes.ListNodes(ctx)
	if err != nil {
		return 0, err
	}
	// Local first: an offline remote node must not consume the startup
	// deadline before the sandbox on this panel's own engine is stopped.
	sort.SliceStable(nodes, func(i, j int) bool {
		return nodes[i] != nil && nodes[i].ID == "local" &&
			(nodes[j] == nil || nodes[j].ID != "local")
	})

	stopped := 0
	var errs []error
	for _, n := range nodes {
		if n == nil || n.ID == "" {
			continue
		}
		p.mu.Lock()
		if len(p.running) > 0 {
			p.mu.Unlock()
			break
		}
		last := p.sandboxLastUsed[n.ID]
		p.mu.Unlock()
		if !all && !sandboxIdleExpired(last, time.Now(), idle) {
			continue
		}

		client := p.client
		if p.clientFor != nil {
			resolved, resolveErr := p.clientFor(ctx, n.ID)
			if resolveErr != nil {
				errs = append(errs, fmt.Errorf("resolve node %s: %w", n.ID, resolveErr))
				continue
			}
			client = resolved
		} else if n.ID != "local" {
			continue
		}
		if client == nil {
			continue
		}
		container, err := client.ContainerByName(ctx, sandboxName)
		if err != nil {
			errs = append(errs, fmt.Errorf("find sandbox on %s: %w", n.ID, err))
			continue
		}
		if container == nil || container.State != "running" {
			p.mu.Lock()
			delete(p.sandboxLastUsed, n.ID)
			p.mu.Unlock()
			continue
		}

		// Hold the shared mutex across the stop so Run cannot claim the
		// sandbox between the activity check and the stop.
		p.mu.Lock()
		if len(p.running) > 0 {
			p.mu.Unlock()
			break
		}
		last = p.sandboxLastUsed[n.ID]
		if !all && !sandboxIdleExpired(last, time.Now(), idle) {
			p.mu.Unlock()
			continue
		}
		stopErr := client.StopContainer(ctx, container.ID, 20*time.Second)
		if stopErr == nil {
			delete(p.sandboxLastUsed, n.ID)
			stopped++
		}
		p.mu.Unlock()
		if stopErr != nil {
			errs = append(errs, fmt.Errorf("stop sandbox on %s: %w", n.ID, stopErr))
		}
	}
	return stopped, errors.Join(errs...)
}

func sandboxIdleExpired(last, now time.Time, idle time.Duration) bool {
	return !last.IsZero() && now.Sub(last) >= idle
}

// waitSandbox polls `docker info` inside the sandbox until the daemon answers.
// Every attempt has its own deadline and drain, so a command that hangs while
// dockerd is still booting can never eat the whole deployment budget.
func (p *Pipeline) waitSandbox(ctx context.Context, sink io.Writer, containerName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	attempt := 0
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		attempt++
		attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		code, err := p.sandboxInfoOnce(attemptCtx, containerName)
		cancel()
		if err == nil && code == 0 {
			return nil
		}
		if err == nil {
			err = fmt.Errorf("docker info exit code %d", code)
		}
		last = err
		if attempt == 1 || attempt%5 == 0 {
			sink.Write([]byte(fmt.Sprintf("[*] waiting for the build sandbox to start (attempt %d)\n", attempt)))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if last == nil {
		last = errors.New("timed out")
	}
	return fmt.Errorf("build sandbox did not become ready: %w", last)
}

// sandboxInfoOnce runs one `docker info` inside the sandbox and returns its
// exit code, ending as soon as the context is done.
func (p *Pipeline) sandboxInfoOnce(ctx context.Context, containerName string) (int, error) {
	result, err := p.client.RunExec(ctx, containerName, []string{"docker", "info"}, nil)
	if err != nil {
		return -1, err
	}
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, result.Output)
		result.Output.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		result.Output.Close()
		<-done
		return -1, ctx.Err()
	}
	return result.Wait(ctx)
}

// sandboxLimits resolves the CPU and memory the sandbox may use. Unset values
// mean "half of this node", so a build cannot starve what is running.
func (p *Pipeline) sandboxLimits(ctx context.Context) (float64, int64) {
	cpu, memory := p.buildCPULimit, p.buildMemoryLimit
	if cpu > 0 && memory > 0 {
		return cpu, memory
	}
	info, err := p.client.Info(ctx)
	if err != nil {
		return cpu, memory
	}
	if cpu <= 0 {
		cpu = float64(info.NCPU) / 2
		// Small nodes: the panel idles at a few percent of a core and builds
		// run one at a time (BUILD_CONCURRENCY), so giving the single active
		// build the whole node beats dragging a half-core build out until the
		// deployment timeout kills it.
		if cpu < 2 && info.NCPU >= 2 {
			cpu = 2
		}
		if cpu < 1 {
			cpu = 1
		}
		if cpu > 4 {
			cpu = 4
		}
	}
	if memory <= 0 {
		memory = info.MemTotal / 2
		if memory < 1<<30 {
			memory = 1 << 30
		}
		if memory > 8<<30 {
			memory = 8 << 30
		}
	}
	return cpu, memory
}

// sandboxLimitDrift reports whether an existing sandbox's limits differ from
// explicitly configured ones (BUILD_CPU_LIMIT, BUILD_MEMORY_LIMIT).
func (p *Pipeline) sandboxLimitDrift(ctx context.Context, id string) bool {
	if p.buildCPULimit <= 0 && p.buildMemoryLimit <= 0 {
		return false
	}
	info, err := p.client.InspectContainer(ctx, id)
	if err != nil || info.HostConfig == nil {
		return false
	}
	if p.buildCPULimit > 0 && float64(info.HostConfig.NanoCPUs)/1e9 != p.buildCPULimit {
		return true
	}
	if p.buildMemoryLimit > 0 && info.HostConfig.Memory != p.buildMemoryLimit {
		return true
	}
	return false
}

func sandboxLimitText(cpu float64, memory int64) string {
	text := fmt.Sprintf("%.1f CPU", cpu)
	if cpu == float64(int(cpu)) {
		text = fmt.Sprintf("%.0f CPU", cpu)
	}
	if memory > 0 {
		text += " · " + formatBytes(memory) + " RAM"
	}
	if cpu <= 0 && memory <= 0 {
		text = "no explicit limits"
	}
	return text
}

// formatBytes renders a byte count the way the dashboard does.
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
