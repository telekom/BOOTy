//go:build linux

package system

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectVendor(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"HPE", "HPE\n", VendorHPE},
		{"Hewlett Packard Enterprise", "Hewlett Packard Enterprise\n", VendorHPE},
		{"Lenovo", "Lenovo\n", VendorLenovo},
		{"Dell Inc.", "Dell Inc.\n", VendorDell},
		{"QEMU", "QEMU\n", VendorGeneric},
		{"empty", "", VendorGeneric},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			path := filepath.Join(tmp, "sys_vendor")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			old := sysVendorPath
			sysVendorPath = path
			defer func() { sysVendorPath = old }()

			if got := DetectVendor(); got != tc.want {
				t.Errorf("DetectVendor() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDetectVendor_MissingFile(t *testing.T) {
	old := sysVendorPath
	sysVendorPath = "/nonexistent/sys_vendor"
	defer func() { sysVendorPath = old }()

	if got := DetectVendor(); got != VendorGeneric {
		t.Errorf("DetectVendor() = %q, want %q", got, VendorGeneric)
	}
}

func TestParseVendor(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"Dell Inc.", VendorDell},
		{"dell", VendorDell},
		{"DELL EMC", VendorDell},
		{"HPE", VendorHPE},
		{"Hewlett Packard Enterprise", VendorHPE},
		{"Lenovo", VendorLenovo},
		{"LENOVO", VendorLenovo},
		{"QEMU", VendorGeneric},
		{"  HPE  ", VendorHPE},
	}

	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			if got := ParseVendor(tc.raw); got != tc.want {
				t.Errorf("ParseVendor(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestVendorKernelModules(t *testing.T) {
	tests := []struct {
		vendor string
		want   int
	}{
		{VendorHPE, 3},
		{VendorLenovo, 1},
		{VendorDell, 2},
		{VendorGeneric, 0},
		{"unknown", 0},
	}

	for _, tc := range tests {
		t.Run(tc.vendor, func(t *testing.T) {
			got := VendorKernelModules(tc.vendor)
			if len(got) != tc.want {
				t.Errorf("VendorKernelModules(%q) returned %d modules, want %d", tc.vendor, len(got), tc.want)
			}
		})
	}
}
