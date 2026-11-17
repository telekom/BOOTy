package config

import (
	"strings"
	"testing"
)

func TestValidateDiskEncryption(t *testing.T) {
	tests := []struct {
		name string
		set  func(*Config)
		want string
	}{
		{"off", func(*Config) {}, ""},
		{"valid", func(c *Config) {}, "-"},
		{"partition number", func(c *Config) { c.Provision.DiskEncryption.Target = "3" }, "-"},
		{"bad mode", func(c *Config) { c.Provision.DiskEncryption.Mode = "luks1" }, "want \"luks2-tpm2\" or \"luks2-tpm2-uki\""},
		{"bad target", func(c *Config) { c.Provision.DiskEncryption.Target = "data" }, "partition number"},
		{"no recovery URL", func(c *Config) { c.Provision.DiskEncryption.RecoveryKeyURL = "" }, "recoveryKeyURL is required"},
		{"mirroring", func(c *Config) { c.Provision.Disk.MirroringMode = "raid-1" }, "whole-disk image only"},
		{"ab", func(c *Config) { c.Provision.Image.Mode = ImageModeAB }, "whole-disk image only"},
		{"status token", func(c *Config) {
			c.Provision.DiskEncryption.StatusURL, c.Provision.DiskEncryption.StatusToken = "https://p/s", "status-only"
		}, "-"},
		{"status reuses provisioning token", func(c *Config) {
			c.Provision.DiskEncryption.StatusURL, c.Provision.DiskEncryption.StatusReuseProvisioningToken = "https://p/s", true
		}, "-"},
		{"status without token", func(c *Config) { c.Provision.DiskEncryption.StatusURL = "https://p/s" }, "statusToken is required"},
		{"status token and reuse", func(c *Config) {
			d := &c.Provision.DiskEncryption
			d.StatusURL, d.StatusToken, d.StatusReuseProvisioningToken = "https://p/s", "t", true
		}, "exclude each other"},
		{"status token without URL", func(c *Config) { c.Provision.DiskEncryption.StatusToken = "t" }, "need provision.diskEncryption.statusURL"},
		{"status token with newline", func(c *Config) {
			c.Provision.DiskEncryption.StatusURL, c.Provision.DiskEncryption.StatusToken = "https://p/s", "t\nX-Evil: 1"
		}, "control characters"},
		{"whole-disk", func(c *Config) { c.Provision.Image.Mode = ImageModeWholeDisk; c.Provision.Disk.MirroringMode = "none" }, "-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{}
			if tt.want != "" {
				c.Provision.DiskEncryption = DiskEncryptionConfig{Mode: DiskEncryptionLUKS2TPM2, RecoveryKeyURL: "https://p/key"}
			}
			tt.set(c)
			errs := strings.Join(c.validateDiskEncryption(), "; ")
			switch tt.want {
			case "", "-":
				if errs != "" {
					t.Fatalf("unexpected errors: %s", errs)
				}
			default:
				if !strings.Contains(errs, tt.want) {
					t.Fatalf("errors %q, want %q", errs, tt.want)
				}
			}
		})
	}
}

func TestValidateUKISigning(t *testing.T) {
	uki := func(signing string) func(*Config) {
		return func(c *Config) {
			c.Provision.DiskEncryption = DiskEncryptionConfig{Mode: DiskEncryptionLUKS2TPM2UKI, RecoveryKeyURL: "https://p/key"}
			c.Provision.SecureBoot.Signing = signing
			c.Provision.Image.CosignPublicKeys = []string{"/cosign.pub"}
			c.Provision.SecureBoot.ModuleSigning.Enrollment = ModuleSigningEnrollExternal
		}
	}
	tests := []struct {
		name string
		set  func(*Config)
		want string
	}{
		{"all off", func(*Config) {}, ""},
		{"explicit off", func(c *Config) { c.Provision.SecureBoot.Signing = "off" }, ""},
		{"per-host", uki("per-host"), ""},
		{"ci with pinned key", func(c *Config) { uki("ci")(c); c.Provision.DiskEncryption.PCRPublicKeyFile = "/pcr.pem" }, ""},
		{"uki without signer", uki(""), `needs provision.secureBoot.signing "per-host" or "ci"`},
		{"uki without cosign", func(c *Config) { uki("ci")(c); c.Provision.Image.CosignPublicKeys = nil }, "needs cosign verification of the image"},
		{"pinned key with per-host", func(c *Config) { uki("per-host")(c); c.Provision.DiskEncryption.PCRPublicKeyFile = "/pcr.pem" }, "BOOTy makes the key"},
		{"signer without uki", func(c *Config) { c.Provision.SecureBoot.Signing = "ci" }, ""},
		{"uki with MOK enrollment", func(c *Config) { uki("per-host")(c); c.Provision.SecureBoot.ModuleSigning.Enrollment = "" }, "MokList does not reach the kernel"},
		{"pinned key without uki", func(c *Config) { c.Provision.DiskEncryption.PCRPublicKeyFile = "/pcr.pem" }, `needs provision.diskEncryption.mode "luks2-tpm2-uki"`},
		{"uki with keyless cosign", func(c *Config) {
			uki("ci")(c)
			c.Provision.Image.CosignPublicKeys = nil
			c.Provision.Image.CosignTrustedRoot = "/deploy/trusted_root.json"
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{}
			tt.set(c)
			errs := strings.Join(c.validateDiskEncryption(), "; ")
			if tt.want == "" && errs != "" {
				t.Fatalf("unexpected errors: %s", errs)
			}
			if !strings.Contains(errs, tt.want) {
				t.Fatalf("errors %q, want %q", errs, tt.want)
			}
		})
	}
}

