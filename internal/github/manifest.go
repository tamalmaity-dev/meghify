package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Manifest is the registration payload GitHub turns into a GitHub App. The
// panel builds one per instance; the browser POSTs it to GitHub, and the user
// only has to confirm.
type Manifest struct {
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	Description        string            `json:"description,omitempty"`
	HookAttributes     HookAttributes    `json:"hook_attributes"`
	RedirectURL        string            `json:"redirect_url"`
	SetupURL           string            `json:"setup_url"`
	Public             bool              `json:"public"`
	DefaultEvents      []string          `json:"default_events"`
	DefaultPermissions map[string]string `json:"default_permissions"`
}

// HookAttributes tells GitHub where to deliver webhooks.
type HookAttributes struct {
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

// BuildManifest describes the app the panel wants for one instance: it needs to
// read repository contents (to clone, including private repos), see metadata
// (to attribute pushes) and read commit email addresses, and it subscribes to
// push events so a commit to the tracked branch can redeploy.
func BuildManifest(panelURL, name, webhookURL string) Manifest {
	panelURL = strings.TrimRight(panelURL, "/")
	return Manifest{
		Name:        name,
		URL:         panelURL,
		Description: "Deploys repositories from this Meghify panel.",
		HookAttributes: HookAttributes{
			URL:    webhookURL,
			Active: true,
		},
		RedirectURL: panelURL + "/api/github/callback",
		SetupURL:    panelURL + "/api/github/setup",
		Public:      false,
		DefaultEvents: []string{
			"push",
		},
		DefaultPermissions: map[string]string{
			"contents": "read",
			"metadata": "read",
			"emails":   "read",
		},
	}
}

// VerifyWebhookSignature checks GitHub's X-Hub-Signature-256 header, which is
// "sha256=<hex>" — an HMAC of the raw body keyed with the app's webhook secret.
func VerifyWebhookSignature(secret string, body []byte, signature string) bool {
	const prefix = "sha256="
	signature = strings.TrimSpace(signature)
	if secret == "" || !strings.HasPrefix(signature, prefix) {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(signature, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}
