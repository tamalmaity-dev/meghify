package deploy

import (
	"context"
	"fmt"
	"io"

	"github.com/tamalmaity-dev/meghify/internal/docker"
)

// proxyNetwork is the shared network applications and the reverse proxy join,
// so the proxy can route to any app regardless of its private network.
const proxyNetwork = "meghify"

// ensureProxy makes sure a Traefik reverse proxy runs on the target engine and
// on the shared network. Docker-compose installs ship one already; hosts
// without it (systemd installs) get it created on the first deployment that
// has a domain. Failure is reported but never fails the deployment: the app
// itself is still reachable through its published ports.
func (p *Pipeline) ensureProxy(ctx context.Context, sink io.Writer) error {
	if p.proxyImage == "" || p.client == nil {
		return nil
	}
	if _, err := p.client.EnsureNetwork(ctx, proxyNetwork, false); err != nil {
		return err
	}

	container, err := p.client.ContainerByName(ctx, "proxy")
	if err != nil {
		return err
	}
	if container != nil {
		if container.State == "running" {
			return nil
		}
		sink.Write([]byte("[*] starting the proxy\n"))
		return p.client.StartContainer(ctx, container.ID)
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
		"--entrypoints.web.address=:80",
		"--entrypoints.websecure.address=:443",
	}
	mounts := make([]docker.Mount, 0, 2)
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
		Labels:        map[string]string{"meghify.role": "proxy"},
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
