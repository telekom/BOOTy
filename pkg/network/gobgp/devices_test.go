//go:build linux

package gobgp

import (
	"testing"

	"github.com/vishvananda/netlink"

	"github.com/telekom/BOOTy/pkg/network"
)

func TestSysctlBool(t *testing.T) {
	for _, tt := range []struct {
		b    *bool
		def  bool
		want string
	}{
		{nil, true, "1"}, {nil, false, "0"},
		{new(true), false, "1"}, {new(false), true, "0"},
	} {
		if got := sysctlBool(tt.b, tt.def); got != tt.want {
			t.Errorf("sysctlBool(%v, %v) = %q, want %q", tt.b, tt.def, got, tt.want)
		}
	}
}

func TestBoolValue(t *testing.T) {
	if boolValue(nil) || boolValue(new(false)) || !boolValue(new(true)) {
		t.Fatal("boolValue: unset and false are false, true is true")
	}
}

// The netplan tunnel MTU wins; without it the VXLAN device gets the NIC MTU
// minus the VXLAN overhead.
func TestVXLANDeviceMTU(t *testing.T) {
	o := &OverlayTier{cfg: &Config{MTU: 9100}}
	if got := o.vxlanDeviceMTU(); got != 9050 {
		t.Errorf("default VXLAN MTU = %d, want 9050", got)
	}
	o.cfg.OverlayDevices.VXLANMTU = 9000
	if got := o.vxlanDeviceMTU(); got != 9000 {
		t.Errorf("netplan VXLAN MTU = %d, want 9000", got)
	}
}

// The netplan MAC, mac-learning and arp-proxy go onto the VXLAN device;
// unset learning and proxy are off, and a bad MAC fails.
func TestApplyVXLANSettings(t *testing.T) {
	o := &OverlayTier{cfg: &Config{OverlayDevices: network.OverlayDevices{
		VXLANMAC: "02:54:c0:00:02:13", VXLANLearning: new(true), VXLANARPProxy: new(true),
	}}}
	v := &netlink.Vxlan{Learning: false, Proxy: false}
	if err := o.applyVXLANSettings(v); err != nil {
		t.Fatal(err)
	}
	if v.HardwareAddr.String() != "02:54:c0:00:02:13" || !v.Learning || !v.Proxy {
		t.Errorf("vxlan = mac %s learning %v proxy %v", v.HardwareAddr, v.Learning, v.Proxy)
	}
	o.cfg.OverlayDevices = network.OverlayDevices{}
	v = &netlink.Vxlan{Learning: true, Proxy: true}
	if err := o.applyVXLANSettings(v); err != nil {
		t.Fatal(err)
	}
	if v.HardwareAddr != nil || v.Learning || v.Proxy {
		t.Errorf("unset settings: mac %s learning %v proxy %v, want none, off, off", v.HardwareAddr, v.Learning, v.Proxy)
	}
	o.cfg.OverlayDevices.VXLANMAC = "02:54"
	if err := o.applyVXLANSettings(&netlink.Vxlan{}); err == nil {
		t.Error("bad VXLAN MAC accepted")
	}
}
