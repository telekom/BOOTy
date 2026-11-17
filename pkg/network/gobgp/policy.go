package gobgp

import (
	"fmt"
	"strconv"
	"strings"

	apipb "github.com/osrg/gobgp/v4/api"
	"github.com/osrg/gobgp/v4/pkg/apiutil"
	"github.com/osrg/gobgp/v4/pkg/packet/bgp"
)

const (
	// asnMax2Byte is the maximum 2-byte ASN value, used to select
	// between 2-octet and 4-octet BGP community / RD formats.
	asnMax2Byte = 65535

	// routeTargetLocalAdminMax is the largest local-admin value that can be
	// encoded with a 4-octet ASN route-target extended community.
	routeTargetLocalAdminMax = 65535
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
	switch strings.ToLower(strings.TrimSpace(s)) {
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
	switch strings.ToLower(strings.TrimSpace(s)) {
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

// ParseRouteTarget parses an EVPN route target in either "ASN:value" or
// "RT:ASN:value" form with bgp.ParseRouteTarget, which rejects a value that
// does not fit the field (a 4-octet ASN has a 16-bit value).
func ParseRouteTarget(s string) (asn, value uint32, err error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) == 3 && strings.EqualFold(parts[0], "RT") {
		parts = parts[1:]
	}
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid route target %q, expected ASN:value or RT:ASN:value", s)
	}
	rt, err := bgp.ParseRouteTarget(strings.TrimSpace(parts[0]) + ":" + strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid route target %q: %w", s, err)
	}
	switch v := rt.(type) {
	case *bgp.TwoOctetAsSpecificExtended:
		return uint32(v.AS), v.LocalAdmin, nil
	case *bgp.FourOctetAsSpecificExtended:
		return v.AS, uint32(v.LocalAdmin), nil
	default:
		return 0, 0, fmt.Errorf("invalid route target %q, expected an AS number, not an IP address", s)
	}
}

// ValidateCommunities checks all community strings for validity.
func ValidateCommunities(cfg *CommunityConfig) error {
	if cfg == nil {
		return nil
	}
	for _, c := range cfg.Standard {
		if _, _, err := ParseStandardCommunity(strings.TrimSpace(c)); err != nil {
			return err
		}
	}
	for _, c := range cfg.Extended {
		parts := strings.SplitN(strings.TrimSpace(c), ":", 3)
		if len(parts) != 3 {
			return fmt.Errorf("invalid extended community %q, expected TYPE:ASN:value", c)
		}
		if _, err := strconv.ParseUint(parts[1], 10, 32); err != nil {
			return fmt.Errorf("invalid extended community ASN %q: %w", parts[1], err)
		}
		if _, err := strconv.ParseUint(parts[2], 10, 32); err != nil {
			return fmt.Errorf("invalid extended community value %q: %w", parts[2], err)
		}
	}
	for _, c := range cfg.Large {
		parts := strings.SplitN(strings.TrimSpace(c), ":", 3)
		if len(parts) != 3 {
			return fmt.Errorf("invalid large community %q, expected GA:LD1:LD2", c)
		}
		for i, p := range parts {
			if _, err := strconv.ParseUint(p, 10, 32); err != nil {
				return fmt.Errorf("invalid large community part[%d] %q: %w", i, p, err)
			}
		}
	}
	return nil
}

// parseRouteDistinguisher marshals an RD "IPv4:NN" (type 1, 16-bit NN),
// "ASN:NN" with a 2-byte ASN (type 0, 32-bit NN) or with a 4-byte ASN
// (type 2, 16-bit NN), like FRR. bgp.ParseRouteDistinguisher rejects a
// value out of range.
func parseRouteDistinguisher(rd string) (*apipb.RouteDistinguisher, error) {
	parsed, err := bgp.ParseRouteDistinguisher(rd)
	if err != nil {
		return nil, fmt.Errorf("route distinguisher %q: %w", rd, err)
	}
	msg, err := apiutil.MarshalRD(parsed)
	if err != nil {
		return nil, fmt.Errorf("route distinguisher %q: %w", rd, err)
	}
	return msg, nil
}
