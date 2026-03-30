//go:build linux

package health

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// MinimumCPUCheck verifies the system has at least a minimum number of CPU cores.
type MinimumCPUCheck struct {
	MinCPUs int
	// ProcCPUInfoPath allows overriding /proc/cpuinfo for testing.
	ProcCPUInfoPath string
}

// Name returns the check identifier.
func (c *MinimumCPUCheck) Name() string { return "minimum-cpu" }

// Severity returns the check severity level.
func (c *MinimumCPUCheck) Severity() Severity { return SeverityCritical }

func (c *MinimumCPUCheck) cpuInfoPath() string {
	if c.ProcCPUInfoPath != "" {
		return c.ProcCPUInfoPath
	}
	return "/proc/cpuinfo"
}

// Run executes the minimum CPU check.
func (c *MinimumCPUCheck) Run(_ context.Context) CheckResult {
	if c.MinCPUs <= 0 {
		return CheckResult{
			Name:     c.Name(),
			Status:   StatusPass,
			Severity: c.Severity(),
			Message:  "no minimum CPU requirement configured",
		}
	}

	count, err := countProcessors(c.cpuInfoPath())
	if err != nil {
		return CheckResult{
			Name:     c.Name(),
			Status:   StatusFail,
			Severity: c.Severity(),
			Message:  "cannot read CPU info",
			Details:  err.Error(),
		}
	}

	if count < c.MinCPUs {
		return CheckResult{
			Name:     c.Name(),
			Status:   StatusFail,
			Severity: c.Severity(),
			Message:  fmt.Sprintf("insufficient CPUs: %d < %d minimum", count, c.MinCPUs),
		}
	}

	return CheckResult{
		Name:     c.Name(),
		Status:   StatusPass,
		Severity: c.Severity(),
		Message:  fmt.Sprintf("CPUs OK: %d >= %d minimum", count, c.MinCPUs),
	}
}

// countProcessors counts "processor" lines in /proc/cpuinfo.
func countProcessors(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // best-effort close on read-only file

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "processor") {
			continue
		}
		// Verify it's "processor\t: N"
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if _, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		return count, fmt.Errorf("scan %s: %w", path, err)
	}
	return count, nil
}
