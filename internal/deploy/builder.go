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

	// secret is redacted from every line of command output: git and docker
	// occasionally echo the credential embedded in a clone URL.
	secret string
}

// newBuilder pulls the helper image if needed, then creates and starts the
// container for one deployment.
func (p *Pipeline) newBuilder(ctx context.Context, sink io.Writer, app *db.Application, secret string) (*builder, error) {
	if p.helperImage == "" || p.client == nil {
		return nil, errHelperUnavailable
	}

	if ok, _ := p.client.ImageExists(ctx, p.helperImage); !ok {
		sink.Write([]byte("[*] pulling the build helper image " + p.helperImage + "\n"))
		if err := p.client.PullImage(ctx, p.helperImage, sink); err != nil {
			return nil, fmt.Errorf("%w: pull %s: %v", errHelperUnavailable, p.helperImage, err)
		}
	}

	short := app.ID
	if len(short) > 8 {
		short = short[:8]
	}
	name := fmt.Sprintf("builder-%s-%d", short, time.Now().Unix())

	var mounts []docker.Mount
	if p.helperSocket != "" {
		mounts = append(mounts, docker.Mount{Source: p.helperSocket, Target: "/var/run/docker.sock"})
	}

	id, err := p.client.CreateContainer(ctx, &docker.ContainerSpec{
		Name:        name,
		Image:       p.helperImage,
		Cmd:         []string{"sh", "-c", "while true; do sleep 3600; done"},
		Labels:      map[string]string{"meghify.role": "build-helper"},
		Volumes:     mounts,
		MemoryLimit: p.buildMemoryLimit,
		CPULimit:    p.buildCPULimit,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errHelperUnavailable, err)
	}
	if err := p.client.StartContainer(ctx, id); err != nil {
		_ = p.client.RemoveContainer(ctx, id)
		return nil, fmt.Errorf("%w: %v", errHelperUnavailable, err)
	}

	return &builder{client: p.client, sink: sink, id: id, secret: secret}, nil
}

// close stops and removes the helper container.
func (b *builder) close(ctx context.Context) {
	_ = b.client.StopContainer(ctx, b.id, 10*time.Second)
	_ = b.client.RemoveContainer(ctx, b.id)
}

// run executes a command inside the helper, streaming its redacted output.
func (b *builder) run(ctx context.Context, cmd ...string) error {
	result, err := b.client.RunExec(ctx, b.id, cmd, nil)
	if err != nil {
		return err
	}
	redacted := &redactWriter{w: b.sink, secret: b.secret}
	_, copyErr := io.Copy(redacted, result.Output)
	result.Output.Close()
	redacted.Flush()
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
