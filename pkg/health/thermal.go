package health

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ThermalStateCheck verifies CPU thermal zones are within safe limits.
type ThermalStateCheck struct {
	// SysThermalPath allows overriding /sys/class/thermal for testing.
	SysThermalPath string
	// MaxTempMilliC is the maximum temperature in milli-degrees Celsius (default: 95000 = 95°C).
	MaxTempMilliC int
}

// Name returns the check identifier.
func (c *ThermalStateCheck) Name() string { return "thermal-state" }

// Severity returns the check severity level.
func (c *ThermalStateCheck) Severity() Severity { return SeverityWarning }

func (c *ThermalStateCheck) sysPath() string {
	if c.SysThermalPath != "" {
		return c.SysThermalPath
	}
	return "/sys/class/thermal"
}

func (c *ThermalStateCheck) maxTemp() int {
	if c.MaxTempMilliC > 0 {
		return c.MaxTempMilliC
	}
	return 95000 // 95°C
}

// Run executes the thermal zone check.
func (c *ThermalStateCheck) Run(_ context.Context) CheckResult {
	entries, err := os.ReadDir(c.sysPath())
	if err != nil {
		return CheckResult{
			Name:     c.Name(),
			Status:   StatusPass,
			Severity: c.Severity(),
			Message:  "thermal zone info not available",
		}
	}

	var hot []string
	maxTemp := c.maxTemp()
	checked := 0

	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "thermal_zone") {
			continue
		}
		checked++

		tempFile := filepath.Join(c.sysPath(), e.Name(), "temp")
		data, err := os.ReadFile(tempFile)
		if err != nil {
			continue
		}

		temp, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			continue
		}

		if temp > maxTemp {
			hot = append(hot, fmt.Sprintf("%s: %d°C", e.Name(), temp/1000))
		}
	}

	if len(hot) > 0 {
		return CheckResult{
			Name:     c.Name(),
			Status:   StatusFail,
			Severity: c.Severity(),
			Message:  fmt.Sprintf("%d thermal zone(s) above %d°C", len(hot), maxTemp/1000),
			Details:  strings.Join(hot, "; "),
		}
	}

	return CheckResult{
		Name:     c.Name(),
		Status:   StatusPass,
		Severity: c.Severity(),
		Message:  fmt.Sprintf("all %d thermal zone(s) within limits", checked),
	}
}
