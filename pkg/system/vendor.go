//go:build linux

// vendor.go provides hardware vendor detection via DMI sysfs.
package system

import (
	"log/slog"
	"os"
	"strings"
)

// Vendor constants for known hardware vendors detected via DMI sys_vendor.
const (
	VendorHPE     = "hpe"
	VendorLenovo  = "lenovo"
	VendorDell    = "dell"
	VendorGeneric = "generic"
)

// sysVendorPath is the sysfs path for DMI vendor detection.
// Overridden in tests.
var sysVendorPath = "/sys/class/dmi/id/sys_vendor"

// DetectVendor reads DMI sys_vendor to identify the hardware vendor.
func DetectVendor() string {
	data, err := os.ReadFile(sysVendorPath) //nolint:gosec // path is package-level constant
	if err != nil {
		slog.Debug("could not read sys_vendor", "error", err)
		return VendorGeneric
	}
	return ParseVendor(strings.TrimSpace(string(data)))
}

// ParseVendor normalizes a raw DMI vendor string to a canonical vendor constant.
func ParseVendor(raw string) string {
	vendor := strings.ToLower(strings.TrimSpace(raw))

	switch {
	case strings.Contains(vendor, "hpe") || strings.Contains(vendor, "hewlett"):
		return VendorHPE
	case strings.Contains(vendor, "lenovo"):
		return VendorLenovo
	case strings.Contains(vendor, "dell"):
		return VendorDell
	default:
		slog.Debug("unknown vendor, using generic", "vendor", vendor)
		return VendorGeneric
	}
}

// VendorKernelModules returns vendor-specific kernel modules to load.
func VendorKernelModules(vendor string) []string {
	switch vendor {
	case VendorHPE:
		return []string{"hpilo", "hpwdt", "ilo_hwmon"}
	case VendorLenovo:
		return []string{"ibm_rtl"}
	case VendorDell:
		return []string{"dcdbas", "dell_rbu"}
	default:
		return nil
	}
}
