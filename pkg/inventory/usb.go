//go:build linux

package inventory

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ScanUSBDevices enumerates USB devices from sysfs.
func ScanUSBDevices() []USBDeviceInfo {
	return scanUSBFrom(SysUSBPath)
}

func scanUSBFrom(basePath string) []USBDeviceInfo {
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return nil
	}

	var devices []USBDeviceInfo
	for _, entry := range entries {
		devPath := filepath.Join(basePath, entry.Name())

		vendorID := readSysfs(devPath, "idVendor")
		productID := readSysfs(devPath, "idProduct")
		if vendorID == "" || productID == "" {
			continue
		}

		dev := USBDeviceInfo{
			VendorID:  vendorID,
			ProductID: productID,
			Name:      readSysfs(devPath, "product"),
			Class:     ClassifyUSBDevice(readSysfs(devPath, "bDeviceClass")),
		}

		busStr := readSysfs(devPath, "busnum")
		if b, err := strconv.Atoi(busStr); err == nil {
			dev.Bus = b
		}
		devStr := readSysfs(devPath, "devnum")
		if d, err := strconv.Atoi(devStr); err == nil {
			dev.Device = d
		}

		devices = append(devices, dev)
	}
	return devices
}

// usbClassNames maps USB class codes to human-readable names.
var usbClassNames = map[string]string{
	"00": "Interface-defined",
	"01": "Audio",
	"02": "CDC Communications",
	"03": "HID",
	"06": "Imaging",
	"07": "Printer",
	"08": "Mass Storage",
	"09": "Hub",
	"0a": "CDC Data",
	"0e": "Video",
	"e0": "Wireless",
	"ef": "Miscellaneous",
	"ff": "Vendor-specific",
}

// ClassifyUSBDevice returns a human-readable class name.
func ClassifyUSBDevice(classCode string) string {
	classCode = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(classCode)), "0x")
	if classCode == "" {
		return "Unknown"
	}
	if name, ok := usbClassNames[classCode]; ok {
		return name
	}
	return "Unknown (" + classCode + ")"
}
