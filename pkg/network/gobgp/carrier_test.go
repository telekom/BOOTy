//go:build linux

package gobgp

import (
	"context"
	"log/slog"
	"testing"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

func linkUpdate(name string, flags uint32) netlink.LinkUpdate {
	u := netlink.LinkUpdate{Link: &netlink.Device{LinkAttrs: netlink.LinkAttrs{Name: name}}}
	u.IfInfomsg = nl.IfInfomsg{IfInfomsg: unix.IfInfomsg{Flags: flags}}
	return u
}

// Carrier loss stops the session once, a repeated loss does nothing, the
// carrier coming back starts it once, and other interfaces are ignored.
// IFF_LOWER_UP is the carrier; IFF_UP alone is not.
func TestCarrierWatchDisableEnable(t *testing.T) {
	var calls []string
	w := &carrierWatch{
		peers: map[string]string{"ens1f0np0": "fe80::1%ens1f0np0"},
		down:  map[string]bool{},
		disable: func(_ context.Context, addr string) error {
			calls = append(calls, "disable "+addr)
			return nil
		},
		enable: func(_ context.Context, addr string) error {
			calls = append(calls, "enable "+addr)
			return nil
		},
		log: slog.Default(),
	}
	steps := []struct {
		update netlink.LinkUpdate
		want   []string
	}{
		{linkUpdate("ens1f0np0", unix.IFF_UP|unix.IFF_LOWER_UP), nil},
		{linkUpdate("ens1f0np0", unix.IFF_UP), []string{"disable fe80::1%ens1f0np0"}},
		{linkUpdate("ens1f0np0", unix.IFF_UP), []string{"disable fe80::1%ens1f0np0"}},
		{linkUpdate("eth9", 0), []string{"disable fe80::1%ens1f0np0"}},
		{linkUpdate("ens1f0np0", unix.IFF_UP|unix.IFF_LOWER_UP), []string{"disable fe80::1%ens1f0np0", "enable fe80::1%ens1f0np0"}},
		{linkUpdate("ens1f0np0", unix.IFF_UP|unix.IFF_LOWER_UP), []string{"disable fe80::1%ens1f0np0", "enable fe80::1%ens1f0np0"}},
		{netlink.LinkUpdate{}, []string{"disable fe80::1%ens1f0np0", "enable fe80::1%ens1f0np0"}},
	}
	for i, s := range steps {
		w.handleUpdate(s.update)
		if len(calls) != len(s.want) {
			t.Fatalf("step %d: calls = %v, want %v", i, calls, s.want)
		}
		for j := range s.want {
			if calls[j] != s.want[j] {
				t.Fatalf("step %d: calls = %v, want %v", i, calls, s.want)
			}
		}
	}
}
