package auth

import (
	"testing"
	"time"
)

func TestLoginLimiterLocksAndResetsAfterWindow(t *testing.T) {
	now := time.Now()
	limiter := newLoginLimiter(2, time.Minute)
	limiter.now = func() time.Time { return now }

	for i := 0; i < 2; i++ {
		if ok, _ := limiter.allow("account:a@example.com"); !ok {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
	if ok, wait := limiter.allow("account:a@example.com"); ok || wait <= 0 || wait > time.Minute {
		t.Fatalf("third attempt = allowed %v, wait %s; want the remaining window", ok, wait)
	}

	now = now.Add(2 * time.Minute)
	if ok, _ := limiter.allow("account:a@example.com"); !ok {
		t.Fatal("a new window should allow the account again")
	}
}

func TestLoginLimiterResetClearsSuccessfulLoginBudget(t *testing.T) {
	now := time.Now()
	limiter := newLoginLimiter(1, time.Minute)
	limiter.now = func() time.Time { return now }

	if ok, _ := limiter.allow("ip:203.0.113.4"); !ok {
		t.Fatal("first attempt should be allowed")
	}
	if ok, _ := limiter.allow("ip:203.0.113.4"); ok {
		t.Fatal("second attempt should be limited")
	}
	limiter.reset("ip:203.0.113.4")
	if ok, _ := limiter.allow("ip:203.0.113.4"); !ok {
		t.Fatal("reset should restore the budget")
	}
}
