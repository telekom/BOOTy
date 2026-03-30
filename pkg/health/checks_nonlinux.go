//go:build !linux

package health

import "context"

// MinimumCPUCheck verifies the system has at least a minimum number of CPU cores.
type MinimumCPUCheck struct {
	MinCPUs         int
	ProcCPUInfoPath string
}

// Name returns the check identifier.
func (c *MinimumCPUCheck) Name() string { return "minimum-cpu" }

// Severity returns the check severity level.
func (c *MinimumCPUCheck) Severity() Severity { return SeverityCritical }

// Run returns a skipped result on non-linux platforms.
func (c *MinimumCPUCheck) Run(_ context.Context) CheckResult {
	return CheckResult{
		Name:     c.Name(),
		Status:   StatusSkip,
		Severity: c.Severity(),
		Message:  "unsupported on non-linux platform",
	}
}

// DiskIOErrorCheck verifies SCSI I/O error counters for block devices.
type DiskIOErrorCheck struct {
	SysBlockPath string
}

// Name returns the check identifier.
func (c *DiskIOErrorCheck) Name() string { return "disk-ioerr" }

// Severity returns the check severity level.
func (c *DiskIOErrorCheck) Severity() Severity { return SeverityWarning }

// Run returns a skipped result on non-linux platforms.
func (c *DiskIOErrorCheck) Run(_ context.Context) CheckResult {
	return CheckResult{
		Name:     c.Name(),
		Status:   StatusSkip,
		Severity: c.Severity(),
		Message:  "unsupported on non-linux platform",
	}
}

// DiskPresenceCheck verifies at least one disk is present.
type DiskPresenceCheck struct {
	SysBlockPath string
}

// Name returns the check identifier.
func (c *DiskPresenceCheck) Name() string { return "disk-presence" }

// Severity returns the check severity level.
func (c *DiskPresenceCheck) Severity() Severity { return SeverityCritical }

// Run returns a skipped result on non-linux platforms.
func (c *DiskPresenceCheck) Run(_ context.Context) CheckResult {
	return CheckResult{
		Name:     c.Name(),
		Status:   StatusSkip,
		Severity: c.Severity(),
		Message:  "unsupported on non-linux platform",
	}
}

// MemoryECCCheck verifies no uncorrectable ECC memory errors exist.
type MemoryECCCheck struct {
	EdacPath string
}

// Name returns the check identifier.
func (c *MemoryECCCheck) Name() string { return "memory-ecc" }

// Severity returns the check severity level.
func (c *MemoryECCCheck) Severity() Severity { return SeverityCritical }

// Run returns a skipped result on non-linux platforms.
func (c *MemoryECCCheck) Run(_ context.Context) CheckResult {
	return CheckResult{
		Name:     c.Name(),
		Status:   StatusSkip,
		Severity: c.Severity(),
		Message:  "unsupported on non-linux platform",
	}
}

// MinimumMemoryCheck verifies the system has at least a minimum amount of RAM.
type MinimumMemoryCheck struct {
	MinGiB          int
	ProcMemInfoPath string
}

// Name returns the check identifier.
func (c *MinimumMemoryCheck) Name() string { return "minimum-memory" }

// Severity returns the check severity level.
func (c *MinimumMemoryCheck) Severity() Severity { return SeverityCritical }

// Run returns a skipped result on non-linux platforms.
func (c *MinimumMemoryCheck) Run(_ context.Context) CheckResult {
	return CheckResult{
		Name:     c.Name(),
		Status:   StatusSkip,
		Severity: c.Severity(),
		Message:  "unsupported on non-linux platform",
	}
}

// NICLinkStateCheck verifies all physical NICs have link up.
type NICLinkStateCheck struct {
	SysNetPath string
}

// Name returns the check identifier.
func (c *NICLinkStateCheck) Name() string { return "nic-link-state" }

// Severity returns the check severity level.
func (c *NICLinkStateCheck) Severity() Severity { return SeverityWarning }

// Run returns a skipped result on non-linux platforms.
func (c *NICLinkStateCheck) Run(_ context.Context) CheckResult {
	return CheckResult{
		Name:     c.Name(),
		Status:   StatusSkip,
		Severity: c.Severity(),
		Message:  "unsupported on non-linux platform",
	}
}

// ThermalStateCheck verifies CPU thermal zones are within safe limits.
type ThermalStateCheck struct {
	SysThermalPath string
	MaxTempMilliC  int
}

// Name returns the check identifier.
func (c *ThermalStateCheck) Name() string { return "thermal-state" }

// Severity returns the check severity level.
func (c *ThermalStateCheck) Severity() Severity { return SeverityWarning }

// Run returns a skipped result on non-linux platforms.
func (c *ThermalStateCheck) Run(_ context.Context) CheckResult {
	return CheckResult{
		Name:     c.Name(),
		Status:   StatusSkip,
		Severity: c.Severity(),
		Message:  "unsupported on non-linux platform",
	}
}
