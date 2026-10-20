package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tamalmaity-dev/meghify/internal/config"
	"github.com/tamalmaity-dev/meghify/internal/db"
)

// Service implements login, session and API token handling.
type Service struct {
	store   *db.Store
	box     *SecretBox
	cfg     *config.Config
	nowFunc func() time.Time
	// Separate buckets prevent one noisy source from locking out every account
	// behind the same NAT, while still making one account expensive to guess.
	loginByAccount *loginLimiter
	loginByIP      *loginLimiter
}

// NewService builds the auth service.
func NewService(store *db.Store, box *SecretBox, cfg *config.Config) *Service {
	return &Service{
		store:   store,
		box:     box,
		cfg:     cfg,
		nowFunc: time.Now,
		// Ten guesses per account and forty per source address in a quarter of
		// an hour: enough for a mistyped password, far too few for a brute-force
		// run against an Argon2id verifier.
		loginByAccount: newLoginLimiter(10, 15*time.Minute),
		loginByIP:      newLoginLimiter(40, 15*time.Minute),
	}
}

var (
	// ErrInvalidCredentials is returned for unknown emails or bad passwords.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrTOTPRequired means a second factor is still needed.
	ErrTOTPRequired = errors.New("totp code required")
	// ErrUserExists is returned when an email is already registered.
	ErrUserExists = errors.New("user already exists")
	// ErrForbidden means the caller lacks the required role.
	ErrForbidden = errors.New("forbidden")
	// ErrSignupDisabled blocks registration when AllowSignup is false.
	ErrSignupDisabled = errors.New("registration is disabled")
	// ErrNoUsers is returned when bootstrap has not happened yet.
	ErrNoUsers = errors.New("no users exist")
	// ErrRateLimited means too many login attempts were made in a short window.
	ErrRateLimited = errors.New("too many login attempts")
)

// HasUsers reports whether the instance has been bootstrapped.
func (s *Service) HasUsers(ctx context.Context) (bool, error) {
	users, err := s.store.Users.ListUsers(ctx)
	if err != nil {
		return false, err
	}
	return len(users) > 0, nil
}

// Register creates a member. The very first user becomes an admin; afterwards
// registration is blocked unless AllowSignup is explicitly enabled.
func (s *Service) Register(ctx context.Context, email, password, name string) (*db.User, error) {
	hasUsers, err := s.HasUsers(ctx)
	if err != nil {
		return nil, err
	}
	if hasUsers && !s.cfg.AllowSignup {
		return nil, ErrSignupDisabled
	}
	role := db.RoleMember
	if !hasUsers {
		role = db.RoleAdmin
	}
	return s.createUser(ctx, email, password, name, role)
}

// RegisterAsAdmin creates a member on behalf of an admin, bypassing the closed
// signup flag and forcing the requested role.
func (s *Service) RegisterAsAdmin(ctx context.Context, email, password, name, role string) (*db.User, error) {
	if role != db.RoleAdmin && role != db.RoleMember {
		role = db.RoleMember
	}
	return s.createUser(ctx, email, password, name, role)
}

func (s *Service) createUser(ctx context.Context, email, password, name, role string) (*db.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") {
		return nil, errors.New("a valid email is required")
	}
	if len(password) < s.cfg.PasswordMinLen {
		return nil, fmt.Errorf("password must be at least %d characters", s.cfg.PasswordMinLen)
	}

	existing, err := s.store.Users.UserByEmail(ctx, email)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return nil, err
	}
	if existing != nil {
		return nil, ErrUserExists
	}

	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}

	user := &db.User{
		ID:           db.NewID(),
		Email:        email,
		PasswordHash: hash,
		Name:         strings.TrimSpace(name),
		Role:         role,
	}
	if err := s.store.Users.CreateUser(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}

