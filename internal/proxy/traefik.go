// Package proxy turns panel resources into reverse-proxy configuration.
//
// Traefik is the default: it reads labels off containers, so the panel only has
// to set the right labels when it creates a container and routing plus TLS
// happen without any proxy API calls.
package proxy

import (
	"fmt"
	"strings"

	"github.com/tamalmaity-dev/meghify/internal/db"
)

const (
	// LabelPrefix namespaces every label the panel manages.
	LabelPrefix = "meghify."
	// TraefikEnable is the opt-in label for Traefik's Docker provider.
	TraefikEnable = "traefik.enable"
)

// Router describes the HTTP surface of one application.
type Router struct {
	ApplicationID string
	Name          string
	Port          int
	Domains       []*db.Domain
	Labels        map[string]string
}

// NewRouter builds the label set Traefik needs to route an application.
func NewRouter(app *db.Application, domains []*db.Domain) *Router {
	name := slug(app.Name)
	labels := map[string]string{
		LabelPrefix + "application": app.ID,
		LabelPrefix + "port":        fmt.Sprint(app.Port),
		TraefikEnable:               "true",
	}

	rules := make([]string, 0, len(domains))
	for _, d := range domains {
		host := strings.ToLower(strings.TrimSpace(d.Host))
		if host == "" {
			continue
		}
		labels[fmt.Sprintf("%sdomain.%s", LabelPrefix, host)] = host

		hostOnly := strings.SplitN(host, ":", 2)[0]
		if d.TLSEnabled {
			labels["traefik.http.routers."+name+".tls"] = "true"
			labels["traefik.http.routers."+name+".tls.certresolver"] = "letsencrypt"
			rules = append(rules, "Host(`"+hostOnly+"`)")
			continue
		}
		rules = append(rules, "Host(`"+hostOnly+"`)")
	}

	if len(rules) == 0 {
		// Without a domain the app is still reachable on the panel host directly
		// through its published port, but Traefik is not enabled.
		delete(labels, TraefikEnable)
	} else {
		labels["traefik.http.routers."+name+".rule"] = strings.Join(rules, " || ")
		labels["traefik.http.routers."+name+".entrypoints"] = "websecure,web"
		if _, ok := labels["traefik.http.routers."+name+".tls"]; ok {
			labels["traefik.http.routers."+name+".middlewares"] = "redirect-https@file"
		}
	}

	labels["traefik.http.services."+name+".loadbalancer.server.port"] = fmt.Sprint(app.Port)
	return &Router{
		ApplicationID: app.ID,
		Name:          app.Name,
		Port:          app.Port,
		Domains:       domains,
		Labels:        labels,
	}
}

// slug converts a resource name into a proxy-safe identifier.
func slug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "app"
	}
	return out
}

// MiddlewareNames lists the proxy level middlewares the panel installs.
func MiddlewareNames() []string {
	return []string{"redirect-https", "compress", "rate-limit"}
}
