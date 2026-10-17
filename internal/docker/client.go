// Package docker talks to the Docker Engine API on the local node.
//
// The Docker host is configurable so the same client serves the panel container
// (unix socket bind) and a remote node reached over an SSH tunnel.
package docker

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/system"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
)

// ErrNoSuchContainer is returned when a container id is unknown to the engine.
var ErrNoSuchContainer = errors.New("no such container")

// Client wraps the official Docker SDK with the operations the panel needs.
type Client struct {
	cli *client.Client
}

// NewClient dials the Engine API. host is a docker-style URL such as
// unix:///var/run/docker.sock or tcp://127.0.0.1:2375.
func NewClient(host string) (*Client, error) {
	opts := []client.Opt{client.WithHost(host), client.WithAPIVersionNegotiation()}
	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("docker client %s: %w", host, err)
	}
	return &Client{cli: cli}, nil
}

// NewClientFromTCP dials an engine over TCP with optional TLS material.
func NewClientFromTCP(addr, caPath, certPath, keyPath string) (*Client, error) {
	if addr == "" {
		return nil, errors.New("docker address is required")
	}
	if !strings.Contains(addr, "://") {
		addr = "tcp://" + addr
	}

	httpClient := &http.Client{Timeout: 0}
	if caPath != "" && certPath != "" && keyPath != "" {
		tlsConfig, err := loadTLS(caPath, certPath, keyPath)
		if err != nil {
			return nil, err
		}
		httpClient.Transport = &http.Transport{TLSClientConfig: tlsConfig}
	}

	cli, err := client.NewClientWithOpts(
		client.WithHost(addr),
		client.WithHTTPClient(httpClient),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, err
	}
	return &Client{cli: cli}, nil
}

// NewClientFromTunnel dials an engine reached through an SSH tunnel.
//
// The tunnel terminates on loopback and the remote daemon presents a
// self-signed certificate, so certificate verification is skipped here. Security
// comes from the SSH transport, not from the certificate chain.
func NewClientFromTunnel(addr string) (*Client, error) {
	if addr == "" {
		return nil, errors.New("tunnel address is required")
	}
	host := addr
	if !strings.Contains(host, "://") {
		host = "tcp://" + host
	}

	httpClient := &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			// #nosec G402 -- the tunnel is the trust boundary.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, //nolint:gosec
		},
	}

	cli, err := client.NewClientWithOpts(
		client.WithHost(host),
		client.WithHTTPClient(httpClient),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, err
	}
	return &Client{cli: cli}, nil
}

// Ping verifies the engine is reachable.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := c.cli.Ping(ctx)
	return err
}

// Info returns engine information used to populate the node record.
func (c *Client) Info(ctx context.Context) (system.Info, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	info, err := c.cli.Info(ctx)
	if err != nil {
		return system.Info{}, fmt.Errorf("docker info: %w", err)
	}
	return info, nil
}

// --- containers ---

// ContainerSpec describes a container to create.
type ContainerSpec struct {
	Name          string
	Image         string
	Env           []string
	Cmd           []string
	Labels        map[string]string
	Ports         map[int]int // container port -> host port
	Volumes       []Mount
	Networks      []string
	RestartPolicy container.RestartPolicyMode
	HealthCheck   *container.HealthConfig
	MemoryLimit   int64
	CPULimit      float64
	Exposed       []int
	// Privileged is required by the build sandbox: Docker-in-Docker needs its
	// own kernel namespaces (the build never touches the host engine).
	Privileged bool
	// ShmSize raises /dev/shm for builder containers (bytes); zero keeps the
	// engine default.
	ShmSize int64
}

// Mount is a bind or named volume mapping.
type Mount struct {
	Source   string
	Target   string
	ReadOnly bool
}

// ContainerInfo is the panel's view of a running container.
type ContainerInfo struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Image   string            `json:"image"`
	State   string            `json:"state"`
	Status  string            `json:"status"`
	Labels  map[string]string `json:"labels"`
	Ports   []string          `json:"ports"`
	Created int64             `json:"created"`
}

