// vendor.go provides hardware vendor detection via DMI sysfs.
//go:build linux

package system

import (
	"log/slog"
	"os"
	"strings"
)

// Vendor constants for known BMC implementations.
const (
	VendorHPE     = "hpe"
	VendorLenovo  = "lenovo"
	VendorDell    = "dell"
	VendorGeneric = "generic"
)

// DetectVendor reads DMI sys_vendor to identify the hardware vendor.
func DetectVendor() string {
	data, err := os.ReadFile("/sys/class/dmi/id/sys_vendor")
	if err != nil {
		slog.Debug("could not read sys_vendor", "error", err)
		return VendorGeneric
	}
	vendor := strings.TrimSpace(strings.ToLower(string(data)))

	switch {
	case strings.Contains(vendor, "hpe") || strings.Contains(vendor, "hewlett"):
		return VendorHPE
	case strings.Contains(vendor, "lenovo"):
		return VendorLenovo
	case strings.Contains(vendor, "dell"):
		return VendorDell
	default:
		slog.Info("Unknown vendor, using generic", "vendor", vendor)
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