// The UKI mode with a module sysext: the provisioner enrolls the per-host
// module certificate in db, the kernel puts it in .platform, and dm-verity
// checks the root hash signature against .platform
// (CONFIG_DM_VERITY_VERIFY_ROOTHASH_SIG_PLATFORM_KEYRING), so the
// combination is allowed with external enrollment.
func TestUKIWithModuleSysext(t *testing.T) {
	ab := &ABConfig{}
	for _, tc := range []struct {
		name, signing, enrollment string
		wantErr                   string
	}{
		{"per-host external", SigningPerHost, ModuleSigningEnrollExternal, ""},
		{"per-host efi-helper", SigningPerHost, "", "MokList does not reach the kernel"},
		{"ci", SigningCI, ModuleSigningEnrollExternal, "sysext needs signing per-host"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{}
			c.Provision.DiskEncryption = DiskEncryptionConfig{Mode: DiskEncryptionLUKS2TPM2UKI, RecoveryKeyURL: "https://p/key"}
			c.Provision.Image.CosignPublicKeys = []string{"/cosign.pub"}
			sb := &c.Provision.SecureBoot
			sb.Signing, sb.MOKCertPath = tc.signing, "/usr/share/ci/mok.der"
			sb.ModuleSigning = ModuleSigningConfig{Enrollment: tc.enrollment, Target: ModuleSigningTargetSysext}
			errs := strings.Join(append(c.validateDiskEncryption(), validateModuleSigning("", sb, ab)...), "; ")
			if tc.wantErr == "" && errs != "" {
				t.Fatalf("unexpected errors: %s", errs)
			}
			if !strings.Contains(errs, tc.wantErr) {
				t.Fatalf("errors %q, want %q", errs, tc.wantErr)
			}
		})
	}
}

func TestDiskEncryptionDisablesKexec(t *testing.T) {
	c := &Config{}
	if c.KexecDisabled() {
		t.Fatal("kexec disabled by default")
	}
	c.Provision.DiskEncryption.Mode = DiskEncryptionLUKS2TPM2
	if !c.KexecDisabled() {
		t.Fatal("disk encryption must reboot through the firmware")
	}
}

func TestUKICmdlinePolicy(t *testing.T) {
	for _, tc := range []struct{ mode, policy, wantErr string }{
		{DiskEncryptionLUKS2TPM2UKI, "", ""},
		{DiskEncryptionLUKS2TPM2UKI, UKICmdlinePolicyWarn, ""},
		{DiskEncryptionLUKS2TPM2UKI, UKICmdlinePolicyEnforce, ""},
		{DiskEncryptionLUKS2TPM2UKI, "off", "ukiCmdlinePolicy \"off\""},
		{DiskEncryptionLUKS2TPM2, UKICmdlinePolicyEnforce, "ukiCmdlinePolicy need"},
	} {
		c := &Config{}
		c.Provision.DiskEncryption = DiskEncryptionConfig{Mode: tc.mode, RecoveryKeyURL: "https://p/key", UKICmdlinePolicy: tc.policy}
		c.Provision.Image.CosignPublicKeys = []string{"/cosign.pub"}
		c.Provision.SecureBoot.Signing = SigningPerHost
		c.Provision.SecureBoot.ModuleSigning.Enrollment = ModuleSigningEnrollExternal
		errs := strings.Join(c.validateDiskEncryption(), "; ")
		if (tc.wantErr == "") != (errs == "") || !strings.Contains(errs, tc.wantErr) {
			t.Errorf("%s %q: errors %q, want %q", tc.mode, tc.policy, errs, tc.wantErr)
		}
	}
}
