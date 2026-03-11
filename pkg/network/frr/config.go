package frr

import (
	"fmt"
	"net"
	"strings"
	"text/template"

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

type frrConfigData struct {
	ASN              uint32
	UnderlayIP       string
	NICs             []string
	VRFName          string
	IsOnefabric      bool
	DCGWIPs          []string
	LeafASN          uint32
	LocalASN         uint32
	OverlayAggregate string
	VPNRT            string
}

const frrConfigTemplate = `frr version 10.3
frr defaults datacenter
!
router bgp {{ .ASN }} vrf {{ .VRFName }}
 bgp router-id {{ .UnderlayIP }}
 no bgp default ipv4-unicast
 bgp bestpath as-path multipath-relax
 neighbor fabric peer-group
 neighbor fabric remote-as external
{{- range .NICs }}
 neighbor {{ . }} interface peer-group fabric
{{- end }}
 !
 address-family ipv4 unicast
  redistribute connected
 exit-address-family
 !
 address-family l2vpn evpn
  neighbor fabric activate
  advertise-all-vni
 exit-address-family
exit
!
{{- if .IsOnefabric }}
{{- range .DCGWIPs }}
 neighbor {{ . }} remote-as internal
 neighbor {{ . }} update-source {{ $.UnderlayIP }}
{{- end }}
 !
 address-family l2vpn evpn
{{- range .DCGWIPs }}
  neighbor {{ . }} activate
{{- end }}
{{- if .OverlayAggregate }}
  aggregate-address {{ .OverlayAggregate }}
{{- end }}
{{- if .VPNRT }}
  route-target both {{ .VPNRT }}
{{- end }}
 exit-address-family
{{- end }}
line vty
!
`

// RenderConfig generates the FRR configuration file content.
func RenderConfig(cfg *network.Config, underlayIP string, nics []string) (string, error) {
	data := frrConfigData{
		ASN:        cfg.ASN,
		UnderlayIP: underlayIP,
		NICs:       nics,
		VRFName:    cfg.VRFName,
	}

	if cfg.DCGWIPs != "" {
		data.IsOnefabric = true
		data.DCGWIPs = strings.Split(cfg.DCGWIPs, ",")
		data.LeafASN = cfg.LeafASN
		data.LocalASN = cfg.LocalASN
		data.OverlayAggregate = cfg.OverlayAggregate
		data.VPNRT = cfg.VPNRT
	}

	tmpl, err := template.New("frr").Parse(frrConfigTemplate)
	if err != nil {
		return "", fmt.Errorf("parse FRR template: %w", err)
	}

	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("execute FRR template: %w", err)
	}

	return sb.String(), nil
}
