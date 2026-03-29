package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mustNewTokenManager(t *testing.T, url, token string, log *slog.Logger) *TokenManager {
	t.Helper()
	tm, err := NewTokenManager(url, token, log)
	if err != nil {
		t.Fatalf("NewTokenManager(%q): %v", url, err)
	}
	return tm
}

func TestTokenManager_Acquire(t *testing.T) {
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)

		if r.Header.Get("Authorization") != "Bearer bootstrap-token" {
			t.Errorf("expected bootstrap token, got %q", r.Header.Get("Authorization"))
		}

		resp := TokenResponse{
			AccessToken:  "jwt-token-abc",
			RefreshToken: "refresh-xyz",
			ExpiresIn:    3600,
			TokenType:    "Bearer",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	tm := mustNewTokenManager(t, server.URL, "bootstrap-token", slog.Default())
	if err := tm.Acquire(context.Background(), "SN123", "aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	if got := tm.Token(); got != "jwt-token-abc" {
		t.Errorf("expected token 'jwt-token-abc', got %q", got)
	}

	if callCount.Load() != 1 {
		t.Errorf("expected 1 call, got %d", callCount.Load())
	}
}

func TestTokenManager_Acquire_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	tm := mustNewTokenManager(t, server.URL, "bad-token", slog.Default())
	if err := tm.Acquire(context.Background(), "SN123", "aa:bb:cc:dd:ee:ff"); err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestTokenManager_Token_ThreadSafe(t *testing.T) {
	tm := mustNewTokenManager(t, "http://127.0.0.1", "initial", slog.Default())

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				_ = tm.Token()
			}
		}()
	}
	// concurrent writer
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 100 {
			tm.mu.Lock()
			tm.token = fmt.Sprintf("token-%d", i)
			tm.mu.Unlock()
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent Token() access timed out")
	}
}

func TestTokenManager_Renew(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		resp := TokenResponse{
			AccessToken:  "renewed-token",
			RefreshToken: "new-refresh",
			ExpiresIn:    7200,
			TokenType:    "Bearer",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	tm := mustNewTokenManager(t, server.URL, "old-token", slog.Default())
	tm.refreshToken = "old-refresh"
	tm.expiresAt = time.Now().Add(time.Hour)

	if err := tm.renew(context.Background()); err != nil {
		t.Fatalf("renew failed: %v", err)
	}

	if got := tm.Token(); got != "renewed-token" {
		t.Errorf("expected 'renewed-token', got %q", got)
	}
}

func TestTokenManager_RenewWithRetry_EventualSuccess(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		resp := TokenResponse{
			AccessToken: "retry-token",
			ExpiresIn:   3600,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	tm := mustNewTokenManager(t, server.URL, "token", slog.Default())
	tm.refreshToken = "refresh"
	tm.expiresAt = time.Now().Add(time.Hour)
	tm.backoff = func(_ int) time.Duration { return time.Millisecond }

	if err := tm.renewWithRetry(context.Background()); err != nil {
		t.Fatalf("renewWithRetry should succeed after retries: %v", err)
	}

	if got := tm.Token(); got != "retry-token" {
		t.Errorf("expected 'retry-token', got %q", got)
	}
}

func TestTokenManager_RenewWithRetry_Exhausted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	tm := mustNewTokenManager(t, server.URL, "token", slog.Default())
	tm.refreshToken = "refresh"
	tm.expiresAt = time.Now().Add(time.Hour)
	tm.backoff = func(_ int) time.Duration { return time.Millisecond }

	if err := tm.renewWithRetry(context.Background()); err == nil {
		t.Fatal("expected error after exhausting retries")
	}
}

func TestTokenManager_RenewWithRetry_ContextCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tm := mustNewTokenManager(t, server.URL, "token", slog.Default())
	tm.refreshToken = "refresh"
	tm.expiresAt = time.Now().Add(time.Hour)

	if err := tm.renewWithRetry(ctx); err == nil {
		t.Fatal("expected error when context is canceled")
	}
}

func TestTokenManager_StartRenewal_NotAcquired(t *testing.T) {
	tm := mustNewTokenManager(t, "http://127.0.0.1", "token", slog.Default())
	if err := tm.StartRenewal(context.Background()); err == nil {
		t.Fatal("expected error starting renewal without Acquire")
	}
}

