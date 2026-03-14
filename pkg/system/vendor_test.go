package system

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseVendor(t *testing.T) {
	tests := []struct {
		raw  string
		want Vendor
	}{
		{raw: "HPE", want: VendorHPE},
		{raw: "Hewlett Packard Enterprise", want: VendorHPE},
		{raw: "Lenovo", want: VendorLenovo},
		{raw: "LENOVO", want: VendorLenovo},
		{raw: "Dell Inc.", want: VendorDell},
		{raw: "Supermicro", want: VendorGeneric},
		{raw: "", want: VendorGeneric},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if got := ParseVendor(tt.raw); got != tt.want {
				t.Errorf("ParseVendor(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestDetectVendorFromPath(t *testing.T) {
	t.Run("hpe", func(t *testing.T) {
		path := writeTempVendor(t, "HPE")
		if got := DetectVendorFromPath(path); got != VendorHPE {
			t.Errorf("DetectVendorFromPath() = %q, want %q", got, VendorHPE)
		}
	})
	t.Run("nonexistent", func(t *testing.T) {
		if got := DetectVendorFromPath("/nonexistent/path"); got != VendorGeneric {
			t.Errorf("DetectVendorFromPath() = %q, want %q", got, VendorGeneric)
		}
	})
}

func TestKernelModules(t *testing.T) {
	tests := []struct {
		vendor Vendor
		want   int
	}{
		{vendor: VendorHPE, want: 3},
		{vendor: VendorLenovo, want: 3},
		{vendor: VendorDell, want: 3},
		{vendor: VendorGeneric, want: 0},
	}
	for _, tt := range tests {
		t.Run(string(tt.vendor), func(t *testing.T) {
			mods := KernelModules(tt.vendor)
			if len(mods) != tt.want {
				t.Errorf("KernelModules(%q) = %d modules, want %d", tt.vendor, len(mods), tt.want)
			}
		})
	}
}

func writeTempVendor(t *testing.T, vendor string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sys_vendor")
	if err := os.WriteFile(path, []byte(vendor+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
