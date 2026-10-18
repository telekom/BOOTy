package network

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// dnsServer answers every query on 127.0.0.x with an A record of ip, or
// not at all; it counts the queries.
func dnsServer(t *testing.T, host string, port string, ip net.IP, queries *atomic.Int32) {
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
			if ip == nil || n < 12 {
				continue
			}
			q := 12 // the question: the name, type and class
			for q < n && buf[q] != 0 {
				q += int(buf[q]) + 1
			}
			resp := append([]byte(nil), buf[:q+5]...)
			resp[2], resp[3] = 0x81, 0x80 // response, recursion
			resp[6], resp[7] = 0, 1       // one answer
			resp[10], resp[11] = 0, 0     // no additional (EDNS) record
			resp = append(resp, 0xc0, 12, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4)
			resp = append(resp, ip.To4()...)
			_, _ = pc.WriteTo(resp, from)
		}
	}()
}

// A lookup asks every server at once and takes the first answer: the first
// server never answers, the second does.
func TestDNSFanout(t *testing.T) {
	probe, err := (&net.ListenConfig{}).ListenPacket(context.Background(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(probe.LocalAddr().String())
	_ = probe.Close()
	origPort := dnsPort
	t.Cleanup(func() { dnsPort = origPort })
	dnsPort = port
	var silent, answering atomic.Int32
	dnsServer(t, "127.0.0.1", port, nil, &silent)
	dnsServer(t, "127.0.0.2", port, net.IPv4(192, 0, 2, 7), &answering)

	f := &fanout{servers: []string{"127.0.0.1", "127.0.0.2"}}
	r := &net.Resolver{PreferGo: true, Dial: f.dial}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	ips, err := r.LookupIP(ctx, "ip4", "image.l2-parity.test.")
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
