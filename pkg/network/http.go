package network

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// WaitForHTTP polls target with HTTP HEAD until reachable.
func WaitForHTTP(ctx context.Context, target string, timeout time.Duration) error {
	if target == "" {
		return fmt.Errorf("empty connectivity target URL")
	}

	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 10 * time.Second}
	attempt := 0
	retryTicker := time.NewTicker(1 * time.Second)
	defer retryTicker.Stop()

	for time.Now().Before(deadline) {
		attempt++
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, http.NoBody)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}

		resp, err := client.Do(req) //nolint:gosec // target is from trusted config
		if err == nil {
			_ = resp.Body.Close()
			// Any HTTP response proves the network path works. The server
			// may return 401 (auth required) or other non-2xx codes, but
			// that still means connectivity is established.
			slog.Info("network connectivity established", "target", target, "status", resp.StatusCode, "attempt", attempt)
			return nil
		}

		slog.Debug("connectivity check failed", "target", target, "attempt", attempt, "error", err)

		select {
		case <-ctx.Done():
			return fmt.Errorf("context canceled: %w", ctx.Err())
		case <-retryTicker.C:
		}
	}

	return fmt.Errorf("network connectivity timeout after %s (%d attempts)", timeout, attempt)
}
