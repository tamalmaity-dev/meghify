package deploy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/docker"
)

// GitCredentialResolver mints a short-lived credential for an application that
// clones a private repository (for example through a GitHub App installation).
//
// A nil error with an empty token means "clone anonymously". An error aborts
// the build: retrying a private clone without credentials would only produce a
// confusing authentication failure.
type GitCredentialResolver func(app *db.Application) (username, token string, err error)

// runDockerfile builds from an explicit Dockerfile and swaps the container.
//
// The git clone and the image build run inside the helper container; hosts
// that carry git and the docker CLI themselves keep the panel-local path as a
// fallback when the helper image is unavailable.
func (p *Pipeline) runDockerfile(ctx context.Context, sink io.Writer, app *db.Application, payload Payload) error {
	imageRef := p.imageReference(app)

	if err := p.buildDockerfileInHelper(ctx, sink, app, imageRef, payload); err != nil {
		if !errors.Is(err, errHelperUnavailable) {
			return err
		}
		if hostErr := p.buildDockerfileOnHost(ctx, sink, app, imageRef, payload); hostErr != nil {
			return fmt.Errorf("no build helper available (%v) and the local build failed: %w", err, hostErr)
		}
	}

	if p.registry.URL != "" && p.registry.User != "" {
		sink.Write([]byte("[3/4] pushing " + imageRef + "\n"))
		if err := p.client.PushImage(ctx, imageRef, p.registryAuthHeader(), sink); err != nil {
			return fmt.Errorf("push: %w", err)
		}
	} else {
		sink.Write([]byte("[3/4] skipped push (no registry configured)\n"))
	}

	return p.release(ctx, sink, app, imageRef)
}

// buildDockerfileInHelper clones the repository and runs docker build inside
// the helper container.
func (p *Pipeline) buildDockerfileInHelper(ctx context.Context, sink io.Writer, app *db.Application, imageRef string, payload Payload) error {
	clone, secret, err := p.cloneURL(app)
	if err != nil {
		return err
	}
	build, err := p.newBuilder(ctx, sink, app, secret)
	if err != nil {
		return err
	}
	defer build.close(ctx)

	branch := valueOrDefault(app.GitBranch, "main")
	sink.Write([]byte("[1/4] cloning " + redactText(clone, secret) + " (" + branch + ")\n"))
	if err := build.clone(ctx, clone, branch); err != nil {
		return fmt.Errorf("clone: %w", err)
	}

	dockerfile := strings.TrimPrefix(strings.TrimSpace(app.Dockerfile), "/")
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}

	buildArgs, err := p.buildArgs(ctx, app)
	if err != nil {
		return err
	}

	// Secrets stay out of the process arguments: values are exported inside
	// the script and referenced by name with --build-arg.
	var script strings.Builder
	script.WriteString("set -e\n")
	script.WriteString("cd " + shellQuote(builderContextDir(app)) + "\n")
	for name, value := range buildArgs {
		if !validEnvName(name) {
			continue
		}
		script.WriteString("export " + name + "=" + shellQuote(*value) + "\n")
	}
	script.WriteString("docker build --progress=plain -f " + shellQuote(dockerfile) + " -t " + shellQuote(imageRef))
	if payload.NoCache {
		script.WriteString(" --no-cache --pull")
	}
	script.WriteString(" --label " + shellQuote("meghify.application="+app.ID))
	script.WriteString(" --label " + shellQuote("meghify.environment="+app.EnvironmentID))
	for name := range buildArgs {
		if !validEnvName(name) {
			continue
		}
		script.WriteString(" --build-arg " + name)
	}
	script.WriteString(" .\n")
	if err := build.writeFile(ctx, "/workspace/build.sh", script.String()); err != nil {
		return err
	}

	sink.Write([]byte("[2/4] building " + imageRef + "\n"))
	return build.run(ctx, "sh", "/workspace/build.sh")
}

