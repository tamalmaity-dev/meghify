package deploy

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tamalmaity-dev/meghify/internal/db"
)

// The redactor must catch a secret even when it is split across two writes,
// which is exactly what happens when git prints a clone URL mid-stream.
func TestRedactWriter(t *testing.T) {
	const secret = "ghs_secret_token_123"

	var out bytes.Buffer
	w := &redactWriter{w: &out, secret: secret}

	// Feed the text byte by byte: every boundary case is exercised.
	text := "clone https://x-access-token:" + secret + "@github.com/acme/app.git failed"
	for i := 0; i < len(text); i++ {
		if _, err := w.Write([]byte{text[i]}); err != nil {
			t.Fatal(err)
		}
	}
	w.Flush()

	got := out.String()
	if strings.Contains(got, secret) {
		t.Fatalf("secret leaked through redaction: %s", got)
	}
	if !strings.Contains(got, "********") {
		t.Fatalf("expected redaction marker, got: %s", got)
	}
	if !strings.Contains(got, "clone https://x-access-token:") {
		t.Fatalf("surrounding text was mangled: %s", got)
	}
}

func TestRedactWriterNoSecret(t *testing.T) {
	var out bytes.Buffer
	w := &redactWriter{w: &out}
	if _, err := w.Write([]byte("plain output")); err != nil {
		t.Fatal(err)
	}
	w.Flush()
	if out.String() != "plain output" {
		t.Fatalf("unexpected passthrough: %q", out.String())
	}
}

func TestRedactText(t *testing.T) {
	if got := redactText("https://u:tok123@github.com/x.git", "tok123"); strings.Contains(got, "tok123") {
		t.Fatalf("secret leaked: %s", got)
	}
	if got := redactText("https://github.com/x.git", ""); got != "https://github.com/x.git" {
		t.Fatalf("empty secret should leave text alone: %s", got)
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("main"); got != "'main'" {
		t.Fatalf("unexpected quoting: %s", got)
	}
	if got := shellQuote("we're"); got != `'we'\''re'` {
		t.Fatalf("single quote not escaped: %s", got)
	}
}

func TestValidEnvName(t *testing.T) {
	for _, ok := range []string{"NODE_ENV", "_x", "A1"} {
		if !validEnvName(ok) {
			t.Fatalf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "1A", "A-B", "A B", "A.B", "hé"} {
		if validEnvName(bad) {
			t.Fatalf("%q should be invalid", bad)
		}
	}
}

func TestBuilderContextDir(t *testing.T) {
	cases := []struct {
		base string
		want string
	}{
		{"", "/workspace/src"},
		{"/", "/workspace/src"},
		{"apps/web", "/workspace/src/apps/web"},
		{"/apps/web/", "/workspace/src/apps/web"},
	}
	for _, tc := range cases {
		app := &db.Application{BaseDirectory: tc.base}
		if got := builderContextDir(app); got != tc.want {
			t.Fatalf("base %q: got %s, want %s", tc.base, got, tc.want)
		}
	}
}

// The dynamic routing file must carry every guarantee the old container
// labels carried: router pair per domain, path prefix, gzip, robots, the
// HTTP->HTTPS redirect, the letsencrypt resolver, and a backend addressed by
// the application container's stable name.
func TestDynamicConfigYAMLTLS(t *testing.T) {
	app := &db.Application{ID: "app-1", Name: "roopsa-web", Port: 3000}
	domains := []*db.Domain{{
		ID:             "d1",
		ApplicationID:  app.ID,
		Host:           "roopsa-web.example.com",
		TLSEnabled:     true,
		SearchIndexing: true,
	}}
	out := DynamicConfigYAML(app, domains, true)

	for _, want := range []string{
		"Host(`roopsa-web.example.com`)",
		"https-app-app-1-0:",
		"http-app-app-1-0:",
		"certResolver: letsencrypt",
		"app-app-1-gzip:",
		"url: \"http://meghify-app-app-1:3000\"",
		"passHostHeader: true",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "X-Robots-Tag") {
		t.Fatalf("search indexing is on, robots middleware must be absent:\n%s", out)
	}
}

func TestDynamicConfigYAMLPathAndRobots(t *testing.T) {
	app := &db.Application{ID: "app-1", Name: "api", Port: 8080}
	domains := []*db.Domain{{
		ID:             "d1",
		ApplicationID:  app.ID,
		Host:           "panel.example.com",
		TLSEnabled:     true,
		Path:           "/api/v3",
		Port:           9090,
		RedirectHTTPS:  true,
		SearchIndexing: false,
	}}
	// No ACME account configured: TLS routers exist, but without a resolver.
	out := DynamicConfigYAML(app, domains, false)

	for _, want := range []string{
		"Host(`panel.example.com`) && PathPrefix(`/api/v3`)",
		"app-app-1-robots-0:",
		"X-Robots-Tag: none",
		"app-app-1-redirect-to-https-0:",
		"scheme: https",
		"url: \"http://meghify-app-app-1:9090\"",
		"- http",
		"- https",
		"tls:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "certResolver") {
		t.Fatalf("no ACME account, yet a resolver was attached:\n%s", out)
	}
	// A plain domain never gets an https router (it would have nothing to
	// serve) — the redirect option only applies where TLS exists.
	plain := []*db.Domain{{
		ID: "d2", ApplicationID: app.ID, Host: "plain.example.com",
		RedirectHTTPS: true, SearchIndexing: true,
	}}
	if out := DynamicConfigYAML(app, plain, true); strings.Contains(out, "- https") {
		t.Fatalf("plain domain must not get an https router:\n%s", out)
	}
}

func TestDynamicConfigYAMLEmpty(t *testing.T) {
	out := DynamicConfigYAML(&db.Application{ID: "app-1", Name: "x", Port: 80}, nil, true)
	// Traefik rejects standalone empty sections and one unparseable file
	// poisons the whole directory, so an app without domains must yield a
	// comment-only file.
	if strings.Contains(out, "http:") || strings.Contains(out, "routers") {
		t.Fatalf("a domainless app must not emit a config skeleton:\n%s", out)
	}
	if !strings.Contains(out, "managed by meghify") {
		t.Fatalf("expected the managed header comment, got:\n%s", out)
	}
}
