package system

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseVendor(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want Vendor
	}{
		{name: "hpe-short", raw: "HPE", want: VendorHPE},
		{name: "hpe-full", raw: "Hewlett Packard Enterprise", want: VendorHPE},
		{name: "lenovo", raw: "Lenovo", want: VendorLenovo},
		{name: "lenovo-upper", raw: "LENOVO", want: VendorLenovo},
		{name: "dell", raw: "Dell Inc.", want: VendorDell},
		{name: "supermicro-generic", raw: "Supermicro", want: VendorGeneric},
		{name: "empty-generic", raw: "", want: VendorGeneric},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
		want   []string
	}{
		{vendor: VendorHPE, want: []string{"hpilo", "hpwdt", "ilo_hwmon"}},
		{vendor: VendorLenovo, want: []string{"ibm_rtl", "ipmi_si", "ipmi_devintf"}},
		{vendor: VendorDell, want: []string{"dell_rbu", "ipmi_si", "ipmi_devintf"}},
		{vendor: VendorGeneric, want: nil},
	}
	for _, tt := range tests {
		t.Run(string(tt.vendor), func(t *testing.T) {
			mods := KernelModules(tt.vendor)
			if !reflect.DeepEqual(mods, tt.want) {
				t.Errorf("KernelModules(%q) = %v, want %v", tt.vendor, mods, tt.want)
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
