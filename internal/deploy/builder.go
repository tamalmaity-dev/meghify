package deploy

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/docker"
)

// errHelperUnavailable reports that the build helper container could not be
// provisioned (no image configured, pull failed, engine refused). Deployment
// code falls back to running the tools on the panel host, which older installs
// may still carry.
var errHelperUnavailable = errors.New("build helper unavailable")

// builder runs git, buildpack and compose commands inside a disposable helper
// container on the target engine.
//
// This mirrors Coolify's model: the panel itself stays minimal (its image is
// distroless and carries neither git nor nixpacks), while the helper image —
// git, nixpacks, railpack, the docker CLI — reaches the same engine through
// the mounted socket. One container serves one deployment, then is removed, so
// no credential or half-written workspace outlives the build.
type builder struct {
	client *docker.Client
	sink   io.Writer
	id     string

	// sandboxed marks that the helper builds against the isolated build
	// daemon; the resulting image then has to be imported into the host
	// engine before the release step can use it.
	sandboxed bool

	// deploymentID links the helper to its deployment so a cancellation can
	// tear the container down.
	deploymentID string
	pipeline     *Pipeline

	// secret is redacted from every line of command output: git and docker
	// occasionally echo the credential embedded in a clone URL.
	secret string
}

// newBuilder pulls the helper image if needed, then creates and starts the
// container for one deployment. Builds run against the isolated build sandbox
// when it is available (or when BUILD_SANDBOX=on); the host socket fallback
// exists for engines where nested Docker is not permitted.
func (p *Pipeline) newBuilder(ctx context.Context, sink io.Writer, app *db.Application, secret, deploymentID string, hostSocket bool) (*builder, error) {
	if p.helperImage == "" || p.client == nil {
		return nil, errHelperUnavailable
	}

	if ok, _ := p.client.ImageExists(ctx, p.helperImage); !ok {
		sink.Write([]byte("[*] pulling the build helper image " + p.helperImage + "\n"))
		if err := p.client.PullImageRetry(ctx, p.helperImage, sink); err != nil {
			return nil, fmt.Errorf("%w: pull %s: %v", errHelperUnavailable, p.helperImage, err)
		}
	}

	short := app.ID
	if len(short) > 8 {
		short = short[:8]
	}
	name := fmt.Sprintf("builder-%s-%d", short, time.Now().Unix())

	spec := &docker.ContainerSpec{
		Name:        name,
		Image:       p.helperImage,
		Cmd:         []string{"sh", "-c", "while true; do sleep 3600; done"},
		Labels:      map[string]string{"meghify.role": "build-helper"},
		MemoryLimit: p.buildMemoryLimit,
		CPULimit:    p.buildCPULimit,
	}
	sandboxed := false

	// Build packs and Dockerfile builds work against the sandbox. Compose
	// stacks keep the host socket: `docker compose up` has to create the
	// application containers on the engine that runs them.
	if !hostSocket && p.sandboxMode != "off" {
		sandbox, err := p.ensureSandbox(ctx, sink)
		switch {
		case err == nil:
			spec.Env = append(spec.Env, "DOCKER_HOST="+sandbox.Endpoint)
			spec.Networks = append(spec.Networks, sandbox.Network)
			sandboxed = true
		case p.sandboxMode == "on":
			// BUILD_SANDBOX=on: never silently fall back to the host engine.
			return nil, fmt.Errorf("build sandbox required (BUILD_SANDBOX=on) but unavailable: %w", err)
		default:
			sink.Write([]byte("[!] build sandbox unavailable, falling back to the host engine: " + err.Error() + "\n"))
		}
	}

	if !sandboxed && hostSocket && p.helperSocket != "" {
		spec.Volumes = append(spec.Volumes, docker.Mount{Source: p.helperSocket, Target: "/var/run/docker.sock"})
	}

	id, err := p.client.CreateContainer(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errHelperUnavailable, err)
	}
	// Register before the start call: a kill that arrives while the container
	// is starting must still find and remove it.
	p.registerHelper(deploymentID, id, p.client)
	if err := p.client.StartContainer(ctx, id); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_ = p.client.RemoveContainer(cleanupCtx, id)
		cancel()
		p.unregisterHelper(deploymentID, id)
		return nil, fmt.Errorf("%w: %v", errHelperUnavailable, err)
	}

	return &builder{client: p.client, sink: sink, id: id, secret: secret, sandboxed: sandboxed, deploymentID: deploymentID, pipeline: p}, nil
}

