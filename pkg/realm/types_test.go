package realm

import (
	"testing"
)

func TestMountTypes(t *testing.T) {
	m := Mount{
		CreateMount: true,
		EnableMount: false,
		Name:        "test",
		Source:      "tmpfs",
		Path:        "/tmp",
		FSType:      "tmpfs",
	}

	if m.Name != "test" {
		t.Errorf("Mount.Name = %q, want %q", m.Name, "test")
	}
	if !m.CreateMount {
		t.Error("Mount.CreateMount should be true")
	}
	if m.EnableMount {
		t.Error("Mount.EnableMount should be false")
	}
}

func TestDeviceTypes(t *testing.T) {
	d := Device{
		CreateDevice: true,
		Name:         "null",
		Path:         "/dev/null",
		Major:        1,
		Minor:        3,
	}

	if d.Name != "null" {
		t.Errorf("Device.Name = %q, want %q", d.Name, "null")
	}
	if d.Major != 1 {
		t.Errorf("Device.Major = %d, want 1", d.Major)
	}
	if d.Minor != 3 {
		t.Errorf("Device.Minor = %d, want 3", d.Minor)
	}
}
