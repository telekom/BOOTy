package config

import (
	"strings"
	"testing"
)

func TestValidateSecureBootPinnedDigests(t *testing.T) {
	validDigest := strings.Repeat("a", 64)
	cfg := &Config{}
	cfg.Provision.SecureBoot.PinnedDigests = map[string]string{
		"shim":   "sha256:" + validDigest,
		"grub":   validDigest,
		"kernel": validDigest,
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestValidateSecureBootPinnedDigestsRejectsInvalidConfig(t *testing.T) {
	cases := []struct {
		name    string
		pins    map[string]string
		wantErr string
	}{
		{
			name:    "unknown component",
			pins:    map[string]string{"initrd": strings.Repeat("a", 64)},
			wantErr: `provision.secureBoot.pinnedDigests["initrd"]: unsupported component`,
		},
		{
			name:    "bad digest",
			pins:    map[string]string{"kernel": "not-a-digest"},
			wantErr: `provision.secureBoot.pinnedDigests["kernel"]: must be 64 hex characters`,
		},
		{
			name:    "empty digest",
			pins:    map[string]string{"kernel": ""},
			wantErr: `provision.secureBoot.pinnedDigests["kernel"]: must not be empty`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{}
			cfg.Provision.SecureBoot.PinnedDigests = tc.pins

			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestValidateModuleSigning(t *testing.T) {
	ab := &ABConfig{PreserveExisting: true}
	for _, tc := range []struct {
		m        ModuleSigningConfig
		mode     string
		wantErr  bool
		reEnable bool
	}{
		{ModuleSigningConfig{Enrollment: "bogus"}, "", false, false}, // disabled: not checked
		{ModuleSigningConfig{Enabled: true}, "", false, false},
		{ModuleSigningConfig{Enabled: true, Enrollment: "external", CertDir: "/etc/keys"}, "", false, false},
		{ModuleSigningConfig{Enabled: true, Enrollment: "bogus"}, "", true, false},
		{ModuleSigningConfig{Enabled: true, CertDir: "relative"}, "", true, false},
		{ModuleSigningConfig{Enabled: true, CertDir: "/a/../b"}, "", true, false},
		{ModuleSigningConfig{Enabled: true}, ImageModeAB, true, false},
		{ModuleSigningConfig{Enabled: true, Enrollment: "external"}, ImageModeAB, false, false},
		// Secure Boot back on before the enrollment boot refuses the helper.
		{ModuleSigningConfig{Enabled: true}, "", true, true},
		{ModuleSigningConfig{Enabled: true, Enrollment: "mokutil"}, "", false, true},
		{ModuleSigningConfig{Enabled: true, Enrollment: "external"}, "", false, true},
		{ModuleSigningConfig{Enabled: true, Target: "sysext"}, "", false, false},
		{ModuleSigningConfig{Enabled: true, Target: "sysext", CertDir: "/etc/keys"}, "", true, false},
		{ModuleSigningConfig{Enabled: true, Target: "bogus"}, "", true, false},
	} {
		errs := validateModuleSigning(tc.mode, tc.reEnable, &tc.m, ab)
		if (len(errs) > 0) != tc.wantErr {
			t.Errorf("%+v mode=%q: errs=%v", tc.m, tc.mode, errs)
		}
	}
}
