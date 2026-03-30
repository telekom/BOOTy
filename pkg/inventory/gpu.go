//go:build linux

package inventory

import (
	"os"
	"path/filepath"
	"strconv"
)

// gpuPCIClasses identifies GPU/accelerator PCI class codes.
var gpuPCIClasses = map[string]bool{
	"0x030000": true, // VGA compatible controller
	"0x030100": true, // XGA compatible controller
	"0x030200": true, // 3D controller (compute GPUs like A100)
	"0x030800": true, // Display controller (other)
	"0x120000": true, // Processing accelerator
	"0x120100": true, // AI inference accelerator
}

// ScanGPUs enumerates GPU/accelerator devices from sysfs.
func ScanGPUs() []GPUInfo {
	return scanGPUsFrom(SysPCIPath)
}

func scanGPUsFrom(basePath string) []GPUInfo {
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return nil
	}

	var gpus []GPUInfo
	for _, entry := range entries {
		devPath := filepath.Join(basePath, entry.Name())
		classData := readSysfs(devPath, "class")
		if !gpuPCIClasses[classData] {
			continue
		}

		gpu := GPUInfo{
			PCIAddr: entry.Name(),
			Vendor:  readSysfs(devPath, "vendor"),
		}

		deviceID := readSysfs(devPath, "device")
		gpu.Name = resolveGPUName(gpu.Vendor, deviceID)
		gpu.Driver = readDriverName(devPath)

		numaStr := readSysfs(devPath, "numa_node")
		if n, err := strconv.Atoi(numaStr); err == nil {
			gpu.NUMANode = n
		}

		gpu.SRIOVCapable = fileExists(filepath.Join(devPath, "sriov_totalvfs"))

		gpus = append(gpus, gpu)
	}
	return gpus
}

// knownGPUNames maps "vendor:device" PCI IDs to human-readable GPU names.
var knownGPUNames = map[string]string{
	"0x10de:0x20b0": "NVIDIA A100-SXM4-40GB",
	"0x10de:0x20b2": "NVIDIA A100-SXM4-80GB",
	"0x10de:0x2330": "NVIDIA H100-SXM5-80GB",
	"0x10de:0x26b5": "NVIDIA L40S",
	"0x1002:0x740c": "AMD Instinct MI250X",
	"0x1002:0x740f": "AMD Instinct MI300X",
	"0x8086:0x0bda": "Intel Data Center GPU Max 1550",
}

func resolveGPUName(vendorID, deviceID string) string {
	key := vendorID + ":" + deviceID
	if name, ok := knownGPUNames[key]; ok {
		return name
	}
	if deviceID != "" {
		return "GPU " + deviceID
	}
	if vendorID != "" {
		return "GPU " + vendorID
	}
	return "GPU unknown"
}

func readDriverName(devPath string) string {
	link, err := os.Readlink(filepath.Join(devPath, "driver"))
	if err != nil {
		return ""
	}
	return filepath.Base(link)
}
