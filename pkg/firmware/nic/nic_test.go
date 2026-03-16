package nic

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectVendorFrom(t *testing.T) {
	root := t.TempDir()

	tests := []struct {
		name     string
		pciAddr  string
		vendorID string
		expected Vendor
	}{
		{"mellanox", "0000:03:00.0", "0x15b3", VendorMellanox},
		{"intel", "0000:04:00.0", "0x8086", VendorIntel},
		{"broadcom", "0000:05:00.0", "0x14e4", VendorBroadcom},
		{"unknown", "0000:06:00.0", "0x1234", VendorUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			devDir := filepath.Join(root, tc.pciAddr)
			if err := os.MkdirAll(devDir, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(devDir, "vendor"), []byte(tc.vendorID+"\n"), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}

			got := detectVendorFrom(root, tc.pciAddr)
			if got != tc.expected {
				t.Errorf("detectVendorFrom = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestDetectVendorFrom_Missing(t *testing.T) {
	got := detectVendorFrom("/nonexistent", "0000:00:00.0")
	if got != VendorUnknown {
		t.Errorf("missing device = %q, want %q", got, VendorUnknown)
	}
}

func TestCompare_Match(t *testing.T) {
	baseline := &Baseline{
		Parameters: map[string]string{
			"SRIOV_EN": "True",
			"NUM_VFS":  "16",
		},
	}
	state := &FirmwareState{
		Parameters: map[string]Parameter{
			"SRIOV_EN": {Current: "True"},
			"NUM_VFS":  {Current: "16"},
		},
	}

	diff := Compare(baseline, state)
	if !diff.Match {
		t.Error("expected match")
	}
	if len(diff.Changes) != 0 {
		t.Errorf("changes = %d, want 0", len(diff.Changes))
	}
}

func TestCompare_Mismatch(t *testing.T) {
	baseline := &Baseline{
		Parameters: map[string]string{
			"SRIOV_EN": "True",
			"NUM_VFS":  "16",
		},
	}
	state := &FirmwareState{
		Parameters: map[string]Parameter{
			"SRIOV_EN": {Current: "False"},
			"NUM_VFS":  {Current: "16"},
		},
	}

	diff := Compare(baseline, state)
	if diff.Match {
		t.Error("expected mismatch")
	}
	if len(diff.Changes) != 1 {
		t.Fatalf("changes = %d, want 1", len(diff.Changes))
	}
	if diff.Changes[0].Name != "SRIOV_EN" {
		t.Errorf("changed param = %q", diff.Changes[0].Name)
	}
}

func TestCompare_MissingParam(t *testing.T) {
	baseline := &Baseline{
		Parameters: map[string]string{
			"SRIOV_EN": "True",
		},
	}
	state := &FirmwareState{
		Parameters: map[string]Parameter{},
	}

	diff := Compare(baseline, state)
	if diff.Match {
		t.Error("expected mismatch")
	}
	if len(diff.Changes) == 0 {
		t.Fatal("expected at least one change")
	}
	if diff.Changes[0].Actual != "(missing)" {
		t.Errorf("actual = %q, want (missing)", diff.Changes[0].Actual)
	}
}

func TestRegistry_ForNIC(t *testing.T) {
	mockMgr := &mockManager{vendor: VendorMellanox}
	reg := NewRegistry(mockMgr)

	nic := &Identifier{
		PCIAddress: "0000:03:00.0",
		VendorID:   "0x15b3",
	}

	mgr, err := reg.ForNIC(nic)
	if err != nil {
		t.Fatalf("ForNIC: %v", err)
	}
	if mgr.Vendor() != VendorMellanox {
		t.Errorf("vendor = %q", mgr.Vendor())
	}
}

func TestRegistry_ForNIC_NotFound(t *testing.T) {
	reg := NewRegistry()
	nic := &Identifier{PCIAddress: "0000:00:00.0", VendorID: "0x9999"}

	_, err := reg.ForNIC(nic)
	if err == nil {
		t.Error("expected error for unknown NIC")
	}
}

func TestRegistry_ForNIC_Nil(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.ForNIC(nil); err == nil {
		t.Fatal("expected error for nil NIC")
	}
}

func TestVendorConstants(t *testing.T) {
	if string(VendorMellanox) != "mellanox" {
		t.Error("VendorMellanox wrong")
	}
	if string(VendorIntel) != "intel" {
		t.Error("VendorIntel wrong")
	}
	if string(VendorBroadcom) != "broadcom" {
		t.Error("VendorBroadcom wrong")
	}
}

type mockManager struct {
	vendor Vendor
}

func (m *mockManager) Vendor() Vendor { return m.vendor }
func (m *mockManager) Supported(nic *Identifier) bool {
	return pciVendorMap[nic.VendorID] == m.vendor
}
func (m *mockManager) Capture(_ context.Context, _ *Identifier) (*FirmwareState, error) {
	return &FirmwareState{}, nil
}
func (m *mockManager) Apply(_ context.Context, _ *Identifier, _ []FlagChange) error {
	return nil
}