// IssueSession creates a session for an already authenticated user and hands the
// raw token to the caller. Used by bootstrap and by the TOTP verification step.
func (s *Service) IssueSession(ctx context.Context, user *db.User, ip, userAgent string) (*db.Session, string, error) {
	token, err := RandomToken(32)
	if err != nil {
		return nil, "", err
	}
	session := &db.Session{
		ID:        db.NewID(),
		UserID:    user.ID,
		TokenHash: hashToken(token),
		IP:        ip,
		UserAgent: userAgent,
		ExpiresAt: s.nowFunc().Add(s.cfg.SessionTTL),
	}
	if err := s.store.Users.CreateSession(ctx, session); err != nil {
		return nil, "", err
	}
	return session, token, nil
}

// Login verifies credentials and issues a session.
//
// When the account has TOTP enabled a code is mandatory. A wrong code is
// indistinguishable from a wrong password to the caller.
func (s *Service) Login(ctx context.Context, email, password, code, ip, userAgent string) (*db.User, *db.Session, string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	accountKey := "account:" + email
	ipKey := "ip:" + strings.TrimSpace(ip)
	if ok, _ := s.loginByAccount.allow(accountKey); !ok {
		return nil, nil, "", ErrRateLimited
	}
	if ok, _ := s.loginByIP.allow(ipKey); !ok {
		return nil, nil, "", ErrRateLimited
	}

	user, err := s.store.Users.UserByEmail(ctx, email)
	if errors.Is(err, db.ErrNotFound) {
		// Spend the same time hashing so timing does not leak account existence.
		_, _ = HashPassword(password)
		return nil, nil, "", ErrInvalidCredentials
	}
	if err != nil {
		return nil, nil, "", err
	}

	if err := VerifyPassword(password, user.PasswordHash); err != nil {
		return nil, nil, "", ErrInvalidCredentials
	}

	if user.TOTPEnabled {
		if code == "" {
			return nil, nil, "", ErrTOTPRequired
		}
		if err := s.VerifyUserTOTP(ctx, user, code); err != nil {
			return nil, nil, "", ErrInvalidCredentials
		}
	}

	s.loginByAccount.reset(accountKey)
	s.loginByIP.reset(ipKey)
	session, token, err := s.IssueSession(ctx, user, ip, userAgent)
	if err != nil {
		return nil, nil, "", err
	}
	return user, session, token, nil
}

// CompleteTOTPLogin finishes a two-factor login by issuing a session.
func (s *Service) CompleteTOTPLogin(ctx context.Context, user *db.User, ip, userAgent string) (*db.Session, string, error) {
	return s.IssueSession(ctx, user, ip, userAgent)
}

