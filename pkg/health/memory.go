package health

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// MemoryECCCheck verifies no uncorrectable ECC memory errors exist.
type MemoryECCCheck struct {
	// EdacPath allows overriding /sys/devices/system/edac for testing.
	EdacPath string
}

// Name returns the check identifier.
func (c *MemoryECCCheck) Name() string { return "memory-ecc" }

// Severity returns the check severity level.
func (c *MemoryECCCheck) Severity() Severity { return SeverityCritical }

func (c *MemoryECCCheck) edacPath() string {
	if c.EdacPath != "" {
		return c.EdacPath
	}
	return "/sys/devices/system/edac/mc"
}

// Run executes the ECC memory error check.
func (c *MemoryECCCheck) Run(_ context.Context) CheckResult {
	entries, err := os.ReadDir(c.edacPath())
	if err != nil {
		// No EDAC support — not an error, just info.
		return CheckResult{
			Name:     c.Name(),
			Status:   StatusPass,
			Severity: c.Severity(),
			Message:  "EDAC not available, skipping ECC check",
		}
	}

	var totalUE int
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "mc") {
			continue
		}
		ueFile := c.edacPath() + "/" + e.Name() + "/ue_count"
		data, err := os.ReadFile(ueFile)
		if err != nil {
			continue
		}
		count, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			continue
		}
		totalUE += count
	}

	if totalUE > 0 {
		return CheckResult{
			Name:     c.Name(),
			Status:   StatusFail,
			Severity: c.Severity(),
			Message:  fmt.Sprintf("%d uncorrectable ECC error(s) detected", totalUE),
		}
	}

	return CheckResult{
		Name:     c.Name(),
		Status:   StatusPass,
		Severity: c.Severity(),
		Message:  "no uncorrectable ECC errors",
	}
}

// MinimumMemoryCheck verifies the system has at least a minimum amount of RAM.
type MinimumMemoryCheck struct {
	MinGB int
	// ProcMemInfoPath allows overriding /proc/meminfo for testing.
	ProcMemInfoPath string
}

// Name returns the check identifier.
func (c *MinimumMemoryCheck) Name() string { return "minimum-memory" }

// Severity returns the check severity level.
func (c *MinimumMemoryCheck) Severity() Severity { return SeverityCritical }

func (c *MinimumMemoryCheck) memInfoPath() string {
	if c.ProcMemInfoPath != "" {
		return c.ProcMemInfoPath
	}
	return "/proc/meminfo"
}

// Run executes the minimum memory check.
func (c *MinimumMemoryCheck) Run(_ context.Context) CheckResult {
	if c.MinGB <= 0 {
		return CheckResult{
			Name:     c.Name(),
			Status:   StatusPass,
			Severity: c.Severity(),
			Message:  "no minimum memory requirement configured",
		}
	}

	totalKB, err := readMemTotal(c.memInfoPath())
	if err != nil {
		return CheckResult{
			Name:     c.Name(),
			Status:   StatusFail,
			Severity: c.Severity(),
			Message:  "cannot read memory info",
			Details:  err.Error(),
		}
	}

	totalGB := totalKB / (1024 * 1024)
	if totalGB < int64(c.MinGB) {
		return CheckResult{
			Name:     c.Name(),
			Status:   StatusFail,
			Severity: c.Severity(),
			Message:  fmt.Sprintf("insufficient memory: %d GB < %d GB minimum", totalGB, c.MinGB),
		}
	}

	return CheckResult{
		Name:     c.Name(),
		Status:   StatusPass,
		Severity: c.Severity(),
		Message:  fmt.Sprintf("memory OK: %d GB >= %d GB minimum", totalGB, c.MinGB),
	}
}

// readMemTotal reads MemTotal from a meminfo-formatted file.
func readMemTotal(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // best-effort close on read-only file

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				v, err := strconv.ParseInt(fields[1], 10, 64)
				if err != nil {
					return 0, fmt.Errorf("parse MemTotal: %w", err)
				}
				return v, nil
			}
		}
	}
	return 0, fmt.Errorf("MemTotal not found in %s", path)
}
