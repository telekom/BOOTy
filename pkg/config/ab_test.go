package config

import "testing"

func TestABConfigResolvedTargetSlot(t *testing.T) {
	tests := []struct {
		name string
		cfg  ABConfig
		want string
	}{
		{name: "empty defaults to slot a", cfg: ABConfig{}, want: ABSlotA},
		{name: "inactive from active a selects b", cfg: ABConfig{ActiveSlot: ABSlotA, TargetSlot: ABTargetInactive}, want: ABSlotB},
		{name: "inactive from active b selects a", cfg: ABConfig{ActiveSlot: ABSlotB, TargetSlot: ABTargetInactive}, want: ABSlotA},
		{name: "explicit target wins", cfg: ABConfig{ActiveSlot: ABSlotA, TargetSlot: ABSlotA}, want: ABSlotA},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.cfg.ResolvedTargetSlot()
			if err != nil {
				t.Fatalf("ResolvedTargetSlot() error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ResolvedTargetSlot() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestABConfigPartitionLayoutTargetsInactiveSlot(t *testing.T) {
	layout, err := (&ABConfig{ActiveSlot: ABSlotA, TargetSlot: ABTargetInactive, RootSizeMB: 4096}).PartitionLayout("/dev/sda")
	if err != nil {
		t.Fatalf("PartitionLayout() error: %v", err)
	}
	if layout.Device != "/dev/sda" {
		t.Fatalf("Device = %q, want /dev/sda", layout.Device)
	}
	if len(layout.Partitions) != 4 {
		t.Fatalf("partitions = %d, want 4", len(layout.Partitions))
	}
	if layout.Partitions[1].Mountpoint != "" {
		t.Fatalf("slot A mountpoint = %q, want empty", layout.Partitions[1].Mountpoint)
	}
	if layout.Partitions[2].Mountpoint != "/" {
		t.Fatalf("slot B mountpoint = %q, want /", layout.Partitions[2].Mountpoint)
	}
	if layout.Partitions[3].Mountpoint != "/var/lib/booty" {
		t.Fatalf("state mountpoint = %q", layout.Partitions[3].Mountpoint)
	}
}

func TestValidateAcceptsABImageMode(t *testing.T) {
	cfg := &Config{}
	cfg.Provision.Image.Mode = ImageModeAB
	cfg.Provision.AB.ActiveSlot = ABSlotB
	cfg.Provision.AB.TargetSlot = ABTargetInactive
	cfg.Provision.AB.RootSizeMB = 8192

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
	if cfg.Provision.AB.TargetSlot != ABTargetInactive {
		t.Fatalf("target slot normalized to %q, want inactive", cfg.Provision.AB.TargetSlot)
	}
}

func TestValidateRejectsABPreserveOutsideABMode(t *testing.T) {
	cfg := &Config{}
	cfg.Provision.Image.Mode = ImageModeWholeDisk
	cfg.Provision.AB.PreserveExisting = true

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}
