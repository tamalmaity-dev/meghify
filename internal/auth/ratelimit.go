package auth

import (
	"sync"
	"time"
)

// loginLimiter is a small fixed-window limiter for password authentication.
// It is deliberately in-process: the panel is a single self-hosted control
// plane, and keeping it here avoids another service just to protect login.
type loginLimiter struct {
	mu      sync.Mutex
	entries map[string]limiterEntry

	max    int
	window time.Duration
	now    func() time.Time
}

type limiterEntry struct {
	count   int
	resetAt time.Time
}

func newLoginLimiter(max int, window time.Duration) *loginLimiter {
	if max < 1 {
		max = 1
	}
	if window <= 0 {
		window = 15 * time.Minute
	}
	return &loginLimiter{
		entries: map[string]limiterEntry{},
		max:     max,
		window:  window,
		now:     time.Now,
	}
}

// allow consumes one attempt for every key. It returns false (and how long the
// caller should wait) when any key has exhausted its budget.
func (l *loginLimiter) allow(keys ...string) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	// Opportunistic cleanup keeps a long-running panel from accumulating one
	// entry per email address ever tried.
	if len(l.entries) > 4096 {
		for key, entry := range l.entries {
			if !entry.resetAt.After(now) {
				delete(l.entries, key)
			}
		}
	}

	wait := time.Duration(0)
	for _, key := range keys {
		if key == "" {
			continue
		}
		entry := l.entries[key]
		if !entry.resetAt.After(now) {
			entry = limiterEntry{resetAt: now.Add(l.window)}
		}
		if entry.count >= l.max {
			until := entry.resetAt.Sub(now)
			if until > wait {
				wait = until
			}
			continue
		}
		entry.count++
		l.entries[key] = entry
	}
	if wait > 0 {
		return false, wait
	}
	return true, 0
}

func (l *loginLimiter) reset(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, key := range keys {
		delete(l.entries, key)
	}
}