// close stops and removes the helper container. Cleanup must survive a
// canceled deployment context, so it runs on its own short deadline.
func (b *builder) close(ctx context.Context) {
	if b.pipeline != nil {
		b.pipeline.unregisterHelper(b.deploymentID, b.id)
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	_ = b.client.StopContainer(cleanupCtx, b.id, 10*time.Second)
	_ = b.client.RemoveContainer(cleanupCtx, b.id)
}

// transferImage copies an image built inside the sandbox into the host engine:
// save inside the sandbox, stream the archive through an exec, load on the
// engine that runs applications. The archive is written to the helper's
// throwaway filesystem first so daemon chatter can never corrupt the stream.
func (b *builder) transferImage(ctx context.Context, ref string) error {
	const archive = "/workspace/image.tgz"
	b.sink.Write([]byte("[*] transferring the built image to the server's engine\n"))
	save := "docker save " + shellQuote(ref) + " | gzip -1 > " + shellQuote(archive)
	if err := b.run(ctx, "sh", "-c", save); err != nil {
		return fmt.Errorf("save image: %w", err)
	}

	result, err := b.client.RunExec(ctx, b.id, []string{"cat", archive}, nil)
	if err != nil {
		return fmt.Errorf("read image: %w", err)
	}
	loadErr := b.client.LoadImage(ctx, result.Output)
	result.Output.Close()
	code, waitErr := result.Wait(ctx)
	if loadErr != nil {
		return loadErr
	}
	if waitErr != nil {
		return waitErr
	}
	if code != 0 {
		return fmt.Errorf("image transfer exited with code %d", code)
	}

	// The sandbox keeps the build cache, not every built image: the host now
	// owns this one, and dropping it inside keeps the sandbox volume small.
	_ = b.run(ctx, "sh", "-c", "docker rmi "+shellQuote(ref)+" > /dev/null 2>&1 || true")
	return nil
}

// run executes a command inside the helper, streaming its redacted output.
// The copy runs in its own goroutine so a canceled deployment context (a user
// kill or the deploy timeout) unblocks immediately: the exec stream is closed,
// and the caller then removes the helper container, which kills the command.
func (b *builder) run(ctx context.Context, cmd ...string) error {
	result, err := b.client.RunExec(ctx, b.id, cmd, nil)
	if err != nil {
		return err
	}
	copyDone := make(chan error, 1)
	go func() {
		redacted := &redactWriter{w: b.sink, secret: b.secret}
		_, copyErr := io.Copy(redacted, result.Output)
		result.Output.Close()
		redacted.Flush()
		copyDone <- copyErr
	}()
	select {
	case copyErr := <-copyDone:
		if copyErr != nil {
			return copyErr
		}
	case <-ctx.Done():
		result.Output.Close()
		<-copyDone
		return ctx.Err()
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

// clone fetches the application's source into /workspace/src. A fresh clone
// per deployment keeps the container stateless.
func (b *builder) clone(ctx context.Context, cloneURL, branch string) error {
	script := "rm -rf /workspace && mkdir -p /workspace && " +
		"git clone --depth 1 --branch " + shellQuote(branch) + " " + shellQuote(cloneURL) + " /workspace/src"
	return b.run(ctx, "sh", "-c", script)
}

// writeFile copies a small file into the helper through a tar stream, which
// needs no shared filesystem between the panel and the container.
func (b *builder) writeFile(ctx context.Context, path, content string) error {
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	header := &tar.Header{
		Name:     strings.TrimPrefix(path, "/"),
		Mode:     0o755,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
	}
	if err := writer.WriteHeader(header); err != nil {
		return err
	}
	if _, err := writer.Write([]byte(content)); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return b.client.CopyToContainer(ctx, b.id, "/", &buffer)
}

// redactWriter replaces a secret in a byte stream. It holds back the tail that
// could still turn out to be part of a match, so a secret split across two
// writes is still caught.
type redactWriter struct {
	w      io.Writer
	secret string
	tail   []byte
}

func (r *redactWriter) Write(p []byte) (int, error) {
	if r.secret == "" {
		if _, err := r.w.Write(p); err != nil {
			return 0, err
		}
		return len(p), nil
	}

	// Combine with the held-back tail into fresh storage, replace every
	// complete match, then hold back the longest suffix that could still turn
	// out to be the start of a match split across writes.
	data := make([]byte, 0, len(r.tail)+len(p))
	data = append(data, r.tail...)
	data = append(data, p...)
	data = replaceSecret(data, r.secret)

	hold := 0
	limit := len(r.secret) - 1
	if limit > len(data) {
		limit = len(data)
	}
	for k := limit; k > 0; k-- {
		if bytes.HasSuffix(data, []byte(r.secret)[:k]) {
			hold = k
			break
		}
	}

	emit := data[:len(data)-hold]
	r.tail = append([]byte(nil), data[len(data)-hold:]...)
	if len(emit) > 0 {
		if _, err := r.w.Write(emit); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Flush writes the held-back tail once the stream is done.
func (r *redactWriter) Flush() {
	if len(r.tail) == 0 {
		return
	}
	_, _ = r.w.Write(replaceSecret(r.tail, r.secret))
	r.tail = nil
}

func replaceSecret(b []byte, secret string) []byte {
	if secret == "" {
		return b
	}
	return bytes.ReplaceAll(b, []byte(secret), []byte("********"))
}

// redactText removes a secret from a string that will be logged.
func redactText(text, secret string) string {
	if secret == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, "********")
}

// shellQuote wraps a string for safe interpolation into an sh -c command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
