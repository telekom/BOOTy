// Package auth implements JWT token management for CAPRF communication.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// TokenResponse represents the server's token endpoint response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`            //nolint:gosec // G117: this is the token endpoint response
	RefreshToken string `json:"refresh_token,omitempty"` //nolint:gosec // G117: this is the token endpoint response
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// TokenManager handles JWT acquisition, renewal, and failure recovery.
type TokenManager struct {
	tokenURL     string
	token        string
	refreshToken string
	expiresAt    time.Time
	mu           sync.RWMutex
	client       *http.Client
	log          *slog.Logger
	onFatal      func()
	backoff      func(attempt int) time.Duration
}

// NewTokenManager creates a token manager with an initial bootstrap token.
func NewTokenManager(tokenURL, bootstrapToken string, log *slog.Logger) *TokenManager {
	if log == nil {
		log = slog.Default()
	}
	return &TokenManager{
		tokenURL: tokenURL,
		token:    bootstrapToken,
		client:   &http.Client{Timeout: 15 * time.Second},
		log:      log.With("component", "auth"),
		backoff:  defaultBackoff,
	}
}

// SetOnFatal sets the callback invoked when token renewal is permanently exhausted.
// Must be called before StartRenewal.
func (tm *TokenManager) SetOnFatal(fn func()) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.onFatal = fn
}

// Acquire exchanges the bootstrap token for a JWT from the token endpoint.
func (tm *TokenManager) Acquire(ctx context.Context, serial, bmcMAC string) error {
	body := fmt.Sprintf(`{"machineSerial":%q,"bmcMAC":%q}`, serial, bmcMAC)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tm.tokenURL,
		strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("create token request: %w", err)
	}

	tm.mu.RLock()
	req.Header.Set("Authorization", "Bearer "+tm.token)
	tm.mu.RUnlock()
	req.Header.Set("Content-Type", "application/json")

	resp, err := tm.client.Do(req) //nolint:gosec // G704: token URL from trusted config
	if err != nil {
		return fmt.Errorf("acquire token: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("acquire token: status %d", resp.StatusCode)
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("decode token response: %w", err)
	}

	tm.mu.Lock()
	tm.token = tokenResp.AccessToken
	tm.refreshToken = tokenResp.RefreshToken
	tm.expiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	tm.mu.Unlock()

	tm.log.Info("JWT acquired", "expiresIn", tokenResp.ExpiresIn)

	return nil
}

// Token returns the current token for use in Authorization headers.
func (tm *TokenManager) Token() string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	return tm.token
}

// StartRenewal begins the background renewal goroutine.
// Renews at 80% of token lifetime.
func (tm *TokenManager) StartRenewal(ctx context.Context) {
	go tm.renewLoop(ctx)
}

func (tm *TokenManager) renewLoop(ctx context.Context) {
	for {
		tm.mu.RLock()
		remaining := time.Until(tm.expiresAt)
		tm.mu.RUnlock()

		if remaining <= 0 {
			remaining = 30 * time.Second
		}

		renewAfter := time.Duration(float64(remaining) * 0.8)

		select {
		case <-time.After(renewAfter):
			if err := tm.renewWithRetry(ctx); err != nil {
				tm.log.Error("token renewal exhausted", "error", err)
				tm.mu.RLock()
				fatal := tm.onFatal
				tm.mu.RUnlock()
				if fatal != nil {
					fatal()
				}

				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (tm *TokenManager) renew(ctx context.Context) error {
	tm.mu.RLock()
	refresh := tm.refreshToken
	tm.mu.RUnlock()

	body := fmt.Sprintf(`{"refresh_token":%q}`, refresh)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tm.tokenURL,
		strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("create renewal request: %w", err)
	}

	tm.mu.RLock()
	req.Header.Set("Authorization", "Bearer "+tm.token)
	tm.mu.RUnlock()
	req.Header.Set("Content-Type", "application/json")

	resp, err := tm.client.Do(req) //nolint:gosec // G704: token URL from trusted config
	if err != nil {
		return fmt.Errorf("renew token: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("renew token: status %d", resp.StatusCode)
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("decode renewal response: %w", err)
	}

	tm.mu.Lock()
	tm.token = tokenResp.AccessToken
	if tokenResp.RefreshToken != "" {
		tm.refreshToken = tokenResp.RefreshToken
	}
	tm.expiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	tm.mu.Unlock()

	tm.log.Info("JWT renewed", "expiresIn", tokenResp.ExpiresIn)

	return nil
}

func (tm *TokenManager) renewWithRetry(ctx context.Context) error {
	var lastErr error

	for attempt := range 5 {
		if err := tm.renew(ctx); err != nil {
			lastErr = err
			tm.log.Warn("renewal attempt failed", "attempt", attempt+1, "error", err)

			backoff := tm.backoff(attempt)

			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return fmt.Errorf("renewal canceled: %w", ctx.Err())
			}

			continue
		}

		return nil
	}

	return fmt.Errorf("renewal exhausted after 5 attempts: %w", lastErr)
}

func defaultBackoff(attempt int) time.Duration {
	return time.Duration(1<<attempt) * time.Second
}
