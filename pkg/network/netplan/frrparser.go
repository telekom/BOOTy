package netplan

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// FRRParams holds parameters extracted from an FRR configuration file.
type FRRParams struct {
	ASN             uint32
	LocalASN        uint32 // local-as override (BM4X uses per-neighbor local-as).
	RouterID        string
	UnnumberedPeers []string // Interface names from "neighbor X interface" lines.
	NumberedPeers   []string // IPv4 addresses from "neighbor X remote-as" lines.
	NumberedPeersV6 []string // IPv6 addresses from "neighbor X remote-as" lines.
	EVPN            bool     // True if "address-family l2vpn evpn" found.
	AdvertiseAllVNI bool     // True if "advertise-all-vni" found.
	// L3VNIs lists VNIs mapped to a VRF ("vni N" at top level for the default
	// VRF or inside a "vrf" block). FRR advertises only Type-5 routes for them;
	// advertise-all-vni produces Type-2/Type-3 routes for L2VNIs only.
	L3VNIs []uint32
}

// Regex patterns for FRR config extraction.
var (
	reRouterBGP       = regexp.MustCompile(`(?m)^router\s+bgp\s+(\d+)`)
	reRouterID        = regexp.MustCompile(`(?m)^\s+bgp\s+router-id\s+(\S+)`)
	reNeighborIface   = regexp.MustCompile(`(?m)^\s+neighbor\s+(\S+)\s+interface\s+(?:remote-as|peer-group)`)
	reNeighborIPv4    = regexp.MustCompile(`(?m)^\s+neighbor\s+(\d+\.\d+\.\d+\.\d+)\s+remote-as\s+(\S+)`)
	reNeighborIPv6    = regexp.MustCompile(`(?m)^\s+neighbor\s+([0-9a-fA-F]*:[0-9a-fA-F:]+)\s+remote-as\s+(\S+)`)
	reL2VPNEVPN       = regexp.MustCompile(`(?m)^\s+address-family\s+l2vpn\s+evpn`)
	reAdvertiseAllVNI = regexp.MustCompile(`(?m)^\s+advertise-all-vni`)
	reLocalAS         = regexp.MustCompile(`(?m)^\s+neighbor\s+\S+\s+local-as\s+(\d+)`)
)

// ParseFRRConfig extracts networking parameters from an FRR configuration file.
func ParseFRRConfig(path string) (*FRRParams, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read frr config %s: %w", path, err)
	}
	return ParseFRRConfigBytes(data)
}

// ParseFRRConfigBytes extracts networking parameters from FRR config content.
func ParseFRRConfigBytes(data []byte) (*FRRParams, error) {
	content := string(data)
	params := &FRRParams{}

	if m := reRouterBGP.FindStringSubmatch(content); len(m) > 1 {
		asn, err := strconv.ParseUint(m[1], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("parse ASN %q: %w", m[1], err)
		}
		params.ASN = uint32(asn)
	}
	if m := reRouterID.FindStringSubmatch(content); len(m) > 1 {
		params.RouterID = m[1]
	}

	params.UnnumberedPeers = extractUnnumberedPeers(content)
	params.NumberedPeers = extractNumberedPeers(content, reNeighborIPv4, "169.254.", "127.")
	params.NumberedPeersV6 = extractNumberedPeers(content, reNeighborIPv6, "fd00:7:caa5:", "fe80:")

	if m := reLocalAS.FindStringSubmatch(content); len(m) > 1 {
		if localASN, err := strconv.ParseUint(m[1], 10, 32); err == nil {
			params.LocalASN = uint32(localASN)
		}
	}

	params.EVPN = reL2VPNEVPN.MatchString(content)
	params.AdvertiseAllVNI = reAdvertiseAllVNI.MatchString(content)
	params.L3VNIs = extractL3VNIs(content)
	return params, nil
}

// extractL3VNIs returns VNIs that FRR maps to a VRF: a "vni N" statement at
// the top level (default VRF) or inside a "vrf NAME" block. A "vni N" block
// under "router bgp" configures an L2VNI and is not included.
func extractL3VNIs(content string) []uint32 {
	var vnis []uint32
	inRouter := false
	for line := range strings.Lines(content) {
		trimmed := strings.TrimSpace(line)
		topLevel := line != "" && line[0] != ' ' && line[0] != '\t'
		switch {
		case topLevel && strings.HasPrefix(trimmed, "router "):
			inRouter = true
			continue
		case topLevel && (trimmed == "exit" || trimmed == "exit-vrf" || strings.HasPrefix(trimmed, "vrf ")):
			inRouter = false
			continue
		}
		fields := strings.Fields(trimmed)
		if inRouter || len(fields) != 2 || fields[0] != "vni" {
			continue
		}
		if vni, err := strconv.ParseUint(fields[1], 10, 32); err == nil && !slices.Contains(vnis, uint32(vni)) {
			vnis = append(vnis, uint32(vni))
		}
	}
	return vnis
}

// extractUnnumberedPeers returns deduplicated interface names from neighbor lines.
func extractUnnumberedPeers(content string) []string {
	seen := make(map[string]bool)
	var peers []string
	for _, m := range reNeighborIface.FindAllStringSubmatch(content, -1) {
		iface := m[1]
		if !seen[iface] {
			peers = append(peers, iface)
			seen[iface] = true
		}
	}
	return peers
}

// extractNumberedPeers returns deduplicated IPs from neighbor lines, skipping
// addresses that start with any of the given prefixes.
func extractNumberedPeers(content string, re *regexp.Regexp, skipPrefixes ...string) []string {
	seen := make(map[string]bool)
	var peers []string
	for _, m := range re.FindAllStringSubmatch(content, -1) {
		ip := m[1]
		if shouldSkipPeer(ip, skipPrefixes) || seen[ip] {
			continue
		}
		peers = append(peers, ip)
		seen[ip] = true
	}
	return peers
}

func shouldSkipPeer(ip string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(ip, p) {
			return true
		}
	}
	return false
}
