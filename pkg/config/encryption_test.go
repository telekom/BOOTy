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
		{"bad mode", func(c *Config) { c.Provision.DiskEncryption.Mode = "luks1" }, "want \"luks2-tpm2\""},
		{"bad target", func(c *Config) { c.Provision.DiskEncryption.Target = "data" }, "partition number"},
		{"no recovery URL", func(c *Config) { c.Provision.DiskEncryption.RecoveryKeyURL = "" }, "recoveryKeyURL is required"},
		{"mirroring", func(c *Config) { c.Provision.Disk.MirroringMode = "raid-1" }, "whole-disk image only"},
		{"ab", func(c *Config) { c.Provision.Image.Mode = ImageModeAB }, "whole-disk image only"},
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
