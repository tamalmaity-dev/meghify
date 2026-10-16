package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("record not found")

// UserRepo persists panel members, sessions and API tokens.
type UserRepo struct{ db *sql.DB }

// CreateUser inserts a member.
func (r *UserRepo) CreateUser(ctx context.Context, u *User) error {
	now := Text(time.Now().UTC())
	q := `INSERT INTO users (id, email, password_hash, name, role, totp_secret, totp_enabled, totp_confirmed, created_at, updated_at)
	      VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := r.db.ExecContext(ctx, q,
		u.ID, u.Email, u.PasswordHash, u.Name, u.Role, u.TOTPSecret, u.TOTPEnabled, u.TOTPEnabled, now, now,
	); err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	u.CreatedAt, u.UpdatedAt = Parse(now), Parse(now)
	return nil
}

// UserByEmail loads a member by email address.
func (r *UserRepo) UserByEmail(ctx context.Context, email string) (*User, error) {
	return r.scanUser(r.db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, name, role, totp_secret, totp_enabled, created_at, updated_at
		   FROM users WHERE email = ?`, email))
}

// UserByID loads a member by id.
func (r *UserRepo) UserByID(ctx context.Context, id string) (*User, error) {
	return r.scanUser(r.db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, name, role, totp_secret, totp_enabled, created_at, updated_at
		   FROM users WHERE id = ?`, id))
}

func (r *UserRepo) scanUser(row *sql.Row) (*User, error) {
	var u User
	var createdAt, updatedAt string
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.Role,
		&u.TOTPSecret, &u.TOTPEnabled, &createdAt, &updatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("scan user: %w", err)
	}
	u.CreatedAt, u.UpdatedAt = Parse(createdAt), Parse(updatedAt)
	return &u, nil
}

// ListUsers returns every member, newest first.
func (r *UserRepo) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, email, password_hash, name, role, totp_secret, totp_enabled, created_at, updated_at
		   FROM users ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*User
	for rows.Next() {
		var u User
		var createdAt, updatedAt string
		if err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.Role,
			&u.TOTPSecret, &u.TOTPEnabled, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		u.CreatedAt, u.UpdatedAt = Parse(createdAt), Parse(updatedAt)
		out = append(out, &u)
	}
	return out, rows.Err()
}

// UpdateUserProfile changes display name and role.
func (r *UserRepo) UpdateUserProfile(ctx context.Context, id, name, role string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE users SET name = ?, role = ?, updated_at = ? WHERE id = ?`,
		name, role, Text(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// UpdateUserPassword replaces a password hash and clears TOTP, forcing re-enrolment.
func (r *UserRepo) UpdateUserPassword(ctx context.Context, id, hash string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, totp_secret = '', totp_enabled = 0, updated_at = ? WHERE id = ?`,
		hash, Text(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// SetTOTP stores a pending or confirmed TOTP seed.
func (r *UserRepo) SetTOTP(ctx context.Context, id, secret string, confirmed bool) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE users SET totp_secret = ?, totp_confirmed = ?, totp_enabled = ?, updated_at = ? WHERE id = ?`,
		secret, confirmed, confirmed, Text(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// DeleteUser removes a member. Cascades to sessions and tokens.
func (r *UserRepo) DeleteUser(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}

// CreateSession stores a session.
func (r *UserRepo) CreateSession(ctx context.Context, s *Session) error {
	q := `INSERT INTO sessions (id, user_id, token_hash, ip, user_agent, expires_at, created_at)
	      VALUES (?, ?, ?, ?, ?, ?, ?)`
	if _, err := r.db.ExecContext(ctx, q,
		s.ID, s.UserID, s.TokenHash, s.IP, s.UserAgent, Text(s.ExpiresAt), Text(time.Now().UTC()),
	); err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// SessionByTokenHash resolves a session from its hashed token.
func (r *UserRepo) SessionByTokenHash(ctx context.Context, hash string) (*Session, error) {
	var s Session
	var expiresAt, createdAt string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, token_hash, ip, user_agent, expires_at, created_at
		   FROM sessions WHERE token_hash = ?`, hash).
		Scan(&s.ID, &s.UserID, &s.TokenHash, &s.IP, &s.UserAgent, &expiresAt, &createdAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	s.ExpiresAt, s.CreatedAt = Parse(expiresAt), Parse(createdAt)
	return &s, nil
}

// DeleteSession removes one session.
func (r *UserRepo) DeleteSession(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// DeleteExpiredSessions prunes stale sessions. Returns the row count removed.
func (r *UserRepo) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, Text(time.Now().UTC()))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CreateAPIToken stores an automation token.
func (r *UserRepo) CreateAPIToken(ctx context.Context, t *APIToken) error {
	q := `INSERT INTO api_tokens (id, user_id, name, token_hash, prefix, scopes, expires_at, last_used_at, created_at)
	      VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := r.db.ExecContext(ctx, q,
		t.ID, t.UserID, t.Name, t.TokenHash, t.Prefix, encodeScopes(t.Scopes),
		NullString(TextOptional(t.ExpiresAt)), NullString(TextOptional(t.LastUsedAt)), Text(time.Now().UTC()),
	); err != nil {
		return fmt.Errorf("insert api token: %w", err)
	}
	return nil
}

// ListAPITokens returns tokens belonging to a user.
func (r *UserRepo) ListAPITokens(ctx context.Context, userID string) ([]*APIToken, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, name, token_hash, prefix, scopes, expires_at, last_used_at, created_at
		   FROM api_tokens WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*APIToken
	for rows.Next() {
		var t APIToken
		var scopes, createdAt string
		var expiresAt, lastUsedAt sql.NullString
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.TokenHash, &t.Prefix,
			&scopes, &expiresAt, &lastUsedAt, &createdAt); err != nil {
			return nil, err
		}
		t.Scopes = decodeScopes(scopes)
		t.CreatedAt = Parse(createdAt)
		if expiresAt.Valid {
			at := Parse(expiresAt.String)
			t.ExpiresAt = &at
		}
		if lastUsedAt.Valid {
			at := Parse(lastUsedAt.String)
			t.LastUsedAt = &at
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

// APITokenByHash resolves a token from its hash.
func (r *UserRepo) APITokenByHash(ctx context.Context, hash string) (*APIToken, error) {
	var t APIToken
	var scopes, createdAt string
	var expiresAt, lastUsedAt sql.NullString
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, name, token_hash, prefix, scopes, expires_at, last_used_at, created_at
		   FROM api_tokens WHERE token_hash = ?`, hash).
		Scan(&t.ID, &t.UserID, &t.Name, &t.TokenHash, &t.Prefix,
			&scopes, &expiresAt, &lastUsedAt, &createdAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	t.Scopes = decodeScopes(scopes)
	t.CreatedAt = Parse(createdAt)
	if expiresAt.Valid {
		at := Parse(expiresAt.String)
		t.ExpiresAt = &at
	}
	if lastUsedAt.Valid {
		at := Parse(lastUsedAt.String)
		t.LastUsedAt = &at
	}
	return &t, nil
}

// TouchAPIToken records the last time a token authenticated.
func (r *UserRepo) TouchAPIToken(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, Text(time.Now().UTC()), id)
	return err
}

// DeleteAPIToken revokes a token.
func (r *UserRepo) DeleteAPIToken(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return rowsAffected(res)
}
