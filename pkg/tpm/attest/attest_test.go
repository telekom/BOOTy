package attest

import (
	"testing"
)

func TestMeasurementLog_Add(t *testing.T) {
	var log MeasurementLog
	m := log.Add(PCRProvisioner, "test-step", []byte("payload"))
	if m.PCR != PCRProvisioner {
		t.Errorf("PCR = %d, want %d", m.PCR, PCRProvisioner)
	}
	if m.Algorithm != AlgSHA256 {
		t.Errorf("Algorithm = %q, want %q", m.Algorithm, AlgSHA256)
	}
	if m.Digest == "" {
		t.Error("Digest should not be empty")
	}
	if len(log.Entries) != 1 {
		t.Errorf("log entries = %d, want 1", len(log.Entries))
	}
}

func TestMeasurementLog_DigestsForPCR(t *testing.T) {
	var log MeasurementLog
	log.Add(PCRImage, "image-write", []byte("image-data"))
	log.Add(PCRConfig, "config-hash", []byte("config-data"))
	log.Add(PCRImage, "image-verify", []byte("image-check"))

	digests := log.DigestsForPCR(PCRImage)
	if len(digests) != 2 {
		t.Errorf("digests for PCR image = %d, want 2", len(digests))
	}
	digests = log.DigestsForPCR(PCRConfig)
	if len(digests) != 1 {
		t.Errorf("digests for PCR config = %d, want 1", len(digests))
	}
	digests = log.DigestsForPCR(PCRProvisioner)
	if len(digests) != 0 {
		t.Errorf("digests for unused PCR = %d, want 0", len(digests))
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Enabled {
		t.Error("default config should be disabled")
	}
	if cfg.DevicePath != "/dev/tpmrm0" {
		t.Errorf("DevicePath = %q, want /dev/tpmrm0", cfg.DevicePath)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("default config validation failed: %v", err)
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{name: "disabled", cfg: Config{Enabled: false}},
		{name: "valid enabled", cfg: Config{Enabled: true, DevicePath: "/dev/tpmrm0", AttestPCRs: []int{0, 7}}},
		{name: "empty device", cfg: Config{Enabled: true}, wantErr: true},
		{name: "pcr out of range", cfg: Config{Enabled: true, DevicePath: "/dev/tpmrm0", PCRImageIdx: 30}, wantErr: true},
		{name: "attest pcr bad", cfg: Config{Enabled: true, DevicePath: "/dev/tpmrm0", AttestPCRs: []int{25}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestVerifyPCRs(t *testing.T) {
	t.Run("matching", func(t *testing.T) {
		exp := map[int][]byte{0: {0x01, 0x02}, 7: {0x03, 0x04}}
		act := map[int][]byte{0: {0x01, 0x02}, 7: {0x03, 0x04}}
		r := VerifyPCRs(exp, act)
		if !r.Verified {
			t.Errorf("expected verified, got errors: %v", r.Errors)
		}
	})
	t.Run("mismatch", func(t *testing.T) {
		exp := map[int][]byte{0: {0x01}}
		act := map[int][]byte{0: {0xFF}}
		r := VerifyPCRs(exp, act)
		if r.Verified {
			t.Error("expected verification to fail")
		}
	})
	t.Run("missing", func(t *testing.T) {
		exp := map[int][]byte{0: {0x01}, 7: {0x02}}
		act := map[int][]byte{0: {0x01}}
		r := VerifyPCRs(exp, act)
		if r.Verified {
			t.Error("expected verification to fail for missing PCR")
		}
	})
}
