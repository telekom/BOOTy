package types

import "testing"

func TestConstants(t *testing.T) {
	if ReadImage != "readImage" {
		t.Errorf("ReadImage = %q, want %q", ReadImage, "readImage")
	}
	if WriteImage != "writeImage" {
		t.Errorf("WriteImage = %q, want %q", WriteImage, "writeImage")
	}
}

func TestBootyConfigDefaults(t *testing.T) {
	cfg := BootyConfig{}

	if cfg.Action != "" {
		t.Errorf("default Action = %q, want empty", cfg.Action)
	}
	if cfg.Compressed {
		t.Error("default Compressed should be false")
	}
	if cfg.DryRun {
		t.Error("default DryRun should be false")
	}
	if cfg.DropToShell {
		t.Error("default DropToShell should be false")
	}
	if cfg.WipeDevice {
		t.Error("default WipeDevice should be false")
	}
	if cfg.GrowDisk {
		t.Error("default GrowDisk should be false")
	}
	if cfg.GrowPartition != 0 {
		t.Errorf("default GrowPartition = %d, want 0", cfg.GrowPartition)
	}
}
