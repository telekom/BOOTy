// Package auth implements JWT token management for CAPRF communication.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// TokenResponse represents the server's token endpoint response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`            //nolint:gosec // G101: struct field for token endpoint response, not a hardcoded credential
	RefreshToken string `json:"refresh_token,omitempty"` //nolint:gosec // G101: struct field for token endpoint response, not a hardcoded credential
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// tokenRequest is the JSON body sent to the token endpoint.
type tokenRequest struct {
	MachineSerial string `json:"machineSerial"`
	BMCMAC        string `json:"bmcMAC"`
	Algorithm     string `json:"algorithm,omitempty"`
}

// TokenManager handles JWT acquisition, renewal, and failure recovery.
type TokenManager struct {
	tokenURL       string
	token          string
	refreshToken   string
	expiresAt      time.Time
	mu             sync.RWMutex
	client         *http.Client
	log            *slog.Logger
	onFatal        func()
	backoff        func(attempt int) time.Duration
	algorithm      string
	acquired       bool
	renewalStarted bool // true once StartRenewal launches renewLoop
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

// SetAlgorithm configures the token algorithm (e.g. RS256, ES256) sent in requests.
// Must be called before Acquire or StartRenewal.
func (tm *TokenManager) SetAlgorithm(alg string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.algorithm = alg
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
	tm.mu.RLock()
	alg := tm.algorithm
	tm.mu.RUnlock()
	reqBody := tokenRequest{
		MachineSerial: serial,
		BMCMAC:        bmcMAC,
		Algorithm:     alg,
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tm.tokenURL,
		bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create token request: %w", err)
	}

	tm.mu.RLock()
	req.Header.Set("Authorization", "Bearer "+tm.token)
	tm.mu.RUnlock()
	req.Header.Set("Content-Type", "application/json")

	resp, err := tm.client.Do(req) //nolint:gosec // G107: token URL comes from validated configuration, not user input
	if err != nil {
		return fmt.Errorf("acquire token from %s: %w", tm.tokenURL, err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("acquire token from %s: status %d", tm.tokenURL, resp.StatusCode)
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("decode token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return fmt.Errorf("acquire token from %s: empty access_token in response", tm.tokenURL)
	}
	if tokenResp.ExpiresIn <= 0 {
		return fmt.Errorf("acquire token from %s: invalid expires_in %d", tm.tokenURL, tokenResp.ExpiresIn)
	}

	tm.mu.Lock()
	tm.token = tokenResp.AccessToken
	tm.refreshToken = tokenResp.RefreshToken
	tm.expiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	tm.acquired = true
	tm.mu.Unlock()

	tm.log.Info("jwt acquired", "expiresIn", tokenResp.ExpiresIn)

	return nil
}

// Token returns the current token for use in Authorization headers.
func (tm *TokenManager) Token() string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	return tm.token
}

// StartRenewal begins the background renewal goroutine.
// Renews at 80% of token lifetime. Must be called after a successful Acquire.
// Idempotent: subsequent calls after the first are a no-op.
func (tm *TokenManager) StartRenewal(ctx context.Context) error {
	tm.mu.Lock()
	if !tm.acquired {
		tm.mu.Unlock()
		return fmt.Errorf("cannot start renewal: acquire has not been called")
	}
	if tm.renewalStarted {
		tm.mu.Unlock()
		return nil
	}
	tm.renewalStarted = true
	tm.mu.Unlock()
	go tm.renewLoop(ctx)
	return nil
}

func (tm *TokenManager) renewLoop(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	// Drain the initial fire so we start with a proper sleep.
	<-timer.C

	for {
		tm.mu.RLock()
		remaining := time.Until(tm.expiresAt)
		tm.mu.RUnlock()

		if remaining <= 0 {
			// Token already expired — attempt renewal immediately.
			timer.Reset(0)
		} else {
			// Renew at 80% of remaining lifetime to avoid expiry races.
			timer.Reset(time.Duration(float64(remaining) * 0.8))
		}

		select {
		case <-timer.C:
			if err := tm.renewWithRetry(ctx); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					tm.log.Debug("token renewal stopped", "reason", err)
					return
				}
				tm.log.Error("token renewal exhausted", "error", err)
				tm.mu.RLock()
				fatal := tm.onFatal
				tm.mu.RUnlock()
				if fatal != nil {
					fatal()
					return
				}
				tm.log.Warn("token renewal exhausted without fatal handler, continuing retry loop")
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

	type renewRequest struct {
		RefreshToken string `json:"refresh_token,omitempty"` //nolint:gosec // G101: struct field for token request body, not a hardcoded credential
	}
	data, err := json.Marshal(renewRequest{RefreshToken: refresh})
	if err != nil {
		return fmt.Errorf("marshal renewal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tm.tokenURL,
		bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create renewal request: %w", err)
	}

	tm.mu.RLock()
	req.Header.Set("Authorization", "Bearer "+tm.token)
	tm.mu.RUnlock()
	req.Header.Set("Content-Type", "application/json")

	resp, err := tm.client.Do(req) //nolint:gosec // G107: token URL comes from validated configuration, not user input
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

	if tokenResp.AccessToken == "" {
		return fmt.Errorf("renew token: empty access_token in response")
	}
	if tokenResp.ExpiresIn <= 0 {
		return fmt.Errorf("renew token: invalid expires_in %d", tokenResp.ExpiresIn)
	}

	tm.mu.Lock()
	tm.token = tokenResp.AccessToken
	if tokenResp.RefreshToken != "" {
		tm.refreshToken = tokenResp.RefreshToken
	}
	tm.expiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	tm.mu.Unlock()

	tm.log.Info("jwt renewed", "expiresIn", tokenResp.ExpiresIn)

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
