package deploy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/docker"
)

// GitCredentialResolver returns a credential for a repository URL, if configured.
type GitCredentialResolver func(repository string) (username, token string, ok bool)

// runDockerfile builds from an explicit Dockerfile and swaps the container.
func (p *Pipeline) runDockerfile(ctx context.Context, sink io.Writer, app *db.Application, payload Payload) error {
	workspace, err := p.prepareWorkspace(ctx, sink, app)
	if err != nil {
		return err
	}

	imageRef := p.imageReference(app)
	sink.Write([]byte("[1/4] building " + imageRef + "\n"))

	buildArgs, err := p.buildArgs(ctx, app)
	if err != nil {
		return err
	}
	if _, err := p.client.Build(ctx, docker.BuildRequest{
		ContextDir: workspace,
		Dockerfile: filepath.Join(app.BaseDirectory, app.Dockerfile),
		Tags:       []string{imageRef},
		BuildArgs:  buildArgs,
		Labels: map[string]string{
			"meghify.application": app.ID,
			"meghify.environment": app.EnvironmentID,
		},
		NoCache: payload.NoCache,
		Pull:    payload.NoCache,
	}, sink); err != nil {
		return fmt.Errorf("build: %w", err)
	}

	if p.registry.URL != "" && p.registry.User != "" {
		sink.Write([]byte("[2/4] pushing " + imageRef + "\n"))
		if err := p.client.PushImage(ctx, imageRef, p.registryAuthHeader(), sink); err != nil {
			return fmt.Errorf("push: %w", err)
		}
	} else {
		sink.Write([]byte("[2/4] skipped push (no registry configured)\n"))
	}

	return p.release(ctx, sink, app, imageRef)
}

// runAutoBuild generates a Dockerfile with a buildpack then builds it.
//
// Nixpacks and Railpack both emit a Dockerfile from source detection. Running
// them keeps the panel small: no buildpack engine is linked into the binary.
func (p *Pipeline) runAutoBuild(ctx context.Context, sink io.Writer, app *db.Application, payload Payload) error {
	workspace, err := p.prepareWorkspace(ctx, sink, app)
	if err != nil {
		return err
	}

	binary := "nixpacks"
	if app.BuildPack == db.BuildRailpack {
		binary = "railpack"
	}
	if _, err := exec.LookPath(binary); err != nil {
		return fmt.Errorf("%s is not installed on this node", binary)
	}

	cmd := exec.CommandContext(ctx, binary, "build", ".", "--name", p.imageReference(app))
	cmd.Dir = workspace
	cmd.Stdout = sink
	cmd.Stderr = sink
	cmd.Env = append(os.Environ(), "NIXPACKS_PATH="+workspace)
	sink.Write([]byte("[1/4] running " + binary + "\n"))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s build: %w", binary, err)
	}

	return p.release(ctx, sink, app, p.imageReference(app))
}

// runImage pulls a prebuilt image and releases it.
func (p *Pipeline) runImage(ctx context.Context, sink io.Writer, app *db.Application) error {
	imageRef := app.GitRepository
	if imageRef == "" {
		return fmt.Errorf("applications with build pack %q need gitRepository to hold the image reference", db.BuildImage)
	}
	sink.Write([]byte("[1/4] pulling " + imageRef + "\n"))
	if err := p.client.PullImage(ctx, imageRef, sink); err != nil {
		return err
	}
	return p.release(ctx, sink, app, imageRef)
}

// runCompose deploys a Docker Compose stack through the compose CLI.
func (p *Pipeline) runCompose(ctx context.Context, sink io.Writer, app *db.Application) error {
	if strings.TrimSpace(app.Dockerfile) == "" {
		return fmt.Errorf("applications with build pack %q must store the compose definition in the dockerfile field", db.BuildCompose)
	}
	workspace, err := p.prepareWorkspace(ctx, sink, app)
	if err != nil {
		return err
	}

	composePath := filepath.Join(workspace, "meghify-compose.yaml")
	if err := os.WriteFile(composePath, []byte(app.Dockerfile), 0o644); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "docker", "compose", "-p", sanitizeName(app.Name), "-f", composePath, "up", "-d", "--remove-orphans")
	cmd.Dir = workspace
	cmd.Stdout = sink
	cmd.Stderr = sink
	sink.Write([]byte("[1/3] applying compose stack\n"))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("compose up: %w", err)
	}
	sink.Write([]byte("[ok] compose stack applied\n"))
	return nil
}

