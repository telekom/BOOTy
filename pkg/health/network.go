package health

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NICLinkStateCheck verifies all physical NICs have link up.
type NICLinkStateCheck struct {
	// SysNetPath allows overriding /sys/class/net for testing.
	SysNetPath string
}

// Name returns the check identifier.
func (c *NICLinkStateCheck) Name() string { return "nic-link-state" }

// Severity returns the check severity level.
func (c *NICLinkStateCheck) Severity() Severity { return SeverityWarning }

func (c *NICLinkStateCheck) sysPath() string {
	if c.SysNetPath != "" {
		return c.SysNetPath
	}
	return "/sys/class/net"
}

// Run executes the NIC link state check.
func (c *NICLinkStateCheck) Run(_ context.Context) CheckResult {
	entries, err := os.ReadDir(c.sysPath())
	if err != nil {
		return CheckResult{
			Name:     c.Name(),
			Status:   StatusSkip,
			Severity: c.Severity(),
			Message:  fmt.Sprintf("cannot read %s", c.sysPath()),
			Details:  err.Error(),
		}
	}

	var down []string
	physCount := 0
	for _, e := range entries {
		name := e.Name()
		if name == "lo" || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") {
			continue
		}

		// Check if it's a physical device (has a /device symlink).
		deviceLink := filepath.Join(c.sysPath(), name, "device")
		if _, err := os.Lstat(deviceLink); err != nil {
			continue
		}
		physCount++

		// Read carrier state.
		carrierFile := filepath.Join(c.sysPath(), name, "carrier")
		data, err := os.ReadFile(carrierFile)
		if err != nil {
			down = append(down, name)
			continue
		}
		if strings.TrimSpace(string(data)) != "1" {
			down = append(down, name)
		}
	}

	if len(down) > 0 {
		return CheckResult{
			Name:     c.Name(),
			Status:   StatusFail,
			Severity: c.Severity(),
			Message:  fmt.Sprintf("%d/%d NIC(s) link down", len(down), physCount),
			Details:  strings.Join(down, ", "),
		}
	}

	return CheckResult{
		Name:     c.Name(),
		Status:   StatusPass,
		Severity: c.Severity(),
		Message:  fmt.Sprintf("all %d physical NIC(s) link up", physCount),
	}
}
