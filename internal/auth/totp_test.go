package auth

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/tamalmaity-dev/meghify/internal/config"
	"github.com/tamalmaity-dev/meghify/internal/db"
)

func testAuthService(t *testing.T) (*Service, *db.Store) {
	t.Helper()
	handle, err := db.OpenPath("file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "auth.db")))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	store := db.OpenStore(handle)
	box, err := NewSecretBox("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("secret box: %v", err)
	}
	cfg := &config.Config{SessionTTL: time.Hour, APITokenTTL: time.Hour, PasswordMinLen: 10}
	return NewService(store, box, cfg), store
}

func TestTOTPLoginRequiresAndAcceptsCode(t *testing.T) {
	service, store := testAuthService(t)
	ctx := context.Background()
	user, err := service.Register(ctx, "admin@example.com", "correct horse battery staple", "Admin")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	_, secret, err := service.EnrollTOTP(ctx, user)
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	stored, err := store.Users.UserByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if stored.TOTPSecret == secret {
		t.Fatal("TOTP secret was stored in plaintext")
	}
	opened, err := service.openTOTPSecret(ctx, stored)
	if err != nil {
		t.Fatalf("decrypt TOTP secret: %v", err)
	}
	if opened != secret {
		t.Fatal("decrypted TOTP secret does not match the enrollment secret")
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	if err := service.ConfirmTOTP(ctx, user, code); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	if _, _, _, err := service.Login(ctx, user.Email, "correct horse battery staple", "", "203.0.113.7", "test"); err != ErrTOTPRequired {
		t.Fatalf("login without code error = %v, want ErrTOTPRequired", err)
	}

	code, err = totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	loggedIn, session, raw, err := service.Login(ctx, user.Email, "correct horse battery staple", code, "203.0.113.7", "test")
	if err != nil {
		t.Fatalf("login with code: %v", err)
	}
	if loggedIn.ID != user.ID || session == nil || raw == "" {
		t.Fatalf("login returned user=%v session=%v raw=%q", loggedIn, session, raw)
	}
}
