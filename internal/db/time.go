package db

import (
	"time"

	"github.com/google/uuid"
)

// Text renders a timestamp for storage. RFC3339Nano sorts lexicographically,
// which keeps ORDER BY usable on both drivers.
func Text(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// Parse reads a stored timestamp. Empty input yields the zero time so that
// nullable columns do not need special handling at every call site.
func Parse(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t.UTC()
	}
	// Fall back to the classic sqlite "2006-01-02 15:04:05" shape.
	if t, err := time.Parse("2006-01-02 15:04:05", value); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

// NewID returns a URL-safe unique identifier.
func NewID() string { return uuid.NewString() }

// NullString renders an optional string, returning nil for the empty value so
// it can be stored as SQL NULL.
func NullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
