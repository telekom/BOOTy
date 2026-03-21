package broadcom

import (
	"context"
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
		{" ", "unknown"},
	}

	for _, tc := range tests {
		got := DriverType(&nic.Identifier{Driver: tc.driver})
		if got != tc.expected {
			t.Errorf("DriverType(%q) = %q, want %q", tc.driver, got, tc.expected)
		}
	}
}

func TestDriverType_Nil(t *testing.T) {
	got := DriverType(nil)
	if got != "unknown" {
		t.Errorf("DriverType(nil) = %q, want unknown", got)
	}
}

// MockCommander returns pre-canned outputs for testing.
type MockCommander struct {
	ethtoolOut   string
	privFlagsOut string
	err          error
}

func (m *MockCommander) CombinedOutput(_ context.Context, cmd string, args ...string) ([]byte, error) {
	if m.err != nil {
		return nil, m.err
	}
	if len(args) > 0 && args[0] == "--show-priv-flags" {
		return []byte(m.privFlagsOut), nil
	}
	return []byte(m.ethtoolOut), nil
}

func TestCapture_Success(t *testing.T) {
	ethtoolOutput := `driver: bnxt_en
version: 204.0.19.0/800c
firmware-version: bnxt_en_driver_216.0.57.0/bnxt_phy_driver_216_53_57
`
	privFlagsOutput := `Private flags for eth0:
disable-rrc         : off
gre-tunnel-lro      : on
hds                 : off
`

	mock := &MockCommander{
		ethtoolOut:   ethtoolOutput,
		privFlagsOut: privFlagsOutput,
	}
	m := NewWithCommander(nil, mock)

	nicID := &nic.Identifier{
		PCIAddress: "0000:01:00.0",
		Interface:  "eth0",
		VendorID:   "0x14e4",
		DeviceID:   "0x1604",
		Driver:     "bnxt_en",
	}

	state, err := m.Capture(context.Background(), nicID)
	if err != nil {
		t.Fatalf("Capture failed: %v", err)
	}
	if state == nil {
		t.Fatal("state is nil")
	}

	// Check that firmware version was captured
	if state.FWVersion != "bnxt_en_driver_216.0.57.0/bnxt_phy_driver_216_53_57" {
		t.Errorf("firmware version = %q", state.FWVersion)
	}

	// Check that both read-only and modifiable parameters were captured
	if param, ok := state.Parameters["driver"]; !ok || param.Current != "bnxt_en" {
		t.Errorf("driver parameter missing or wrong")
	}
	if param, ok := state.Parameters["disable-rrc"]; !ok || param.Current != "off" || param.ReadOnly {
		t.Errorf("disable-rrc flag not captured or marked as read-only")
	}
	if param, ok := state.Parameters["gre-tunnel-lro"]; !ok || param.Current != "on" || param.ReadOnly {
		t.Errorf("gre-tunnel-lro flag not captured or marked as read-only")
	}

	// Verify NIC identity is preserved
	if state.NIC.PCIAddress != "0000:01:00.0" {
		t.Errorf("NIC identity lost")
	}
}

func TestCapture_NoInterface(t *testing.T) {
	m := New(nil)
	nicID := &nic.Identifier{
		PCIAddress: "0000:01:00.0",
		VendorID:   "0x14e4",
	}

	_, err := m.Capture(context.Background(), nicID)
	if err == nil {
		t.Fatal("Capture should fail without interface name")
	}
}

func TestApply_Success(t *testing.T) {
	setCalled := false
	mock := &MockCommander{
		ethtoolOut:   "",
		privFlagsOut: "Private flags for eth0:\ndisable-rrc: off\n",
	}

	m := NewWithCommander(nil, mock)

	nicID := &nic.Identifier{
		PCIAddress: "0000:01:00.0",
		Interface:  "eth0",
		VendorID:   "0x14e4",
	}

	changes := []nic.FlagChange{
		{Name: "disable-rrc", Value: "on"},
	}

	// We're testing that Apply succeeds; the mock will be called
	_ = setCalled
	err := m.Apply(context.Background(), nicID, changes)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
}
