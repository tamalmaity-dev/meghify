package deploy

// Routing is served through Traefik's file provider: one dynamic
// configuration file per application, rewritten in the running proxy through
// the Docker copy API. Dokploy builds its routing on the same pattern
// (dynamic/<appName>.yml with routers that address containers by name).
// Compared with routing labels this fixes three things:
//
//   - domain, path, TLS and port edits apply hot, without a redeployment;
//   - routing no longer depends on a deployment finishing: the routes of
//     declared domains exist even while nothing is running, so a missing or
//     crashed container answers Traefik's honest 502 Bad Gateway instead of
//     the bare "404 page not found" of an orphaned label route;
//   - a rolling release (or a reverted one) needs no routing change at all,
//     because the route addresses the container's stable name on the shared
//     proxy network and Docker DNS resolves it per request.

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/tamalmaity-dev/meghify/internal/db"
	"github.com/tamalmaity-dev/meghify/internal/docker"
)

const (
	// proxyDynDir is where the proxy's file provider watches inside the proxy
	// container; the panel uploads files there through the Docker copy API.
	proxyDynDir = "/etc/traefik/dynamic"
	// proxyCfgVolume is the named volume mounted at proxyDynDir, so routing
	// files written by one deployment survive proxy recreations and boots.
	proxyCfgVolume = "meghify-proxy-cfg"
	// proxyGen marks the proxy layout generation: ensureProxy recreates an
	// older proxy so a panel upgrade installs the new flags and the volume.
	proxyGen = "2"
)

// routeFileName is the dynamic configuration file of one application.
func routeFileName(appID string) string { return "app-" + appID + ".yml" }

