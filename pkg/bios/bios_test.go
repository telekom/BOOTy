package bios

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompare_Match(t *testing.T) {
	baseline := &Baseline{
		Settings: map[string]string{
			"BootMode":   "Uefi",
			"SecureBoot": "Enabled",
		},
	}
	state := &State{
		Settings: map[string]Setting{
			"BootMode":   {CurrentValue: "Uefi"},
			"SecureBoot": {CurrentValue: "Enabled"},
		},
	}

	diff := Compare(baseline, state)
	if !diff.Matches {
		t.Error("expected match")
	}
	if len(diff.Changes) != 0 {
		t.Errorf("changes = %d", len(diff.Changes))
	}
}

func TestCompare_Mismatch(t *testing.T) {
	baseline := &Baseline{
		Settings: map[string]string{
			"BootMode":   "Uefi",
			"SecureBoot": "Enabled",
		},
	}
	state := &State{
		Settings: map[string]Setting{
			"BootMode":   {CurrentValue: "Legacy"},
			"SecureBoot": {CurrentValue: "Enabled"},
		},
	}

	diff := Compare(baseline, state)
	if diff.Matches {
		t.Error("expected mismatch")
	}
	if len(diff.Changes) != 1 {
		t.Fatalf("changes = %d, want 1", len(diff.Changes))
	}
	if diff.Changes[0].Name != "BootMode" {
		t.Errorf("changed = %q", diff.Changes[0].Name)
	}
}

func TestCompare_MissingSetting(t *testing.T) {
	baseline := &Baseline{
		Settings: map[string]string{"Missing": "value"},
	}
	state := &State{
		Settings: map[string]Setting{},
	}

	diff := Compare(baseline, state)
	if diff.Matches {
		t.Error("expected mismatch")
	}
	if diff.Changes[0].Actual != "" {
		t.Errorf("actual = %q, want empty", diff.Changes[0].Actual)
	}
}

func TestDetectVendorFrom(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected Vendor
	}{
		{"hpe", "HPE\n", VendorHPE},
		{"hewlett", "Hewlett Packard Enterprise\n", VendorHPE},
		{"dell", "Dell Inc.\n", VendorDell},
		{"lenovo", "Lenovo\n", VendorLenovo},
		{"supermicro", "Supermicro\n", VendorSupermicro},
		{"unknown", "ACME Corp\n", VendorUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "sys_vendor")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			got, err := detectVendorFrom(path)
			if err != nil {
				t.Fatalf("detectVendorFrom: %v", err)
			}
			if got != tc.expected {
				t.Errorf("vendor = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestDetectVendorFrom_Missing(t *testing.T) {
	_, err := detectVendorFrom("/nonexistent")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestVendorConstants(t *testing.T) {
	vendors := []struct {
		v    Vendor
		want string
	}{
		{VendorHPE, "HPE"},
		{VendorDell, "Dell Inc."},
		{VendorLenovo, "Lenovo"},
		{VendorSupermicro, "Supermicro"},
		{VendorUnknown, "Unknown"},
	}
	for _, tc := range vendors {
		if string(tc.v) != tc.want {
			t.Errorf("vendor %q != %q", tc.v, tc.want)
		}
	}
}
