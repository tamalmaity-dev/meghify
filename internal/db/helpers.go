package db

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// rowsAffected converts a zero-length UPDATE/DELETE into ErrNotFound.
func rowsAffected(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// nullString converts a nullable column to a plain string, mapping NULL to "".
func nullString(v sql.NullString) string {
	if v.Valid {
		return v.String
	}
	return ""
}

// TextOptional renders a nil-able timestamp as SQL NULL.
func TextOptional(t *time.Time) string {
	if t == nil {
		return ""
	}
	return Text(*t)
}

// encodeScopes serialises a token scope list to JSON.
func encodeScopes(scopes []string) string {
	if len(scopes) == 0 {
		return "[]"
	}
	b, err := json.Marshal(scopes)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// decodeScopes parses a stored scope list, tolerating malformed values.
func decodeScopes(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}