// DynamicConfigYAML renders the Traefik file-provider configuration for one
// application: an http/https router pair per domain, per-domain middlewares
// (gzip compression, the optional robots header, HTTP->HTTPS redirects) and a
// load-balancer service per domain that addresses the application container by
// its stable name. certResolver decides whether TLS domains ask Let's Encrypt
// for certificates; without it TLS routers still exist and serve what the
// default resolver produces.
//
// Router and middleware names carry the application's slug so parallel files
// can never collide inside Traefik's file-provider namespace.
func DynamicConfigYAML(app *db.Application, domains []*db.Domain, certResolver bool) string {
	slug := routerSlug(app)
	backend := "http://" + docker.ManagedName(ContainerName(app)) + ":"

	type routed struct {
		host, hostOnly string
		domain         *db.Domain
		index          int
	}
	valid := make([]routed, 0, len(domains))
	seen := map[string]bool{}
	for _, domain := range domains {
		host := strings.ToLower(strings.TrimSpace(domain.Host))
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		valid = append(valid, routed{
			host:     host,
			hostOnly: strings.SplitN(host, ":", 2)[0],
			domain:   domain,
			index:    len(valid),
		})
	}

	var routers, middlewares, services strings.Builder
	for _, entry := range valid {
		domain := entry.domain
		index := entry.index

		rule := "Host(`" + entry.hostOnly + "`)"
		canonical, alternate := WWWRedirectHosts(entry.hostOnly, domain.WWWRedirect)
		if alternate != "" {
			rule = "(Host(`" + canonical + "`) || Host(`" + alternate + "`))"
		}
		if path := domainPath(domain); path != "/" {
			rule += " && PathPrefix(`" + path + "`)"
		}

		port := domain.Port
		if port <= 0 {
			port = app.Port
		}

		services.WriteString("    " + slug + "-" + fmt.Sprint(index) + ":\n")
		services.WriteString("      loadBalancer:\n")
		services.WriteString("        passHostHeader: true\n")
		services.WriteString("        servers:\n")
		services.WriteString("          - url: " + yamlString(backend+fmt.Sprint(port)) + "\n")

		shared := []string{slug + "-gzip"}
		if !domain.SearchIndexing {
			shared = append(shared, slug+"-robots-"+fmt.Sprint(index))
			middlewares.WriteString("    " + slug + "-robots-" + fmt.Sprint(index) + ":\n")
			middlewares.WriteString("      headers:\n")
			middlewares.WriteString("        customResponseHeaders:\n")
			middlewares.WriteString("          X-Robots-Tag: none\n")
		}
		if alternate != "" {
			shared = append(shared, slug+"-www-"+fmt.Sprint(index))
			scheme := "http"
			if domain.TLSEnabled {
				scheme = "https"
			}
			middlewares.WriteString("    " + slug + "-www-" + fmt.Sprint(index) + ":\n")
			middlewares.WriteString("      redirectRegex:\n")
			middlewares.WriteString("        regex: " + yamlString("^https?://"+regexp.QuoteMeta(alternate)+"(.*)") + "\n")
			middlewares.WriteString("        replacement: " + yamlString(scheme+"://"+canonical+"$1") + "\n")
			middlewares.WriteString("        permanent: true\n")
		}

		if domain.TLSEnabled {
			routers.WriteString("    https-" + slug + "-" + fmt.Sprint(index) + ":\n")
			routers.WriteString("      entryPoints:\n")
			routers.WriteString("        - https\n")
			routers.WriteString("      rule: " + yamlString(rule) + "\n")
			routers.WriteString("      service: " + slug + "-" + fmt.Sprint(index) + "\n")
			routers.WriteString("      middlewares:\n")
			for _, name := range shared {
				routers.WriteString("        - " + name + "\n")
			}
			if certResolver {
				routers.WriteString("      tls:\n")
				routers.WriteString("        certResolver: letsencrypt\n")
			} else {
				// A bare "tls:" key is a YAML null, not an empty mapping.
				// Traefik needs a TLS configuration on the router to attach it
				// to the HTTPS entrypoint; without one it accepts the handshake
				// with the fallback certificate but answers 404.
				routers.WriteString("      tls: {}\n")
			}

			// The matching plain-HTTP router: redirect when configured,
			// otherwise serve the same content without TLS.
			routers.WriteString("    http-" + slug + "-" + fmt.Sprint(index) + ":\n")
			routers.WriteString("      entryPoints:\n")
			routers.WriteString("        - http\n")
			routers.WriteString("      rule: " + yamlString(rule) + "\n")
			routers.WriteString("      service: " + slug + "-" + fmt.Sprint(index) + "\n")
			routers.WriteString("      middlewares:\n")
			for _, name := range shared {
				routers.WriteString("        - " + name + "\n")
			}
			if domain.RedirectHTTPS {
				redirect := slug + "-redirect-to-https-" + fmt.Sprint(index)
				middlewares.WriteString("    " + redirect + ":\n")
				middlewares.WriteString("      redirectScheme:\n")
				middlewares.WriteString("        scheme: https\n")
				routers.WriteString("        - " + redirect + "\n")
			}
			continue
		}

		routers.WriteString("    http-" + slug + "-" + fmt.Sprint(index) + ":\n")
		routers.WriteString("      entryPoints:\n")
		routers.WriteString("        - http\n")
		routers.WriteString("      rule: " + yamlString(rule) + "\n")
		routers.WriteString("      service: " + slug + "-" + fmt.Sprint(index) + "\n")
		routers.WriteString("      middlewares:\n")
		for _, name := range shared {
			routers.WriteString("        - " + name + "\n")
		}
	}

	out := &strings.Builder{}
	out.WriteString("# Dynamic routing for application " + app.ID + ", managed by meghify.\n")
	out.WriteString("# Rewritten on domain edits, after deployments and at boot; direct\n")
	out.WriteString("# edits are overwritten.\n")
	if len(valid) == 0 {
		// Comments only: Traefik's file decoder rejects standalone empty
		// sections (routers: {} and friends), and one unparseable file in
		// the directory poisons every route in it. A comment-only document
		// is a valid "no routes" file.
		return out.String()
	}
	out.WriteString("http:\n")
	out.WriteString("  middlewares:\n")
	out.WriteString("    " + slug + "-gzip:\n")
	out.WriteString("      compress: {}\n")
	out.WriteString(middlewares.String())
	out.WriteString("  routers:\n")
	out.WriteString(routers.String())
	out.WriteString("  services:\n")
	out.WriteString(services.String())
	return out.String()
}

