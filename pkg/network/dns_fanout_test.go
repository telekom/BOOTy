package network

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

// dnsAnswer answers query q: rcode, an A record of ip unless nil, TC when
// truncated. The EDNS record of the query is dropped.
func dnsAnswer(q []byte, rcode byte, ip net.IP, truncated bool) []byte {
	end := 12 // the question: the name, type and class
	for end < len(q) && q[end] != 0 {
		end += int(q[end]) + 1
	}
	resp := append([]byte(nil), q[:end+5]...)
	resp[2], resp[3] = 0x81, 0x80|rcode // response, recursion
	if truncated {
		resp[2] |= 0x02
	}
	resp[6], resp[7], resp[10], resp[11] = 0, 0, 0, 0
	if ip != nil {
		resp[7] = 1
		resp = append(resp, 0xc0, 12, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4)
		resp = append(resp, ip.To4()...)
	}
	return resp
}

// udpDNS serves on host:port; answer returns the datagrams to send back
// for a query, after delay.
func udpDNS(t *testing.T, host, port string, delay time.Duration, queries *atomic.Int32, answer func(q []byte) [][]byte) {
	t.Helper()
	pc, err := (&net.ListenConfig{}).ListenPacket(context.Background(), "udp", net.JoinHostPort(host, port))
	if err != nil {
		t.Skipf("listen on %s: %v", host, err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			queries.Add(1)
			q := append([]byte(nil), buf[:n]...)
			go func() {
				time.Sleep(delay)
				for _, r := range answer(q) {
					_, _ = pc.WriteTo(r, from)
				}
			}()
		}
	}()
}

// freePort returns a UDP port that is free on 127.0.0.1.
func freePort(t *testing.T) string {
	t.Helper()
	probe, err := (&net.ListenConfig{}).ListenPacket(context.Background(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(probe.LocalAddr().String())
	_ = probe.Close()
	return port
}

func fanoutTest(t *testing.T, links [][]string) (*fanout, *net.Resolver, string) {
	t.Helper()
	port := freePort(t)
	origPort, origLinger := dnsPort, fanoutLinger
	t.Cleanup(func() { dnsPort, fanoutLinger = origPort, origLinger })
	dnsPort, fanoutLinger = port, 100*time.Millisecond
	f := &fanout{}
	f.set(links)
	return f, &net.Resolver{PreferGo: true, Dial: f.dial}, port
}

func lookup(t *testing.T, r *net.Resolver, timeout time.Duration) ([]net.IP, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return r.LookupIP(ctx, "ip4", "image.l2-parity.test.") //nolint:wrapcheck // test
}

// A lookup asks every link at once and takes the first answer: the first
// server never answers, the second does.
func TestDNSFanout(t *testing.T) {
	_, r, port := fanoutTest(t, [][]string{{"127.0.0.1"}, {"127.0.0.2"}})
	var silent, answering atomic.Int32
	udpDNS(t, "127.0.0.1", port, 0, &silent, func([]byte) [][]byte { return nil })
	udpDNS(t, "127.0.0.2", port, 0, &answering, func(q []byte) [][]byte { return [][]byte{dnsAnswer(q, 0, net.IPv4(192, 0, 2, 7), false)} })
	start := time.Now()
	ips, err := lookup(t, r, 3*time.Second)
	if err != nil || len(ips) != 1 || !ips[0].Equal(net.IPv4(192, 0, 2, 7)) {
		t.Fatalf("LookupIP = %v, %v", ips, err)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("the lookup took %s: it waited for the silent first server", took)
	}
	for deadline := time.Now().Add(time.Second); silent.Load() == 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond) // the silent server counts concurrently
	}
	if silent.Load() == 0 || answering.Load() == 0 {
		t.Fatalf("queries: silent %d, answering %d; want both asked", silent.Load(), answering.Load())
	}
}

