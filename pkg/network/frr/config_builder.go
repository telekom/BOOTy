package frr

import (
	"fmt"
	"net"
	"strings"
)

// FRRConfigBuilder constructs an FRR configuration programmatically.
// Methods can be called in any order; Build() emits sections in canonical
// FRR order regardless.
type FRRConfigBuilder struct {
	asn      uint32
	routerID string

	vrfName    string
	vrfTableID uint32

	peerGroupName string
	peerRemoteAS  string
	nics          []string

	bgpKeepalive uint32
	bgpHold      uint32

	bfdProfileName string
	bfdTransmitMS  uint32
	bfdReceiveMS   uint32

	addressFamilies []addressFamily

	isOnefabric      bool
	dcgwIPs          []string
	overlayAggregate string
	vpnRT            string
}

type addressFamily struct {
	afi  string // "ipv4", "ipv6", "l2vpn"
	safi string // "unicast", "evpn"
}

// NewFRRConfigBuilder creates a new builder with required BGP ASN and router-id.
func NewFRRConfigBuilder(asn uint32, routerID string) *FRRConfigBuilder {
	return &FRRConfigBuilder{
		asn:           asn,
		routerID:      routerID,
		peerGroupName: "fabric",
		peerRemoteAS:  "external",
	}
}

// WithVRF sets VRF name and routing table ID.
func (b *FRRConfigBuilder) WithVRF(name string, tableID uint32) *FRRConfigBuilder {
	b.vrfName = name
	b.vrfTableID = tableID
	return b
}

// WithNICs configures interfaces as BGP unnumbered peers in the peer-group.
func (b *FRRConfigBuilder) WithNICs(nics []string) *FRRConfigBuilder {
	b.nics = nics
	return b
}

// WithBGPTimers sets BGP keepalive and hold timers (seconds).
// If zero, FRR datacenter defaults are used (3s/9s).
func (b *FRRConfigBuilder) WithBGPTimers(keepalive, hold uint32) *FRRConfigBuilder {
	b.bgpKeepalive = keepalive
	b.bgpHold = hold
	return b
}

// WithBFDProfile adds a named BFD profile with transmit/receive intervals.
func (b *FRRConfigBuilder) WithBFDProfile(name string, transmitMS, receiveMS uint32) *FRRConfigBuilder {
	b.bfdProfileName = name
	b.bfdTransmitMS = transmitMS
	b.bfdReceiveMS = receiveMS
	return b
}

// WithAddressFamily adds a BGP address-family (e.g. "ipv4"/"unicast", "l2vpn"/"evpn").
func (b *FRRConfigBuilder) WithAddressFamily(afi, safi string) *FRRConfigBuilder {
	b.addressFamilies = append(b.addressFamilies, addressFamily{afi: afi, safi: safi})
	return b
}

// WithOnefabric enables onefabric mode with DCGW peering, route aggregation, and VPN RT.
func (b *FRRConfigBuilder) WithOnefabric(dcgwIPs []string, aggregate, vpnRT string) *FRRConfigBuilder {
	b.isOnefabric = true
	b.dcgwIPs = dcgwIPs
	b.overlayAggregate = aggregate
	b.vpnRT = vpnRT
	return b
}

// Build generates the complete FRR configuration string.
// Sections are emitted in canonical FRR order.
func (b *FRRConfigBuilder) Build() string {
	var sb strings.Builder

	b.writeHeader(&sb)
	b.writeBFDProfile(&sb)
	b.writeRouterBGP(&sb)
	b.writeFooter(&sb)

	return sb.String()
}

func (b *FRRConfigBuilder) writeHeader(sb *strings.Builder) {
	sb.WriteString("frr version 10.3\n")
	sb.WriteString("frr defaults datacenter\n")
	sb.WriteString("!\n")
}

func (b *FRRConfigBuilder) writeBFDProfile(sb *strings.Builder) {
	if b.bfdProfileName == "" {
		return
	}
	fmt.Fprintf(sb, "bfd\n")
	fmt.Fprintf(sb, " profile %s\n", b.bfdProfileName)
	fmt.Fprintf(sb, "  transmit-interval %d\n", b.bfdTransmitMS)
	fmt.Fprintf(sb, "  receive-interval %d\n", b.bfdReceiveMS)
	sb.WriteString(" exit\n")
	sb.WriteString("exit\n")
	sb.WriteString("!\n")
}

