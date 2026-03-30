//go:build linux

// Package lldp provides LLDP frame listening for switch topology discovery.
package lldp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"golang.org/x/sys/unix"
)

// Neighbor represents a discovered LLDP neighbor (switch).
type Neighbor struct {
	ChassisID   string
	PortID      string
	SystemName  string
	Description string
	Interface   string // local interface where LLDP was received
	TTL         uint16
}

// Listen captures LLDP frames on the given interface until a frame is received
// or the context expires. Uses raw AF_PACKET sockets (no libpcap needed).
func Listen(ctx context.Context, iface string, timeout time.Duration) (*Neighbor, error) {
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, fmt.Errorf("interface %s: %w", iface, err)
	}

	// Create raw socket for LLDP EtherType 0x88cc.
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(htons(0x88cc)))
	if err != nil {
		return nil, fmt.Errorf("raw socket: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()

	// Bind to specific interface.
	addr := unix.SockaddrLinklayer{
		Protocol: htons(0x88cc),
		Ifindex:  ifi.Index,
	}
	if err := unix.Bind(fd, &addr); err != nil {
		return nil, fmt.Errorf("bind to %s: %w", iface, err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	slog.Info("listening for LLDP frames", "interface", iface, "timeout", timeout)

	buf := make([]byte, 1600)
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("LLDP timeout on %s: %w", iface, ctx.Err())
		default:
		}

		// Set a short read deadline so we can check context cancellation.
		tv := unix.Timeval{Sec: 1}
		if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
			return nil, fmt.Errorf("setting read timeout: %w", err)
		}

		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			continue // timeout — re-check context
		}
		if n < 14 {
			continue
		}

		neighbor := parseLLDP(buf[14:n], iface) // skip ethernet header
		if neighbor != nil {
			slog.Info("LLDP neighbor discovered",
				"interface", iface,
				"chassisID", neighbor.ChassisID,
				"portID", neighbor.PortID,
				"systemName", neighbor.SystemName,
			)
			return neighbor, nil
		}
	}
}

// DiscoverAll listens for LLDP on all provided interfaces concurrently.
func DiscoverAll(ctx context.Context, ifaces []string, timeout time.Duration) []Neighbor {
	type result struct {
		neighbor *Neighbor
	}

	results := make(chan result, len(ifaces))
	for _, iface := range ifaces {
		go func(ifName string) {
			n, err := Listen(ctx, ifName, timeout)
			if err != nil {
				slog.Debug("LLDP listen failed", "interface", ifName, "error", err)
				results <- result{nil}
				return
			}
			results <- result{n}
		}(iface)
	}

	var neighbors []Neighbor
	for range ifaces {
		r := <-results
		if r.neighbor != nil {
			neighbors = append(neighbors, *r.neighbor)
		}
	}
	return neighbors
}

// parseLLDP extracts LLDP neighbor information from raw LLDP payload.
func parseLLDP(data []byte, iface string) *Neighbor {
	pkt := gopacket.NewPacket(data, layers.LayerTypeLinkLayerDiscovery, gopacket.NoCopy)

	lldpLayer := pkt.Layer(layers.LayerTypeLinkLayerDiscovery)
	if lldpLayer == nil {
		return nil
	}
	lldp, ok := lldpLayer.(*layers.LinkLayerDiscovery)
	if !ok {
		return nil
	}

	n := &Neighbor{
		Interface: iface,
		TTL:       lldp.TTL,
	}

	switch lldp.ChassisID.Subtype {
	case layers.LLDPChassisIDSubTypeMACAddr:
		if len(lldp.ChassisID.ID) >= 6 {
			n.ChassisID = net.HardwareAddr(lldp.ChassisID.ID).String()
		}
	default:
		n.ChassisID = sanitizeLLDP(string(lldp.ChassisID.ID))
	}

	n.PortID = sanitizeLLDP(string(lldp.PortID.ID))

	infoLayer := pkt.Layer(layers.LayerTypeLinkLayerDiscoveryInfo)
	if infoLayer != nil {
		if info, ok := infoLayer.(*layers.LinkLayerDiscoveryInfo); ok {
			n.SystemName = sanitizeLLDP(info.SysName)
			n.Description = sanitizeLLDP(info.SysDescription)
		}
	}

	return n
}

// GetInterfaceNames returns names of all physical (non-loopback, up) interfaces.
func GetInterfaceNames() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("listing interfaces: %w", err)
	}
	var names []string
	for _, i := range ifaces {
		if i.Flags&net.FlagLoopback != 0 || i.Flags&net.FlagUp == 0 {
			continue
		}
		names = append(names, i.Name)
	}
	return names, nil
}

const maxLLDPFieldLen = 255

// sanitizeLLDP replaces control characters in untrusted LLDP TLV strings
// to prevent log injection and terminal escape sequence attacks.
func sanitizeLLDP(s string) string {
	if len(s) > maxLLDPFieldLen {
		s = s[:maxLLDPFieldLen]
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, s)
}

func htons(v uint16) uint16 {
	return (v << 8) | (v >> 8)
}