// CreateContainer creates a container without starting it.
func (c *Client) CreateContainer(ctx context.Context, spec *ContainerSpec) (string, error) {
	labels := map[string]string{
		"meghify.managed":  "true",
		"meghify.resource": spec.Name,
	}
	for k, v := range spec.Labels {
		labels[k] = v
	}

	exposed := nat.PortSet{}
	binds := make([]string, 0, len(spec.Volumes))
	for _, m := range spec.Volumes {
		if m.Source == "" {
			continue
		}
		binding := m.Source + ":" + m.Target
		if m.ReadOnly {
			binding += ":ro"
		}
		binds = append(binds, binding)
	}
	for p := range spec.Ports {
		exposed[nat.Port(fmt.Sprintf("%d/tcp", p))] = struct{}{}
	}
	for _, p := range spec.Exposed {
		exposed[nat.Port(fmt.Sprintf("%d/tcp", p))] = struct{}{}
	}

	portBindings := nat.PortMap{}
	for cPort, hPort := range spec.Ports {
		if hPort <= 0 {
			continue
		}
		proto := nat.Port(fmt.Sprintf("%d/tcp", cPort))
		portBindings[proto] = []nat.PortBinding{{HostPort: fmt.Sprint(hPort), HostIP: "0.0.0.0"}}
	}

	resp, err := c.cli.ContainerCreate(ctx,
		&container.Config{
			Hostname:     spec.Name,
			Image:        spec.Image,
			Env:          spec.Env,
			Cmd:          spec.Cmd,
			Labels:       labels,
			ExposedPorts: exposed,
			Healthcheck:  spec.HealthCheck,
		},
		&container.HostConfig{
			Binds:         binds,
			PortBindings:  portBindings,
			RestartPolicy: container.RestartPolicy{Name: restartPolicy(spec.RestartPolicy)},
			Privileged:    spec.Privileged,
			ShmSize:       spec.ShmSize,
			Resources: container.Resources{
				Memory:   spec.MemoryLimit,
				NanoCPUs: int64(spec.CPULimit * 1e9),
			},
		},
		nil, nil, containerName(spec.Name),
	)
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}

	if len(spec.Networks) > 0 {
		for _, n := range spec.Networks {
			if err := c.cli.NetworkConnect(ctx, n, resp.ID, nil); err != nil {
				return resp.ID, fmt.Errorf("attach network %s: %w", n, err)
			}
		}
	}
	return resp.ID, nil
}

func restartPolicy(mode container.RestartPolicyMode) container.RestartPolicyMode {
	switch mode {
	case container.RestartPolicyAlways, container.RestartPolicyOnFailure, container.RestartPolicyUnlessStopped:
		return mode
	default:
		return container.RestartPolicyUnlessStopped
	}
}

func containerName(name string) string {
	return "meghify-" + sanitize(name)
}

// ManagedName is the container name the panel uses for a logical name: every
// container carries the meghify- prefix so the panel never mistakes a foreign
// container for one it manages.
func ManagedName(name string) string {
	return containerName(name)
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// StartContainer starts a created container.
func (c *Client) StartContainer(ctx context.Context, id string) error {
	if err := c.cli.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
		return fmt.Errorf("start container: %w", err)
	}
	return nil
}

// RenameContainer gives a container its final managed name. It is how a
// rolling deploy swaps the new container into the name the rest of the panel
// (terminal, logs, restart) looks up.
func (c *Client) RenameContainer(ctx context.Context, id, name string) error {
	if err := c.cli.ContainerRename(ctx, id, containerName(name)); err != nil {
		return fmt.Errorf("rename container: %w", err)
	}
	return nil
}

// StopContainer stops a container, waiting up to timeout for a clean exit.
func (c *Client) StopContainer(ctx context.Context, id string, timeout time.Duration) error {
	seconds := int(timeout.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	if err := c.cli.ContainerStop(ctx, id, container.StopOptions{Timeout: &seconds}); err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("stop container: %w", err)
	}
	return nil
}

