package gobgp

import (
	"fmt"
	"strconv"
	"strings"
)

// CommunityConfig specifies BGP community tagging.
type CommunityConfig struct {
	Standard []string `json:"standard,omitempty"` // "65000:100"
	Extended []string `json:"extended,omitempty"` // "RT:65000:100"
	Large    []string `json:"large,omitempty"`    // "65000:1:100"
}

// PolicyConfig specifies BGP route policy (import/export).
type PolicyConfig struct {
	ImportCommunities CommunityConfig `json:"importCommunities,omitempty"`
	ExportCommunities CommunityConfig `json:"exportCommunities,omitempty"`
	LocalPref         uint32          `json:"localPref,omitempty"`
	MED               uint32          `json:"med,omitempty"`
}

// GracefulRestartConfig specifies BGP graceful restart parameters.
type GracefulRestartConfig struct {
	Enabled     bool   `json:"enabled,omitempty"`
	RestartTime uint32 `json:"restartTime,omitempty"` // seconds, default 120.
}

// ApplyDefaults fills in default values for graceful restart.
func (g *GracefulRestartConfig) ApplyDefaults() {
	if g.RestartTime == 0 {
		g.RestartTime = 120
	}
}

// UnderlayAF specifies the address family for BGP underlay.
type UnderlayAF string

const (
	// AFIPv4 uses IPv4 for the BGP underlay.
	AFIPv4 UnderlayAF = "ipv4"
	// AFIPv6 uses IPv6 for the BGP underlay.
	AFIPv6 UnderlayAF = "ipv6"
	// AFDualStack uses both IPv4 and IPv6.
	AFDualStack UnderlayAF = "dual-stack"
)

// ParseUnderlayAF parses an address family string.
func ParseUnderlayAF(s string) (UnderlayAF, error) {
	switch strings.ToLower(s) {
	case "ipv4", "":
		return AFIPv4, nil
	case "ipv6":
		return AFIPv6, nil
	case "dual-stack", "dualstack":
		return AFDualStack, nil
	default:
		return "", fmt.Errorf("unknown underlay address family: %q", s)
	}
}

// OverlayType specifies the overlay encapsulation.
type OverlayType string

const (
	// OverlayEVPNVXLAN uses EVPN with VXLAN encapsulation.
	OverlayEVPNVXLAN OverlayType = "evpn-vxlan"
	// OverlayL3VPN uses L3VPN for routing.
	OverlayL3VPN OverlayType = "l3vpn"
	// OverlayNone uses no overlay.
	OverlayNone OverlayType = "none"
)

// ParseOverlayType parses an overlay type string.
func ParseOverlayType(s string) (OverlayType, error) {
	switch strings.ToLower(s) {
	case "evpn-vxlan", "":
		return OverlayEVPNVXLAN, nil
	case "l3vpn":
		return OverlayL3VPN, nil
	case "none":
		return OverlayNone, nil
	default:
		return "", fmt.Errorf("unknown overlay type: %q", s)
	}
}

// ParseStandardCommunity parses a "ASN:value" community string.
func ParseStandardCommunity(s string) (asn, value uint16, err error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid community format %q, expected ASN:value", s)
	}
	a, err := strconv.ParseUint(parts[0], 10, 16)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid community ASN %q: %w", parts[0], err)
	}
	v, err := strconv.ParseUint(parts[1], 10, 16)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid community value %q: %w", parts[1], err)
	}
	return uint16(a), uint16(v), nil
}

// ValidateCommunities checks all community strings for validity.
func ValidateCommunities(cfg *CommunityConfig) error {
	for _, c := range cfg.Standard {
		if _, _, err := ParseStandardCommunity(c); err != nil {
			return err
		}
	}
	for _, c := range cfg.Extended {
		parts := strings.SplitN(c, ":", 3)
		if len(parts) != 3 {
			return fmt.Errorf("invalid extended community %q, expected TYPE:ASN:value", c)
		}
	}
	for _, c := range cfg.Large {
		parts := strings.SplitN(c, ":", 3)
		if len(parts) != 3 {
			return fmt.Errorf("invalid large community %q, expected GA:LD1:LD2", c)
		}
	}
	return nil
}
