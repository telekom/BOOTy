//go:build linux

package luks

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// UnlockMethod specifies how LUKS volumes auto-unlock on boot.
type UnlockMethod string

const (
	// UnlockPassphrase requires manual passphrase entry at boot.
	UnlockPassphrase UnlockMethod = "passphrase"
	// UnlockTPM2 binds the key to TPM2 PCR values.
	UnlockTPM2 UnlockMethod = "tpm2"
	// UnlockClevis uses network-bound decryption via tang server.
	UnlockClevis UnlockMethod = "clevis"
	// UnlockKeyFile uses a key file embedded in the initramfs.
	UnlockKeyFile UnlockMethod = "keyfile"
)

// Config holds LUKS encryption configuration.
type Config struct {
	Enabled      bool         `json:"enabled"`
	Partitions   []Target     `json:"partitions"`
	UnlockMethod UnlockMethod `json:"unlockMethod"`
	Passphrase   string       `json:"passphrase,omitempty"`
	TangURL      string       `json:"tangUrl,omitempty"`
	TPMPCRs      []int        `json:"tpmPcrs,omitempty"`
	Cipher       string       `json:"cipher,omitempty"`
	KeySize      int          `json:"keySize,omitempty"`
	Hash         string       `json:"hash,omitempty"`
}

// Target identifies a partition to encrypt.
type Target struct {
	Device     string `json:"device"`
	MappedName string `json:"mappedName"`
	MountPoint string `json:"mountPoint"`
}

// Manager handles LUKS encryption operations.
type Manager struct {
	log *slog.Logger
}

// New creates a LUKS encryption manager.
func New(log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{log: log}
}

// Format creates a LUKS2 volume on the target device.
func (m *Manager) Format(ctx context.Context, target *Target, cfg *Config) error {
	if cfg.Passphrase == "" {
		return fmt.Errorf("passphrase required for LUKS format")
	}

	cipher := cfg.Cipher
	if cipher == "" {
		cipher = "aes-xts-plain64"
	}
	keySize := cfg.KeySize
	if keySize == 0 {
		keySize = 512
	}
	hash := cfg.Hash
	if hash == "" {
		hash = "sha256"
	}

	args := []string{
		"luksFormat",
		"--type", "luks2",
		"--cipher", cipher,
		"--key-size", fmt.Sprintf("%d", keySize),
		"--hash", hash,
		"--key-file", "-",
		"--batch-mode",
		target.Device,
	}

	cmd := exec.CommandContext(ctx, "cryptsetup", args...)
	cmd.Stdin = strings.NewReader(cfg.Passphrase + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cryptsetup luksFormat %s: %s: %w", target.Device, string(out), err)
	}
	m.log.Info("LUKS volume formatted", "device", target.Device, "cipher", cipher)
	return nil
}

// Open maps a LUKS volume to /dev/mapper/<name>.
func (m *Manager) Open(ctx context.Context, target *Target, passphrase string) error {
	cmd := exec.CommandContext(ctx, "cryptsetup", "luksOpen",
		"--key-file", "-",
		target.Device, target.MappedName,
	)
	cmd.Stdin = strings.NewReader(passphrase + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cryptsetup luksOpen %s: %s: %w", target.Device, string(out), err)
	}
	m.log.Info("LUKS volume opened", "device", target.Device, "mapped", target.MappedName)
	return nil
}

// Close unmaps a LUKS volume.
func (m *Manager) Close(ctx context.Context, mappedName string) error {
	cmd := exec.CommandContext(ctx, "cryptsetup", "luksClose", mappedName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cryptsetup luksClose %s: %s: %w", mappedName, string(out), err)
	}
	m.log.Info("LUKS volume closed", "mapped", mappedName)
	return nil
}

// IsLUKS checks if a device contains a LUKS header.
func (m *Manager) IsLUKS(ctx context.Context, device string) bool {
	cmd := exec.CommandContext(ctx, "cryptsetup", "isLuks", device)
	return cmd.Run() == nil
}

// MappedPath returns the /dev/mapper path for a mapped LUKS volume.
func MappedPath(mappedName string) string {
	return "/dev/mapper/" + mappedName
}
