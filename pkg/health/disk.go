package health

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DiskSMARTCheck verifies SMART health status for all block devices.
type DiskSMARTCheck struct {
	// SysBlockPath allows overriding /sys/block for testing.
	SysBlockPath string
}

// Name returns the check identifier.
func (c *DiskSMARTCheck) Name() string { return "disk-smart" }

// Severity returns the check severity level.
func (c *DiskSMARTCheck) Severity() Severity { return SeverityWarning }

func (c *DiskSMARTCheck) sysPath() string {
	if c.SysBlockPath != "" {
		return c.SysBlockPath
	}
	return "/sys/block"
}

// Run executes the disk SMART health check.
func (c *DiskSMARTCheck) Run(_ context.Context) CheckResult {
	entries, err := os.ReadDir(c.sysPath())
	if err != nil {
		return CheckResult{
			Name:     c.Name(),
			Status:   StatusFail,
			Severity: c.Severity(),
			Message:  "cannot read /sys/block",
			Details:  err.Error(),
		}
	}

	var warnings []string
	checked := 0
	for _, e := range entries {
		// Skip virtual devices (loop, ram, dm-).
		name := e.Name()
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "dm-") {
			continue
		}

		// SMART health is exposed via /sys/block/<dev>/device/state or
		// /sys/block/<dev>/device/delete (presence indicates a real device).
		deviceDir := filepath.Join(c.sysPath(), name, "device")
		if _, err := os.Stat(deviceDir); err != nil {
			continue // not a real device
		}

		// Check for SMART errors via ioerr_cnt if available.
		errCnt := filepath.Join(deviceDir, "ioerr_cnt")
		data, err := os.ReadFile(errCnt)
		if err != nil {
			continue // ioerr_cnt not available, skip
		}
		checked++
		count := strings.TrimSpace(string(data))
		if count != "0x0" && count != "0" {
			warnings = append(warnings, fmt.Sprintf("%s: ioerr_cnt=%s", name, count))
		}
	}

	if len(warnings) > 0 {
		return CheckResult{
			Name:     c.Name(),
			Status:   StatusFail,
			Severity: c.Severity(),
			Message:  fmt.Sprintf("%d disk(s) with IO errors", len(warnings)),
			Details:  strings.Join(warnings, "; "),
		}
	}

	return CheckResult{
		Name:     c.Name(),
		Status:   StatusPass,
		Severity: c.Severity(),
		Message:  fmt.Sprintf("checked %d disk(s), no IO errors", checked),
	}
}

// DiskPresenceCheck verifies at least one non-virtual block device exists.
type DiskPresenceCheck struct {
	SysBlockPath string
}

// Name returns the check identifier.
func (c *DiskPresenceCheck) Name() string { return "disk-presence" }

// Severity returns the check severity level.
func (c *DiskPresenceCheck) Severity() Severity { return SeverityCritical }

func (c *DiskPresenceCheck) sysPath() string {
	if c.SysBlockPath != "" {
		return c.SysBlockPath
	}
	return "/sys/block"
}

// Run executes the disk presence check.
func (c *DiskPresenceCheck) Run(_ context.Context) CheckResult {
	entries, err := os.ReadDir(c.sysPath())
	if err != nil {
		return CheckResult{
			Name:     c.Name(),
			Status:   StatusFail,
			Severity: c.Severity(),
			Message:  "cannot read /sys/block",
			Details:  err.Error(),
		}
	}

	var realDisks []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "dm-") {
			continue
		}
		deviceDir := filepath.Join(c.sysPath(), name, "device")
		if _, err := os.Stat(deviceDir); err == nil {
			realDisks = append(realDisks, name)
		}
	}

	if len(realDisks) == 0 {
		return CheckResult{
			Name:     c.Name(),
			Status:   StatusFail,
			Severity: c.Severity(),
			Message:  "no physical disks found",
		}
	}

	return CheckResult{
		Name:     c.Name(),
		Status:   StatusPass,
		Severity: c.Severity(),
		Message:  fmt.Sprintf("found %d disk(s): %s", len(realDisks), strings.Join(realDisks, ", ")),
	}
}
