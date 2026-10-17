//go:build linux

package lldp

import (
	"log/slog"
	"reflect"
	"testing"
	"time"
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
