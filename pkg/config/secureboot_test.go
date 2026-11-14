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
	type m = ModuleSigningConfig
	for _, tc := range []struct {
		sb      SecureBootConfig
		mode    string
		wantErr bool
	}{
		{SecureBootConfig{ModuleSigning: m{Enrollment: "bogus"}}, "", false}, // off: not checked
		{SecureBootConfig{Signing: "off", ModuleSigning: m{Enrollment: "bogus"}}, "", false},
		{SecureBootConfig{Signing: "bogus"}, "", true},
		{SecureBootConfig{Signing: "per-host"}, "", false},
		{SecureBootConfig{Signing: "per-host", ModuleSigning: m{Enrollment: "external", CertDir: "/etc/keys"}}, "", false},
		{SecureBootConfig{Signing: "per-host", ModuleSigning: m{Enrollment: "bogus"}}, "", true},
		{SecureBootConfig{Signing: "per-host", ModuleSigning: m{CertDir: "relative"}}, "", true},
		{SecureBootConfig{Signing: "per-host", ModuleSigning: m{CertDir: "/a/../b"}}, "", true},
		{SecureBootConfig{Signing: "per-host"}, ImageModeAB, true},
		{SecureBootConfig{Signing: "per-host", ModuleSigning: m{Enrollment: "external"}}, ImageModeAB, true},
		// Secure Boot back on before the enrollment boot refuses the helper.
		{SecureBootConfig{Signing: "per-host", ReEnable: true}, "", true},
		{SecureBootConfig{Signing: "per-host", ReEnable: true, MOKPassword: "pw", ModuleSigning: m{Enrollment: "mokutil"}}, "", false},
		{SecureBootConfig{Signing: "per-host", ModuleSigning: m{Enrollment: "mokutil"}}, "", true},
		{SecureBootConfig{Signing: "per-host", ModuleSigning: m{CertDir: "/run/keys"}}, "", true},
		{SecureBootConfig{Signing: "per-host", ReEnable: true, ModuleSigning: m{Enrollment: "external"}}, "", false},
		// ci enrolls the certificate of the image.
		{SecureBootConfig{Signing: "ci", MOKCertPath: "/usr/share/ci/mok.der"}, "", false},
		{SecureBootConfig{Signing: "ci"}, "", true},
		{SecureBootConfig{Signing: "ci", MOKCertPath: "ci/mok.der"}, "", true},
	} {
		errs := validateModuleSigning(tc.mode, &tc.sb, ab)
		if (len(errs) > 0) != tc.wantErr {
			t.Errorf("%+v mode=%q: errs=%v", tc.sb, tc.mode, errs)
		}
	}
}
