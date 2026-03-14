package bios

import (
	"fmt"
	"os"
	"strings"
)

// DetectVendor reads the system vendor from DMI sysfs.
func DetectVendor() (Vendor, error) {
	return detectVendorFrom("/sys/class/dmi/id/sys_vendor")
}

func detectVendorFrom(path string) (Vendor, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return VendorUnknown, fmt.Errorf("read sys_vendor: %w", err)
	}
	v := strings.TrimSpace(string(data))
	return classifyVendor(v), nil
}

func classifyVendor(vendor string) Vendor {
	switch {
	case strings.Contains(vendor, "HPE") || strings.Contains(vendor, "Hewlett"):
		return VendorHPE
	case strings.Contains(vendor, "Lenovo"):
		return VendorLenovo
	case strings.Contains(vendor, "Supermicro"):
		return VendorSupermicro
	case strings.Contains(vendor, "Dell"):
		return VendorDell
	default:
		return VendorUnknown
	}
}
