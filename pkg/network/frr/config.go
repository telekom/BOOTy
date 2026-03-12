package frr

import (
	"fmt"
	"net"
	"strings"

	"github.com/telekom/BOOTy/pkg/network"
)

// DeriveAddresses computes underlay IP, overlay IP, and bridge MAC from
// IPMI information and network configuration.
func DeriveAddresses(cfg *network.Config) (underlayIP, overlayIP, bridgeMAC string, err error) {
	switch {
	case cfg.UnderlayIP != "":
		underlayIP = cfg.UnderlayIP
	case cfg.UnderlaySubnet != "" && cfg.IPMIIP != "" && cfg.IPMISubnet != "":
		underlayIP, err = DeriveIPFromOffset(cfg.IPMIIP, cfg.IPMISubnet, cfg.UnderlaySubnet)
		if err != nil {
			return "", "", "", fmt.Errorf("derive underlay IP: %w", err)
		}
	default:
		return "", "", "", fmt.Errorf("underlay IP or (underlay_subnet + ipmi_ip + ipmi_subnet) required")
	}

	if cfg.OverlaySubnet != "" && cfg.IPMIIP != "" && cfg.IPMISubnet != "" {
		overlayIP, err = DeriveIPFromOffset(cfg.IPMIIP, cfg.IPMISubnet, cfg.OverlaySubnet)
		if err != nil {
			return "", "", "", fmt.Errorf("derive overlay IP: %w", err)
		}
	} else {
		overlayIP = underlayIP
	}

	if cfg.IPMIMAC != "" {
		bridgeMAC = DeriveBridgeMAC(cfg.IPMIMAC)
	} else {
		bridgeMAC = "02:54:00:00:00:01"
	}

	return underlayIP, overlayIP, bridgeMAC, nil
}

// DeriveIPFromOffset calculates a target IP by applying the host offset
// from sourceIP within sourceSubnet to targetSubnet.
func DeriveIPFromOffset(sourceIP, sourceSubnet, targetSubnet string) (string, error) {
	src := net.ParseIP(sourceIP)
	if src == nil {
		return "", fmt.Errorf("invalid source IP: %s", sourceIP)
	}

	_, srcNet, err := net.ParseCIDR(sourceSubnet)
	if err != nil {
		return "", fmt.Errorf("invalid source subnet: %w", err)
	}

	_, tgtNet, err := net.ParseCIDR(targetSubnet)
	if err != nil {
		return "", fmt.Errorf("invalid target subnet: %w", err)
	}

	src4 := src.To4()
	if src4 != nil {
		// Cross-family: IPv4 source + IPv6 target — compute offset from IPv4, apply to IPv6.
		if tgtNet.IP.To4() == nil {
			srcBase := srcNet.IP.To4()
			if srcBase == nil {
				return "", fmt.Errorf("invalid IPv4 source subnet")
			}
			offset := ipToUint32(src4) - ipToUint32(srcBase)
			result := make(net.IP, 16)
			copy(result, tgtNet.IP.To16())
			tgtLast4 := ipToUint32(tgtNet.IP.To16()[12:16]) + offset
			result[12] = byte(tgtLast4 >> 24) //nolint:gosec // intentional truncation for IP byte extraction
			result[13] = byte(tgtLast4 >> 16) //nolint:gosec // intentional truncation
			result[14] = byte(tgtLast4 >> 8)  //nolint:gosec // intentional truncation
			result[15] = byte(tgtLast4)       //nolint:gosec // intentional truncation
			return result.String(), nil
		}
		return deriveIPv4Offset(src4, srcNet, tgtNet)
	}
	return deriveIPv6Offset(src.To16(), srcNet, tgtNet)
}

func deriveIPv4Offset(src net.IP, srcNet, tgtNet *net.IPNet) (string, error) {
	srcBase := srcNet.IP.To4()
	tgtBase := tgtNet.IP.To4()
	if srcBase == nil || tgtBase == nil {
		return "", fmt.Errorf("IPv4/IPv6 mismatch between source and target subnets")
	}

	offset := ipToUint32(src) - ipToUint32(srcBase)
	result := uint32ToIP(ipToUint32(tgtBase) + offset)
	return result.String(), nil
}

func deriveIPv6Offset(src net.IP, srcNet, tgtNet *net.IPNet) (string, error) {
	srcBase := srcNet.IP.To16()
	tgtBase := tgtNet.IP.To16()
	if srcBase == nil || tgtBase == nil {
		return "", fmt.Errorf("invalid IPv6 addresses")
	}

	srcOffset := ipToUint32(src[12:16]) - ipToUint32(srcBase[12:16])
	result := make(net.IP, 16)
	copy(result, tgtBase)
	tgtLast4 := ipToUint32(tgtBase[12:16]) + srcOffset
	result[12] = byte(tgtLast4 >> 24) //nolint:gosec // intentional truncation for IP byte extraction
	result[13] = byte(tgtLast4 >> 16) //nolint:gosec // intentional truncation
	result[14] = byte(tgtLast4 >> 8)  //nolint:gosec // intentional truncation
	result[15] = byte(tgtLast4)       //nolint:gosec // intentional truncation

	return result.String(), nil
}

func ipToUint32(ip net.IP) uint32 {
	ip = ip.To4()
	if ip == nil {
		return 0
	}
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

func uint32ToIP(n uint32) net.IP {
	return net.IPv4(byte(n>>24), byte(n>>16), byte(n>>8), byte(n)) //nolint:gosec // intentional truncation for IP byte extraction
}

// DeriveBridgeMAC creates a locally-administered MAC from the IPMI MAC.
func DeriveBridgeMAC(ipmiMAC string) string {
	parts := strings.Split(strings.ReplaceAll(ipmiMAC, "-", ":"), ":")
	if len(parts) < 6 {
		return "02:54:00:00:00:01"
	}
	return fmt.Sprintf("02:54:%s:%s:%s:%s", parts[2], parts[3], parts[4], parts[5])
}

// RenderConfig generates the FRR configuration file content using the builder.
// overlayIP is used to detect IPv6 and conditionally add the IPv6 address-family.
func RenderConfig(cfg *network.Config, underlayIP, overlayIP string, nics []string) (string, error) {
	b := NewFRRConfigBuilder(cfg.ASN, underlayIP)

	if cfg.VRFName != "" {
		b.WithVRF(cfg.VRFName, cfg.VRFTableID)
	}

	b.WithNICs(nics)

	if cfg.BGPKeepalive > 0 && cfg.BGPHold > 0 {
		b.WithBGPTimers(cfg.BGPKeepalive, cfg.BGPHold)
	}

	if cfg.BFDTransmitMS > 0 && cfg.BFDReceiveMS > 0 {
		b.WithBFDProfile("datacenter", cfg.BFDTransmitMS, cfg.BFDReceiveMS)
	}

	// Always add IPv4 unicast.
	b.WithAddressFamily("ipv4", "unicast")

	// Add IPv6 unicast when overlay is IPv6.
	if isIPv6(overlayIP) {
		b.WithAddressFamily("ipv6", "unicast")
	}

	// Always add l2vpn evpn.
	b.WithAddressFamily("l2vpn", "evpn")

	if cfg.DCGWIPs != "" {
		b.WithOnefabric(
			strings.Split(cfg.DCGWIPs, ","),
			cfg.OverlayAggregate,
			cfg.VPNRT,
		)
	}

	return b.Build(), nil
}
