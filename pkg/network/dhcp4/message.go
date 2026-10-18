package dhcp4

import (
	"encoding/binary"
	"net"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

// Option is a DHCP option that the Lease does not decode.
type Option struct {
	Type layers.DHCPOpt
	Data []byte
}

// Lease is what a DHCPACK assigns.
type Lease struct {
	ServerID     net.IP
	FixedAddress net.IP
	Netmask      net.IPMask
	NextServer   net.IP
	Broadcast    net.IP
	Router       []net.IP
	DNS          []net.IP
	TimeServer   []net.IP
	DomainName   string
	MTU          uint16
	// OtherOptions are the options that have no field above.
	OtherOptions []Option

	Bound  time.Time
	Renew  time.Time // T1
	Rebind time.Time // T2
	Expire time.Time
}

// optRapidCommit is option 80 (RFC 4039).
const optRapidCommit layers.DHCPOpt = 80

// minMessageSize is the smallest BOOTP message (RFC 1542 section 2.1);
// shorter messages are padded, as relays may drop them.
const minMessageSize = 300

// message is a DHCP message the client sends.
type message struct {
	typ     layers.DHCPMsgType
	xid     uint32
	secs    uint16
	ciaddr  net.IP
	mac     net.HardwareAddr
	options []layers.DHCPOption
}

// encode returns the BOOTP payload of m.
func (m *message) encode() ([]byte, error) {
	d := &layers.DHCPv4{
		Operation:    layers.DHCPOpRequest,
		HardwareType: layers.LinkTypeEthernet,
		HardwareLen:  6,
		Xid:          m.xid,
		Secs:         m.secs,
		ClientIP:     ipOrZero(m.ciaddr),
		YourClientIP: net.IPv4zero,
		NextServerIP: net.IPv4zero,
		RelayAgentIP: net.IPv4zero,
		ClientHWAddr: m.mac,
		Options:      append([]layers.DHCPOption{layers.NewDHCPOption(layers.DHCPOptMessageType, []byte{byte(m.typ)})}, m.options...),
	}
	for d.Len() < minMessageSize {
		d.Options = append(d.Options, layers.NewDHCPOption(layers.DHCPOptPad, nil))
	}
	buf := gopacket.NewSerializeBuffer()
	if err := d.SerializeTo(buf, gopacket.SerializeOptions{}); err != nil {
		return nil, err //nolint:wrapcheck // the caller names the message
	}
	return buf.Bytes(), nil
}

func ipOrZero(ip net.IP) net.IP {
	if ip4 := ip.To4(); ip4 != nil {
		return ip4
	}
	return net.IPv4zero
}

// reply is a decoded DHCP message from a server.
type reply struct {
	typ   layers.DHCPMsgType
	xid   uint32
	mac   net.HardwareAddr
	lease Lease
	// rapidCommit is set when an ACK answers a DISCOVER (RFC 4039).
	rapidCommit bool
}

// decodeReply parses a BOOTP payload; ok is false for anything that is
// not a server reply.
func decodeReply(payload []byte, now time.Time) (reply, bool) {
	var d layers.DHCPv4
	if err := d.DecodeFromBytes(payload, gopacket.NilDecodeFeedback); err != nil || d.Operation != layers.DHCPOpReply {
		return reply{}, false
	}
	r := reply{xid: d.Xid, mac: d.ClientHWAddr}
	r.lease.FixedAddress = d.YourClientIP.To4()
	r.lease.Bound = now
	if d.NextServerIP != nil && !d.NextServerIP.IsUnspecified() {
		r.lease.NextServer = d.NextServerIP.To4()
	}
	var t leaseTimes
	for _, o := range d.Options {
		r.apply(o, &t)
	}
	t.set(&r.lease, now)
	return r, true
}

// leaseTimes are options 51, 58 and 59 in seconds.
type leaseTimes struct{ lease, t1, t2 uint32 }

// set derives the lease times; T1 and T2 default to 0.5 and 0.875 of the
// lease time (RFC 2131 section 4.4.5). Without a lease time the lease does
// not end.
func (t leaseTimes) set(l *Lease, now time.Time) {
	if t.lease == 0 {
		return
	}
	if t.t1 == 0 || t.t1 >= t.lease {
		t.t1 = t.lease / 2
	}
	if t.t2 == 0 || t.t2 >= t.lease || t.t2 <= t.t1 {
		t.t2 = t.lease / 8 * 7
	}
	l.Expire = now.Add(time.Duration(t.lease) * time.Second)
	l.Renew = now.Add(time.Duration(t.t1) * time.Second)
	l.Rebind = now.Add(time.Duration(t.t2) * time.Second)
}

// apply decodes one option into the reply.
func (r *reply) apply(o layers.DHCPOption, t *leaseTimes) {
	switch o.Type {
	case layers.DHCPOptMessageType:
		if len(o.Data) == 1 {
			r.typ = layers.DHCPMsgType(o.Data[0])
		}
	case optRapidCommit:
		r.rapidCommit = true
	case layers.DHCPOptLeaseTime:
		t.lease = u32(o.Data)
	case layers.DHCPOptT1:
		t.t1 = u32(o.Data)
	case layers.DHCPOptT2:
		t.t2 = u32(o.Data)
	default:
		if !r.lease.apply(o) {
			r.lease.OtherOptions = append(r.lease.OtherOptions, Option{Type: o.Type, Data: append([]byte(nil), o.Data...)})
		}
	}
}

// apply decodes an option that the Lease has a field for.
func (l *Lease) apply(o layers.DHCPOption) bool {
	switch o.Type {
	case layers.DHCPOptSubnetMask:
		l.Netmask = net.IPMask(o.Data)
	case layers.DHCPOptRouter:
		l.Router = ipList(o.Data)
	case layers.DHCPOptDNS:
		l.DNS = ipList(o.Data)
	case layers.DHCPOptTimeServer:
		l.TimeServer = ipList(o.Data)
	case layers.DHCPOptBroadcastAddr:
		l.Broadcast = ipOne(o.Data)
	case layers.DHCPOptServerID:
		l.ServerID = ipOne(o.Data)
	case layers.DHCPOptDomainName:
		l.DomainName = string(o.Data)
	case layers.DHCPOptInterfaceMTU:
		if len(o.Data) == 2 {
			l.MTU = binary.BigEndian.Uint16(o.Data)
		}
	default:
		return false
	}
	return true
}

func u32(b []byte) uint32 {
	if len(b) != 4 {
		return 0
	}
	return binary.BigEndian.Uint32(b)
}

func ipOne(b []byte) net.IP {
	if len(b) != net.IPv4len {
		return nil
	}
	return net.IP(append([]byte(nil), b...))
}

func ipList(b []byte) []net.IP {
	var out []net.IP
	for i := 0; i+net.IPv4len <= len(b); i += net.IPv4len {
		out = append(out, net.IP(append([]byte(nil), b[i:i+net.IPv4len]...)))
	}
	return out
}
