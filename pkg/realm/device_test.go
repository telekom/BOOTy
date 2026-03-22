//go:build linux

package realm

import "testing"

func TestDefaultDevices(t *testing.T) {
	d := DefaultDevices()

	if len(d.Device) != 3 {
		t.Fatalf("DefaultDevices() returned %d devices, want 3", len(d.Device))
	}

	expected := []struct {
		name  string
		path  string
		major int64
		minor int64
	}{
		{"null", "/dev/null", 1, 3},
		{"random", "/dev/random", 1, 8},
		{"urandom", "/dev/urandom", 1, 9},
	}

	for i, e := range expected {
		if d.Device[i].Name != e.name {
			t.Errorf("Device[%d].Name = %q, want %q", i, d.Device[i].Name, e.name)
		}
		if d.Device[i].Path != e.path {
			t.Errorf("Device[%d].Path = %q, want %q", i, d.Device[i].Path, e.path)
		}
		if d.Device[i].Major != e.major {
			t.Errorf("Device[%d].Major = %d, want %d", i, d.Device[i].Major, e.major)
		}
		if d.Device[i].Minor != e.minor {
			t.Errorf("Device[%d].Minor = %d, want %d", i, d.Device[i].Minor, e.minor)
		}
	}
}

func TestGetDevice(t *testing.T) {
	d := DefaultDevices()

	dev := d.GetDevice("null")
	if dev == nil {
		t.Fatal("GetDevice(\"null\") returned nil")
	}
	if dev.Name != "null" {
		t.Errorf("GetDevice(\"null\").Name = %q", dev.Name)
	}

	dev = d.GetDevice("nonexistent")
	if dev != nil {
		t.Error("GetDevice(\"nonexistent\") should return nil")
	}
}

func TestMakedev(t *testing.T) {
	// Test that makedev produces expected device numbers
	result := makedev(1, 3)
	expected := (1 << 8) | 3
	if result != expected {
		t.Errorf("makedev(1, 3) = %d, want %d", result, expected)
	}
}

func TestCreateDeviceNoDevicesToCreate(t *testing.T) {
	// DefaultDevices all have CreateDevice=false, so CreateDevice() should
	// iterate without calling Mknod.
	d := DefaultDevices()
	if err := d.CreateDevice(); err != nil {
		t.Fatalf("CreateDevice() error: %v", err)
	}
}

func TestCreateDeviceWithMknodFailure(t *testing.T) {
	// Even with CreateDevice=true and an invalid path, the function logs
	// but still returns nil.
	d := &Devices{
		Device: []Device{
			{
				CreateDevice: true,
				Name:         "test",
				Path:         "/nonexistent/path/testdev",
				Mode:         0o666,
				Major:        1,
				Minor:        3,
			},
		},
	}
	if err := d.CreateDevice(); err != nil {
		t.Fatalf("CreateDevice() error: %v", err)
	}
}

func TestCreateDeviceMixed(t *testing.T) {
	// Mix of CreateDevice=true and false.
	d := &Devices{
		Device: []Device{
			{CreateDevice: false, Name: "skip-this"},
			{CreateDevice: true, Name: "try-this", Path: "/nonexistent/devnode", Major: 1, Minor: 1},
			{CreateDevice: false, Name: "skip-also"},
		},
	}
	if err := d.CreateDevice(); err != nil {
		t.Fatalf("CreateDevice() error: %v", err)
	}
}

func TestCreateDeviceEmpty(t *testing.T) {
	d := &Devices{}
	if err := d.CreateDevice(); err != nil {
		t.Fatalf("CreateDevice() error: %v", err)
	}
}