// A fast NXDOMAIN of one link does not beat a later answer of another,
// like resolved (dns_query_ready).
func TestDNSFanoutPrefersSuccess(t *testing.T) {
	_, r, port := fanoutTest(t, [][]string{{"127.0.0.1"}, {"127.0.0.2"}})
	var a, b atomic.Int32
	udpDNS(t, "127.0.0.1", port, 0, &a, func(q []byte) [][]byte { return [][]byte{dnsAnswer(q, 3, nil, false)} })
	udpDNS(t, "127.0.0.2", port, 100*time.Millisecond, &b, func(q []byte) [][]byte {
		return [][]byte{dnsAnswer(q, 0, net.IPv4(192, 0, 2, 7), false)}
	})
	ips, err := lookup(t, r, 3*time.Second)
	if err != nil || len(ips) != 1 || !ips[0].Equal(net.IPv4(192, 0, 2, 7)) {
		t.Fatalf("LookupIP = %v, %v; want the answer of the slower link", ips, err)
	}
}

// One link refuses (no server), the other sends one datagram of another
// query ID and then nothing: the lookup ends at its deadline instead of
// hanging.
func TestDNSFanoutDeadline(t *testing.T) {
	_, r, port := fanoutTest(t, [][]string{{"127.0.0.1"}, {"127.0.0.2"}})
	var b atomic.Int32
	udpDNS(t, "127.0.0.2", port, 0, &b, func(q []byte) [][]byte {
		bad := dnsAnswer(q, 0, net.IPv4(192, 0, 2, 7), false)
		binary.BigEndian.PutUint16(bad, binary.BigEndian.Uint16(bad)+1)
		return [][]byte{bad}
	})
	done := make(chan error, 1)
	go func() {
		_, err := lookup(t, r, time.Second)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a lookup without an answer succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the lookup hangs past its deadline")
	}
}

// A truncated answer: the resolver asks again over TCP, and the fan-out
// sends it to the server that truncated.
func TestDNSFanoutTruncatedGoesToItsServer(t *testing.T) {
	_, r, port := fanoutTest(t, [][]string{{"127.0.0.1"}, {"127.0.0.2"}})
	var a, b atomic.Int32
	udpDNS(t, "127.0.0.1", port, 0, &a, func([]byte) [][]byte { return nil })
	udpDNS(t, "127.0.0.2", port, 0, &b, func(q []byte) [][]byte { return [][]byte{dnsAnswer(q, 0, nil, true)} })
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", net.JoinHostPort("127.0.0.2", port))
	if err != nil {
		t.Skipf("listen tcp: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				var n [2]byte
				if _, err := io.ReadFull(c, n[:]); err != nil {
					return
				}
				q := make([]byte, binary.BigEndian.Uint16(n[:]))
				if _, err := io.ReadFull(c, q); err != nil {
					return
				}
				resp := dnsAnswer(q, 0, net.IPv4(192, 0, 2, 9), false)
				_, _ = c.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(resp))), resp...)) //nolint:gosec // short
			}()
		}
	}()
	ips, err := lookup(t, r, 3*time.Second)
	if err != nil || len(ips) != 1 || !ips[0].Equal(net.IPv4(192, 0, 2, 9)) {
		t.Fatalf("LookupIP = %v, %v; want the TCP answer of the truncating server", ips, err)
	}
}

// A link moves to its next server after a failure.
func TestDNSFanoutFailover(t *testing.T) {
	f := &fanout{}
	f.set([][]string{{"10.0.0.1", "10.0.0.2"}, {"10.0.1.1"}})
	f.failed(0, "10.0.0.1")
	f.failed(0, "10.0.0.1") // a second report of the same failure changes nothing
	if got, _, _ := f.current(); !slices.Equal(got, []string{"10.0.0.2", "10.0.1.1"}) {
		t.Fatalf("current = %v", got)
	}
	f.set(nil)
	if got, _, _ := f.current(); got != nil {
		t.Fatalf("current after reset = %v", got)
	}
}
