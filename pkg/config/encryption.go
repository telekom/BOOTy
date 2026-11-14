package config

import (
	"context"
	"fmt"
	"net"
	"net/url"
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
	// partition is encrypted. HTTPS, or HTTP on loopback only. Required
	// with Mode.
	// Default: ""
	RecoveryKeyURL string `yaml:"recoveryKeyURL" json:"recoveryKeyURL"`

	// StatusURL receives the result of the TPM2 enrollment from the
	// installed system (POST, JSON, the bearer token of this run, no key).
	// HTTPS only.
	// Default: "" (no status)
	StatusURL string `yaml:"statusURL" json:"statusURL"`

	// SkipSecureBootCheck encrypts although the firmware is in setup mode
	// or has no Secure Boot. The installed system binds the volume only
	// once Secure Boot is on in user mode.
	// Default: false
	SkipSecureBootCheck bool `yaml:"skipSecureBootCheck" json:"skipSecureBootCheck"`

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

// UKI reports whether the installed system boots a signed UKI.
func (d *DiskEncryptionConfig) UKI() bool {
	return d.Mode == DiskEncryptionLUKS2TPM2UKI
}

// Enabled reports whether disk encryption is requested.
func (d *DiskEncryptionConfig) Enabled() bool {
	return d.Mode != ""
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
	} else if err := ValidateEscrowURL(d.RecoveryKeyURL); err != nil {
		errs = append(errs, "provision.diskEncryption.recoveryKeyURL: "+err.Error())
	}
	errs = append(errs, c.validateDiskEncryptionTransport()...)
	// The root is encrypted in place after the image is written; layouts
	// that put the root on md, LVM or a declared layout are not supported.
	mirror := strings.ToLower(strings.TrimSpace(c.Provision.Disk.MirroringMode))
	mode := strings.ToLower(strings.TrimSpace(c.Provision.Image.Mode))
	if (mirror != "" && mirror != "none") || len(c.Provision.Disk.RAID) > 0 || c.Provision.Disk.PartitionLayoutRequested() ||
		(mode != "" && mode != ImageModeWholeDisk) {
		errs = append(errs, "provision.diskEncryption works with a whole-disk image only, not with disk mirroring, RAID, a partition layout or an image mode")
	}
	return errs
}

// validateDiskEncryptionTransport checks the values that reach the
// installed system (sourced from a shell file, posted with curl
// --proto =https) and the TLS verification of the key hand-off.
// validateUKISigning checks SECUREBOOT_SIGNING against the disk encryption
// mode: the UKI mode needs a signer.
// The UKI digest comes from the signed image manifest, so the image must
// be cosign-verified.
func (c *Config) validateUKISigning() []string {
	d := &c.Provision.DiskEncryption
	signing := c.Provision.SecureBoot.SigningMode()
	switch signing {
	case SigningOff, SigningPerHost, SigningCI:
	default:
		return []string{fmt.Sprintf("provision.secureBoot.signing %q: want %q, %q or %q",
			c.Provision.SecureBoot.Signing, SigningOff, SigningPerHost, SigningCI)}
	}
	var errs []string
	if !d.UKI() {
		// Without the UKI mode the signer has nothing to sign here; module
		// signing (#935) uses the same setting.
		if d.PCRPublicKeyFile != "" {
			errs = append(errs, fmt.Sprintf("provision.diskEncryption.pcrPublicKeyFile needs provision.diskEncryption.mode %q", DiskEncryptionLUKS2TPM2UKI))
		}
		return errs
	}
	if signing == SigningOff {
		errs = append(errs, fmt.Sprintf("provision.diskEncryption.mode %q needs provision.secureBoot.signing %q or %q",
			DiskEncryptionLUKS2TPM2UKI, SigningPerHost, SigningCI))
	}
	if d.PCRPublicKeyFile != "" && signing != SigningCI {
		errs = append(errs, fmt.Sprintf("provision.diskEncryption.pcrPublicKeyFile needs provision.secureBoot.signing %q: with %q BOOTy makes the key", SigningCI, signing))
	}
	if len(c.Provision.Image.CosignPublicKeys) == 0 {
		errs = append(errs, fmt.Sprintf("provision.diskEncryption.mode %q needs provision.image.cosignPublicKeys: the UKI digest comes from the signed image manifest", DiskEncryptionLUKS2TPM2UKI))
	}
	if d.StatusURL != "" {
		errs = append(errs, fmt.Sprintf("provision.diskEncryption.statusURL is not supported with provision.diskEncryption.mode %q yet", DiskEncryptionLUKS2TPM2UKI))
	}
	return errs
}

func (c *Config) validateDiskEncryptionTransport() []string {
	var errs []string
	const unsafe = "'\"\\ \t\n$`"
	d := &c.Provision.DiskEncryption
	if d.StatusURL != "" {
		if u, err := url.Parse(d.StatusURL); err != nil || u.Scheme != "https" || u.Host == "" || strings.ContainsAny(d.StatusURL, unsafe) {
			errs = append(errs, "provision.diskEncryption.statusURL must be an https URL without quotes, spaces, $ or backquotes")
		}
	}
	if strings.ContainsAny(c.Hostname, unsafe) {
		errs = append(errs, "hostname must not contain quotes, spaces, $ or backquotes with provision.diskEncryption")
	}
	if BoolOr(c.Transport.SkipTLSVerify, false) {
		errs = append(errs, "provision.diskEncryption needs TLS verification: the recovery key must not go to an unverified server (unset SKIP_TLS_VERIFY, use CA_BUNDLE_FILE)")
	}
	return errs
}

// ValidateEscrowURL accepts https URLs and http on a loopback host only:
// INSECURE_TRANSPORT does not apply to the recovery key.
func ValidateEscrowURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("not an absolute URL")
	}
	if u.Scheme == "https" {
		return nil
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); u.Scheme == "http" && (host == "localhost" || (ip != nil && ip.IsLoopback())) {
		return nil
	}
	return fmt.Errorf("needs https (http only on loopback; INSECURE_TRANSPORT does not apply)")
}

// RecoveryKeyEscrow is an optional provider capability: it hands the LUKS
// recovery key to the provisioning server. The provider must not log or
// store the body, which holds the key; the caller zeroes it after the call.
type RecoveryKeyEscrow interface {
	EscrowRecoveryKey(ctx context.Context, body []byte) error
	// CurrentToken is the bearer token that the installed system uses for
	// the status callback (StatusURL).
	CurrentToken() string
}
