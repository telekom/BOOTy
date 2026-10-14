package caprf

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/telekom/BOOTy/pkg/auth"
)

func TestParseVarsTokenRenewalOptions(t *testing.T) {
	cfg, err := ParseVars(strings.NewReader("TOKEN_EXPIRES_AT=2026-10-14T12:00:00Z\nTOKEN_RENEW_MARGIN_SECONDS=120\n" +
		"TOKEN_RENEW_RETRIES=7\nTOKEN_RENEW_BACKOFF_SECONDS=3\n"))
	if err != nil {
		t.Fatal(err)
	}
	tr := cfg.Transport
	if tr.TokenExpiresAt != "2026-10-14T12:00:00Z" || tr.TokenRenewMarginSeconds != 120 || tr.TokenRenewRetries != 7 || tr.TokenRenewBackoffSeconds != 3 {
		t.Fatalf("transport = %+v", tr)
	}
	p := renewalPolicy(&tr)
	if p.Margin != 2*time.Minute || p.Retries != 7 || p.Backoff != 3*time.Second {
		t.Fatalf("policy = %+v", p)
	}
	for _, bad := range []string{"TOKEN_EXPIRES_AT=tomorrow", "TOKEN_RENEW_RETRIES=-1", "TOKEN_RENEW_MARGIN_SECONDS=x"} {
		if _, err := ParseVars(strings.NewReader(bad)); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}

// Without TOKEN_URL, TOKEN_EXPIRES_AT stops the run the margin before the
// expiry, while the token still works for the report.
func TestTokenExpiryWithoutRenewalCallsTheFatalHandler(t *testing.T) {
	for _, tt := range []struct {
		name      string
		expiresIn time.Duration
		margin    int
		earliest  time.Duration
	}{
		{"margin before expiry", 1500 * time.Millisecond, 1, 400 * time.Millisecond},
		{"already expired", -time.Minute, 0, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseVars(strings.NewReader("TOKEN=t\n"))
			if err != nil {
				t.Fatal(err)
			}
			cfg.Transport.TokenExpiresAt = time.Now().Add(tt.expiresIn).Format(time.RFC3339Nano)
			cfg.Transport.TokenRenewMarginSeconds = tt.margin
			c := NewFromConfig(cfg)
			fatal := make(chan error, 1)
			c.SetTokenRenewalFatalHandler(func(err error) { fatal <- err })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			start := time.Now()
			if err := c.StartTokenRenewal(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-fatal:
				if !errors.Is(err, auth.ErrTokenExpired) {
					t.Fatalf("fatal = %v, want ErrTokenExpired", err)
				}
				if d := time.Since(start); d < tt.earliest || d > 1400*time.Millisecond {
					t.Fatalf("fatal after %v", d)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("no fatal call")
			}
		})
	}
}
