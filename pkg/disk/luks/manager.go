//go:build linux

package luks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/telekom/BOOTy/pkg/executil"
)

// validMappedName only allows alphanumeric, dash, and underscore characters—
// safe for use as a device-mapper name.
var validMappedName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// Commander abstracts command execution for easier unit testing.
// Extends executil.Commander with stdin support required by cryptsetup.
type Commander interface {
	executil.Commander
	RunWithInput(ctx context.Context, input, name string, args ...string) ([]byte, error)
}

// ExecCommander executes real system commands with sanitized error output.
type ExecCommander struct {
	executil.ExecCommander
}

// RunWithInput executes a command with stdin input.
func (e *ExecCommander) RunWithInput(ctx context.Context, input, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("exec %s: %w [PATH: %s]", name, err, os.Getenv("PATH"))
	}
	return out, nil
}

// Manager handles LUKS encryption operations.
type Manager struct {
	log *slog.Logger
	cmd Commander
}

// New creates a LUKS encryption manager.
func New(log *slog.Logger) *Manager {
	return NewWithCommander(log, nil)
}

// NewWithCommander creates a manager with a custom commander for tests.
func NewWithCommander(log *slog.Logger, cmd Commander) *Manager {
	if log == nil {
		log = slog.Default()
	}
	if cmd == nil {
		cmd = &ExecCommander{}
	}
	return &Manager{log: log, cmd: cmd}
}

// Format creates a LUKS2 volume on the target device.
func (m *Manager) Format(ctx context.Context, target *Target, cfg *Config) error {
	if target == nil {
		return fmt.Errorf("target required")
	}
	if cfg == nil {
		return fmt.Errorf("config required")
	}
	if strings.TrimSpace(target.Device) == "" {
		return fmt.Errorf("target device required")
	}
	// LUKS format always requires a passphrase for initial volume creation.
	// Post-format enrollment (TPM2, clevis, keyfile) is a separate step.
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
	out, err := m.cmd.RunWithInput(ctx, cfg.Passphrase+"\n", "cryptsetup", args...)
	if err != nil {
		return fmt.Errorf("cryptsetup luksFormat %s: %s: %w", target.Device, strings.TrimSpace(string(out)), err)
	}
	m.log.Info("LUKS volume formatted", "device", target.Device, "cipher", cipher)
	return nil
}

// Open maps a LUKS volume to /dev/mapper/<name>.
func (m *Manager) Open(ctx context.Context, target *Target, passphrase string) error {
	if target == nil {
		return fmt.Errorf("target required")
	}
	if strings.TrimSpace(target.Device) == "" || strings.TrimSpace(target.MappedName) == "" {
		return fmt.Errorf("target device and mapped name required")
	}
	if !validMappedName.MatchString(target.MappedName) {
		return fmt.Errorf("invalid mapped name %q: must contain only alphanumeric, dash, or underscore", target.MappedName)
	}
	if passphrase == "" {
		return fmt.Errorf("passphrase required for LUKS open")
	}
	out, err := m.cmd.RunWithInput(ctx, passphrase+"\n", "cryptsetup", "luksOpen",
		"--key-file", "-",
		target.Device, target.MappedName,
	)
	if err != nil {
		return fmt.Errorf("cryptsetup luksOpen %s: %s: %w", target.Device, strings.TrimSpace(string(out)), err)
	}
	m.log.Info("LUKS volume opened", "device", target.Device, "mapped", target.MappedName)
	return nil
}

// Close unmaps a LUKS volume.
func (m *Manager) Close(ctx context.Context, mappedName string) error {
	if strings.TrimSpace(mappedName) == "" {
		return fmt.Errorf("mapped name required")
	}
	if !validMappedName.MatchString(mappedName) {
		return fmt.Errorf("invalid mapped name %q: must contain only alphanumeric, dash, or underscore", mappedName)
	}
	out, err := m.cmd.Run(ctx, "cryptsetup", "luksClose", mappedName)
	if err != nil {
		return fmt.Errorf("cryptsetup luksClose %s: %s: %w", mappedName, strings.TrimSpace(string(out)), err)
	}
	m.log.Info("LUKS volume closed", "mapped", mappedName)
	return nil
}

// IsLUKS checks if a device contains a LUKS header.
func (m *Manager) IsLUKS(ctx context.Context, device string) bool {
	ok, err := m.IsLUKSWithError(ctx, device)
	if err != nil {
		m.log.Warn("isLUKS check failed", "device", device, "error", err)
	}
	return ok
}

// IsLUKSWithError checks if a device contains a LUKS header and returns errors.
func (m *Manager) IsLUKSWithError(ctx context.Context, device string) (bool, error) {
	if strings.TrimSpace(device) == "" {
		return false, fmt.Errorf("device required")
	}
	out, err := m.cmd.Run(ctx, "cryptsetup", "isLuks", device)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			// cryptsetup returns exit code 1 when device is not LUKS.
			return false, nil
		}
		return false, fmt.Errorf("cryptsetup isLuks %s: %s: %w", device, strings.TrimSpace(string(out)), err)
	}
	return true, nil
}