// Session resolves a token to a user and session, rejecting expired entries.
func (s *Service) Session(ctx context.Context, token string) (*db.User, *db.Session, error) {
	if token == "" {
		return nil, nil, nil
	}
	session, err := s.store.Users.SessionByTokenHash(ctx, hashToken(token))
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if session.ExpiresAt.Before(s.nowFunc()) {
		s.store.Users.DeleteSession(ctx, session.ID)
		return nil, nil, nil
	}
	user, err := s.store.Users.UserByID(ctx, session.UserID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return user, session, nil
}

// Logout invalidates a session.
func (s *Service) Logout(ctx context.Context, sessionID string) error {
	if err := s.store.Users.DeleteSession(ctx, sessionID); err != nil && !errors.Is(err, db.ErrNotFound) {
		return err
	}
	return nil
}

// CreateAPIToken issues a long-lived automation credential. The raw token is
// returned exactly once.
func (s *Service) CreateAPIToken(ctx context.Context, userID, name string, scopes []string, ttl time.Duration) (raw string, token *db.APIToken, err error) {
	// A recognisable prefix makes it obvious in logs and config files that a
	// string is a Meghify credential rather than a generic secret.
	raw, err = RandomToken(28)
	if err != nil {
		return "", nil, err
	}
	raw = "megh_" + raw

	now := s.nowFunc()
	tok := &db.APIToken{
		ID:        db.NewID(),
		UserID:    userID,
		Name:      name,
		TokenHash: hashToken(raw),
		Prefix:    raw[:12],
		Scopes:    scopes,
		CreatedAt: now,
	}
	if ttl > 0 {
		expires := now.Add(ttl)
		tok.ExpiresAt = &expires
	}
	if err := s.store.Users.CreateAPIToken(ctx, tok); err != nil {
		return "", nil, err
	}
	return raw, tok, nil
}

// APIToken authenticates a bearer token and returns the owning user.
func (s *Service) APIToken(ctx context.Context, raw string) (*db.User, *db.APIToken, error) {
	if raw == "" {
		return nil, nil, nil
	}
	token, err := s.store.Users.APITokenByHash(ctx, hashToken(raw))
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if token.ExpiresAt != nil && token.ExpiresAt.Before(s.nowFunc()) {
		s.store.Users.DeleteAPIToken(ctx, token.ID)
		return nil, nil, nil
	}
	user, err := s.store.Users.UserByID(ctx, token.UserID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	s.store.Users.TouchAPIToken(ctx, token.ID)
	return user, token, nil
}

// RotateSessions invalidates every session for a user.
func (s *Service) RotateSessions(ctx context.Context, userID string) error {
	_, err := s.store.Handle().ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// RotateSessionsExcept invalidates every session except the one making the
// request. It is what a password change should do: the current tab stays
// signed in, every other browser is logged out.
func (s *Service) RotateSessionsExcept(ctx context.Context, userID, keepSessionID string) error {
	if keepSessionID == "" {
		return s.RotateSessions(ctx, userID)
	}
	_, err := s.store.Handle().ExecContext(ctx,
		`DELETE FROM sessions WHERE user_id = ? AND id != ?`, userID, keepSessionID)
	return err
}

// Seal encrypts a plaintext secret for storage.
func (s *Service) Seal(plaintext string) (string, error) { return s.box.Encrypt(plaintext) }

// Open decrypts a stored secret.
func (s *Service) Open(sealed string) (string, error) { return s.box.Decrypt(sealed) }

// hashToken returns the storage form of a bearer token. Tokens are hashed so a
// database leak does not immediately yield usable credentials.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// SessionCookieName is the name of the session cookie.
const SessionCookieName = "megh_session"

// BearerPrefix is the accepted authorization header scheme.
const BearerPrefix = "Bearer "

// API token scopes. An empty scope list means the token inherits every right
// of its owner; once scopes are set they are enforced by the auth middleware.
const (
	ScopeRead   = "read"
	ScopeWrite  = "write"
	ScopeDeploy = "deploy"
	ScopeAdmin  = "admin"
)

// ValidAPIScope reports whether a caller-supplied scope is supported.
func ValidAPIScope(scope string) bool {
	switch scope {
	case ScopeRead, ScopeWrite, ScopeDeploy, ScopeAdmin:
		return true
	}
	return false
}

// TokenKind identifies which transport a credential arrived on, which is what
// decides how it should be resolved.
type TokenKind int

const (
	// TokenKindNone means no credential was presented.
	TokenKindNone TokenKind = iota
	// TokenKindSession is a browser cookie.
	TokenKindSession
	// TokenKindBearer is an Authorization: Bearer credential.
	TokenKindBearer
)

// TokenFromRequest extracts the credential from a request together with the
// transport it arrived on. The kind is decided by transport rather than by
// inspecting the token, because guessing from length or prefix is fragile:
// session tokens and API tokens are the same length by design.
func TokenFromRequest(r *http.Request) (string, TokenKind) {
	if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
		return c.Value, TokenKindSession
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, BearerPrefix) {
		if token := strings.TrimSpace(strings.TrimPrefix(auth, BearerPrefix)); token != "" {
			return token, TokenKindBearer
		}
	}
	return "", TokenKindNone
}