// WWWRedirectHosts resolves the canonical and alternate hostnames for a
// domain. The returned alternate is empty when no www redirect is configured.
func WWWRedirectHosts(host, mode string) (canonical, alternate string) {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return "", ""
	}
	isWWW := strings.HasPrefix(host, "www.")
	switch mode {
	case "www":
		if isWWW {
			return host, strings.TrimPrefix(host, "www.")
		}
		return "www." + host, host
	case "non-www":
		if isWWW {
			return strings.TrimPrefix(host, "www."), host
		}
		return host, "www." + host
	default:
		return host, ""
	}
}

// yamlString quotes a YAML scalar. Every generated value that may contain
// backticks, colons or spaces goes through this.
func yamlString(value string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\', '"':
			out.WriteByte('\\')
		}
		out.WriteRune(r)
	}
	out.WriteByte('"')
	return out.String()
}

// ApplicationRoutes pushes one application's routing snapshot into the running
// proxy. It is idempotent and cheap: called after a release, from the domain
// CRUD handlers and at boot.
func (p *Pipeline) ApplicationRoutes(ctx context.Context, app *db.Application, domains []*db.Domain) error {
	if p.proxyImage == "" {
		return nil
	}
	target := p.forNode(ctx, app.NodeID)
	if target.clientErr != nil {
		return target.clientErr
	}
	if target.client == nil {
		return errors.New("no docker engine is configured for the target node")
	}
	if err := target.client.EnsureVolume(ctx, proxyCfgVolume); err != nil {
		return err
	}
	return target.routeUpload(ctx, app, domains)
}

// SyncApplicationRoutes rewrites the routing file of one application from the
// database. Errors are reported to the caller (handlers log them); the proxy
// keeps serving the last good file until then.
func (p *Pipeline) SyncApplicationRoutes(ctx context.Context, appID string) error {
	if p.proxyImage == "" || p.store == nil {
		return nil
	}
	app, err := p.store.Applications.ApplicationByID(ctx, appID)
	if err != nil {
		return err
	}
	domains, err := p.store.Applications.DomainsByApplication(ctx, app.ID)
	if err != nil {
		return err
	}
	return p.ApplicationRoutes(ctx, app, domains)
}

// ClearApplicationRoutes empties an application's routing file on the node
// that hosts it. The copy API can overwrite a file but not remove one, and an
// empty document removes every router of the application from Traefik. Called
// before the application row is deleted, while the node id is still known.
func (p *Pipeline) ClearApplicationRoutes(ctx context.Context, app *db.Application) error {
	if app == nil {
		return nil
	}
	return p.ApplicationRoutes(ctx, &db.Application{ID: app.ID, NodeID: app.NodeID}, nil)
}

