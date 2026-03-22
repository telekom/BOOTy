package cryptenroll

import (
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	if len(c.PCRs) != 1 || c.PCRs[0] != 7 {
		t.Errorf("default PCRs = %v, want [7]", c.PCRs)
	}
	if c.PCRBank != "sha256" {
		t.Errorf("pcrBank = %q", c.PCRBank)
	}
	if c.KeySlot != 2 {
		t.Errorf("keySlot = %d", c.KeySlot)
	}
}

func TestConfig_ApplyDefaults(t *testing.T) {
	c := &Config{LUKSDevice: "/dev/sda1", KeySlot: -1}
	c.ApplyDefaults()
	if len(c.PCRs) == 0 {
		t.Error("PCRs should be set after ApplyDefaults")
	}
	if c.KeySlot != 2 {
		t.Errorf("keySlot = %d", c.KeySlot)
	}
}

func TestConfig_ApplyDefaults_KeySlotZeroPreserved(t *testing.T) {
	t.Helper()
	c := &Config{LUKSDevice: "/dev/sda1", KeySlot: 0}
	c.ApplyDefaults()
	if c.KeySlot != 0 {
		t.Errorf("keySlot 0 should be preserved, got %d", c.KeySlot)
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name:    "valid",
			config:  Config{LUKSDevice: "/dev/sda1", PCRs: []int{7}, KeySlot: 1},
			wantErr: false,
		},
		{
			name:    "no device",
			config:  Config{PCRs: []int{7}},
			wantErr: true,
		},
		{
			name:    "invalid PCR",
			config:  Config{LUKSDevice: "/dev/sda1", PCRs: []int{25}},
			wantErr: true,
		},
		{
			name:    "invalid slot",
			config:  Config{LUKSDevice: "/dev/sda1", PCRs: []int{7}, KeySlot: 32},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestBuildPCRPolicy(t *testing.T) {
	c := &Config{PCRs: []int{7, 8, 14}, PCRBank: "sha256", LUKSDevice: "/dev/sda1"}
	policy := c.BuildPCRPolicy()
	if len(policy.PCRs) != 3 {
		t.Errorf("expected 3 PCRs, got %d", len(policy.PCRs))
	}
	if policy.Descriptions[7] != "secure boot policy" {
		t.Errorf("PCR 7 desc = %q", policy.Descriptions[7])
	}
}

func TestFormatPCRSelection(t *testing.T) {
	result := FormatPCRSelection([]int{14, 7, 8})
	if result != "7+8+14" {
		t.Errorf("FormatPCRSelection = %q, want %q", result, "7+8+14")
	}
}
