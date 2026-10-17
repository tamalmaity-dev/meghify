package deploy

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/docker"
)

// proxyNetwork is the shared network applications and the reverse proxy join,
// so the proxy can route to any app regardless of its private network.
const proxyNetwork = "meghify"

// ensureProxy makes sure a Traefik reverse proxy runs on the target engine and
// on the shared network. Docker-compose installs ship one already; hosts
// without it (systemd installs) get it created on the first deployment that
// has a domain. A proxy from an older panel version is recreated, because its
// flags predate dynamic file routing and the routing volume. Failure is
// reported but never fails the deployment: the app itself is still reachable
// through its published ports.
func (p *Pipeline) ensureProxy(ctx context.Context, sink io.Writer) error {
	if p.proxyImage == "" || p.client == nil {
		return nil
	}
	if _, err := p.client.EnsureNetwork(ctx, proxyNetwork, false); err != nil {
		return err
	}
	if err := p.client.EnsureVolume(ctx, proxyCfgVolume); err != nil {
		sink.Write([]byte("[!] could not ensure the proxy routing volume: " + err.Error() + "\n"))
	}

	container, err := p.client.ContainerByName(ctx, "proxy")
	if err != nil {
		return err
	}
	if container != nil {
		// A proxy from an older generation cannot serve dynamic files: it
		// carries neither the file provider flag nor the routing volume.
		// Recreate it in place — letsencrypt data lives in its own volume.
		info, inspectErr := p.client.InspectContainer(ctx, container.ID)
		if inspectErr == nil {
			if gen := containerLabel(info, "meghify.gen"); gen != proxyGen {
				sink.Write([]byte("[*] upgrading the proxy for dynamic file routing\n"))
				_ = p.client.StopContainer(ctx, container.ID, 10*time.Second)
				_ = p.client.RemoveContainer(ctx, container.ID)
				container = nil
			}
		}
	}
	if container != nil {
		if container.State == "running" {
			return nil
		}
		sink.Write([]byte("[*] starting the proxy\n"))
		inspect, _ := p.client.InspectContainer(ctx, container.ID)
		if inspect == nil || containerLabel(inspect, "meghify.gen") == proxyGen {
			return p.client.StartContainer(ctx, container.ID)
		}
		// Stopped proxy from an older generation: replace it.
		_ = p.client.RemoveContainer(ctx, container.ID)
		container = nil
	}

	if ok, _ := p.client.ImageExists(ctx, p.proxyImage); !ok {
		sink.Write([]byte("[*] pulling the proxy image " + p.proxyImage + "\n"))
		if err := p.client.PullImageRetry(ctx, p.proxyImage, sink); err != nil {
			return fmt.Errorf("pull proxy image: %w", err)
		}
	}

	command := []string{
		"--api.dashboard=false",
		"--providers.docker=true",
		"--providers.docker.exposedbydefault=false",
		"--providers.docker.network=" + proxyNetwork,
		// Domain routing is file-based (drafted from the database and copied
		// into the routing volume) so domain edits apply without a redeploy.
		"--providers.file.directory=" + proxyDynDir,
		// Coolify-compatible entrypoint names: the routers the panel
		// generates reference http/https.
		"--entrypoints.http.address=:80",
		"--entrypoints.https.address=:443",
	}
	mounts := make([]docker.Mount, 0, 3)
	mounts = append(mounts, docker.Mount{Source: proxyCfgVolume, Target: proxyDynDir})
	if p.proxyEmail != "" {
		command = append(command,
			"--certificatesresolvers.letsencrypt.acme.tlschallenge=true",
			"--certificatesresolvers.letsencrypt.acme.email="+p.proxyEmail,
			"--certificatesresolvers.letsencrypt.acme.storage=/letsencrypt/acme.json")
		mounts = append(mounts, docker.Mount{Source: "traefik-letsencrypt", Target: "/letsencrypt"})
	}
	if p.helperSocket != "" {
		mounts = append(mounts, docker.Mount{Source: p.helperSocket, Target: "/var/run/docker.sock", ReadOnly: true})
	}

	id, err := p.client.CreateContainer(ctx, &docker.ContainerSpec{
		Name:          "proxy",
		Image:         p.proxyImage,
		Cmd:           command,
		Labels:        map[string]string{"meghify.role": "proxy", "meghify.gen": proxyGen},
		Ports:         map[int]int{80: valueOrDefault(p.proxyPort, 80), 443: valueOrDefault(p.proxyTLSPort, 443)},
		Volumes:       mounts,
		Networks:      []string{proxyNetwork},
		RestartPolicy: "unless-stopped",
	})
	if err != nil {
		return err
	}
	sink.Write([]byte("[*] starting the proxy\n"))
	if err := p.client.StartContainer(ctx, id); err != nil {
		_ = p.client.RemoveContainer(ctx, id)
		return fmt.Errorf("start proxy: %w (is another reverse proxy on 80/443?)", err)
	}
	return nil
}
