//go:build linux

package drivers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatPCIID(t *testing.T) {
	tests := []struct {
		vendor, device, want string
	}{
		{"0x8086", "0x15b8", "8086:15b8"},
		{"8086", "15b8", "8086:15b8"},
		{"0x15b3", "0x1013", "15b3:1013"},
		{"0X8086", "0X15B8", "8086:15b8"},
	}
	for _, tc := range tests {
		got := formatPCIID(tc.vendor, tc.device)
		if got != tc.want {
			t.Errorf("formatPCIID(%q, %q) = %q, want %q", tc.vendor, tc.device, got, tc.want)
		}
	}
}

func TestLookupModule(t *testing.T) {
	tests := []struct {
		vendor, device, want string
	}{
		{"0x8086", "0x15b8", "e1000e"},
		{"0x15b3", "0x1013", "mlx5_core"},
		{"0x14e4", "0x1750", "bnxt_en"},
		{"0x0000", "0x0000", ""},
	}
	for _, tc := range tests {
		got := LookupModule(tc.vendor, tc.device)
		if got != tc.want {
			t.Errorf("LookupModule(%q, %q) = %q, want %q", tc.vendor, tc.device, got, tc.want)
		}
	}
}

func TestScanPCIDevicesFrom(t *testing.T) {
	root := t.TempDir()

	// Create mock PCI device
	devDir := filepath.Join(root, "0000:00:1f.0")
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for name, data := range map[string]string{
		"vendor": "0x8086\n", "device": "0x15b8\n", "class": "0x020000\n",
	} {
		if err := os.WriteFile(filepath.Join(devDir, name), []byte(data), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	devices, err := scanPCIDevicesFrom(root)
	if err != nil {
		t.Fatalf("scanPCIDevicesFrom: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("devices = %d, want 1", len(devices))
	}
	if devices[0].Module != "e1000e" {
		t.Errorf("module = %q, want e1000e", devices[0].Module)
	}
}

func TestScanPCIDevicesFrom_Empty(t *testing.T) {
	root := t.TempDir()
	devices, err := scanPCIDevicesFrom(root)
	if err != nil {
		t.Fatalf("scanPCIDevicesFrom: %v", err)
	}
	if len(devices) != 0 {
		t.Errorf("devices = %d, want 0", len(devices))
	}
}

func TestScanPCIDevicesFrom_NoSuchDir(t *testing.T) {
	_, err := scanPCIDevicesFrom("/nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent dir")
	}
}

func TestPCIDevice_Fields(t *testing.T) {
	dev := PCIDevice{
		Address:  "0000:00:1f.0",
		VendorID: "0x8086",
		DeviceID: "0x15b8",
		Class:    "0x020000",
		Driver:   "e1000e",
		Module:   "e1000e",
	}

	if dev.Address != "0000:00:1f.0" {
		t.Error("bad address")
	}
	if dev.Driver != "e1000e" {
		t.Error("bad driver")
	}
}

func TestReadSysfsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test")
	if err := os.WriteFile(path, []byte("  hello\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := readSysfsFile(path)
	if got != "hello" {
		t.Errorf("readSysfsFile = %q, want hello", got)
	}
}

func TestReadSysfsFile_Missing(t *testing.T) {
	got := readSysfsFile("/nonexistent/file")
	if got != "" {
		t.Errorf("readSysfsFile missing = %q, want empty", got)
	}
}

func TestNewManager(t *testing.T) {
	m := NewManager(nil)
	if m.modulesDir == "" {
		t.Fatal("modulesDir must not be empty")
	}
	if !strings.HasPrefix(m.modulesDir, "/lib/modules") {
		t.Errorf("unexpected modulesDir value %q", m.modulesDir)
	}
}
