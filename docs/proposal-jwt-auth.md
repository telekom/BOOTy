# Proposal: JWT Token Authentication for CAPRF

## Status: Proposal

## Priority: P1

## Summary

Replace/extend the current static Bearer token auth with JWT (JSON Web
Tokens) for BOOTy ↔ CAPRF communication. BOOTy acquires a JWT at startup,
a background goroutine renews before expiry, and failure to renew triggers
escalating recovery (retry → reboot). CAPRF validates JWT signatures and
claims (machine identity, session ID, expiry).

## Motivation

The current authentication uses a static Bearer token set in `/deploy/vars`
(`TOKEN` field). This has several limitations:

| Current Limitation | Impact |
|-------------------|--------|
| Static token with no expiry | Token valid indefinitely if leaked |
| No machine identity in token | Can't distinguish requests per machine |
| No session binding | Replayed token works for any provisioning run |
| No revocation | Can't invalidate compromised tokens |
| No rotation | Token reuse across multiple provisioning cycles |

### Industry Context

| Tool | Authentication |
|------|---------------|
| **Ironic** | Keystone tokens (OpenStack identity) |
| **MAAS** | API key with OAuth 1.0a |
| **Tinkerbell** | mTLS between worker/server |
| **Kubernetes** | ServiceAccount JWTs with bound tokens |

## Design

### Token Flow

```
┌──────────────────────────────────────────────────────────┐
│ BOOTy Boot Sequence                                      │
│                                                          │
│  1. Read bootstrap token from /deploy/vars (TOKEN field) │
│  2. POST /auth/token with bootstrap token                │
│     Request: { "machineSerial": "...", "bmcMAC": "..." } │
│  3. Receive JWT { "access_token": "...",                 │
│                    "expires_in": 3600,                    │
│                    "refresh_token": "..." }               │
│  4. Start renewal goroutine (~80% of expiry interval)    │
│  5. Use JWT for all subsequent CAPRF requests            │
│  6. On renewal failure after exhausting retries → reboot │
└──────────────────────────────────────────────────────────┘
```

### JWT Claims

```go
// pkg/auth/claims.go
package auth

import "time"

// Claims holds the JWT claims for BOOTy ↔ CAPRF communication.
type Claims struct {
    // Standard claims
    Subject   string    `json:"sub"`  // machine serial number
    IssuedAt  time.Time `json:"iat"`
    ExpiresAt time.Time `json:"exp"`
    Issuer    string    `json:"iss"`  // "caprf"
    Audience  string    `json:"aud"`  // "booty"

    // Custom claims
    MachineSerial   string `json:"machine_serial"`
    BMCMAC          string `json:"bmc_mac"`
    ProvisioningID  string `json:"provisioning_id"` // unique session ID
    Mode            string `json:"mode"`             // "provision", "deprovision"
}
```

### Token Manager

```go
// pkg/auth/manager.go
package auth

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "log/slog"
    "net/http"
    "sync"
    "time"
)

// TokenManager handles JWT acquisition, renewal, and failure recovery.
type TokenManager struct {
    tokenURL     string
    token        string
    refreshToken string
    expiresAt    time.Time
    mu           sync.RWMutex
    client       *http.Client
    log          *slog.Logger
    onFatal      func() // called when renewal is permanently exhausted
}

// NewTokenManager creates a token manager with initial bootstrap token.
func NewTokenManager(tokenURL, bootstrapToken string, log *slog.Logger) *TokenManager {
    return &TokenManager{
        tokenURL: tokenURL,
        token:    bootstrapToken,
        client:   &http.Client{Timeout: 15 * time.Second},
        log:      log,
        onFatal:  rebootMachine,
    }
}

// Acquire exchanges the bootstrap token for a JWT.
func (tm *TokenManager) Acquire(ctx context.Context, serial, bmcMAC string) error {
    reqBody := fmt.Sprintf(`{"machineSerial":"%s","bmcMAC":"%s"}`, serial, bmcMAC)
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, tm.tokenURL,
        strings.NewReader(reqBody))
    if err != nil {
        return fmt.Errorf("create token request: %w", err)
    }
    req.Header.Set("Authorization", "Bearer "+tm.token)
    req.Header.Set("Content-Type", "application/json")

    resp, err := tm.client.Do(req)
    if err != nil {
        return fmt.Errorf("acquire token: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
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

    return nil
}

// Token returns the current JWT for use in Authorization headers.
func (tm *TokenManager) Token() string {
    tm.mu.RLock()
    defer tm.mu.RUnlock()
    return tm.token
}

// StartRenewal begins the background renewal goroutine.
// Renews at 80% of token lifetime.
func (tm *TokenManager) StartRenewal(ctx context.Context) {
    go func() {
        for {
            tm.mu.RLock()
            renewAt := tm.expiresAt.Add(-time.Duration(float64(time.Until(tm.expiresAt)) * 0.2))
            tm.mu.RUnlock()

            select {
            case <-time.After(time.Until(renewAt)):
                if err := tm.renew(ctx); err != nil {
                    tm.log.Error("token renewal failed", "error", err)
                    if err := tm.renewWithRetry(ctx); err != nil {
                        tm.log.Error("token renewal exhausted, triggering reboot")
                        tm.onFatal()
                    }
                }
            case <-ctx.Done():
                return
            }
        }
    }()
}

func (tm *TokenManager) renewWithRetry(ctx context.Context) error {
    var lastErr error
    for attempt := range 5 {
        backoff := time.Duration(1<<attempt) * time.Second
        select {
        case <-time.After(backoff):
        case <-ctx.Done():
            return ctx.Err()
        }
        if err := tm.renew(ctx); err != nil {
            lastErr = err
            continue
        }
        return nil
    }
    return lastErr
}
```

