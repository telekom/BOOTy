package network

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// DHCPMode implements the Mode interface using DHCP on a single interface.
type DHCPMode struct{}

// Setup is a no-op for DHCPMode — the existing DHCPClient in realm handles this.
func (d *DHCPMode) Setup(_ context.Context, _ *Config) error {
	slog.Info("DHCP mode: network setup delegated to legacy DHCP client")
	return nil
}

// WaitForConnectivity polls the target URL until reachable or timeout.
func (d *DHCPMode) WaitForConnectivity(ctx context.Context, target string, timeout time.Duration) error {
	return WaitForHTTP(ctx, target, timeout)
}

// Teardown is a no-op for DHCPMode.
func (d *DHCPMode) Teardown(_ context.Context) error {
	return nil
}

// WaitForHTTP polls target with HTTP HEAD until reachable.
func WaitForHTTP(ctx context.Context, target string, timeout time.Duration) error {
	if target == "" {
		return fmt.Errorf("empty connectivity target URL")
	}

	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 10 * time.Second}
	attempt := 0

	for time.Now().Before(deadline) {
		attempt++
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, http.NoBody)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}

		resp, err := client.Do(req) //nolint:gosec // target is from trusted config
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				slog.Info("Network connectivity established", "target", target, "attempt", attempt)
				return nil
			}
			slog.Debug("Connectivity check: server not ready", "target", target, "status", resp.StatusCode)
		}

		slog.Debug("Connectivity check failed", "target", target, "attempt", attempt, "error", err)

		select {
		case <-ctx.Done():
			return fmt.Errorf("context canceled: %w", ctx.Err())
		case <-time.After(1 * time.Second):
		}
	}

	return fmt.Errorf("network connectivity timeout after %s (%d attempts)", timeout, attempt)
}
