package config

import (
	"strings"
	"testing"
)

func TestValidateRejectsRootPartitionSelectorConflict(t *testing.T) {
	cfg := &Config{}
	cfg.Provision.Disk.RootPartitionLabel = "ubuntu-root"
	cfg.Provision.Disk.RootPartitionNumber = 2

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected root partition selector conflict")
	}
	if got := err.Error(); !strings.Contains(got, "provision.disk.rootPartitionLabel and provision.disk.rootPartitionNumber are mutually exclusive") {
		t.Fatalf("Validate() error = %q", got)
	}
}

func TestValidateRejectsNegativeRootPartitionNumber(t *testing.T) {
	cfg := &Config{}
	cfg.Provision.Disk.RootPartitionNumber = -1

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected negative root partition number error")
	}
	if got := err.Error(); !strings.Contains(got, "provision.disk.rootPartitionNumber must be non-negative") {
		t.Fatalf("Validate() error = %q", got)
	}
}
