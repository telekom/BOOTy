package inventory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanGPUsFrom(t *testing.T) {
	root := t.TempDir()

	// Create a GPU device.
	gpuDir := filepath.Join(root, "0000:3b:00.0")
	os.MkdirAll(gpuDir, 0o755)
	os.WriteFile(filepath.Join(gpuDir, "class"), []byte("0x030200\n"), 0o644)
	os.WriteFile(filepath.Join(gpuDir, "vendor"), []byte("0x10de\n"), 0o644)
	os.WriteFile(filepath.Join(gpuDir, "device"), []byte("0x20b0\n"), 0o644)
	os.WriteFile(filepath.Join(gpuDir, "numa_node"), []byte("0\n"), 0o644)

	// Create a non-GPU device.
	otherDir := filepath.Join(root, "0000:00:00.0")
	os.MkdirAll(otherDir, 0o755)
	os.WriteFile(filepath.Join(otherDir, "class"), []byte("0x060000\n"), 0o644)

	gpus := scanGPUsFrom(root)
	if len(gpus) != 1 {
		t.Fatalf("gpus = %d, want 1", len(gpus))
	}
	if gpus[0].Name != "NVIDIA A100-SXM4-40GB" {
		t.Errorf("name = %q", gpus[0].Name)
	}
	if gpus[0].NUMANode != 0 {
		t.Errorf("numa = %d", gpus[0].NUMANode)
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

	devDir := filepath.Join(root, "1-1")
	os.MkdirAll(devDir, 0o755)
	os.WriteFile(filepath.Join(devDir, "idVendor"), []byte("0781\n"), 0o644)
	os.WriteFile(filepath.Join(devDir, "idProduct"), []byte("5583\n"), 0o644)
	os.WriteFile(filepath.Join(devDir, "product"), []byte("Ultra Fit\n"), 0o644)
	os.WriteFile(filepath.Join(devDir, "bDeviceClass"), []byte("08\n"), 0o644)
	os.WriteFile(filepath.Join(devDir, "busnum"), []byte("1\n"), 0o644)
	os.WriteFile(filepath.Join(devDir, "devnum"), []byte("3\n"), 0o644)

	// Device without vendor/product IDs should be skipped.
	skipDir := filepath.Join(root, "1-0:1.0")
	os.MkdirAll(skipDir, 0o755)

	devices := scanUSBFrom(root)
	if len(devices) != 1 {
		t.Fatalf("devices = %d, want 1", len(devices))
	}
	if devices[0].VendorID != "0781" {
		t.Errorf("vendorId = %q", devices[0].VendorID)
	}
	if devices[0].Bus != 1 {
		t.Errorf("bus = %d", devices[0].Bus)
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
		{"zz", "Unknown (zz)"},
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
	zoneDir := filepath.Join(thermalDir, "thermal_zone0")
	os.MkdirAll(zoneDir, 0o755)
	os.WriteFile(filepath.Join(zoneDir, "type"), []byte("x86_pkg_temp\n"), 0o644)
	os.WriteFile(filepath.Join(zoneDir, "temp"), []byte("45000\n"), 0o644)

	// Create hwmon with fan.
	hw0 := filepath.Join(hwmonDir, "hwmon0")
	os.MkdirAll(hw0, 0o755)
	os.WriteFile(filepath.Join(hw0, "fan1_input"), []byte("2500\n"), 0o644)

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

func TestReadThermalZone_Empty(t *testing.T) {
	reading := readThermalZone("/nonexistent")
	if reading.Name != "" {
		t.Error("expected empty reading")
	}
}

func TestResolveGPUName(t *testing.T) {
	name := resolveGPUName("0x10de", "0x2330")
	if name != "NVIDIA H100-SXM5-80GB" {
		t.Errorf("name = %q", name)
	}

	unknown := resolveGPUName("0x9999", "0x1234")
	if unknown != "GPU 0x1234" {
		t.Errorf("unknown = %q", unknown)
	}
}

func TestExtendedInventoryTypes(t *testing.T) {
	ext := &ExtendedInventory{
		GPUs: []GPUInfo{{Name: "test"}},
		Thermal: ThermalInfo{
			Fans: []FanInfo{{Name: "fan1", RPM: 1000, Status: "ok"}},
		},
		PowerSupplies: []PSUInfo{{Name: "PSU1", Status: "ok", Watts: 800}},
	}
	if len(ext.GPUs) != 1 {
		t.Error("wrong GPU count")
	}
	if ext.Thermal.Fans[0].Status != "ok" {
		t.Error("wrong fan status")
	}
	if ext.PowerSupplies[0].Watts != 800 {
		t.Error("wrong PSU watts")
	}
}
