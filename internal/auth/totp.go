package auth

import (
	"context"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"

	"github.com/pquerna/otp/totp"
	"github.com/tamalmaity-dev/meghify/internal/db"
)

// totpParams control the issuer, period and digits used for new enrolments.
const (
	totpIssuer = "Meghify"
	totpPeriod = 30
)

// EnrollTOTP creates a pending second factor for a user and returns the otpauth
// URL the authenticator app needs. The secret is stored unconfirmed until
// ConfirmTOTP succeeds.
func (s *Service) EnrollTOTP(ctx context.Context, user *db.User) (uri string, secret string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: user.Email,
		Period:      totpPeriod,
	})
	if err != nil {
		return "", "", fmt.Errorf("generate totp: %w", err)
	}
	sealed, err := s.box.Encrypt(key.Secret())
	if err != nil {
		return "", "", err
	}
	if err := s.store.Users.SetTOTP(ctx, user.ID, sealed, false); err != nil {
		return "", "", err
	}
	// Keep the in-memory copy in the same form as the database. The raw secret
	// is returned once for the authenticator app; only the sealed value is
	// persisted.
	user.TOTPSecret = sealed
	return key.URL(), key.Secret(), nil
}

// ConfirmTOTP validates a code against the pending secret and enables 2FA.
func (s *Service) ConfirmTOTP(ctx context.Context, user *db.User, code string) error {
	if user.TOTPSecret == "" {
		return errors.New("no pending totp enrolment")
	}
	secret, err := s.openTOTPSecret(ctx, user)
	if err != nil {
		return err
	}
	if err := s.VerifyTOTP(secret, code); err != nil {
		return err
	}
	sealed, err := s.box.Encrypt(secret)
	if err != nil {
		return err
	}
	if err := s.store.Users.SetTOTP(ctx, user.ID, sealed, true); err != nil {
		return err
	}
	user.TOTPSecret = sealed
	user.TOTPEnabled = true
	return nil
}

// DisableTOTP turns off two-factor authentication after a password check.
func (s *Service) DisableTOTP(ctx context.Context, user *db.User, password string) error {
	if err := VerifyPassword(password, user.PasswordHash); err != nil {
		return ErrInvalidCredentials
	}
	return s.store.Users.SetTOTP(ctx, user.ID, "", false)
}

// VerifyUserTOTP opens the sealed seed and validates a login code.
func (s *Service) VerifyUserTOTP(ctx context.Context, user *db.User, code string) error {
	secret, err := s.openTOTPSecret(ctx, user)
	if err != nil {
		return err
	}
	return s.VerifyTOTP(secret, code)
}

// openTOTPSecret decrypts the stored TOTP seed. Rows created before TOTP
// encryption existed are raw base32; those are accepted once and transparently
// re-sealed so the upgrade does not force every user to re-enrol.
func (s *Service) openTOTPSecret(ctx context.Context, user *db.User) (string, error) {
	if user.TOTPSecret == "" {
		return "", errors.New("totp is not configured")
	}
	plain, err := s.box.Decrypt(user.TOTPSecret)
	if err == nil {
		return plain, nil
	}
	if !looksLikeTOTPSecret(user.TOTPSecret) {
		return "", fmt.Errorf("open totp secret: %w", err)
	}
	legacy := strings.ToUpper(strings.TrimSpace(user.TOTPSecret))
	sealed, sealErr := s.box.Encrypt(legacy)
	if sealErr != nil {
		return "", sealErr
	}
	if err := s.store.Users.SetTOTP(ctx, user.ID, sealed, user.TOTPEnabled); err != nil {
		return "", err
	}
	user.TOTPSecret = sealed
	return legacy, nil
}

// looksLikeTOTPSecret distinguishes a legacy base32 seed from a sealed
// ciphertext. A sealed blob is base64url and may contain lowercase letters,
// '-' or '_', none of which are valid base32 input.
func looksLikeTOTPSecret(value string) bool {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" || strings.ContainsAny(value, "-_") {
		return false
	}
	_, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(value)
	return err == nil
}

// VerifyTOTP checks a code against a secret with a one-step skew window.
func (s *Service) VerifyTOTP(secret, code string) error {
	if secret == "" {
		return errors.New("totp is not configured")
	}
	if !totp.Validate(code, secret) {
		return errors.New("invalid authentication code")
	}
	return nil
}
