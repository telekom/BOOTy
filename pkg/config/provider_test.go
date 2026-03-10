package config

import "testing"

func TestStatusConstants(t *testing.T) {
	tests := []struct {
		status Status
		want   string
	}{
		{StatusInit, "init"},
		{StatusSuccess, "success"},
		{StatusError, "error"},
	}

	for _, tt := range tests {
		if string(tt.status) != tt.want {
			t.Errorf("Status = %q, want %q", tt.status, tt.want)
		}
	}
}

func TestMachineConfigDefaults(t *testing.T) {
	cfg := &MachineConfig{}

	if cfg.MinDiskSizeGB != 0 {
		t.Errorf("expected 0 min disk size, got %d", cfg.MinDiskSizeGB)
	}
	if cfg.Hostname != "" {
		t.Errorf("expected empty hostname, got %s", cfg.Hostname)
	}
	if cfg.ImageURLs != nil {
		t.Errorf("expected nil image URLs, got %v", cfg.ImageURLs)
	}
}
