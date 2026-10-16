package auth

import (
	"context"
	"errors"
	"fmt"

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
	if err := s.store.Users.SetTOTP(ctx, user.ID, key.Secret(), false); err != nil {
		return "", "", err
	}
	return key.URL(), key.Secret(), nil
}

// ConfirmTOTP validates a code against the pending secret and enables 2FA.
func (s *Service) ConfirmTOTP(ctx context.Context, user *db.User, code string) error {
	if user.TOTPSecret == "" {
		return errors.New("no pending totp enrolment")
	}
	if err := s.VerifyTOTP(user.TOTPSecret, code); err != nil {
		return err
	}
	return s.store.Users.SetTOTP(ctx, user.ID, user.TOTPSecret, true)
}

// DisableTOTP turns off two-factor authentication after a password check.
func (s *Service) DisableTOTP(ctx context.Context, user *db.User, password string) error {
	if err := VerifyPassword(password, user.PasswordHash); err != nil {
		return ErrInvalidCredentials
	}
	return s.store.Users.SetTOTP(ctx, user.ID, "", false)
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
