package system

import (
	"os"
	"strings"
)

// Vendor represents a known hardware vendor.
type Vendor string

// Supported hardware vendors.
const (
	VendorHPE     Vendor = "hpe"
	VendorLenovo  Vendor = "lenovo"
	VendorDell    Vendor = "dell"
	VendorGeneric Vendor = "generic"
)

const dmiSysVendorPath = "/sys/class/dmi/id/sys_vendor"

// DetectVendor reads the system vendor from DMI/SMBIOS sysfs data.
func DetectVendor() Vendor {
	return DetectVendorFromPath(dmiSysVendorPath)
}

// DetectVendorFromPath reads vendor info from the given path.
// Exported for testing with alternative sysfs roots.
func DetectVendorFromPath(path string) Vendor {
	data, err := os.ReadFile(path) //nolint:gosec // intentional sysfs read
	if err != nil {
		return VendorGeneric
	}
	return ParseVendor(strings.TrimSpace(string(data)))
}

// ParseVendor maps a raw DMI sys_vendor string to a known Vendor.
func ParseVendor(raw string) Vendor {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "hpe"),
		strings.Contains(lower, "hewlett"):
		return VendorHPE
	case strings.Contains(lower, "lenovo"):
		return VendorLenovo
	case strings.Contains(lower, "dell"):
		return VendorDell
	default:
		return VendorGeneric
	}
}

// KernelModules returns vendor-specific kernel modules to load.
func KernelModules(v Vendor) []string {
	switch v {
	case VendorHPE:
		return []string{"hpilo", "hpwdt", "ilo_hwmon"}
	case VendorLenovo:
		return []string{"ibm_rtl", "ipmi_si", "ipmi_devintf"}
	case VendorDell:
		return []string{"dell_rbu", "ipmi_si", "ipmi_devintf"}
	default:
		return nil
	}
}