func (b *FRRConfigBuilder) writeRouterBGP(sb *strings.Builder) {
	if b.vrfName != "" {
		fmt.Fprintf(sb, "router bgp %d vrf %s\n", b.asn, b.vrfName)
	} else {
		fmt.Fprintf(sb, "router bgp %d\n", b.asn)
	}
	fmt.Fprintf(sb, " bgp router-id %s\n", b.routerID)
	sb.WriteString(" no bgp default ipv4-unicast\n")
	sb.WriteString(" bgp bestpath as-path multipath-relax\n")

	if b.bgpKeepalive > 0 && b.bgpHold > 0 {
		fmt.Fprintf(sb, " timers bgp %d %d\n", b.bgpKeepalive, b.bgpHold)
	}

	fmt.Fprintf(sb, " neighbor %s peer-group\n", b.peerGroupName)
	fmt.Fprintf(sb, " neighbor %s remote-as %s\n", b.peerGroupName, b.peerRemoteAS)
	if b.bfdProfileName != "" {
		fmt.Fprintf(sb, " neighbor %s bfd profile %s\n", b.peerGroupName, b.bfdProfileName)
	}

	for _, nic := range b.nics {
		fmt.Fprintf(sb, " neighbor %s interface peer-group %s\n", nic, b.peerGroupName)
	}

	b.writeAddressFamilies(sb)
	b.writeOnefabric(sb)

	sb.WriteString("exit\n")
	sb.WriteString("!\n")
}

func (b *FRRConfigBuilder) writeAddressFamilies(sb *strings.Builder) {
	// Collect which unicast families are active for EVPN advertise.
	hasIPv4 := false
	hasIPv6 := false

	for _, af := range b.addressFamilies {
		if af.afi == "l2vpn" && af.safi == "evpn" {
			continue // handled after unicast families
		}
		sb.WriteString(" !\n")
		fmt.Fprintf(sb, " address-family %s %s\n", af.afi, af.safi)
		fmt.Fprintf(sb, "  neighbor %s activate\n", b.peerGroupName)
		sb.WriteString("  redistribute connected\n")
		sb.WriteString(" exit-address-family\n")

		if af.afi == "ipv4" {
			hasIPv4 = true
		}
		if af.afi == "ipv6" {
			hasIPv6 = true
		}
	}

	// l2vpn evpn address-family.
	for _, af := range b.addressFamilies {
		if af.afi != "l2vpn" || af.safi != "evpn" {
			continue
		}
		sb.WriteString(" !\n")
		sb.WriteString(" address-family l2vpn evpn\n")
		fmt.Fprintf(sb, "  neighbor %s activate\n", b.peerGroupName)
		sb.WriteString("  advertise-all-vni\n")
		if hasIPv4 {
			sb.WriteString("  advertise ipv4 unicast\n")
		}
		if hasIPv6 {
			sb.WriteString("  advertise ipv6 unicast\n")
		}
		sb.WriteString(" exit-address-family\n")
	}
}

func (b *FRRConfigBuilder) writeOnefabric(sb *strings.Builder) {
	if !b.isOnefabric {
		return
	}

	for _, ip := range b.dcgwIPs {
		fmt.Fprintf(sb, " neighbor %s remote-as internal\n", ip)
		fmt.Fprintf(sb, " neighbor %s update-source %s\n", ip, b.routerID)
	}
	sb.WriteString(" !\n")
	sb.WriteString(" address-family l2vpn evpn\n")
	for _, ip := range b.dcgwIPs {
		fmt.Fprintf(sb, "  neighbor %s activate\n", ip)
	}
	if b.overlayAggregate != "" {
		fmt.Fprintf(sb, "  aggregate-address %s\n", b.overlayAggregate)
	}
	if b.vpnRT != "" {
		fmt.Fprintf(sb, "  route-target both %s\n", b.vpnRT)
	}
	sb.WriteString(" exit-address-family\n")
}

func (b *FRRConfigBuilder) writeFooter(sb *strings.Builder) {
	sb.WriteString("line vty\n")
	sb.WriteString("!\n")
}

// isIPv6 returns true if the given IP string is an IPv6 address.
func isIPv6(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.To4() == nil
}
