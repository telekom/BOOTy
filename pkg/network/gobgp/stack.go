//go:build linux

package gobgp

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/telekom/BOOTy/pkg/network"
)

// Stack composes underlay and overlay tiers into a network.Mode implementation.
type Stack struct {
	underlay *UnderlayTier
	overlay  *OverlayTier
	cfg      *Config
	log      *slog.Logger
}

// NewStack creates a GoBGP stack from the given configuration.
func NewStack(cfg *Config) *Stack {
	underlay := NewUnderlayTier(cfg)
	overlay := NewOverlayTier(cfg)

	return &Stack{
		underlay: underlay,
		overlay:  overlay,
		cfg:      cfg,
		log:      slog.With("mode", "gobgp"),
	}
}

// Setup initializes the underlay and overlay tiers sequentially.
// The cfg parameter satisfies the network.Mode interface; the stack uses
// its own Config parsed at construction time.
func (s *Stack) Setup(ctx context.Context, _ *network.Config) error {
	s.log.Info("Setting up GoBGP network stack",
		"asn", s.cfg.ASN,
		"routerID", s.cfg.RouterID,
		"vni", s.cfg.ProvisionVNI,
	)

	if err := s.underlay.Setup(ctx); err != nil {
		return fmt.Errorf("underlay setup: %w", err)
	}

	// Share the BGP server with the overlay tier.
	s.overlay.SetBgpServer(s.underlay.BgpServer())

	if err := s.overlay.Setup(ctx); err != nil {
		return fmt.Errorf("overlay setup: %w", err)
	}

	s.log.Info("GoBGP network stack ready")
	return nil
}

// WaitForConnectivity waits for at least one BGP peer to establish.
func (s *Stack) WaitForConnectivity(ctx context.Context, _ string, timeout time.Duration) error {
	s.log.Info("Waiting for BGP peer connectivity", "timeout", timeout)

	if err := s.underlay.Ready(ctx, timeout); err != nil {
		return fmt.Errorf("underlay connectivity: %w", err)
	}

	return nil
}

// Teardown tears down the overlay and underlay tiers in reverse order.
func (s *Stack) Teardown(ctx context.Context) error {
	s.log.Info("Tearing down GoBGP network stack")

	var firstErr error

	if err := s.overlay.Teardown(ctx); err != nil {
		s.log.Warn("Overlay teardown error", "error", err)
		firstErr = err
	}

	if err := s.underlay.Teardown(ctx); err != nil {
		s.log.Warn("Underlay teardown error", "error", err)
		if firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}
