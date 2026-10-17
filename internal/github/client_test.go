package github

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testKeyPEM(t *testing.T) (privatePEM string, key *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return string(block), key
}

func TestSignAppJWT(t *testing.T) {
	privatePEM, key := testKeyPEM(t)
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)

	token, err := SignAppJWT(12345, privatePEM, now)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt has %d parts, want 3", len(parts))
	}

	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	if string(header) != `{"alg":"RS256","typ":"JWT"}` {
		t.Fatalf("unexpected header: %s", header)
	}

	claims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var parsed struct {
		Iss int64 `json:"iss"`
		Iat int64 `json:"iat"`
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(claims, &parsed); err != nil {
		t.Fatalf("parse claims: %v", err)
	}
	if parsed.Iss != 12345 {
		t.Fatalf("iss = %d, want 12345", parsed.Iss)
	}
	if got := now.Add(-60 * time.Second).Unix(); parsed.Iat != got {
		t.Fatalf("iat = %d, want %d", parsed.Iat, got)
	}
	if got := now.Add(9 * time.Minute).Unix(); parsed.Exp != got {
		t.Fatalf("exp = %d, want %d", parsed.Exp, got)
	}

	// The signature must verify with the matching public key.
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}
}

func TestSignAppJWTRejectsGarbage(t *testing.T) {
	if _, err := SignAppJWT(1, "not a pem", time.Now()); err == nil {
		t.Fatal("expected an error for a non-PEM key")
	}
}

func TestVerifyWebhookSignature(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	secret := "s3cret"

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	valid := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !VerifyWebhookSignature(secret, body, valid) {
		t.Fatal("valid signature rejected")
	}
	if VerifyWebhookSignature(secret, append(body, ' '), valid) {
		t.Fatal("tampered body accepted")
	}
	if VerifyWebhookSignature(secret, body, "sha1=whatever") {
		t.Fatal("wrong algorithm accepted")
	}
	if VerifyWebhookSignature("", body, valid) {
		t.Fatal("empty secret accepted")
	}
}

func TestBuildManifest(t *testing.T) {
	manifest := BuildManifest("http://panel.test:8080/", "meghify-test", "http://panel.test:8080/api/webhooks/github")

	if manifest.Name != "meghify-test" {
		t.Fatalf("name = %q", manifest.Name)
	}
	if manifest.RedirectURL != "http://panel.test:8080/api/github/callback" {
		t.Fatalf("redirect_url = %q", manifest.RedirectURL)
	}
	if manifest.SetupURL != "http://panel.test:8080/api/github/setup" {
		t.Fatalf("setup_url = %q", manifest.SetupURL)
	}
	if !manifest.HookAttributes.Active || manifest.HookAttributes.URL != "http://panel.test:8080/api/webhooks/github" {
		t.Fatalf("hook attributes = %+v", manifest.HookAttributes)
	}
	if got := manifest.DefaultPermissions["contents"]; got != "read" {
		t.Fatalf("contents permission = %q", got)
	}
	if len(manifest.DefaultEvents) != 1 || manifest.DefaultEvents[0] != "push" {
		t.Fatalf("events = %v", manifest.DefaultEvents)
	}
	if manifest.Public {
		t.Fatal("app should not default to public")
	}
}

// TestClientAgainstStub walks the whole API surface against a stub GitHub so
// URL building, headers and decoding are covered without network access.
func TestClientAgainstStub(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/app-manifests/the-code/conversions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("conversions method = %s", r.Method)
		}
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("conversions should be anonymous, got %q", auth)
		}
		writeJSON(w, map[string]any{
			"id": 99, "slug": "meghify-test", "name": "meghify-test",
			"client_id": "cid", "client_secret": "csecret",
			"pem": "PEM", "webhook_secret": "whsecret",
			"html_url": "https://github.com/apps/meghify-test",
		})
	})
	mux.HandleFunc("/app/installations", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer app-jwt" {
			t.Errorf("installations auth = %q", r.Header.Get("Authorization"))
		}
		writeJSON(w, []map[string]any{{
			"id": 42, "repository_selection": "all",
			"account": map[string]string{"login": "acme", "type": "Organization"},
		}})
	})
	mux.HandleFunc("/app/installations/42/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"token": "ghs_installtoken"})
	})
	mux.HandleFunc("/installation/repositories", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ghs_installtoken" {
			t.Errorf("repositories auth = %q", r.Header.Get("Authorization"))
		}
		writeJSON(w, map[string]any{
			"total_count": 1,
			"repositories": []map[string]any{{
				"id": 7, "full_name": "acme/app", "private": true,
				"default_branch": "main", "clone_url": "https://github.com/acme/app.git",
			}},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClient("")
	client.APIURL = server.URL
	ctx := context.Background()

	creds, err := client.ParseManifest(ctx, "the-code")
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if creds.ID != 99 || creds.WebhookSecret != "whsecret" || creds.PEM != "PEM" {
		t.Fatalf("unexpected credentials: %+v", creds)
	}

	installations, err := client.ListInstallations(ctx, "app-jwt")
	if err != nil {
		t.Fatalf("list installations: %v", err)
	}
	if len(installations) != 1 || installations[0].ID != 42 || installations[0].Account.Login != "acme" {
		t.Fatalf("unexpected installations: %+v", installations)
	}

	token, err := client.InstallationToken(ctx, "app-jwt", 42)
	if err != nil {
		t.Fatalf("installation token: %v", err)
	}
	if token != "ghs_installtoken" {
		t.Fatalf("token = %q", token)
	}

	repos, err := client.ListRepositories(ctx, token)
	if err != nil {
		t.Fatalf("list repositories: %v", err)
	}
	if repos.TotalCount != 1 || repos.Repositories[0].FullName != "acme/app" || !repos.Repositories[0].Private {
		t.Fatalf("unexpected repositories: %+v", repos)
	}
}

func TestClientSurfacesAPIErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, map[string]string{"message": "Bad credentials"})
	}))
	defer server.Close()

	client := NewClient("")
	client.APIURL = server.URL
	_, err := client.ListInstallations(context.Background(), "jwt")
	if err == nil || !strings.Contains(err.Error(), "Bad credentials") {
		t.Fatalf("error = %v, want GitHub's message", err)
	}
}

func TestNewClientDerivesEnterpriseAPIURL(t *testing.T) {
	public := NewClient("https://github.com")
	if public.APIURL != DefaultAPIURL {
		t.Fatalf("public api url = %q", public.APIURL)
	}
	enterprise := NewClient("https://git.corp.test/")
	if enterprise.BaseURL != "https://git.corp.test" || enterprise.APIURL != "https://git.corp.test/api/v3" {
		t.Fatalf("enterprise urls = %q %q", enterprise.BaseURL, enterprise.APIURL)
	}
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}
