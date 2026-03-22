//go:build linux

package secureboot

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// MOKEnroller handles Machine Owner Key enrollment.
type MOKEnroller struct {
	certPath string
	password string
}

// NewMOKEnroller creates a MOK enroller with the certificate path and one-time password.
func NewMOKEnroller(certPath, password string) *MOKEnroller {
	return &MOKEnroller{certPath: certPath, password: password}
}

// Enroll enrolls a MOK certificate for the next reboot using mokutil.
func (m *MOKEnroller) Enroll() error {
	if m.certPath == "" {
		return fmt.Errorf("mok certificate path is empty")
	}
	if _, err := os.Stat(m.certPath); err != nil {
		return fmt.Errorf("mok certificate not found: %w", err)
	}

	slog.Info("enrolling mok certificate", "cert", m.certPath)
	args := []string{"--import", m.certPath}
	if m.password != "" {
		args = append(args, "--root-pw", "--simple-hash")
	}
	cmd := exec.CommandContext(context.Background(), "mokutil", args...) //nolint:gosec // trusted cert path
	if m.password != "" {
		// mokutil reads the password from stdin when --root-pw is used.
		cmd.Stdin = strings.NewReader(m.password + "\n" + m.password + "\n")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mokutil enroll: %s: %w", strings.TrimSpace(string(out)), err)
	}
	slog.Info("mok enrollment queued for next reboot")
	return nil
}

// IsEnrolled checks if the MOK certificate is pending or already enrolled
// by using mokutil --test-key, which directly validates the key against the MOK list.
func (m *MOKEnroller) IsEnrolled() (bool, error) {
	if m.certPath == "" {
		return false, fmt.Errorf("mok certificate path is empty")
	}
	if _, err := os.Stat(m.certPath); err != nil {
		return false, fmt.Errorf("mok certificate not found: %w", err)
	}
	out, err := exec.CommandContext(context.Background(), "mokutil", "--test-key", m.certPath).CombinedOutput() //nolint:gosec // trusted cert path
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
