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
