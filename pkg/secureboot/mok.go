//go:build linux

package secureboot

import (
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
	out, err := exec.Command("mokutil", args...).CombinedOutput() //nolint:gosec // trusted cert path
	if err != nil {
		return fmt.Errorf("mokutil enroll: %s: %w", strings.TrimSpace(string(out)), err)
	}
	slog.Info("mok enrollment queued for next reboot")
	return nil
}

// IsEnrolled checks if the MOK certificate is already enrolled.
func (m *MOKEnroller) IsEnrolled() (bool, error) {
	out, err := exec.Command("mokutil", "--list-enrolled").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("mokutil list-enrolled: %w", err)
	}
	// Simple heuristic: check if cert filename appears in output
	return strings.Contains(string(out), m.certPath), nil
}