// RestartContainer restarts a container.
func (c *Client) RestartContainer(ctx context.Context, id string, timeout time.Duration) error {
	seconds := int(timeout.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	if err := c.cli.ContainerRestart(ctx, id, container.StopOptions{Timeout: &seconds}); err != nil {
		return fmt.Errorf("restart container: %w", err)
	}
	return nil
}

// RemoveContainer deletes a container, forcing removal if still running.
func (c *Client) RemoveContainer(ctx context.Context, id string) error {
	if err := c.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true, RemoveVolumes: false}); err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("remove container: %w", err)
	}
	return nil
}

// ContainerByName resolves a container id by its managed name.
func (c *Client) ContainerByName(ctx context.Context, name string) (*ContainerInfo, error) {
	list, err := c.cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("name", containerName(name))),
	})
	if err != nil {
		return nil, err
	}
	for _, item := range list {
		for _, n := range item.Names {
			if strings.TrimPrefix(n, "/") == containerName(name) {
				return toContainerInfo(item), nil
			}
		}
	}
	return nil, nil
}

// ListContainers returns containers carrying a managed label. An empty
// labelKey skips the extra label filter.
func (c *Client) ListContainers(ctx context.Context, labelKey, labelValue string) ([]*ContainerInfo, error) {
	args := filters.NewArgs(filters.Arg("label", "meghify.managed=true"))
	if labelKey != "" {
		args.Add("label", labelKey+"="+labelValue)
	}
	list, err := c.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return nil, err
	}
	out := make([]*ContainerInfo, 0, len(list))
	for _, item := range list {
		out = append(out, toContainerInfo(item))
	}
	return out, nil
}

func toContainerInfo(item container.Summary) *ContainerInfo {
	ports := make([]string, 0, len(item.Ports))
	for _, p := range item.Ports {
		if p.PublicPort > 0 {
			ports = append(ports, fmt.Sprintf("%s:%d->%d/%s", p.IP, p.PublicPort, p.PrivatePort, p.Type))
			continue
		}
		ports = append(ports, fmt.Sprintf("%d/%s", p.PrivatePort, p.Type))
	}
	return &ContainerInfo{
		ID:      item.ID,
		Name:    strings.TrimPrefix(firstName(item.Names), "/"),
		Image:   item.Image,
		State:   item.State,
		Status:  item.Status,
		Labels:  item.Labels,
		Ports:   ports,
		Created: item.Created,
	}
}

func firstName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// InspectContainer returns full inspection data for a container.
func (c *Client) InspectContainer(ctx context.Context, id string) (*container.InspectResponse, error) {
	info, err := c.cli.ContainerInspect(ctx, id)
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNoSuchContainer
		}
		return nil, fmt.Errorf("inspect container: %w", err)
	}
	return &info, nil
}

// ContainerLogs streams a container's output. Follow blocks until the context ends.
func (c *Client) ContainerLogs(ctx context.Context, id string, tail string, follow bool) (io.ReadCloser, error) {
	reader, err := c.cli.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Timestamps: false,
		Tail:       tail,
	})
	if err != nil {
		return nil, fmt.Errorf("container logs: %w", err)
	}
	return reader, nil
}

// ExecOptions describes an interactive exec session.
type ExecOptions struct {
	ContainerID string
	Cmd         []string
	Rows        uint
	Cols        uint
}

// ExecSession is an interactive shell attached to a container.
type ExecSession struct {
	ID   string
	Conn io.ReadWriteCloser
}

