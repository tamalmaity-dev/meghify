package deploy

import (
	"strings"
	"testing"

	"github.com/tamalmaity-dev/meghify/internal/db"
)

func TestWithDefaultPortInjectsOnlyWhenMissing(t *testing.T) {
	env := withDefaultPort([]string{"NODE_ENV=production"}, 3001)
	if got := env[len(env)-1]; got != "PORT=3001" {
		t.Fatalf("last environment entry = %q, want PORT=3001", got)
	}

	explicit := []string{"PORT=8080", "NODE_ENV=production"}
	got := withDefaultPort(explicit, 3001)
	if strings.Join(got, ",") != strings.Join(explicit, ",") {
		t.Fatalf("explicit PORT was overwritten: %v", got)
	}

	if got := withDefaultPort(nil, 0); len(got) != 0 {
		t.Fatalf("zero port added an environment entry: %v", got)
	}
}

func TestDynamicConfigRoutesToApplicationPortWhenDomainInherits(t *testing.T) {
	app := &db.Application{
		ID:            "app-1",
		Name:          "orchid-oaks",
		Port:          3001,
		EnvironmentID: "env-1",
	}
	domains := []*db.Domain{{
		ID:            "domain-1",
		ApplicationID: app.ID,
		Host:          "orchid.example.test",
		TLSEnabled:    true,
		RedirectHTTPS: true,
	}}

	yaml := DynamicConfigYAML(app, domains, false)
	if !strings.Contains(yaml, "meghify-app-app-1:3001") {
		t.Fatalf("route does not use the application port:\n%s", yaml)
	}
	if !strings.Contains(yaml, "tls: {}") {
		t.Fatalf("TLS router without a resolver is missing an empty TLS config:\n%s", yaml)
	}

	if yaml := DynamicConfigYAML(app, domains, true); !strings.Contains(yaml, "certResolver: letsencrypt") {
		t.Fatalf("TLS router does not reference the resolver:\n%s", yaml)
	}

	domains[0].Port = 8080
	if yaml := DynamicConfigYAML(app, domains, false); !strings.Contains(yaml, "meghify-app-app-1:8080") {
		t.Fatalf("route does not honor the domain override:\n%s", yaml)
	}
}

func TestWWWRedirectRoutesBothHosts(t *testing.T) {
	app := &db.Application{ID: "app-2", Name: "www-app", Port: 3000}
	domain := &db.Domain{
		ID:            "domain-2",
		ApplicationID: app.ID,
		Host:          "example.com",
		TLSEnabled:    true,
		RedirectHTTPS: true,
		WWWRedirect:   "www",
	}

	yaml := DynamicConfigYAML(app, []*db.Domain{domain}, true)
	if !strings.Contains(yaml, "(Host(`www.example.com`) || Host(`example.com`))") {
		t.Fatalf("redirect router does not match both hostnames:\n%s", yaml)
	}
	if !strings.Contains(yaml, "redirectRegex:") {
		t.Fatalf("redirect middleware is missing:\n%s", yaml)
	}
	if !strings.Contains(yaml, "https://www.example.com$1") {
		t.Fatalf("redirect replacement does not target the canonical host:\n%s", yaml)
	}

	domain.Host = "www.example.com"
	domain.WWWRedirect = "non-www"
	yaml = DynamicConfigYAML(app, []*db.Domain{domain}, true)
	if !strings.Contains(yaml, "https://example.com$1") {
		t.Fatalf("non-www redirect does not strip the www prefix:\n%s", yaml)
	}

	domain.WWWRedirect = ""
	yaml = DynamicConfigYAML(app, []*db.Domain{domain}, true)
	if strings.Contains(yaml, "redirectRegex:") {
		t.Fatalf("no-redirect domain unexpectedly has a redirect middleware:\n%s", yaml)
	}
}

func TestWWWRedirectHosts(t *testing.T) {
	cases := []struct {
		host, mode, canonical, alternate string
	}{
		{"example.com", "www", "www.example.com", "example.com"},
		{"www.example.com", "www", "www.example.com", "example.com"},
		{"example.com", "non-www", "example.com", "www.example.com"},
		{"www.example.com", "non-www", "example.com", "www.example.com"},
		{"example.com", "", "example.com", ""},
	}
	for _, tc := range cases {
		canonical, alternate := WWWRedirectHosts(tc.host, tc.mode)
		if canonical != tc.canonical || alternate != tc.alternate {
			t.Errorf("WWWRedirectHosts(%q, %q) = (%q, %q), want (%q, %q)",
				tc.host, tc.mode, canonical, alternate, tc.canonical, tc.alternate)
		}
	}
}
