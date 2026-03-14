package intel

import (
	"testing"

	"github.com/telekom/BOOTy/pkg/firmware/nic"
)

func TestSupported(t *testing.T) {
	m := New(nil)
	if !m.Supported(&nic.Identifier{VendorID: "0x8086"}) {
		t.Error("should support Intel NIC")
	}
	if m.Supported(&nic.Identifier{VendorID: "0x15b3"}) {
		t.Error("should not support Mellanox NIC")
	}
}

func TestVendor(t *testing.T) {
	m := New(nil)
	if m.Vendor() != nic.VendorIntel {
		t.Errorf("vendor = %q", m.Vendor())
	}
}

func TestCriticalParams(t *testing.T) {
	params := CriticalParams()
	if len(params) == 0 {
		t.Error("CriticalParams should not be empty")
	}
	found := false
	for _, p := range params {
		if p == "disable-fw-lldp" {
			found = true
		}
	}
	if !found {
		t.Error("CriticalParams should contain disable-fw-lldp")
	}
}