// ReconcileRoutes runs after the panel restarts: install the current proxy
// generation (recreating an outdated proxy) and rewrite every application's
// routing file from the database. Never fatal — every failure is logged.
func (p *Pipeline) ReconcileRoutes(parent context.Context, logf func(msg string, args ...any)) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if p.proxyImage == "" || p.store == nil {
		return
	}
	ctx, done := context.WithTimeout(parent, time.Minute)
	defer done()

	apps, err := p.store.Applications.ListApplications(ctx)
	if err != nil {
		logf("proxy route reconcile could not list applications", "error", err)
		return
	}

	// Each node runs its own proxy and has its own routing volume. Group by
	// node so ensureProxy runs once per engine, then upload the files the
	// apps on that engine need.
	type nodeGroup struct {
		nodeID  string
		apps    []*db.Application
		domains map[string][]*db.Domain
	}
	groups := map[string]*nodeGroup{}
	for _, app := range apps {
		domains, err := p.store.Applications.DomainsByApplication(ctx, app.ID)
		if err != nil {
			logf("proxy route reconcile could not load domains", "application", app.ID, "error", err)
			continue
		}
		if len(domains) == 0 {
			continue
		}
		nodeID := app.NodeID
		if nodeID == "" {
			nodeID = "local"
		}
		group := groups[nodeID]
		if group == nil {
			group = &nodeGroup{nodeID: app.NodeID, domains: map[string][]*db.Domain{}}
			groups[nodeID] = group
		}
		group.apps = append(group.apps, app)
		group.domains[app.ID] = domains
	}
	// An application-less panel keeps ports 80/443 free: the proxy is only
	// provisioned for real routing work.
	if len(groups) == 0 {
		return
	}

	synced := 0
	for key, group := range groups {
		target := p.forNode(ctx, group.nodeID)
		if target.clientErr != nil || target.client == nil {
			logf("proxy route reconcile skipped an unavailable node", "node", key, "error", target.clientErr)
			continue
		}
		if err := target.ensureProxy(ctx, logSink{f: logf}); err != nil {
			logf("proxy not running, routing reconcile skipped", "node", key, "error", err)
			continue
		}
		for _, app := range group.apps {
			if err := target.routeUpload(ctx, app, group.domains[app.ID]); err != nil {
				logf("proxy route reconcile failed", "application", app.ID, "node", key, "error", err)
				continue
			}
			synced++
		}
	}
	if synced > 0 {
		logf("proxy routes reconciled", "applications", synced)
	}
}

// ACMEEmail resolves the address Traefik uses for Let's Encrypt. An explicit
// ACME_EMAIL always wins. Otherwise the panel uses the first admin account's
// signup address, so a fresh install gets trusted HTTPS without asking the
// operator to edit the environment a second time.
func (p *Pipeline) ACMEEmail(ctx context.Context) string {
	if address := strings.TrimSpace(p.proxyEmail); address != "" {
		return address
	}
	if p.store == nil {
		return ""
	}
	users, err := p.store.Users.ListUsers(ctx)
	if err != nil {
		return ""
	}
	for _, user := range users {
		if user.Role == db.RoleAdmin && strings.Contains(user.Email, "@") {
			return strings.TrimSpace(user.Email)
		}
	}
	return ""
}

// routeUpload writes the file, assuming the proxy exists.
func (p *Pipeline) routeUpload(ctx context.Context, app *db.Application, domains []*db.Domain) error {
	if p.client == nil {
		return errors.New("no docker engine is configured for the target node")
	}
	proxy, err := p.client.ContainerByName(ctx, "proxy")
	if err != nil {
		return err
	}
	if proxy == nil {
		return errors.New("the proxy container is missing")
	}
	content := p.dynamicContent(ctx, app, domains)
	return p.client.CopyToContainer(ctx, proxy.ID, proxyDynDir+"/", tarEntry(routeFileName(app.ID), 0o644, content))
}

// dynamicContent renders with the pipeline's effective ACME flag.
func (p *Pipeline) dynamicContent(ctx context.Context, app *db.Application, domains []*db.Domain) string {
	return DynamicConfigYAML(app, domains, p.ACMEEmail(ctx) != "")
}

// tarEntry packs a single file for the copy API.
func tarEntry(name string, mode int64, content string) *bytes.Buffer {
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	header := &tar.Header{
		Name:     name,
		Mode:     mode,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
		// Give the extracted file a fresh mtime. Traefik's file provider uses
		// fsnotify, but a real mtime also keeps volume inspection and tools
		// that compare timestamps from treating every upload as 1970.
		ModTime: time.Now(),
	}
	_ = writer.WriteHeader(header)
	_, _ = writer.Write([]byte(content))
	_ = writer.Close()
	return &buffer
}

// containerLabel reads a label from a full container inspect.
func containerLabel(info *container.InspectResponse, key string) string {
	if info == nil || info.Config == nil || info.Config.Labels == nil {
		return ""
	}
	return info.Config.Labels[key]
}

// logSink adapts a log function to the io.Writer the proxy setup logs into.
type logSink struct {
	f func(msg string, args ...any)
}

func (l logSink) Write(p []byte) (int, error) {
	text := strings.TrimRight(string(p), "\n")
	if text != "" {
		l.f(text)
	}
	return len(p), nil
}
