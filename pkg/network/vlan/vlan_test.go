//go:build linux

package vlan

import (
	"testing"
)

func TestConfigInterfaceName(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{"standard", Config{ID: 200, Parent: "eno1"}, "eno1.200"},
		{"vlan1", Config{ID: 1, Parent: "eth0"}, "eth0.1"},
		{"high_id", Config{ID: 4094, Parent: "ens3f0"}, "ens3f0.4094"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.InterfaceName()
			if got != tt.want {
				t.Errorf("InterfaceName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSetupInvalidVLANID(t *testing.T) {
	tests := []struct {
		name string
		id   int
	}{
		{"zero", 0},
		{"negative", -1},
		{"too_high", 4095},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Setup(Config{ID: tt.id, Parent: "eth0"})
			if err == nil {
				t.Error("Setup() should fail for invalid VLAN ID")
			}
		})
	}
}

func TestSetupParentNotFound(t *testing.T) {
	_, err := Setup(Config{ID: 100, Parent: "nonexistent_iface_xyz"})
	if err == nil {
		t.Error("Setup() should fail when parent interface does not exist")
	}
}

func TestTeardownNonexistent(t *testing.T) {
	// Teardown of a non-existent interface should succeed silently.
	if err := Teardown("nonexistent_parent_xyz", 999); err != nil {
		t.Errorf("Teardown() of nonexistent interface should return nil, got: %v", err)
	}
}

func TestSetupAllEmpty(t *testing.T) {
	names, err := SetupAll(nil)
	if err != nil {
		t.Errorf("SetupAll(nil) returned error: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("SetupAll(nil) returned %d names, want 0", len(names))
	}
}

func TestTeardownAllEmpty(t *testing.T) {
	// Should not panic on empty input.
	TeardownAll(nil)
}
