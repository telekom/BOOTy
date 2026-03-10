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
