//go:build linux

package lldp

import (
	"log/slog"
	"reflect"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A neighbor stays until its TTL ends, a newer frame replaces it, and TTL
// 0 removes it at once.
func TestReceiverRecord(t *testing.T) {
	r := &receiver{neighbors: map[string]Neighbor{}, expires: map[string]time.Time{}, log: slog.Default()}
	now := time.Unix(1000, 0)
	a := Neighbor{ChassisID: "leaf01", PortID: "eth1", Interface: "ens1f0np0", TTL: 120}
	b := Neighbor{ChassisID: "leaf02", PortID: "eth1", Interface: "ens1f1np1", TTL: 10}
	r.record(&a, now)
	r.record(&b, now)
	if got := r.list(now.Add(5 * time.Second)); !reflect.DeepEqual(got, []Neighbor{a, b}) {
		t.Fatalf("neighbors = %+v", got)
	}
	if got := r.list(now.Add(11 * time.Second)); !reflect.DeepEqual(got, []Neighbor{a}) {
		t.Fatalf("after the TTL of %s: %+v", b.Interface, got)
	}
	a2 := a
	a2.PortID = "eth9"
	r.record(&a2, now.Add(20*time.Second))
	if got := r.list(now.Add(130 * time.Second)); !reflect.DeepEqual(got, []Neighbor{a2}) {
		t.Fatalf("after a newer frame: %+v", got)
	}
	gone := a2
	gone.TTL = 0
	r.record(&gone, now.Add(21*time.Second))
	if got := r.list(now.Add(22 * time.Second)); len(got) != 0 {
		t.Fatalf("after TTL 0: %+v", got)
	}
}

// FirstSeen stays while the same neighbor sends; a different neighbor on
// the interface, or the same one after its TTL ran out, counts as a change.
func TestReceiverChanges(t *testing.T) {
	r := &receiver{neighbors: map[string]Neighbor{}, expires: map[string]time.Time{}, log: slog.Default()}
	t0 := time.Unix(1000, 0)
	n := Neighbor{ChassisID: "leaf01", PortID: "eth1", Interface: "ens1f0np0", TTL: 120}
	for i, step := range []struct {
		at        time.Duration
		chassis   string
		firstSeen time.Duration
		changes   int
	}{
		{0, "leaf01", 0, 0},
		{30 * time.Second, "leaf01", 0, 0},                  // same neighbor
		{60 * time.Second, "leaf02", 60 * time.Second, 1},   // cable moved
		{400 * time.Second, "leaf02", 400 * time.Second, 2}, // back after the TTL
	} {
		f := n
		f.ChassisID = step.chassis
		r.record(&f, t0.Add(step.at))
		got := r.neighbors["ens1f0np0"]
		if !got.FirstSeen.Equal(t0.Add(step.firstSeen)) || got.Changes != step.changes || !got.LastSeen.Equal(t0.Add(step.at)) {
			t.Fatalf("step %d: first %v last %v changes %d", i, got.FirstSeen.Sub(t0), got.LastSeen.Sub(t0), got.Changes)
		}
	}
}

// The packet socket also sees the frames BOOTy sends, and a bridge may
// reflect them; they are not neighbors. A frame from another NIC of the
// host (two NICs cabled to each other) is.
func TestOwnFrame(t *testing.T) {
	mac := []byte{2, 0, 0, 0, 0, 1}
	frame := func(src ...byte) []byte { return append([]byte{1, 0x80, 0xc2, 0, 0, 0x0e}, src...) }
	in := &unix.SockaddrLinklayer{Pkttype: unix.PACKET_MULTICAST}
	for _, tt := range []struct {
		name  string
		frame []byte
		from  unix.Sockaddr
		want  bool
	}{
		{"outgoing", frame(2, 0, 0, 0, 0, 9), &unix.SockaddrLinklayer{Pkttype: unix.PACKET_OUTGOING}, true},
		{"reflected", frame(2, 0, 0, 0, 0, 1), in, true},
		{"switch", frame(2, 0, 0, 0, 0, 9), in, false},
		{"no sockaddr", frame(2, 0, 0, 0, 0, 9), nil, false},
	} {
		if got := ownFrame(tt.frame, tt.from, mac); got != tt.want {
			t.Errorf("%s: ownFrame = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// Only NICs with carrier need a neighbor.
func TestMissingNeighbors(t *testing.T) {
	orig := hasCarrier
	t.Cleanup(func() { hasCarrier = orig })
	hasCarrier = func(iface string) bool { return iface != "eno1" }
	got := missingNeighbors([]string{"eno1", "ens1f0np0", "ens1f1np1"}, []Neighbor{{Interface: "ens1f0np0"}})
	if !reflect.DeepEqual(got, []string{"ens1f1np1"}) {
		t.Fatalf("missing = %v, want [ens1f1np1]", got)
	}
}