// ExecInteractive creates an exec session and attaches a TTY.
func (c *Client) ExecInteractive(ctx context.Context, opts ExecOptions) (*ExecSession, error) {
	cmd := opts.Cmd
	if len(cmd) == 0 {
		cmd = []string{"/bin/sh"}
	}
	create, err := c.cli.ContainerExecCreate(ctx, opts.ContainerID, container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
		AttachStdin:  true,
		Tty:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("exec create: %w", err)
	}

	answer, err := c.cli.ContainerExecAttach(ctx, create.ID, container.ExecAttachOptions{Tty: true})
	if err != nil {
		return nil, fmt.Errorf("exec attach: %w", err)
	}

	resize := c.cli.ContainerExecResize
	if err := resize(ctx, create.ID, container.ResizeOptions{
		Height: opts.RowsOr(24),
		Width:  opts.ColsOr(80),
	}); err != nil {
		answer.Close()
		return nil, fmt.Errorf("exec resize: %w", err)
	}

	return &ExecSession{ID: create.ID, Conn: answer.Conn}, nil
}

// ResizeExec updates the TTY dimensions of an interactive exec session.
func (c *Client) ResizeExec(ctx context.Context, execID string, rows, cols uint) error {
	return c.cli.ContainerExecResize(ctx, execID, container.ResizeOptions{
		Height: rows,
		Width:  cols,
	})
}

// CopyToContainer streams a tar archive into a path inside a container. It is
// how build contexts reach helper containers without a shared filesystem.
func (c *Client) CopyToContainer(ctx context.Context, containerID, dstPath string, content io.Reader) error {
	if err := c.cli.CopyToContainer(ctx, containerID, dstPath, content, container.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("copy to container: %w", err)
	}
	return nil
}

// ExecResult is a running non-interactive exec with a stream of its output.
type ExecResult struct {
	Output io.ReadCloser

	client *Client
	execID string
}

// Wait blocks until the exec finishes and returns its exit code.
func (r *ExecResult) Wait(ctx context.Context) (int, error) {
	for {
		inspect, err := r.client.cli.ContainerExecInspect(ctx, r.execID)
		if err != nil {
			return -1, fmt.Errorf("exec inspect: %w", err)
		}
		if !inspect.Running {
			return inspect.ExitCode, nil
		}
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// RunExec runs a command in a container and streams its demultiplexed output.
// The returned reader must be drained; call Wait for the exit code.
func (c *Client) RunExec(ctx context.Context, containerID string, cmd, env []string) (*ExecResult, error) {
	create, err := c.cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd:          cmd,
		Env:          env,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return nil, fmt.Errorf("exec create: %w", err)
	}

	attach, err := c.cli.ContainerExecAttach(ctx, create.ID, container.ExecAttachOptions{})
	if err != nil {
		return nil, fmt.Errorf("exec attach: %w", err)
	}

	// The hijacked stream carries stdout and stderr with 8-byte frame headers;
	// stdcopy splits them and both are forwarded to the same pipe.
	reader, writer := io.Pipe()
	go func() {
		_, err := stdcopy.StdCopy(writer, writer, attach.Reader)
		attach.Close()
		writer.CloseWithError(err)
	}()

	return &ExecResult{Output: reader, client: c, execID: create.ID}, nil
}

// RowsOr returns the configured terminal height, defaulting to 24.
func (o ExecOptions) RowsOr(def uint) uint {
	if o.Rows == 0 {
		return def
	}
	return o.Rows
}

// ColsOr returns the configured terminal width, defaulting to 80.
func (o ExecOptions) ColsOr(def uint) uint {
	if o.Cols == 0 {
		return def
	}
	return o.Cols
}

// --- images ---

// PullImage pulls an image and streams progress lines to the writer.
func (c *Client) PullImage(ctx context.Context, ref string, sink io.Writer) error {
	reader, err := c.cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull %s: %w", ref, err)
	}
	defer reader.Close()
	if sink == nil {
		sink = io.Discard
	}
	if _, err := readBuildStream(reader, sink); err != nil {
		return fmt.Errorf("pull stream %s: %w", ref, err)
	}
	return nil
}

