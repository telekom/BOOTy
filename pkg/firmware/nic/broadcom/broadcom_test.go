package broadcom

import (
	"testing"

	"github.com/telekom/BOOTy/pkg/firmware/nic"
)

func TestSupported(t *testing.T) {
	m := New(nil)
	if !m.Supported(&nic.Identifier{VendorID: "0x14e4"}) {
		t.Error("should support Broadcom NIC")
	}
	if m.Supported(&nic.Identifier{VendorID: "0x8086"}) {
		t.Error("should not support Intel NIC")
	}
}

func TestVendor(t *testing.T) {
	m := New(nil)
	if m.Vendor() != nic.VendorBroadcom {
		t.Errorf("vendor = %q", m.Vendor())
	}
}

func TestDriverType(t *testing.T) {
	tests := []struct {
		driver   string
		expected string
	}{
		{"tg3", "tg3"},
		{"bnxt_en", "bnxt_en"},
		{"other", "unknown"},
		{"", "unknown"},
	}

	for _, tc := range tests {
		got := DriverType(&nic.Identifier{Driver: tc.driver})
		if got != tc.expected {
			t.Errorf("DriverType(%q) = %q, want %q", tc.driver, got, tc.expected)
		}
	}
}
