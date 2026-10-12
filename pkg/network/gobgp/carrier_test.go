//go:build linux

package gobgp

import (
	"log/slog"
	"testing"
)

// Only a carrier change of an uplink with a session acts, once per change.
func TestCarrierWatchTracksChanges(t *testing.T) {
	w := &carrierWatch{peers: map[string]string{"ens1f0np0": "fe80::1%ens1f0np0"}, down: map[string]bool{}, log: slog.Default()}
	w.handle("eth0", false) // no session: ignored
	if len(w.down) != 0 {
		t.Fatalf("down = %v, want no change for an interface without session", w.down)
	}
	w.handle("ens1f0np0", true) // carrier up while up: nothing to do
	if w.down["ens1f0np0"] {
		t.Fatal("uplink marked down after a carrier-up update")
	}
}
