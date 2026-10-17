//go:build linux

// Package lldp provides LLDP frame listening for switch topology discovery.
package lldp

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Listen captures LLDP frames on the given interface until a frame is received
// or the context expires. Uses raw AF_PACKET sockets (no libpcap needed).
func Listen(ctx context.Context, iface string, timeout time.Duration) (*Neighbor, error) {
	fd, err := openRX(iface)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unix.Close(fd) }()

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	slog.Info("listening for LLDP frames", "interface", iface, "timeout", timeout)

	buf := make([]byte, 1600)
	mac := nicMAC(iface)
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("LLDP timeout on %s: %w", iface, ctx.Err())
		default:
		}

		n, from, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			continue // timeout — re-check context
		}
		if n < 14 || ownFrame(buf[:n], from, mac) {
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

// openRX opens a raw socket that receives the LLDP frames of iface, with
// a 1 s read timeout.
func openRX(iface string) (int, error) {
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return -1, fmt.Errorf("interface %s: %w", iface, err)
	}
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(htons(0x88cc)))
	if err != nil {
		return -1, fmt.Errorf("raw socket: %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrLinklayer{Protocol: htons(0x88cc), Ifindex: ifi.Index}); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("bind to %s: %w", iface, err)
	}
	tv := unix.Timeval{Sec: 1}
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("setting read timeout: %w", err)
	}
	return fd, nil
}

// DiscoverAll listens for LLDP on all provided interfaces concurrently.
func DiscoverAll(ctx context.Context, ifaces []string, timeout time.Duration) []Neighbor {
	type result struct {
		neighbor *Neighbor
	}

	results := make(chan result, len(ifaces))
	for _, iface := range ifaces {
		go func(ifName string) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("panic in LLDP listener goroutine", "interface", ifName, "panic", r)
					results <- result{nil}
				}
			}()

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

// ownFrame reports whether an Ethernet frame read from the packet socket
// of a NIC is one that BOOTy sent on it: the socket sees the outgoing
// frames, and a switch or bridge may reflect them (source MAC = the NIC).
func ownFrame(frame []byte, from unix.Sockaddr, mac net.HardwareAddr) bool {
	if ll, ok := from.(*unix.SockaddrLinklayer); ok && ll.Pkttype == unix.PACKET_OUTGOING {
		return true
	}
	return len(mac) == 6 && len(frame) >= 12 && bytes.Equal(frame[6:12], mac)
}

// nicMAC returns the MAC of a NIC, nil when it is gone.
func nicMAC(iface string) net.HardwareAddr {
	if ifi, err := net.InterfaceByName(iface); err == nil {
		return ifi.HardwareAddr
	}
	return nil
}

func htons(v uint16) uint16 {
	return (v << 8) | (v >> 8)
}