// buildDockerfileOnHost is the panel-local fallback: clone onto the panel's
// filesystem and let the engine build from that context.
func (p *Pipeline) buildDockerfileOnHost(ctx context.Context, sink io.Writer, app *db.Application, imageRef string, payload Payload) error {
	workspace, err := p.prepareWorkspace(ctx, sink, app)
	if err != nil {
		return err
	}
	sink.Write([]byte("[2/4] building " + imageRef + "\n"))

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
	return nil
}

// runAutoBuild generates a Dockerfile with a buildpack then builds it.
//
// Nixpacks and Railpack both emit a Dockerfile from source detection. They run
// inside the helper container so the panel ships neither engine; see newBuilder.
func (p *Pipeline) runAutoBuild(ctx context.Context, sink io.Writer, app *db.Application, payload Payload) error {
	imageRef := p.imageReference(app)

	if err := p.buildAutoInHelper(ctx, sink, app, imageRef); err != nil {
		if !errors.Is(err, errHelperUnavailable) {
			return err
		}
		if hostErr := p.buildAutoOnHost(ctx, sink, app, imageRef); hostErr != nil {
			return fmt.Errorf("no build helper available (%v) and the local build failed: %w", err, hostErr)
		}
	}

	if p.registry.URL != "" && p.registry.User != "" {
		sink.Write([]byte("[3/4] pushing " + imageRef + "\n"))
		if err := p.client.PushImage(ctx, imageRef, p.registryAuthHeader(), sink); err != nil {
			return fmt.Errorf("push: %w", err)
		}
	} else {
		sink.Write([]byte("[3/4] skipped push (no registry configured)\n"))
	}

	return p.release(ctx, sink, app, imageRef)
}

// buildAutoInHelper clones the repository and runs nixpacks or railpack inside
// the helper container.
func (p *Pipeline) buildAutoInHelper(ctx context.Context, sink io.Writer, app *db.Application, imageRef string) error {
	clone, secret, err := p.cloneURL(app)
	if err != nil {
		return err
	}
	build, err := p.newBuilder(ctx, sink, app, secret)
	if err != nil {
		return err
	}
	defer build.close(ctx)

	branch := valueOrDefault(app.GitBranch, "main")
	sink.Write([]byte("[1/4] cloning " + redactText(clone, secret) + " (" + branch + ")\n"))
	if err := build.clone(ctx, clone, branch); err != nil {
		return fmt.Errorf("clone: %w", err)
	}

	binary := "nixpacks"
	if app.BuildPack == db.BuildRailpack {
		binary = "railpack"
	}
	sink.Write([]byte("[2/4] building with " + binary + "\n"))
	if err := build.run(ctx, binary, "build", builderContextDir(app), "--name", imageRef); err != nil {
		return fmt.Errorf("%s build: %w", binary, err)
	}
	return nil
}

// buildAutoOnHost is the panel-local fallback and needs the buildpack binary
// installed on the host that runs the panel.
func (p *Pipeline) buildAutoOnHost(ctx context.Context, sink io.Writer, app *db.Application, imageRef string) error {
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

	cmd := exec.CommandContext(ctx, binary, "build", ".", "--name", imageRef)
	cmd.Dir = workspace
	cmd.Stdout = sink
	cmd.Stderr = sink
	sink.Write([]byte("[2/4] running " + binary + "\n"))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s build: %w", binary, err)
	}
	return nil
}

// runImage pulls a prebuilt image and releases it.
func (p *Pipeline) runImage(ctx context.Context, sink io.Writer, app *db.Application) error {
	imageRef := app.GitRepository
	if imageRef == "" {
		return fmt.Errorf("applications with build pack %q need gitRepository to hold the image reference", db.BuildImage)
	}
	sink.Write([]byte("[1/4] pulling " + imageRef + "\n"))
	if err := p.client.PullImageRetry(ctx, imageRef, sink); err != nil {
		return err
	}
	return p.release(ctx, sink, app, imageRef)
}

