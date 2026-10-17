package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// SignAppJWT builds the short-lived RS256 JWT that authenticates the panel as
// the GitHub App itself. Installation tokens are minted with it.
//
// GitHub's clock skew tolerance is 60 seconds, so iat is backdated by the same
// amount and the token lives for nine minutes (the cap is ten).
func SignAppJWT(appID int64, privateKeyPEM string, now time.Time) (string, error) {
	key, err := parsePrivateKey(privateKeyPEM)
	if err != nil {
		return "", err
	}

	claims, err := json.Marshal(map[string]any{
		"iss": appID,
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
	})
	if err != nil {
		return "", err
	}

	signingInput := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`)) +
		"." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign app jwt: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// parsePrivateKey accepts the PEM formats GitHub hands out (PKCS#1) and the
// PKCS#8 form users paste when setting an app up by hand.
func parsePrivateKey(pemData string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, errors.New("private key is not PEM encoded")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not RSA")
	}
	return key, nil
}

// InstallationToken mints a one-hour token for one installation. It is the
// credential used to clone private repositories and list their contents.
func (c *Client) InstallationToken(ctx context.Context, jwt string, installationID int64) (string, error) {
	var out struct {
		Token string `json:"token"`
	}
	endpoint := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.APIURL, installationID)
	if err := c.do(ctx, http.MethodPost, endpoint, jwt, nil, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", errors.New("github returned an empty installation token")
	}
	return out.Token, nil
}