// PullImageRetry pulls an image, retrying once on failure. Engines with the
// containerd image store (Docker 29's default) occasionally abort the final
// layer commit of a pull ("failed commit on ref … no such file"); the retry
// finds every blob already in the store and completes in seconds.
func (c *Client) PullImageRetry(ctx context.Context, ref string, sink io.Writer) error {
	err := c.PullImage(ctx, ref, sink)
	if err == nil {
		return nil
	}
	if sink != nil {
		sink.Write([]byte("[!] " + ref + " pull failed, retrying once: " + err.Error() + "\n"))
	}
	return c.PullImage(ctx, ref, sink)
}

// ListImages returns images present on the node.
func (c *Client) ListImages(ctx context.Context) ([]image.Summary, error) {
	list, err := c.cli.ImageList(ctx, image.ListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	return list, nil
}

// RemoveImage deletes an image from the node.
func (c *Client) RemoveImage(ctx context.Context, ref string) error {
	if _, err := c.cli.ImageRemove(ctx, ref, image.RemoveOptions{Force: false, PruneChildren: true}); err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("remove image: %w", err)
	}
	return nil
}

// ImageExists reports whether a reference exists locally.
func (c *Client) ImageExists(ctx context.Context, ref string) (bool, error) {
	_, _, err := c.cli.ImageInspectWithRaw(ctx, ref)
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// LoadImage imports an image tar (optionally gzipped) into the node's image
// store. The build sandbox uses it to copy the image it built into the engine
// that runs the application.
func (c *Client) LoadImage(ctx context.Context, input io.Reader) error {
	response, err := c.cli.ImageLoad(ctx, input)
	if err != nil {
		return fmt.Errorf("load image: %w", err)
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		return fmt.Errorf("read load response: %w", err)
	}
	return nil
}

// PruneImages removes dangling images. Returns bytes reclaimed.
func (c *Client) PruneImages(ctx context.Context) (int64, error) {
	report, err := c.cli.ImagesPrune(ctx, filters.NewArgs(filters.Arg("dangling", "true")))
	if err != nil {
		return 0, fmt.Errorf("prune images: %w", err)
	}
	return int64(report.SpaceReclaimed), nil
}

// --- networks ---

// EnsureNetwork creates a bridge network if it does not exist and returns its id.
func (c *Client) EnsureNetwork(ctx context.Context, name string, internal bool) (string, error) {
	args := filters.NewArgs(filters.Arg("name", name))
	list, err := c.cli.NetworkList(ctx, network.ListOptions{Filters: args})
	if err != nil {
		return "", fmt.Errorf("list networks: %w", err)
	}
	for _, n := range list {
		if n.Name == name {
			return n.ID, nil
		}
	}

	resp, err := c.cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver:   "bridge",
		Internal: internal,
		Labels:   map[string]string{"meghify.managed": "true"},
	})
	if err != nil {
		return "", fmt.Errorf("create network: %w", err)
	}
	if resp.Warning != "" {
		// Warnings are common and non-fatal (overlapping subnets).
		_ = resp.Warning
	}
	return resp.ID, nil
}

// RemoveNetwork deletes a network by name; a missing network is not an error.
func (c *Client) RemoveNetwork(ctx context.Context, name string) error {
	if err := c.cli.NetworkRemove(ctx, name); err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("remove network: %w", err)
	}
	return nil
}

// DiskUsage reports total bytes used by images, containers and volumes.
func (c *Client) DiskUsage(ctx context.Context) (int64, error) {
	usage, err := c.cli.DiskUsage(ctx, types.DiskUsageOptions{})
	if err != nil {
		return 0, fmt.Errorf("disk usage: %w", err)
	}

	// LayersSize is the engine's own accounting of image bytes on disk, which is
	// what matters for capacity planning. Containers and volumes are bounded by
	// the images they come from, so they are not double counted here.
	return usage.LayersSize + usage.BuilderSize, nil
}

// Close releases the underlying HTTP connections.
func (c *Client) Close() error { return c.cli.Close() }

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "No such") ||
		strings.Contains(err.Error(), "not found") ||
		strings.Contains(err.Error(), "404")
}
