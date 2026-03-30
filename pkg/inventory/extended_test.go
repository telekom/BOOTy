//go:build linux

package inventory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanGPUsFrom(t *testing.T) {
	root := t.TempDir()

	// Create a GPU device.
	gpuDir := "0000:3b:00.0"
	writeFile(t, root, filepath.Join(gpuDir, "class"), "0x030200\n")
	writeFile(t, root, filepath.Join(gpuDir, "vendor"), "0x10de\n")
	writeFile(t, root, filepath.Join(gpuDir, "device"), "0x20b0\n")
	writeFile(t, root, filepath.Join(gpuDir, "numa_node"), "0\n")

	// Create a non-GPU device.
	otherDir := "0000:00:00.0"
	writeFile(t, root, filepath.Join(otherDir, "class"), "0x060000\n")

	gpus := scanGPUsFrom(root)
	if len(gpus) != 1 {
		t.Fatalf("gpus = %d, want 1", len(gpus))
	}
	if gpus[0].Name != "NVIDIA A100-SXM4-40GB" {
		t.Errorf("name = %q, want %q", gpus[0].Name, "NVIDIA A100-SXM4-40GB")
	}
	if gpus[0].NUMANode != 0 {
		t.Errorf("numa = %d, want 0", gpus[0].NUMANode)
	}
}

func TestScanGPUsFrom_Empty(t *testing.T) {
	gpus := scanGPUsFrom("/nonexistent")
	if len(gpus) != 0 {
		t.Errorf("gpus = %d, want 0", len(gpus))
	}
}

func TestScanUSBFrom(t *testing.T) {
	root := t.TempDir()

	devDir := "1-1"
	writeFile(t, root, filepath.Join(devDir, "idVendor"), "0781\n")
	writeFile(t, root, filepath.Join(devDir, "idProduct"), "5583\n")
	writeFile(t, root, filepath.Join(devDir, "product"), "Ultra Fit\n")
	writeFile(t, root, filepath.Join(devDir, "bDeviceClass"), "08\n")
	writeFile(t, root, filepath.Join(devDir, "busnum"), "1\n")
	writeFile(t, root, filepath.Join(devDir, "devnum"), "3\n")

	// Device without vendor/product IDs should be skipped.
	skipDir := filepath.Join(root, "1-0:1.0")
	if err := os.MkdirAll(skipDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	devices := scanUSBFrom(root)
	if len(devices) != 1 {
		t.Fatalf("devices = %d, want 1", len(devices))
	}
	if devices[0].VendorID != "0781" {
		t.Errorf("vendorId = %q, want %q", devices[0].VendorID, "0781")
	}
	if devices[0].Class != "Mass Storage" {
		t.Errorf("class = %q, want %q", devices[0].Class, "Mass Storage")
	}
	if devices[0].Bus != 1 {
		t.Errorf("bus = %d, want 1", devices[0].Bus)
	}
}

func TestClassifyUSBDevice(t *testing.T) {
	tests := []struct {
		code string
		want string
	}{
		{"08", "Mass Storage"},
		{"09", "Hub"},
		{"03", "HID"},
		{"0x08", "Mass Storage"},
		{" 0x08 ", "Mass Storage"},
		{"0E", "Video"},
		{"zz", "Unknown (zz)"},
		{"", "Unknown"},
	}
	for _, tc := range tests {
		got := ClassifyUSBDevice(tc.code)
		if got != tc.want {
			t.Errorf("ClassifyUSBDevice(%q) = %q, want %q", tc.code, got, tc.want)
		}
	}
}

func TestCollectThermalFrom(t *testing.T) {
	thermalDir := t.TempDir()
	hwmonDir := t.TempDir()

	// Create thermal zone.
	writeFile(t, thermalDir, filepath.Join("thermal_zone0", "type"), "x86_pkg_temp\n")
	writeFile(t, thermalDir, filepath.Join("thermal_zone0", "temp"), "45000\n")

	// Create hwmon with fan.
	writeFile(t, hwmonDir, filepath.Join("hwmon0", "fan1_input"), "2500\n")

	info := collectThermalFrom(thermalDir, hwmonDir)
	if len(info.CPUTemps) != 1 {
		t.Fatalf("cpuTemps = %d, want 1", len(info.CPUTemps))
	}
	if info.CPUTemps[0].TempC != 45.0 {
		t.Errorf("tempC = %f, want 45.0", info.CPUTemps[0].TempC)
	}
	if len(info.Fans) != 1 {
		t.Fatalf("fans = %d, want 1", len(info.Fans))
	}
	if info.Fans[0].RPM != 2500 {
		t.Errorf("rpm = %d, want 2500", info.Fans[0].RPM)
	}
}

func TestCollectThermalFrom_FanZeroRPM(t *testing.T) {
	hwmonDir := t.TempDir()
	writeFile(t, hwmonDir, filepath.Join("hwmon0", "fan1_input"), "0\n")

	info := collectThermalFrom(t.TempDir(), hwmonDir)
	if len(info.Fans) != 1 {
		t.Fatalf("fans = %d, want 1", len(info.Fans))
	}
	if info.Fans[0].Status != "failed" {
		t.Errorf("status = %q, want %q", info.Fans[0].Status, "failed")
	}
}

func TestCollectThermalFrom_Empty(t *testing.T) {
	info := collectThermalFrom("/nonexistent", "/nonexistent")
	if len(info.CPUTemps) != 0 {
		t.Errorf("cpuTemps = %d, want 0", len(info.CPUTemps))
	}
	if len(info.Fans) != 0 {
		t.Errorf("fans = %d, want 0", len(info.Fans))
	}
}

func TestReadThermalZone_Empty(t *testing.T) {
	reading := readThermalZone("/nonexistent")
	if reading.Name != "" {
		t.Error("expected empty reading")
	}
}

func TestResolveGPUName(t *testing.T) {
	name := resolveGPUName("0x10de", "0x2330")
	if name != "NVIDIA H100-SXM5-80GB" {
		t.Errorf("name = %q, want %q", name, "NVIDIA H100-SXM5-80GB")
	}

	unknown := resolveGPUName("0x9999", "0x1234")
	if unknown != "GPU 0x1234" {
		t.Errorf("unknown = %q, want %q", unknown, "GPU 0x1234")
	}

	emptyDev := resolveGPUName("0x9999", "")
	if emptyDev != "GPU 0x9999" {
		t.Errorf("emptyDev = %q, want %q", emptyDev, "GPU 0x9999")
	}

	bothEmpty := resolveGPUName("", "")
	if bothEmpty != "GPU unknown" {
		t.Errorf("bothEmpty = %q, want %q", bothEmpty, "GPU unknown")
	}
}

func TestExtendedInventoryTypes(t *testing.T) {
	ext := &ExtendedInventory{
		GPUs: []GPUInfo{{Name: "test"}},
		Thermal: &ThermalInfo{
			Fans: []FanInfo{{Name: "fan1", RPM: 1000, Status: "ok"}},
		},
		PowerSupplies: []PSUInfo{{Name: "PSU1", Status: "ok", Watts: 800}},
	}
	if len(ext.GPUs) != 1 {
		t.Errorf("GPU count = %d, want 1", len(ext.GPUs))
	}
	if ext.Thermal.Fans[0].Status != "ok" {
		t.Errorf("fan status = %q, want %q", ext.Thermal.Fans[0].Status, "ok")
	}
	if ext.PowerSupplies[0].Watts != 800 {
		t.Errorf("PSU watts = %d, want 800", ext.PowerSupplies[0].Watts)
	}
}