func TestTokenManager_StartRenewal_RenewsBeforeExpiry(t *testing.T) {
	renewed := make(chan struct{})
	var renewCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := TokenResponse{
			AccessToken: "renewed",
			ExpiresIn:   60,
		}
		_ = json.NewEncoder(w).Encode(resp)
		// Signal AFTER encoding the response so the client has data to read.
		if renewCalls.Add(1) == 1 {
			close(renewed)
		}
	}))
	defer server.Close()

	tm := mustNewTokenManager(t, server.URL, "bootstrap", slog.Default())
	// Set an already-expired token so renewLoop fires the immediate path (timer.Reset(0)).
	tm.mu.Lock()
	tm.token = "initial-jwt"
	tm.refreshToken = "refresh"
	tm.expiresAt = time.Now().Add(-time.Second)
	tm.acquired = true
	tm.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := tm.StartRenewal(ctx); err != nil {
		t.Fatalf("StartRenewal failed: %v", err)
	}

	// Wait for the server to finish sending the renewal response.
	select {
	case <-renewed:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for renewal")
	}

	// Poll briefly: the goroutine needs to read and apply the HTTP response.
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case <-ticker.C:
			if tm.Token() == "renewed" {
				return
			}
		case <-timeout:
			t.Fatalf("expected token 'renewed', got %q", tm.Token())
		}
	}
}

func TestTokenManager_StartRenewal_OnFatalCalledOnExhaustion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	tm := mustNewTokenManager(t, server.URL, "bootstrap", slog.Default())
	tm.mu.Lock()
	tm.token = "initial-jwt"
	tm.refreshToken = "refresh"
	tm.expiresAt = time.Now().Add(-time.Second)
	tm.acquired = true
	tm.mu.Unlock()
	tm.backoff = func(_ int) time.Duration { return time.Millisecond }

	var fatalCalls atomic.Int32
	fatalCalled := make(chan struct{})
	tm.SetOnFatal(func() {
		if fatalCalls.Add(1) == 1 {
			close(fatalCalled)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := tm.StartRenewal(ctx); err != nil {
		t.Fatalf("StartRenewal failed: %v", err)
	}

	select {
	case <-fatalCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fatal callback")
	}
}

func TestTokenManager_StartRenewal_ContextCancelDoesNotCallOnFatal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	requestStarted := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() {
			close(requestStarted)
			cancel()
		})
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	tm := mustNewTokenManager(t, server.URL, "bootstrap", slog.Default())
	tm.mu.Lock()
	tm.token = "initial-jwt"
	tm.refreshToken = "refresh"
	tm.expiresAt = time.Now().Add(-time.Second)
	tm.acquired = true
	tm.mu.Unlock()
	tm.backoff = func(_ int) time.Duration { return time.Hour }

	fatalCalled := make(chan struct{}, 1)
	tm.SetOnFatal(func() {
		select {
		case fatalCalled <- struct{}{}:
		default:
		}
	})

	if err := tm.StartRenewal(ctx); err != nil {
		t.Fatalf("StartRenewal failed: %v", err)
	}

	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for renewal request")
	}

	select {
	case <-fatalCalled:
		t.Fatal("onFatal should not be called on context cancellation")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestTokenManager_SetAlgorithm_ThreadSafe(t *testing.T) {
	tm := mustNewTokenManager(t, "http://127.0.0.1", "token", slog.Default())
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tm.SetAlgorithm("RS256")
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent SetAlgorithm timed out")
	}
}

func TestNewTokenManager_RejectsPlainHTTP(t *testing.T) {
	_, err := NewTokenManager("http://example.com/token", "token", nil)
	if err == nil {
		t.Fatal("expected error for non-localhost HTTP URL")
	}
}

func TestNewTokenManager_AllowsLocalhostHTTP(t *testing.T) {
	tm, err := NewTokenManager("http://localhost/token", "token", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tm == nil {
		t.Fatal("expected non-nil TokenManager")
	}
}

func TestNewTokenManager_AllowsHTTPS(t *testing.T) {
	tm, err := NewTokenManager("https://api.example.com/token", "token", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tm == nil {
		t.Fatal("expected non-nil TokenManager")
	}
}