// runCompose deploys a Docker Compose stack through the compose CLI. It runs
// inside the helper container (which carries the compose plugin), falling back
// to the panel host's own compose for installs without a helper.
func (p *Pipeline) runCompose(ctx context.Context, sink io.Writer, app *db.Application) error {
	if strings.TrimSpace(app.Dockerfile) == "" {
		return fmt.Errorf("applications with build pack %q must store the compose definition in the dockerfile field", db.BuildCompose)
	}

	if err := p.composeInHelper(ctx, sink, app); err != nil {
		if !errors.Is(err, errHelperUnavailable) {
			return err
		}
		return p.composeOnHost(ctx, sink, app)
	}
	return nil
}

func (p *Pipeline) composeInHelper(ctx context.Context, sink io.Writer, app *db.Application) error {
	build, err := p.newBuilder(ctx, sink, app, "")
	if err != nil {
		return err
	}
	defer build.close(ctx)

	sink.Write([]byte("[1/2] applying compose stack\n"))
	if err := build.writeFile(ctx, "/workspace/compose.yaml", app.Dockerfile); err != nil {
		return err
	}
	command := "cd /workspace && docker compose -p " + shellQuote(sanitizeName(app.Name)) +
		" -f /workspace/compose.yaml up -d --remove-orphans"
	if err := build.run(ctx, "sh", "-c", command); err != nil {
		return fmt.Errorf("compose up: %w", err)
	}
	sink.Write([]byte("[ok] compose stack applied\n"))
	return nil
}

