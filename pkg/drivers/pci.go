package drivers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PCIDevice represents a PCI device found in sysfs.
type PCIDevice struct {
	Address  string `json:"address"`
	VendorID string `json:"vendorId"`
	DeviceID string `json:"deviceId"`
	Class    string `json:"class"`
	Driver   string `json:"driver,omitempty"`
	Module   string `json:"module,omitempty"`
}

// pciModuleMap maps PCI vendor:device IDs to kernel module names.
var pciModuleMap = map[string]string{
	// Intel NICs
	"8086:15b8": "e1000e",
	"8086:1533": "igb",
	"8086:1572": "i40e",
	"8086:158b": "i40e",
	"8086:1592": "ice",
	"8086:1889": "iavf",
	"8086:10fb": "ixgbe",
	"8086:15e3": "igc",
	// Broadcom NICs
	"14e4:165f": "tg3",
	"14e4:1657": "tg3",
	"14e4:16d6": "tg3",
	"14e4:16d7": "tg3",
	"14e4:16d8": "tg3",
	"14e4:1750": "bnxt_en",
	"14e4:1751": "bnxt_en",
	// Mellanox NICs
	"15b3:1013": "mlx5_core",
	"15b3:1015": "mlx5_core",
	"15b3:1017": "mlx5_core",
	"15b3:101b": "mlx5_core",
	"15b3:a2d6": "mlx5_core",
	// NVMe
	"8086:f1a5": "nvme",
	"144d:a808": "nvme",
	"1987:5012": "nvme",
	// RAID controllers
	"1000:005d": "megaraid_sas",
	"1000:005f": "megaraid_sas",
	"1000:0073": "megaraid_sas",
}

// ScanPCIDevices reads PCI devices from sysfs.
func ScanPCIDevices() ([]PCIDevice, error) {
	return scanPCIDevicesFrom("/sys/bus/pci/devices")
}

func scanPCIDevicesFrom(basePath string) ([]PCIDevice, error) {
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return nil, fmt.Errorf("read PCI devices dir: %w", err)
	}

	devices := make([]PCIDevice, 0, len(entries))
	for _, entry := range entries {
		devPath := filepath.Join(basePath, entry.Name())
		dev := PCIDevice{
			Address:  entry.Name(),
			VendorID: readSysfsFile(filepath.Join(devPath, "vendor")),
			DeviceID: readSysfsFile(filepath.Join(devPath, "device")),
			Class:    readSysfsFile(filepath.Join(devPath, "class")),
		}

		// Check if driver is bound
		driverLink, err := os.Readlink(filepath.Join(devPath, "driver"))
		if err == nil {
			dev.Driver = filepath.Base(driverLink)
		}

		// Map PCI ID to module
		pciID := formatPCIID(dev.VendorID, dev.DeviceID)
		if mod, ok := pciModuleMap[pciID]; ok {
			dev.Module = mod
		}

		devices = append(devices, dev)
	}
	return devices, nil
}

// RequiredModules returns the kernel modules needed for detected PCI hardware.
func RequiredModules() ([]string, error) {
	devices, err := ScanPCIDevices()
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var modules []string
	for _, dev := range devices {
		if dev.Module != "" && !seen[dev.Module] {
			seen[dev.Module] = true
			modules = append(modules, dev.Module)
		}
	}
	return modules, nil
}

func readSysfsFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// formatPCIID converts sysfs vendor/device values (0x8086, 0x15b8) to "8086:15b8".
func formatPCIID(vendor, device string) string {
	v := strings.TrimPrefix(vendor, "0x")
	d := strings.TrimPrefix(device, "0x")
	return v + ":" + d
}

// LookupModule returns the kernel module name for a PCI vendor:device ID.
func LookupModule(vendorID, deviceID string) string {
	pciID := formatPCIID(vendorID, deviceID)
	return pciModuleMap[pciID]
}
