package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// renewalServer answers renewals: the first failures calls fail with 503,
// then it issues "renewed" tokens.
func renewalServer(t *testing.T, failures int32) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) <= failures {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(TokenResponse{AccessToken: "renewed", ExpiresIn: 3600})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// startRenewal starts the renewal of a token that expires in lifetime and
// returns the channel of fatal reasons.
func startRenewal(t *testing.T, url string, lifetime time.Duration, p RenewalPolicy) (*TokenManager, chan error) {
	t.Helper()
	tm := mustNewTokenManager(t, url, "bootstrap", slog.Default())
	tm.SetRenewalPolicy(p)
	tm.mu.Lock()
	tm.token, tm.acquired = "initial", true
	tm.lifetime = lifetime
	tm.expiresAt = time.Now().Add(lifetime)
	tm.mu.Unlock()
	fatal := make(chan error, 1)
	tm.SetOnFatal(func(err error) { fatal <- err })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := tm.StartRenewal(ctx); err != nil {
		t.Fatal(err)
	}
	return tm, fatal
}

func waitToken(t *testing.T, tm *TokenManager, want string, fatal chan error) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for tm.Token() != want {
		select {
		case err := <-fatal:
			t.Fatalf("renewal failed: %v", err)
		case <-deadline:
			t.Fatalf("token = %q, want %q", tm.Token(), want)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// The renewal starts the margin before expiry and replaces the token.
func TestRenewalRenewsAtTheMargin(t *testing.T) {
	srv, calls := renewalServer(t, 0)
	tm, fatal := startRenewal(t, srv.URL, 2*time.Second, RenewalPolicy{Margin: 1900 * time.Millisecond})
	waitToken(t, tm, "renewed", fatal)
	if calls.Load() != 1 {
		t.Fatalf("renewal calls = %d, want 1", calls.Load())
	}
}

// Transient failures are retried with backoff until a renewal succeeds.
func TestRenewalRetriesTransientFailures(t *testing.T) {
	srv, calls := renewalServer(t, 2)
	tm, fatal := startRenewal(t, srv.URL, time.Minute, RenewalPolicy{Margin: time.Minute, Retries: 3, Backoff: 10 * time.Millisecond})
	waitToken(t, tm, "renewed", fatal)
	if calls.Load() != 3 {
		t.Fatalf("renewal calls = %d, want 2 failures and 1 success", calls.Load())
	}
}

// When every attempt fails, the fatal handler gets the reason once.
func TestRenewalFailsForGoodAfterTheRetries(t *testing.T) {
	srv, calls := renewalServer(t, 1000)
	_, fatal := startRenewal(t, srv.URL, time.Minute, RenewalPolicy{Margin: time.Minute, Retries: 3, Backoff: 10 * time.Millisecond})
	select {
	case err := <-fatal:
		if errors.Is(err, ErrTokenExpired) || calls.Load() != 3 {
			t.Fatalf("fatal = %v after %d calls, want the retries exhausted after 3", err, calls.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no fatal call")
	}
}

// The attempts stop when the token expires, also with retries left: the
// backoff never sleeps past the expiry.
func TestRenewalStopsAtExpiry(t *testing.T) {
	srv, _ := renewalServer(t, 1000)
	start := time.Now()
	_, fatal := startRenewal(t, srv.URL, 300*time.Millisecond, RenewalPolicy{Margin: 300 * time.Millisecond, Retries: 100, Backoff: time.Hour})
	select {
	case err := <-fatal:
		if !errors.Is(err, ErrTokenExpired) {
			t.Fatalf("fatal = %v, want ErrTokenExpired", err)
		}
		if d := time.Since(start); d > 3*time.Second {
			t.Fatalf("fatal after %v, want it at the expiry", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no fatal call")
	}
}

func TestRenewMargin(t *testing.T) {
	for _, tt := range []struct{ margin, lifetime, want time.Duration }{
		{0, 30 * time.Minute, 6 * time.Minute},
		{time.Minute, 30 * time.Minute, time.Minute},
		{time.Hour, 30 * time.Minute, 30 * time.Minute},
	} {
		if got := RenewMargin(tt.margin, tt.lifetime); got != tt.want {
			t.Errorf("RenewMargin(%v, %v) = %v, want %v", tt.margin, tt.lifetime, got, tt.want)
		}
	}
}