func (p *Pipeline) composeOnHost(ctx context.Context, sink io.Writer, app *db.Application) error {
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
// replacement is healthy. The new container is created under a temporary name
// (the previous one still owns the final name), then renamed once the old
// container is gone — the same rolling pattern, and what makes
// redeploys work.
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

	if len(domains) > 0 {
		addresses := make([]string, 0, len(domains))
		for _, domain := range domains {
			scheme := "https"
			if !domain.TLSEnabled {
				scheme = "http"
			}
			addresses = append(addresses, scheme+"://"+domain.Host)
		}
		sink.Write([]byte("[*] domains: " + strings.Join(addresses, ", ") + "\n"))
	}

	// The app's private network must exist before the container is created:
	// the create call attaches it. Applications with domains also join the
	// shared proxy network, where the reverse proxy routes to them.
	if _, err := p.client.EnsureNetwork(ctx, networkName(app), false); err != nil {
		return fmt.Errorf("ensure network: %w", err)
	}
	networks := []string{networkName(app)}
	if len(domains) > 0 && p.proxyImage != "" {
		if err := p.ensureProxy(ctx, sink); err != nil {
			sink.Write([]byte("[!] proxy unavailable, domain routing may not work: " + err.Error() + "\n"))
		}
		networks = append(networks, proxyNetwork)
	}

	// Find the live container once; the replacement is created under a
	// temporary name, so this lookup only ever sees the previous one.
	previous, err := p.client.ContainerByName(ctx, app.Name)
	if err != nil {
		sink.Write([]byte("[!] could not inspect previous container: " + err.Error() + "\n"))
		previous = nil
	}
	if app.PreDeployCommand != "" && previous != nil {
		sink.Write([]byte("[*] pre-deployment: " + app.PreDeployCommand + "\n"))
		if err := p.runContainerCommand(ctx, sink, previous.ID, app.PreDeployCommand); err != nil {
			return fmt.Errorf("pre-deployment command failed: %w", err)
		}
	}

	sink.Write([]byte("[4/4] creating container\n"))
	ports := map[int]int{app.Port: 0}
	publishedPorts := false
	if mappings, err := parseMappings(app.PortMappings); err != nil {
		sink.Write([]byte("[!] ignoring port mappings: " + err.Error() + "\n"))
	} else {
		for containerPort, hostPort := range mappings {
			ports[containerPort] = hostPort
			publishedPorts = true
		}
	}
	containerID, err := p.client.CreateContainer(ctx, &docker.ContainerSpec{
		Name:          fmt.Sprintf("%s-%d", app.Name, time.Now().Unix()),
		Image:         imageRef,
		Env:           runtimeEnv,
		Labels:        ProxyLabels(app, domains),
		Ports:         ports,
		Exposed:       []int{app.Port},
		Volumes:       toMounts(volumes),
		Networks:      networks,
		RestartPolicy: "unless-stopped",
		HealthCheck:   healthCheckFor(app),
		MemoryLimit:   app.MemoryLimit,
		CPULimit:      app.CPULimit,
	})
	if err != nil {
		return err
	}

	// Host ports cannot be bound twice, so an application that publishes one
	// must retire the old container before the replacement starts. Everything
	// else swaps without downtime: start and health-check first, retire after.
	if publishedPorts && previous != nil {
		sink.Write([]byte("[ok] releasing published ports from the previous container\n"))
		p.client.StopContainer(ctx, previous.ID, 20*time.Second)
		p.client.RemoveContainer(ctx, previous.ID)
		previous = nil
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

	if previous != nil && previous.ID != containerID {
		sink.Write([]byte("[ok] retiring previous container\n"))
		p.client.StopContainer(ctx, previous.ID, 20*time.Second)
		p.client.RemoveContainer(ctx, previous.ID)
	}

	// Only one container may carry the application's name; the new one takes
	// it after the old one is gone.
	if err := p.client.RenameContainer(ctx, containerID, app.Name); err != nil {
		sink.Write([]byte("[!] could not rename container: " + err.Error() + "\n"))
	}

	if app.PostDeployCommand != "" {
		sink.Write([]byte("[*] post-deployment: " + app.PostDeployCommand + "\n"))
		if err := p.runContainerCommand(ctx, sink, containerID, app.PostDeployCommand); err != nil {
			return fmt.Errorf("post-deployment command failed: %w", err)
		}
	}

	p.store.Applications.PatchApplicationImage(ctx, app.ID, imageRef)
	p.store.Applications.PatchApplicationStatus(ctx, app.ID, db.StatusRunning)
	return nil
}

// runContainerCommand executes a shell command inside a container and streams
// its output to the deployment log.
func (p *Pipeline) runContainerCommand(ctx context.Context, sink io.Writer, containerID, command string) error {
	result, err := p.client.RunExec(ctx, containerID, []string{"sh", "-c", command}, nil)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(sink, result.Output)
	result.Output.Close()
	if copyErr != nil {
		return copyErr
	}
	code, err := result.Wait(ctx)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("command exited with code %d", code)
	}
	return nil
}

// parseMappings splits the stored "host:container" list into the container →
// host map the container spec expects. Entries are validated on write; a
// malformed leftover is skipped with a warning in the deployment log.
func parseMappings(raw string) (map[int]int, error) {
	mappings := map[int]int{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return mappings, nil
	}
	for _, entry := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		hostRaw, containerRaw, found := strings.Cut(entry, ":")
		if !found {
			return nil, fmt.Errorf("port mapping %q is not hostPort:containerPort", entry)
		}
		host, hostErr := strconv.Atoi(strings.TrimSpace(hostRaw))
		container, containerErr := strconv.Atoi(strings.TrimSpace(containerRaw))
		if hostErr != nil || containerErr != nil || host < 1 || host > 65535 || container < 1 || container > 65535 {
			return nil, fmt.Errorf("port mapping %q has ports outside 1-65535", entry)
		}
		mappings[container] = host
	}
	return mappings, nil
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
	clone, secret, err := p.cloneURL(app)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1",
		"--branch", valueOrDefault(app.GitBranch, "main"), clone, workdir)
	out, flush := redactedWriter(sink, secret)
	cmd.Stdout, cmd.Stderr = out, out
	err = cmd.Run()
	flush()
	if err != nil {
		return err
	}
	// The credential was minted for this clone and expires within the hour.
	// Keep it out of .git/config so a later fetch cannot pick up a stale token.
	if clone != app.GitRepository {
		scrub := exec.CommandContext(ctx, "git", "remote", "set-url", "origin", app.GitRepository)
		scrub.Dir = workdir
		scrub.Stdout, scrub.Stderr = sink, sink
		if err := scrub.Run(); err != nil {
			sink.Write([]byte("[!] could not strip credentials from origin\n"))
		}
	}
	return nil
}

