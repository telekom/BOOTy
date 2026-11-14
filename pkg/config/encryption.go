package config

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// DiskEncryptionLUKS2TPM2 encrypts a partition with LUKS2 and binds it to
// the TPM2 on the first boot with Secure Boot on (docs/disk-encryption.md).
const DiskEncryptionLUKS2TPM2 = "luks2-tpm2"

// DiskEncryptionLUKS2TPM2UKI boots a signed unified kernel image (UKI) and
// binds the volume to PCR 7 and a signed PCR 11 policy on the first boot
// with Secure Boot on. SecureBootConfig.Signing selects who signs the UKI
// (docs/disk-encryption.md#signed-uki-and-pcr-11).
const DiskEncryptionLUKS2TPM2UKI = "luks2-tpm2-uki"

// DiskEncryptionTargetRoot selects the root partition of the image.
const DiskEncryptionTargetRoot = "root"

// DiskEncryptionConfig controls disk encryption of the installed system.
type DiskEncryptionConfig struct {
	// Mode turns encryption on. Valid values: "" (off), "luks2-tpm2",
	// "luks2-tpm2-uki".
	// Default: ""
	Mode string `yaml:"mode" json:"mode"`

	// Target is the partition to encrypt: "root" or a partition number of
	// the target disk.
	// Default: "" (root)
	Target string `yaml:"target" json:"target"`

	// RecoveryKeyURL receives the recovery key (POST, JSON) before the
	// partition is encrypted. Required with Mode.
	// Default: ""
	RecoveryKeyURL string `yaml:"recoveryKeyURL" json:"recoveryKeyURL"`

	// AllowFailure provisions without encryption when the checks before
	// the first disk change fail (no TPM, image without the tools,
	// unsupported layout). Errors after that always fail the run.
	// Default: false
	AllowFailure bool `yaml:"allowFailure" json:"allowFailure"`

	// PCRPublicKeyFile pins the PCR 11 policy key with luks2-tpm2-uki and
	// SECUREBOOT_SIGNING=ci: a PEM public key file in the ramdisk that
	// must equal the .pcrpkey section of the UKI.
	// Default: "" (the .pcrpkey of the verified UKI is used as is)
	PCRPublicKeyFile string `yaml:"pcrPublicKeyFile" json:"pcrPublicKeyFile"`
}

// Enabled reports whether disk encryption is requested.
func (d *DiskEncryptionConfig) Enabled() bool {
	return d.Mode != ""
}

// UKI reports whether the installed system boots a signed UKI.
func (d *DiskEncryptionConfig) UKI() bool {
	return d.Mode == DiskEncryptionLUKS2TPM2UKI
}

// TargetPartition returns 0 for the root partition or the partition number.
func (d *DiskEncryptionConfig) TargetPartition() (int, error) {
	t := strings.TrimSpace(d.Target)
	if t == "" || t == DiskEncryptionTargetRoot {
		return 0, nil
	}
	n, err := strconv.Atoi(t)
	if err != nil || n < 1 || n > 128 {
		return 0, fmt.Errorf("provision.diskEncryption.target %q: want %q or a partition number 1-128", d.Target, DiskEncryptionTargetRoot)
	}
	return n, nil
}

func (c *Config) validateDiskEncryption() []string {
	d := &c.Provision.DiskEncryption
	if !d.Enabled() {
		return c.validateUKISigning()
	}
	var errs []string
	if d.Mode != DiskEncryptionLUKS2TPM2 && !d.UKI() {
		errs = append(errs, fmt.Sprintf("provision.diskEncryption.mode %q: want %q or %q", d.Mode, DiskEncryptionLUKS2TPM2, DiskEncryptionLUKS2TPM2UKI))
	}
	errs = append(errs, c.validateUKISigning()...)
	if _, err := d.TargetPartition(); err != nil {
		errs = append(errs, err.Error())
	}
	if strings.TrimSpace(d.RecoveryKeyURL) == "" {
		errs = append(errs, "provision.diskEncryption.recoveryKeyURL is required with provision.diskEncryption.mode")
	}
	// The root is encrypted in place after the image is written; layouts
	// that put the root on md, LVM or a declared layout are not supported.
	mirror := strings.ToLower(strings.TrimSpace(c.Provision.Disk.MirroringMode))
	mode := strings.ToLower(strings.TrimSpace(c.Provision.Image.Mode))
	if (mirror != "" && mirror != "none") || len(c.Provision.Disk.RAID) > 0 || c.Provision.Disk.PartitionLayout != nil ||
		(mode != "" && mode != ImageModeWholeDisk) {
		errs = append(errs, "provision.diskEncryption works with a whole-disk image only, not with disk mirroring, RAID, a partition layout or an image mode")
	}
	return errs
}

// validateUKISigning checks SECUREBOOT_SIGNING against the disk encryption
// mode: the UKI mode needs a signer, and the signer is used only by it.
// The UKI digest comes from the signed image manifest, so the image must
// be cosign-verified.
func (c *Config) validateUKISigning() []string {
	d := &c.Provision.DiskEncryption
	signing := c.Provision.SecureBoot.SigningMode()
	var errs []string
	switch signing {
	case SecureBootSigningOff, SecureBootSigningPerHost, SecureBootSigningCI:
	default:
		return []string{fmt.Sprintf("provision.secureBoot.signing %q: want %q, %q or %q",
			c.Provision.SecureBoot.Signing, SecureBootSigningOff, SecureBootSigningPerHost, SecureBootSigningCI)}
	}
	if !d.UKI() {
		if signing != SecureBootSigningOff {
			errs = append(errs, fmt.Sprintf("provision.secureBoot.signing %q needs provision.diskEncryption.mode %q", signing, DiskEncryptionLUKS2TPM2UKI))
		}
		if d.PCRPublicKeyFile != "" {
			errs = append(errs, fmt.Sprintf("provision.diskEncryption.pcrPublicKeyFile needs provision.diskEncryption.mode %q", DiskEncryptionLUKS2TPM2UKI))
		}
		return errs
	}
	if signing == SecureBootSigningOff {
		errs = append(errs, fmt.Sprintf("provision.diskEncryption.mode %q needs provision.secureBoot.signing %q or %q",
			DiskEncryptionLUKS2TPM2UKI, SecureBootSigningPerHost, SecureBootSigningCI))
	}
	if d.PCRPublicKeyFile != "" && signing != SecureBootSigningCI {
		errs = append(errs, fmt.Sprintf("provision.diskEncryption.pcrPublicKeyFile needs provision.secureBoot.signing %q: with %q BOOTy makes the key", SecureBootSigningCI, signing))
	}
	if len(c.Provision.Image.CosignPublicKeys) == 0 {
		errs = append(errs, fmt.Sprintf("provision.diskEncryption.mode %q needs provision.image.cosignPublicKeys: the UKI digest comes from the signed image manifest", DiskEncryptionLUKS2TPM2UKI))
	}
	return errs
}

// RecoveryKeyEscrow is an optional provider capability: it hands the LUKS
// recovery key to the provisioning server. The provider must not log or
// store the body, which holds the key; the caller zeroes it after the call.
type RecoveryKeyEscrow interface {
	EscrowRecoveryKey(ctx context.Context, body []byte) error
}
