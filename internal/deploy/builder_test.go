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
