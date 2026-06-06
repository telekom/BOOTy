//go:build !linux

package network

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// DHCPMode is only functional on Linux, where raw netlink/DHCP access is
// available. The non-Linux implementation keeps tests and config parsing
// portable while returning an explicit setup error.
type DHCPMode struct{}

func NewDHCPMode() *DHCPMode {
	return &DHCPMode{}
}

func (d *DHCPMode) Setup(context.Context, *Config) error {
	return fmt.Errorf("DHCP mode is only supported on Linux")
}

func (d *DHCPMode) WaitForConnectivity(ctx context.Context, target string, timeout time.Duration) error {
	return WaitForHTTP(ctx, target, timeout)
}

func (d *DHCPMode) Teardown(context.Context) error {
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
			slog.Info("network connectivity established", "target", target, "status", resp.StatusCode, "attempt", attempt)
			return nil
		}

		slog.Debug("network connectivity probe failed", "target", target, "attempt", attempt, "error", err)

		select {
		case <-ctx.Done():
			return fmt.Errorf("connectivity check canceled: %w", ctx.Err())
		case <-retryTicker.C:
		}
	}

	return fmt.Errorf("connectivity to %s not established within %s", target, timeout)
}