func (p *Pipeline) gitFetch(ctx context.Context, sink io.Writer, app *db.Application, workdir string) error {
	remote, secret, err := p.cloneURL(app)
	if err != nil {
		return err
	}
	fetch := exec.CommandContext(ctx, "git", "fetch", "--depth", "1", remote, valueOrDefault(app.GitBranch, "main"))
	fetch.Dir = workdir
	out, flush := redactedWriter(sink, secret)
	fetch.Stdout, fetch.Stderr = out, out
	err = fetch.Run()
	flush()
	if err != nil {
		return err
	}

	reset := exec.CommandContext(ctx, "git", "reset", "--hard", "FETCH_HEAD")
	reset.Dir = workdir
	reset.Stdout, reset.Stderr = sink, sink
	return reset.Run()
}

// redactedWriter wraps a sink so a credential embedded in command output is
// never written to the deployment log. The returned flush must be called after
// the command finishes.
func redactedWriter(sink io.Writer, secret string) (io.Writer, func()) {
	if secret == "" {
		return sink, func() {}
	}
	redactor := &redactWriter{w: sink, secret: secret}
	return redactor, redactor.Flush
}

// cloneURL injects credentials for private repositories. The credential is
// minted fresh for every git operation: GitHub App installation tokens live for
// one hour, so a token embedded at clone time would be expired by the next
// deployment. The secret is returned separately so it can be redacted from
// logs.
func (p *Pipeline) cloneURL(app *db.Application) (clone, secret string, err error) {
	if p.git == nil {
		return app.GitRepository, "", nil
	}
	user, token, err := p.git(app)
	if err != nil {
		return "", "", err
	}
	if token == "" {
		return app.GitRepository, "", nil
	}
	if !strings.Contains(app.GitRepository, "://") {
		return fmt.Sprintf("https://%s:%s@%s", user, token, app.GitRepository), token, nil
	}
	return strings.Replace(app.GitRepository, "://", "://"+user+":"+token+"@", 1), token, nil
}

// builderContextDir is where the build runs inside the helper: the repository
// root, or the configured base directory inside it.
func builderContextDir(app *db.Application) string {
	base := strings.Trim(strings.TrimSpace(app.BaseDirectory), "/")
	if base == "" {
		return "/workspace/src"
	}
	return "/workspace/src/" + base
}

// validEnvName reports whether a name can be exported in a shell script and is
// accepted by --build-arg.
func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for index, r := range name {
		switch {
		case r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'):
		case r >= '0' && r <= '9' && index > 0:
		default:
			return false
		}
	}
	return true
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

// ProxyLabels builds the container labels the proxy (and the panel itself)
// reads: the traefik router wiring for every attached domain, plus the
// meghify bookkeeping labels. Exported so the API can show them read-only.
func ProxyLabels(app *db.Application, domains []*db.Domain) map[string]string {
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

// valueOrDefault returns def when s is zero or whitespace.
func valueOrDefault[T int | string](value T, def T) T {
	var zero T
	switch any(value).(type) {
	case string:
		if strings.TrimSpace(any(value).(string)) == "" {
			return def
		}
	default:
		if value == zero {
			return def
		}
	}
	return value
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