// release starts the new container and retires the previous one only after the
// replacement is healthy.
func (p *Pipeline) release(ctx context.Context, sink io.Writer, app *db.Application, imageRef string) error {
	runtimeEnv, err := p.envList(ctx, app.ID, false)
	if err != nil {
		return err
	}

	domains, err := p.store.Applications.DomainsByApplication(ctx, app.ID)
	if err != nil {
		return err
	}

	volumes, err := p.store.Applications.VolumesByTarget(ctx, db.TargetApplication, app.ID)
	if err != nil {
		return err
	}

	sink.Write([]byte("[3/4] creating container\n"))
	containerID, err := p.client.CreateContainer(ctx, &docker.ContainerSpec{
		Name:          app.Name,
		Image:         imageRef,
		Env:           runtimeEnv,
		Labels:        proxyLabels(app, domains),
		Ports:         map[int]int{app.Port: 0},
		Exposed:       []int{app.Port},
		Volumes:       toMounts(volumes),
		Networks:      []string{networkName(app)},
		RestartPolicy: "unless-stopped",
		HealthCheck:   healthCheckFor(app),
	})
	if err != nil {
		return err
	}

	if _, err := p.client.EnsureNetwork(ctx, networkName(app), false); err != nil {
		sink.Write([]byte("[!] network warning: " + err.Error() + "\n"))
	}

	sink.Write([]byte("[4/4] starting container " + shortID(containerID) + "\n"))
	if err := p.client.StartContainer(ctx, containerID); err != nil {
		p.client.RemoveContainer(ctx, containerID)
		return fmt.Errorf("start container: %w", err)
	}

	if app.HealthCheck != "" {
		if err := p.waitHealthy(ctx, containerID, 90*time.Second, sink); err != nil {
			sink.Write([]byte("[!] health check failed, reverting to previous container\n"))
			p.client.StopContainer(ctx, containerID, 10*time.Second)
			p.client.RemoveContainer(ctx, containerID)
			return err
		}
		sink.Write([]byte("[ok] container is healthy\n"))
	}

	previous, err := p.client.ContainerByName(ctx, app.Name)
	if err != nil {
		sink.Write([]byte("[!] could not inspect previous container: " + err.Error() + "\n"))
	} else if previous != nil && previous.ID != containerID {
		sink.Write([]byte("[ok] retiring previous container\n"))
		p.client.StopContainer(ctx, previous.ID, 20*time.Second)
		p.client.RemoveContainer(ctx, previous.ID)
	}

	p.store.Applications.PatchApplicationStatus(ctx, app.ID, db.StatusRunning)
	return nil
}

// waitHealthy polls a container until its healthcheck reports healthy.
func (p *Pipeline) waitHealthy(ctx context.Context, containerID string, timeout time.Duration, sink io.Writer) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		info, err := p.client.InspectContainer(ctx, containerID)
		if err != nil {
			return err
		}
		if info.State == nil {
			return fmt.Errorf("container vanished during start")
		}
		if info.State.Health != nil {
			switch info.State.Health.Status {
			case "healthy":
				return nil
			case "unhealthy":
				return fmt.Errorf("container health check reported unhealthy")
			}
		} else if info.State.Running {
			// No healthcheck configured: a running container is good enough.
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("container did not become healthy within %s", timeout)
}

// prepareWorkspace clones or refreshes the application's source tree and
// returns the directory that the build context should be sent from.
func (p *Pipeline) prepareWorkspace(ctx context.Context, sink io.Writer, app *db.Application) (string, error) {
	root := filepath.Join(p.dataDir, "workspaces", app.ID)
	workdir := filepath.Join(root, "source")

	baseDir := strings.TrimSpace(app.BaseDirectory)
	if baseDir == "" || baseDir == "/" {
		baseDir = ""
	} else {
		baseDir = strings.Trim(baseDir, "/")
	}
	if baseDir != "" {
		workdir = filepath.Join(workdir, baseDir)
	}

	if app.GitRepository == "" {
		// Image deployments have no source; build from an empty directory.
		if err := os.MkdirAll(filepath.Join(workdir, ".stub"), 0o755); err != nil {
			return "", err
		}
		return workdir, nil
	}

	if _, err := os.Stat(workdir); os.IsNotExist(err) {
		sink.Write([]byte("[0/4] cloning " + app.GitRepository + " (" + app.GitBranch + ")\n"))
		if err := p.gitClone(ctx, sink, app, workdir); err != nil {
			return "", err
		}
	} else {
		sink.Write([]byte("[0/4] updating source\n"))
		if err := p.gitFetch(ctx, sink, app, workdir); err != nil {
			sink.Write([]byte("[!] fetch failed, recloning\n"))
			os.RemoveAll(workdir)
			if err := p.gitClone(ctx, sink, app, workdir); err != nil {
				return "", err
			}
		}
	}

	// Build in a copy so a failed run cannot corrupt the cache.
	staging := filepath.Join(root, "build")
	os.RemoveAll(staging)
	if err := copyDir(workdir, staging); err != nil {
		return "", fmt.Errorf("stage build context: %w", err)
	}
	return staging, nil
}

func (p *Pipeline) gitClone(ctx context.Context, sink io.Writer, app *db.Application, workdir string) error {
	if err := os.MkdirAll(filepath.Dir(workdir), 0o755); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1",
		"--branch", valueOrDefault(app.GitBranch, "main"), p.cloneURL(app), workdir)
	cmd.Stdout, cmd.Stderr = sink, sink
	return cmd.Run()
}

