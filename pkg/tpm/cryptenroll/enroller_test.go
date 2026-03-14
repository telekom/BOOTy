package cryptenroll

import (
	"testing"
)

func TestConfig_ApplyDefaults(t *testing.T) {
	cfg := Config{LUKSDevice: "/dev/sda2"}
	cfg.ApplyDefaults()

	if len(cfg.PCRs) != 1 || cfg.PCRs[0] != 7 {
		t.Errorf("PCRs = %v, want [7]", cfg.PCRs)
	}
	if cfg.PCRBank != "sha256" {
		t.Errorf("PCRBank = %q", cfg.PCRBank)
	}
	if cfg.KeySlot != 1 {
		t.Errorf("KeySlot = %d", cfg.KeySlot)
	}
	if cfg.TPMPath != "/dev/tpmrm0" {
		t.Errorf("TPMPath = %q", cfg.TPMPath)
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		err  bool
	}{
		{
			"valid",
			Config{LUKSDevice: "/dev/sda2", PCRs: []int{7}, PCRBank: "sha256", KeySlot: 1},
			false,
		},
		{
			"no device",
			Config{PCRs: []int{7}, PCRBank: "sha256"},
			true,
		},
		{
			"invalid PCR",
			Config{LUKSDevice: "/dev/sda2", PCRs: []int{25}, PCRBank: "sha256"},
			true,
		},
		{
			"negative PCR",
			Config{LUKSDevice: "/dev/sda2", PCRs: []int{-1}, PCRBank: "sha256"},
			true,
		},
		{
			"invalid bank",
			Config{LUKSDevice: "/dev/sda2", PCRs: []int{7}, PCRBank: "md5"},
			true,
		},
		{
			"invalid slot",
			Config{LUKSDevice: "/dev/sda2", PCRs: []int{7}, PCRBank: "sha256", KeySlot: 32},
			true,
		},
		{
			"sha384 bank",
			Config{LUKSDevice: "/dev/sda2", PCRs: []int{7}, PCRBank: "sha384"},
			false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.err {
				t.Errorf("Validate() err = %v, wantErr %v", err, tc.err)
			}
		})
	}
}

func TestBuildPCRPolicy(t *testing.T) {
	digests := map[int]string{
		7:  "a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0",
		14: "b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1",
	}

	policy, err := BuildPCRPolicy([]int{14, 7}, "sha256", digests)
	if err != nil {
		t.Fatalf("BuildPCRPolicy: %v", err)
	}

	if policy.PCRs[0] != 7 {
		t.Errorf("first PCR = %d, want 7 (sorted)", policy.PCRs[0])
	}
	if policy.PolicyHash == "" {
		t.Error("empty policy hash")
	}
	if policy.Bank != "sha256" {
		t.Errorf("bank = %q", policy.Bank)
	}
}

func TestBuildPCRPolicy_Empty(t *testing.T) {
	_, err := BuildPCRPolicy(nil, "sha256", nil)
	if err == nil {
		t.Error("expected error for empty PCRs")
	}
}

func TestBuildPCRPolicy_MissingDigest(t *testing.T) {
	_, err := BuildPCRPolicy([]int{7}, "sha256", map[int]string{})
	if err == nil {
		t.Error("expected error for missing digest")
	}
}

func TestBuildPCRPolicy_InvalidDigest(t *testing.T) {
	_, err := BuildPCRPolicy([]int{7}, "sha256", map[int]string{7: "not-hex"})
	if err == nil {
		t.Error("expected error for invalid hex")
	}
}

func TestFormatPCRSelection(t *testing.T) {
	tests := []struct {
		pcrs []int
		want string
	}{
		{[]int{7}, "7"},
		{[]int{14, 7}, "7+14"},
		{[]int{7, 8, 9}, "7+8+9"},
	}
	for _, tc := range tests {
		got := FormatPCRSelection(tc.pcrs)
		if got != tc.want {
			t.Errorf("FormatPCRSelection(%v) = %q, want %q", tc.pcrs, got, tc.want)
		}
	}
}

func TestPCRDescription(t *testing.T) {
	if d := PCRDescription(7); d != "SecureBoot state" {
		t.Errorf("PCR 7 = %q", d)
	}
	if d := PCRDescription(14); d != "MOK/provisioner identity" {
		t.Errorf("PCR 14 = %q", d)
	}
	if d := PCRDescription(20); d != "reserved" {
		t.Errorf("PCR 20 = %q", d)
	}
}

func TestNew(t *testing.T) {
	cfg := Config{LUKSDevice: "/dev/sda2"}
	e := New(nil, &cfg)
	if e == nil {
		t.Fatal("New returned nil")
	}
	if e.cfg.TPMPath != "/dev/tpmrm0" {
		t.Errorf("TPMPath = %q", e.cfg.TPMPath)
	}
}

func TestEnrollResult_Types(t *testing.T) {
	result := EnrollResult{
		KeySlot:     1,
		TPMPath:     "/dev/tpmrm0",
		Recoverable: true,
		PCRPolicy: PCRPolicy{
			PCRs: []int{7},
			Bank: "sha256",
		},
	}
	if result.KeySlot != 1 {
		t.Errorf("KeySlot = %d", result.KeySlot)
	}
	if !result.Recoverable {
		t.Error("not recoverable")
	}
}
