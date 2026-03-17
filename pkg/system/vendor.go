package system

import (
	"os"
	"strings"
)

// Vendor represents a known hardware vendor.
type Vendor string

// Supported hardware vendors.
const (
	VendorHPE        Vendor = "HPE"
	VendorLenovo     Vendor = "Lenovo"
	VendorDell       Vendor = "Dell Inc."
	VendorSupermicro Vendor = "Supermicro"
	VendorGeneric    Vendor = "Generic"
)

const dmiSysVendorPath = "/sys/class/dmi/id/sys_vendor"

// DetectVendor reads the system vendor from DMI/SMBIOS sysfs data.
func DetectVendor() Vendor {
	return detectVendorFromPath(dmiSysVendorPath)
}

// detectVendorFromPath reads vendor info from the given path.
func detectVendorFromPath(path string) Vendor {
	data, err := os.ReadFile(path) //nolint:gosec // intentional sysfs read
	if err != nil {
		return VendorGeneric
	}
	return ParseVendor(strings.TrimSpace(string(data)))
}

// ParseVendor maps a raw DMI sys_vendor string to a known Vendor.
func ParseVendor(raw string) Vendor {
	normalized := strings.Join(strings.Fields(strings.ToLower(raw)), " ")
	switch {
	case normalized == "hp", normalized == "hpe",
		normalized == "hewlett packard enterprise",
		strings.Contains(normalized, "hewlett packard enterprise"):
		return VendorHPE
	case strings.Contains(normalized, "lenovo"):
		return VendorLenovo
	case strings.Contains(normalized, "dell"):
		return VendorDell
	case strings.Contains(normalized, "supermicro"):
		return VendorSupermicro
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
	case VendorSupermicro:
		return []string{"ipmi_si", "ipmi_devintf"}
	default:
		return []string{}
	}
}
