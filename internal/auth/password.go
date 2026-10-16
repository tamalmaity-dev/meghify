package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. RFC 9106 recommends these as a balanced second-choice
// profile; they cost roughly 60MB and 2 iterations on modern hardware, which is
// acceptable on a Raspberry Pi where the panel must stay small.
const (
	argonTime    uint32 = 2
	argonMemory  uint32 = 64 * 1024
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32
)

var (
	// ErrInvalidPassword means the hash does not match the supplied password.
	ErrInvalidPassword = errors.New("invalid password")
)

// HashPassword derives an Argon2id hash encoded as
// $argon2id$v=19$m=...,t=...,p=...$salt$hash
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword checks a password against a stored hash in constant time.
func VerifyPassword(password, encoded string) error {
	salt, key, params, err := decodeArgon2(encoded)
	if err != nil {
		return err
	}
	candidate := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, params.keyLen)
	if subtle.ConstantTimeCompare(candidate, key) == 1 {
		return nil
	}
	return ErrInvalidPassword
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
	keyLen  uint32
}

func decodeArgon2(encoded string) (salt, key []byte, p argonParams, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return nil, nil, p, fmt.Errorf("unsupported password hash format")
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return nil, nil, p, fmt.Errorf("parse argon2 params: %w", err)
	}
	if salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil {
		return nil, nil, p, fmt.Errorf("decode salt: %w", err)
	}
	if key, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil {
		return nil, nil, p, fmt.Errorf("decode hash: %w", err)
	}
	p.keyLen = uint32(len(key))
	return salt, key, p, nil
}

// RandomToken returns a URL-safe random string of n characters.
func RandomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
