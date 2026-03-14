package mellanox

import (
	"testing"

	"github.com/telekom/BOOTy/pkg/firmware/nic"
)

func TestSupported(t *testing.T) {
	m := New(nil)
	if !m.Supported(&nic.Identifier{VendorID: "0x15b3"}) {
		t.Error("should support Mellanox NIC")
	}
	if m.Supported(&nic.Identifier{VendorID: "0x8086"}) {
		t.Error("should not support Intel NIC")
	}
}

func TestVendor(t *testing.T) {
	m := New(nil)
	if m.Vendor() != nic.VendorMellanox {
		t.Errorf("vendor = %q", m.Vendor())
	}
}

func TestParseMstconfigLine(t *testing.T) {
	tests := []struct {
		line    string
		wantOK  bool
		name    string
		current string
	}{
		{"SRIOV_EN                    True(True)", true, "SRIOV_EN", "True"},
		{"NUM_OF_VFS                  16(0)", true, "NUM_OF_VFS", "16"},
		{"LINK_TYPE_P1                ETH(ETH)", true, "LINK_TYPE_P1", "ETH"},
		{"", false, "", ""},
		{"Device #1:", false, "", ""},
		{"Configurations:", false, "", ""},
		{"X", false, "", ""},
	}

	for _, tc := range tests {
		name, param, ok := parseMstconfigLine(tc.line)
		if ok != tc.wantOK {
			t.Errorf("parseMstconfigLine(%q) ok=%v, want %v", tc.line, ok, tc.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if name != tc.name {
			t.Errorf("name = %q, want %q", name, tc.name)
		}
		if param.Current != tc.current {
			t.Errorf("current = %q, want %q", param.Current, tc.current)
		}
	}
}

func TestCriticalParams(t *testing.T) {
	params := CriticalParams()
	if len(params) == 0 {
		t.Error("CriticalParams should not be empty")
	}
	found := false
	for _, p := range params {
		if p == "SRIOV_EN" {
			found = true
		}
	}
	if !found {
		t.Error("CriticalParams should contain SRIOV_EN")
	}
}
