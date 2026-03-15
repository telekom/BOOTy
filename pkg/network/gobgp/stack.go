//go:build linux

package gobgp

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/telekom/BOOTy/pkg/network"
	"github.com/vishvananda/netlink"
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

	// Create VRF first so underlay can assign dummy/NICs to it.
	if err := s.overlay.CreateVRF(); err != nil {
		return fmt.Errorf("create VRF: %w", err)
	}

	if err := s.underlay.Setup(ctx); err != nil {
		return fmt.Errorf("underlay setup: %w", err)
	}

	// Share the BGP server with the overlay tier.
	s.overlay.SetBgpServer(s.underlay.BgpServer())

	if err := s.overlay.Setup(ctx); err != nil {
		// Clean up the underlay that was already started.
		if teardownErr := s.underlay.Teardown(ctx); teardownErr != nil {
			s.log.Warn("Failed to tear down underlay after overlay failure", "error", teardownErr)
		}
		// Clean up the VRF created earlier.
		s.cleanupVRF()
		return fmt.Errorf("overlay setup: %w", err)
	}

	s.log.Info("GoBGP network stack ready")
	return nil
}

// WaitForConnectivity waits for BGP to establish and then polls the target
// URL until reachable, consistent with other network modes.
func (s *Stack) WaitForConnectivity(ctx context.Context, target string, timeout time.Duration) error {
	s.log.Info("Waiting for BGP peer connectivity", "timeout", timeout)

	if err := s.underlay.Ready(ctx, timeout); err != nil {
		return fmt.Errorf("underlay connectivity: %w", err)
	}

	if target != "" {
		s.log.Info("BGP established, polling target URL", "target", target)
		if err := network.WaitForHTTP(ctx, target, timeout); err != nil {
			return fmt.Errorf("target connectivity: %w", err)
		}
	}

	return nil
}

// cleanupVRF removes the VRF link if it was created by the overlay tier.
func (s *Stack) cleanupVRF() {
	name := s.overlay.cfg.VRFName
	if name == "" || !s.overlay.createdVRF {
		return
	}
	link, err := netlink.LinkByName(name)
	if err != nil {
		return
	}
	if err := netlink.LinkDel(link); err != nil {
		s.log.Warn("Failed to delete VRF", "name", name, "error", err)
	}
}

// Teardown tears down the overlay and underlay tiers in reverse order.
// VRF is deleted last since both tiers may have interfaces enslaved to it.
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

	// Delete VRF after both tiers have detached their interfaces.
	s.cleanupVRF()

	return firstErr
}
