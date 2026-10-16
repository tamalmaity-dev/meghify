// Package auth provides password hashing, secret encryption, session and API
// token handling, and the HTTP middleware that guards protected routes.
package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	// ErrInvalidCiphertext is returned when sealed data cannot be opened.
	ErrInvalidCiphertext = errors.New("invalid ciphertext")
)

// SecretBox seals plaintext with AES-256-GCM.
//
// The key is derived from the instance secret with SHA-256, so any 32+ byte
// string works as AUTH_SECRET without extra KDF parameters.
type SecretBox struct {
	aead cipher.AEAD
}

// NewSecretBox derives a key from the instance secret.
func NewSecretBox(secret string) (*SecretBox, error) {
	if len(strings.TrimSpace(secret)) < 16 {
		return nil, errors.New("auth secret must be at least 16 characters")
	}
	sum := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &SecretBox{aead: aead}, nil
}

// Encrypt seals plaintext and returns a base64 blob of nonce + ciphertext.
func (b *SecretBox) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := b.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Decrypt opens a blob produced by Encrypt.
func (b *SecretBox) Decrypt(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		return "", fmt.Errorf("%w: bad base64", ErrInvalidCiphertext)
	}
	if len(raw) < b.aead.NonceSize() {
		return "", ErrInvalidCiphertext
	}
	plaintext, err := b.aead.Open(nil, raw[:b.aead.NonceSize()], raw[b.aead.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidCiphertext, err)
	}
	return string(plaintext), nil
}