func (p *Pipeline) gitFetch(ctx context.Context, sink io.Writer, app *db.Application, workdir string) error {
	fetch := exec.CommandContext(ctx, "git", "fetch", "origin", valueOrDefault(app.GitBranch, "main"))
	fetch.Dir = workdir
	fetch.Stdout, fetch.Stderr = sink, sink
	if err := fetch.Run(); err != nil {
		return err
	}

	reset := exec.CommandContext(ctx, "git", "reset", "--hard", "FETCH_HEAD")
	reset.Dir = workdir
	reset.Stdout, reset.Stderr = sink, sink
	return reset.Run()
}

// cloneURL injects credentials for private repositories.
func (p *Pipeline) cloneURL(app *db.Application) string {
	if p.git == nil {
		return app.GitRepository
	}
	user, token, ok := p.git(app.GitRepository)
	if !ok || token == "" {
		return app.GitRepository
	}
	if !strings.Contains(app.GitRepository, "://") {
		return fmt.Sprintf("https://%s:%s@%s", user, token, app.GitRepository)
	}
	return strings.Replace(app.GitRepository, "://", "://"+user+":"+token+"@", 1)
}

// imageReference computes the tag used for a build.
func (p *Pipeline) imageReference(app *db.Application) string {
	registry := strings.TrimSuffix(p.registry.URL, "/")
	if registry == "" {
		registry = "meghify.local"
	}
	short := app.ID[:8]
	return fmt.Sprintf("%s/%s:%s-%d", registry, sanitizeName(app.Name), short, time.Now().Unix())
}

// registryAuthHeader builds the X-Registry-Auth payload for a push.
func (p *Pipeline) registryAuthHeader() string {
	if p.registry.User == "" {
		return ""
	}
	payload, _ := json.Marshal(map[string]string{
		"username": p.registry.User,
		"password": p.registry.Pass,
	})
	return base64.URLEncoding.EncodeToString(payload)
}

// buildArgs assembles the image build argument list.
func (p *Pipeline) buildArgs(ctx context.Context, app *db.Application) (map[string]*string, error) {
	vars, err := p.envList(ctx, app.ID, true)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*string, len(vars)+1)
	for _, kv := range vars {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		value := parts[1]
		out[parts[0]] = &value
	}
	ref := p.imageReference(app)
	out["PASS_IMAGE_REF"] = &ref
	return out, nil
}

// envList returns KEY=VALUE entries for a resource. When buildOnly is true only
// build-time variables are returned.
func (p *Pipeline) envList(ctx context.Context, targetID string, buildOnly bool) ([]string, error) {
	vars, err := p.store.Applications.EnvVarsByTarget(ctx, db.TargetApplication, targetID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(vars))
	for _, v := range vars {
		if buildOnly != v.IsBuild {
			continue
		}
		plain, err := p.secrets.Decrypt(v.ValueEnc)
		if err != nil {
			return nil, fmt.Errorf("decrypt %s: %w", v.Key, err)
		}
		out = append(out, v.Key+"="+plain)
	}
	return out, nil
}

// toMounts converts stored volume bindings into docker mounts.
func toMounts(volumes []*db.Volume) []docker.Mount {
	out := make([]docker.Mount, 0, len(volumes))
	for _, v := range volumes {
		out = append(out, docker.Mount{Source: v.HostPath, Target: v.MountPath, ReadOnly: v.ReadOnly})
	}
	return out
}

func proxyLabels(app *db.Application, domains []*db.Domain) map[string]string {
	labels := map[string]string{
		"meghify.application": app.ID,
		"meghify.port":        fmt.Sprint(app.Port),
	}
	if len(domains) == 0 {
		return labels
	}

	rules := make([]string, 0, len(domains))
	for _, d := range domains {
		labels["meghify.domain."+d.Host] = d.Host
		if !d.TLSEnabled {
			labels["meghify.domain."+d.Host+".tls"] = "false"
		}
		rules = append(rules, "Host(`"+d.Host+"`)")
	}
	labels["traefik.enable"] = "true"
	labels["traefik.http.routers."+sanitizeName(app.Name)+".rule"] = "PathPrefix(`/`) && (" + strings.Join(rules, " || ") + ")"
	labels["traefik.http.services."+sanitizeName(app.Name)+".loadbalancer.server.port"] = fmt.Sprint(app.Port)
	return labels
}

func healthCheckFor(app *db.Application) *container.HealthConfig {
	if app.HealthCheck == "" {
		return nil
	}
	return &container.HealthConfig{
		Test:        []string{"CMD-SHELL", "curl -fsS http://127.0.0.1:" + fmt.Sprint(app.Port) + app.HealthCheck + " || exit 1"},
		Interval:    15 * time.Second,
		Timeout:     5 * time.Second,
		Retries:     3,
		StartPeriod: 20 * time.Second,
	}
}

func valueOrDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// copyDir recursively copies a directory tree.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case info.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case info.Mode().IsRegular():
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return copyFile(path, target, info.Mode())
		default:
			return nil
		}
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
