//go:build linux

package dhcp4

import (
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/mdlayher/packet"
	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"
)

// rawTransport sends broadcasts and receives replies on a packet socket of
// the NIC, which works before the NIC has an address; unicasts go through
// a UDP socket bound to the lease address and the NIC.
type rawTransport struct {
	iface *net.Interface
	conn  *packet.Conn
}

// dhcpReplies passes IPv4 UDP datagrams to port 68 that are not fragments.
var dhcpReplies = []bpf.Instruction{
	bpf.LoadAbsolute{Off: 23, Size: 1},
	bpf.JumpIf{Cond: bpf.JumpNotEqual, Val: unix.IPPROTO_UDP, SkipTrue: 6},
	bpf.LoadAbsolute{Off: 20, Size: 2},
	bpf.JumpIf{Cond: bpf.JumpBitsSet, Val: 0x1fff, SkipTrue: 4},
	bpf.LoadMemShift{Off: 14},
	bpf.LoadIndirect{Off: 16, Size: 2},
	bpf.JumpIf{Cond: bpf.JumpNotEqual, Val: 68, SkipTrue: 1},
	bpf.RetConstant{Val: 0xffff},
	bpf.RetConstant{Val: 0},
}

func openTransport(iface *net.Interface) (transport, error) {
	filter, err := bpf.Assemble(dhcpReplies)
	if err != nil {
		return nil, fmt.Errorf("assemble DHCP filter: %w", err)
	}
	conn, err := packet.Listen(iface, packet.Raw, unix.ETH_P_IP, &packet.Config{Filter: filter})
	if err != nil {
		return nil, fmt.Errorf("packet socket on %s: %w", iface.Name, err)
	}
	return &rawTransport{iface: iface, conn: conn}, nil
}

// broadcast sends with the IP TOS of networkd (CS6).
func (t *rawTransport) broadcast(src net.IP, m *message) error {
	payload, err := m.encode()
	if err != nil {
		return fmt.Errorf("encode DHCP %s: %w", m.typ, err)
	}
	eth := &layers.Ethernet{SrcMAC: t.iface.HardwareAddr, DstMAC: layers.EthernetBroadcast, EthernetType: layers.EthernetTypeIPv4}
	ip := &layers.IPv4{Version: 4, TTL: 64, TOS: 0xc0, Protocol: layers.IPProtocolUDP, SrcIP: ipOrZero(src), DstIP: net.IPv4bcast}
	udp := &layers.UDP{SrcPort: 68, DstPort: 67}
	if err := udp.SetNetworkLayerForChecksum(ip); err != nil {
		return fmt.Errorf("DHCP checksum: %w", err)
	}
	buf := gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(buf, gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true},
		eth, ip, udp, gopacket.Payload(payload)); err != nil {
		return fmt.Errorf("serialize DHCP %s: %w", m.typ, err)
	}
	if _, err := t.conn.WriteTo(buf.Bytes(), &packet.Addr{HardwareAddr: layers.EthernetBroadcast}); err != nil {
		return fmt.Errorf("send DHCP %s on %s: %w", m.typ, t.iface.Name, err)
	}
	return nil
}

func (t *rawTransport) unicast(src, dst net.IP, m *message) error {
	payload, err := m.encode()
	if err != nil {
		return fmt.Errorf("encode DHCP %s: %w", m.typ, err)
	}
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.IPPROTO_UDP)
	if err != nil {
		return fmt.Errorf("UDP socket: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	for _, o := range []struct{ level, opt int }{{unix.SOL_SOCKET, unix.SO_REUSEADDR}, {unix.SOL_SOCKET, unix.SO_REUSEPORT}} {
		if err := unix.SetsockoptInt(fd, o.level, o.opt, 1); err != nil {
			return fmt.Errorf("setsockopt: %w", err)
		}
	}
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_TOS, 0xc0); err != nil {
		return fmt.Errorf("IP_TOS: %w", err)
	}
	if err := unix.BindToDevice(fd, t.iface.Name); err != nil {
		return fmt.Errorf("bind to %s: %w", t.iface.Name, err)
	}
	var from, to unix.SockaddrInet4
	from.Port, to.Port = 68, 67
	copy(from.Addr[:], src.To4())
	copy(to.Addr[:], dst.To4())
	if err := unix.Bind(fd, &from); err != nil {
		return fmt.Errorf("bind %s:68: %w", src, err)
	}
	if err := unix.Sendto(fd, payload, 0, &to); err != nil {
		return fmt.Errorf("send DHCP %s to %s: %w", m.typ, dst, err)
	}
	return nil
}

func (t *rawTransport) receive(timeout time.Duration) ([]byte, error) {
	if err := t.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, fmt.Errorf("read deadline: %w", err)
	}
	buf := make([]byte, 65535)
	for {
		n, _, err := t.conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				return nil, errTimeout
			}
			return nil, fmt.Errorf("receive DHCP: %w", err)
		}
		p := gopacket.NewPacket(buf[:n], layers.LayerTypeEthernet, gopacket.DecodeOptions{Lazy: true, NoCopy: true})
		if udp, ok := p.Layer(layers.LayerTypeUDP).(*layers.UDP); ok && udp.DstPort == 68 {
			return append([]byte(nil), udp.Payload...), nil
		}
	}
}

func (t *rawTransport) close() error {
	return t.conn.Close() //nolint:wrapcheck // closing
}
