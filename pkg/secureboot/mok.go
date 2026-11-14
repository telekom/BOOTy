//go:build linux

package secureboot

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	exec "github.com/telekom/BOOTy/pkg/executil"
)

// mokutilCommand builds the mokutil exec command, inside chroot unless it
// is empty; overridable in tests.
var mokutilCommand = func(ctx context.Context, chroot string, args ...string) *exec.Cmd {
	if chroot != "" {
		return exec.CommandContext(ctx, "chroot", append([]string{chroot, "mokutil"}, args...)...) //nolint:gosec // trusted root and cert path
	}
	return exec.CommandContext(ctx, "mokutil", args...) //nolint:gosec // trusted cert path
}

// MOKEnroller handles Machine Owner Key enrollment.
type MOKEnroller struct {
	certPath string
	password string
	chroot   string
}

// NewMOKEnroller creates a MOK enroller with the certificate path and one-time password.
func NewMOKEnroller(certPath, password string) *MOKEnroller {
	return &MOKEnroller{certPath: certPath, password: password}
}

// InChroot runs mokutil in root, the installed system with /run and
// efivarfs bound: the BOOTy ramdisks do not ship mokutil, the targets that
// boot through shim do. The certificate path must be the same inside and
// outside, as for a file in /run.
func (m *MOKEnroller) InChroot(root string) *MOKEnroller {
	m.chroot = root
	return m
}

// Enroll enrolls a MOK certificate for the next reboot using mokutil.
func (m *MOKEnroller) Enroll(ctx context.Context) error {
	if m.certPath == "" {
		return fmt.Errorf("mok certificate path is empty")
	}
	if _, err := os.Stat(m.certPath); err != nil {
		return fmt.Errorf("mok certificate not found: %w", err)
	}

	slog.Info("enrolling mok certificate", "cert", m.certPath)
	if m.password == "" {
		return fmt.Errorf("mok enrollment needs a one-time password for MokManager")
	}
	// mokutil asks for the one-time password twice; it reads it from stdin
	// when stdin is not a terminal.
	cmd := mokutilCommand(ctx, m.chroot, "--import", m.certPath)
	cmd.Stdin = strings.NewReader(m.password + "\n" + m.password + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mokutil enroll: %s: %w", strings.TrimSpace(string(out)), err)
	}
	slog.Info("mok enrollment queued for next reboot")
	return nil
}

// IsEnrolled checks if the MOK certificate is pending or already enrolled
// by using mokutil --test-key, which directly validates the key against the MOK list.
func (m *MOKEnroller) IsEnrolled(ctx context.Context) (bool, error) {
	if m.certPath == "" {
		return false, fmt.Errorf("mok certificate path is empty")
	}
	if _, err := os.Stat(m.certPath); err != nil {
		return false, fmt.Errorf("mok certificate not found: %w", err)
	}
	out, err := mokutilCommand(ctx, m.chroot, "--test-key", m.certPath).CombinedOutput()
	if err != nil {
		// mokutil --test-key exits non-zero when the key is not enrolled.
		outStr := strings.TrimSpace(string(out))
		if strings.Contains(outStr, "is not enrolled") {
			return false, nil
		}
		return false, fmt.Errorf("mokutil test-key: %s: %w", outStr, err)
	}
	return true, nil
}