### CAPRF Client Integration

```go
// pkg/caprf/client.go — modified to use TokenManager
func (c *Client) doPost(ctx context.Context, url, body string) error {
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, url,
        strings.NewReader(body))
    if err != nil {
        return fmt.Errorf("create request: %w", err)
    }

    // Use TokenManager for dynamic JWT if available, otherwise static token
    if c.tokenMgr != nil {
        req.Header.Set("Authorization", "Bearer "+c.tokenMgr.Token())
    } else if c.cfg.Token != "" {
        req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
    }
    // ...
}
```

### Required Binaries in Initramfs

No additional binaries needed. JWT handling is pure Go using
`golang.org/x/crypto` for signature verification.

### Go Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/golang-jwt/jwt/v5` | JWT parsing and validation |

**Note**: `golang-jwt/jwt` is a pure Go library with no CGO dependency.
It supports RS256, ES256, PS256 algorithms. No external binaries needed.

### Configuration

```bash
# /deploy/vars
export TOKEN="<bootstrap-bearer-token>"          # initial auth (existing field)
export TOKEN_URL="https://caprf.example.com/auth/token"  # JWT endpoint
export TOKEN_ALGORITHM="RS256"                   # RS256 or ES256
```

## Files Changed

| File | Change |
|------|--------|
| `pkg/auth/manager.go` | `TokenManager` — acquire, renew, reboot |
| `pkg/auth/claims.go` | JWT claims types |
| `pkg/auth/manager_test.go` | Unit tests |
| `pkg/caprf/client.go` | Integrate `TokenManager` for dynamic auth |
| `pkg/config/provider.go` | `TokenURL`, `TokenAlgorithm` config fields |
| `go.mod` | Add `github.com/golang-jwt/jwt/v5` |

## Testing

### Unit Tests

- `auth/manager_test.go`:
  - Table-driven: acquire success, acquire failure (401/500/timeout)
  - Renewal timing: verify renewal fires at 80% of expiry
  - Retry exhaustion: verify `onFatal` callback fires
  - Concurrent token access (race detector)
  - Mock HTTP server issuing and validating test JWTs

### E2E Tests

- **ContainerLab** (tag `e2e_integration`):
  - Mock CAPRF server with JWT endpoint
  - BOOTy acquires JWT → provisions → renewal happens mid-provision
  - Verify: requests use refreshed token after renewal
  - Verify: expired token triggers renewal, not failure

## Risks

| Risk | Mitigation |
|------|------------|
| JWT library vulnerability | Pin version; monitor CVEs |
| Token renewal during critical step | Renewal is async; current token valid until expiry |
| Clock skew between BOOTy and CAPRF | Allow 30s leeway in `exp` claim validation |
| Reboot loop on permanent auth failure | Exponential backoff between reboot attempts |

## Effort Estimate

6–10 engineering days (TokenManager + CAPRF integration + CAPRF server-side
JWT issuing + tests).
