package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/tamalmaity-dev/meghify/internal/db"
)

// ComposeServiceProject is the isolated compose project for a service record.
func ComposeServiceProject(service *db.Service) string {
	return "meghify-svc-" + sanitizeName(service.ID)
}

// DeployService applies a stored compose stack on the service's target engine.
// Unlike applications, a compose service has no image build or release phase:
// the job is just `docker compose up -d` in a helper that can reach the target
// engine's socket.
func (p *Pipeline) DeployService(ctx context.Context, serviceID string) error {
	service, err := p.store.Resources.ServiceByID(ctx, serviceID)
	if err != nil {
		return err
	}
	if err := p.store.Resources.PatchServiceStatus(ctx, service.ID, db.StatusDeploying); err != nil {
		return err
	}

	logPath := filepath.Join(p.dataDir, "services", service.ID+".log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		_ = p.store.Resources.PatchServiceStatus(context.WithoutCancel(ctx), service.ID, db.StatusError)
		return err
	}
	logFile, err := os.Create(logPath)
	if err != nil {
		_ = p.store.Resources.PatchServiceStatus(context.WithoutCancel(ctx), service.ID, db.StatusError)
		return err
	}
	defer logFile.Close()
	sink := io.Writer(logFile)

	target := p.forNode(ctx, service.NodeID)
	err = target.applyService(ctx, sink, service, "up")
	if err != nil {
		_ = p.store.Resources.PatchServiceStatus(context.WithoutCancel(ctx), service.ID, db.StatusError)
		return err
	}
	return p.store.Resources.PatchServiceStatus(ctx, service.ID, db.StatusRunning)
}

// RemoveService brings a compose stack down before its database row is
// deleted. It is best-effort: the caller still deletes the row when the node is
// unreachable, otherwise a dead server would make a service impossible to
// remove from the panel.
func (p *Pipeline) RemoveService(ctx context.Context, service *db.Service) error {
	if service == nil {
		return nil
	}
	target := p.forNode(ctx, service.NodeID)
	return target.applyService(ctx, io.Discard, service, "down")
}

func (p *Pipeline) applyService(ctx context.Context, sink io.Writer, service *db.Service, action string) error {
	target := p.forNode(ctx, service.NodeID)
	if target.clientErr != nil {
		return fmt.Errorf("target server is unavailable: %w", target.clientErr)
	}
	if target.client == nil {
		return fmt.Errorf("no docker engine is configured for the target node")
	}

	app := &db.Application{ID: service.ID, Name: service.Name}
	build, err := target.newBuilder(ctx, sink, app, "", "service-"+service.ID, true)
	if err == nil {
		defer build.close(ctx)
		if err := build.writeFile(ctx, "/workspace/compose.yaml", service.Compose); err != nil {
			return err
		}
		command := "cd /workspace && docker compose -p " + shellQuote(ComposeServiceProject(service)) +
			" -f /workspace/compose.yaml " + action + " -d --remove-orphans"
		if action == "down" {
			// `down -d` is not a docker compose flag; down is already detached
			// cleanup, while up uses -d to return to the caller.
			command = "cd /workspace && docker compose -p " + shellQuote(ComposeServiceProject(service)) +
				" -f /workspace/compose.yaml down --remove-orphans"
		}
		if err := build.run(ctx, "sh", "-c", command); err != nil {
			return fmt.Errorf("compose %s: %w", action, err)
		}
		return nil
	}
	if !errors.Is(err, errHelperUnavailable) {
		return err
	}
	// A host install may carry the docker compose plugin even when the helper
	// image is not configured. Remote services cannot use this fallback: the
	// panel's local docker socket is not the remote engine.
	if service.NodeID != "" && service.NodeID != "local" && service.NodeID != db.LocalNodeID {
		return err
	}
	return p.applyServiceOnHost(ctx, sink, service, action)
}

// RemoveComposeApplication runs `docker compose down` for an application whose
// build pack is compose. Compose-created containers do not carry the panel's
// application label, so label-based container cleanup alone would leave the
// stack running after the application row is deleted.
func (p *Pipeline) RemoveComposeApplication(ctx context.Context, app *db.Application) error {
	if app == nil || app.BuildPack != db.BuildCompose {
		return nil
	}
	target := p.forNode(ctx, app.NodeID)
	if target.clientErr != nil {
		return fmt.Errorf("target server is unavailable: %w", target.clientErr)
	}
	if target.client == nil {
		return fmt.Errorf("no docker engine is configured for the target node")
	}
	build, err := target.newBuilder(ctx, io.Discard, app, "", "teardown-"+app.ID, true)
	if err == nil {
		defer build.close(ctx)
		if err := build.writeFile(ctx, "/workspace/compose.yaml", app.Dockerfile); err != nil {
			return err
		}
		command := "cd /workspace && docker compose -p " + shellQuote(ComposeProjectName(app)) +
			" -f /workspace/compose.yaml down --remove-orphans"
		return build.run(ctx, "sh", "-c", command)
	}
	if !errors.Is(err, errHelperUnavailable) {
		return err
	}
	if app.NodeID != "" && app.NodeID != "local" && app.NodeID != db.LocalNodeID {
		return err
	}
	dir := filepath.Join(p.dataDir, "workspaces", app.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	composePath := filepath.Join(dir, "meghify-compose.yaml")
	if err := os.WriteFile(composePath, []byte(app.Dockerfile), 0o600); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "docker", "compose", "-p", ComposeProjectName(app),
		"-f", composePath, "down", "--remove-orphans")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose down: %w", err)
	}
	return nil
}

func (p *Pipeline) applyServiceOnHost(ctx context.Context, sink io.Writer, service *db.Service, action string) error {
	dir := filepath.Join(p.dataDir, "services", service.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	composePath := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(service.Compose), 0o644); err != nil {
		return err
	}
	args := []string{"compose", "-p", ComposeServiceProject(service), "-f", composePath}
	if action == "down" {
		args = append(args, "down", "--remove-orphans")
	} else {
		args = append(args, "up", "-d", "--remove-orphans")
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	cmd.Stdout = sink
	cmd.Stderr = sink
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose %s: %w", action, err)
	}
	return nil
}
