//go:build linux

package bfd

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestRFC5880PacketFields is the evidence of docs/rfc-compliance.md for
// the fields RFC 5880 section 6.8.7 fixes in every transmitted packet:
// version 1, C, A, D and M clear, Length 24 without authentication, Required
// Min Echo RX Interval zero without echo support, and the slow transmit
// interval of at least one second before Up (section 6.8.3).
func TestRFC5880PacketFields(t *testing.T) {
	s := NewSession(Timers{DesiredMinTx: 300 * time.Millisecond, RequiredMinRx: 300 * time.Millisecond, DetectMult: 3}, 7)
	p := s.Packet()
	b := p.Marshal()
	if b[0]>>5 != 1 {
		t.Errorf("version = %d, want 1", b[0]>>5)
	}
	if b[1]&0x0f != 0 {
		t.Errorf("C/A/D/M bits = %#x, want 0", b[1]&0x0f)
	}
	if b[3] != 24 || len(b) != 24 {
		t.Errorf("length = %d (%d bytes), want 24", b[3], len(b))
	}
	if p.RequiredMinEchoRxUS != 0 {
		t.Errorf("Required Min Echo RX = %d, want 0 (no echo)", p.RequiredMinEchoRxUS)
	}
	if p.DesiredMinTxUS < 1_000_000 {
		t.Errorf("Desired Min TX before Up = %d us, want at least 1 s", p.DesiredMinTxUS)
	}
	if p.MyDiscriminator == 0 {
		t.Error("My Discriminator is zero")
	}
}

// TestRFC5881SourcePortAndTTL checks the send socket of a session: a source
// port of 49152-65535 (RFC 5881 section 4) and TTL 255 (section 5, also
// for multihop sessions in BOOTy, which RFC 5883 section 5 allows).
func TestRFC5881SourcePortAndTTL(t *testing.T) {
	for _, mh := range []bool{false, true} {
		conn, err := openTx(&Peer{Addr: netip.MustParseAddr("127.0.0.1"), Multihop: mh})
		if err != nil {
			t.Fatal(err)
		}
		port := conn.LocalAddr().(*net.UDPAddr).Port
		if port < srcPortMin || port > srcPortMax {
			t.Errorf("multihop=%v: source port %d, want 49152-65535", mh, port)
		}
		raw, err := conn.(*net.UDPConn).SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var ttl int
		var serr error
		if err := raw.Control(func(fd uintptr) { ttl, serr = unix.GetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_TTL) }); err != nil || serr != nil {
			t.Fatalf("read TTL: %v %v", err, serr)
		}
		if ttl != 255 {
			t.Errorf("multihop=%v: TTL %d, want 255", mh, ttl)
		}
		_ = conn.Close()
	}
}
