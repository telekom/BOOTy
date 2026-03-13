package health

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunAll(t *testing.T) {
	t.Run("runs all checks", func(t *testing.T) {
		checks := []Check{
			&stubCheck{name: "a", sev: SeverityInfo, result: StatusPass},
			&stubCheck{name: "b", sev: SeverityWarning, result: StatusFail},
		}

		results, critical := RunAll(context.Background(), checks, "")
		if critical {
			t.Error("expected no critical failure")
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
		if results[0].Status != StatusPass {
			t.Errorf("check a: expected pass, got %s", results[0].Status)
		}
		if results[1].Status != StatusFail {
			t.Errorf("check b: expected fail, got %s", results[1].Status)
		}
	})

	t.Run("skips checks in skip list", func(t *testing.T) {
		checks := []Check{
			&stubCheck{name: "a", sev: SeverityInfo, result: StatusPass},
			&stubCheck{name: "b", sev: SeverityWarning, result: StatusFail},
		}

		results, critical := RunAll(context.Background(), checks, "b")
		if critical {
			t.Error("expected no critical failure")
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
		if results[1].Status != StatusSkip {
			t.Errorf("check b: expected skip, got %s", results[1].Status)
		}
	})

	t.Run("reports critical failure", func(t *testing.T) {
		checks := []Check{
			&stubCheck{name: "crit", sev: SeverityCritical, result: StatusFail},
		}

		_, critical := RunAll(context.Background(), checks, "")
		if !critical {
			t.Error("expected critical failure")
		}
	})

	t.Run("empty skip list", func(t *testing.T) {
		checks := []Check{
			&stubCheck{name: "a", sev: SeverityInfo, result: StatusPass},
		}

		results, _ := RunAll(context.Background(), checks, "")
		if results[0].Status != StatusPass {
			t.Errorf("expected pass, got %s", results[0].Status)
		}
	})
}

func TestParseSkipList(t *testing.T) {
	tests := []struct {
		input    string
		expected map[string]struct{}
	}{
		{"", map[string]struct{}{}},
		{"a,b", map[string]struct{}{"a": {}, "b": {}}},
		{" a , b , c ", map[string]struct{}{"a": {}, "b": {}, "c": {}}},
		{"disk-smart", map[string]struct{}{"disk-smart": {}}},
	}

	for _, tc := range tests {
		m := parseSkipList(tc.input)
		if len(m) != len(tc.expected) {
			t.Errorf("parseSkipList(%q): got %d entries, want %d", tc.input, len(m), len(tc.expected))
		}
		for k := range tc.expected {
			if _, ok := m[k]; !ok {
				t.Errorf("parseSkipList(%q): missing key %q", tc.input, k)
			}
		}
	}
}

func TestDiskPresenceCheck(t *testing.T) {
	t.Run("finds real disks", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "sda", "device"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "loop0"), 0o755); err != nil {
			t.Fatal(err)
		}

		c := &DiskPresenceCheck{SysBlockPath: dir}
		result := c.Run(context.Background())

		if result.Status != StatusPass {
			t.Errorf("expected pass, got %s: %s", result.Status, result.Message)
		}
		if result.Name != "disk-presence" {
			t.Errorf("expected name disk-presence, got %s", result.Name)
		}
	})

	t.Run("no disks found", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "loop0"), 0o755); err != nil {
			t.Fatal(err)
		}

		c := &DiskPresenceCheck{SysBlockPath: dir}
		result := c.Run(context.Background())

		if result.Status != StatusFail {
			t.Errorf("expected fail, got %s", result.Status)
		}
		if result.Severity != SeverityCritical {
			t.Errorf("expected critical severity, got %s", result.Severity)
		}
	})
}

func TestDiskSMARTCheck(t *testing.T) {
	t.Run("no errors", func(t *testing.T) {
		dir := t.TempDir()
		devDir := filepath.Join(dir, "sda", "device")
		if err := os.MkdirAll(devDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(devDir, "ioerr_cnt"), []byte("0x0\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		c := &DiskSMARTCheck{SysBlockPath: dir}
		result := c.Run(context.Background())

		if result.Status != StatusPass {
			t.Errorf("expected pass, got %s: %s", result.Status, result.Message)
		}
	})

	t.Run("with IO errors", func(t *testing.T) {
		dir := t.TempDir()
		devDir := filepath.Join(dir, "sda", "device")
		if err := os.MkdirAll(devDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(devDir, "ioerr_cnt"), []byte("0x5\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		c := &DiskSMARTCheck{SysBlockPath: dir}
		result := c.Run(context.Background())

		if result.Status != StatusFail {
			t.Errorf("expected fail, got %s", result.Status)
		}
	})
}

func TestMemoryECCCheck(t *testing.T) {
	t.Run("no EDAC directory", func(t *testing.T) {
		c := &MemoryECCCheck{EdacPath: "/nonexistent/path"}
		result := c.Run(context.Background())

		if result.Status != StatusPass {
			t.Errorf("expected pass (no EDAC), got %s", result.Status)
		}
	})

	t.Run("no errors", func(t *testing.T) {
		dir := t.TempDir()
		mcDir := filepath.Join(dir, "mc0")
		if err := os.MkdirAll(mcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(mcDir, "ue_count"), []byte("0\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		c := &MemoryECCCheck{EdacPath: dir}
		result := c.Run(context.Background())

		if result.Status != StatusPass {
			t.Errorf("expected pass, got %s: %s", result.Status, result.Message)
		}
	})

	t.Run("uncorrectable errors", func(t *testing.T) {
		dir := t.TempDir()
		mcDir := filepath.Join(dir, "mc0")
		if err := os.MkdirAll(mcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(mcDir, "ue_count"), []byte("3\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		c := &MemoryECCCheck{EdacPath: dir}
		result := c.Run(context.Background())

		if result.Status != StatusFail {
			t.Errorf("expected fail, got %s", result.Status)
		}
	})
}

func TestMinimumMemoryCheck(t *testing.T) {
	t.Run("sufficient memory", func(t *testing.T) {
		meminfo := filepath.Join(t.TempDir(), "meminfo")
		if err := os.WriteFile(meminfo, []byte("MemTotal:       16384000 kB\nMemFree:        8192000 kB\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		c := &MinimumMemoryCheck{MinGB: 8, ProcMemInfoPath: meminfo}
		result := c.Run(context.Background())

		if result.Status != StatusPass {
			t.Errorf("expected pass, got %s: %s", result.Status, result.Message)
		}
	})

	t.Run("insufficient memory", func(t *testing.T) {
		meminfo := filepath.Join(t.TempDir(), "meminfo")
		if err := os.WriteFile(meminfo, []byte("MemTotal:       4194304 kB\nMemFree:        2097152 kB\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		c := &MinimumMemoryCheck{MinGB: 8, ProcMemInfoPath: meminfo}
		result := c.Run(context.Background())

		if result.Status != StatusFail {
			t.Errorf("expected fail, got %s", result.Status)
		}
	})

	t.Run("no minimum configured", func(t *testing.T) {
		c := &MinimumMemoryCheck{MinGB: 0}
		result := c.Run(context.Background())

		if result.Status != StatusPass {
			t.Errorf("expected pass (no min), got %s", result.Status)
		}
	})
}

func TestMinimumCPUCheck(t *testing.T) {
	t.Run("sufficient CPUs", func(t *testing.T) {
		cpuinfo := filepath.Join(t.TempDir(), "cpuinfo")
		content := "processor\t: 0\nmodel name\t: Test CPU\n\nprocessor\t: 1\nmodel name\t: Test CPU\n\nprocessor\t: 2\nmodel name\t: Test CPU\n\nprocessor\t: 3\nmodel name\t: Test CPU\n"
		if err := os.WriteFile(cpuinfo, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		c := &MinimumCPUCheck{MinCPUs: 2, ProcCPUInfoPath: cpuinfo}
		result := c.Run(context.Background())

		if result.Status != StatusPass {
			t.Errorf("expected pass, got %s: %s", result.Status, result.Message)
		}
	})

	t.Run("insufficient CPUs", func(t *testing.T) {
		cpuinfo := filepath.Join(t.TempDir(), "cpuinfo")
		content := "processor\t: 0\nmodel name\t: Test CPU\n"
		if err := os.WriteFile(cpuinfo, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		c := &MinimumCPUCheck{MinCPUs: 4, ProcCPUInfoPath: cpuinfo}
		result := c.Run(context.Background())

		if result.Status != StatusFail {
			t.Errorf("expected fail, got %s", result.Status)
		}
	})

	t.Run("no minimum configured", func(t *testing.T) {
		c := &MinimumCPUCheck{MinCPUs: 0}
		result := c.Run(context.Background())

		if result.Status != StatusPass {
			t.Errorf("expected pass (no min), got %s", result.Status)
		}
	})
}

func TestNICLinkStateCheck(t *testing.T) {
	t.Run("all links up", func(t *testing.T) {
		dir := t.TempDir()
		eth0 := filepath.Join(dir, "eth0")
		if err := os.MkdirAll(eth0, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("/sys/devices/pci0/0000:00:1f.6", filepath.Join(eth0, "device")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(eth0, "carrier"), []byte("1\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		c := &NICLinkStateCheck{SysNetPath: dir}
		result := c.Run(context.Background())

		if result.Status != StatusPass {
			t.Errorf("expected pass, got %s: %s", result.Status, result.Message)
		}
	})

	t.Run("link down", func(t *testing.T) {
		dir := t.TempDir()
		eth0 := filepath.Join(dir, "eth0")
		if err := os.MkdirAll(eth0, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("/sys/devices/pci0/0000:00:1f.6", filepath.Join(eth0, "device")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(eth0, "carrier"), []byte("0\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		c := &NICLinkStateCheck{SysNetPath: dir}
		result := c.Run(context.Background())

		if result.Status != StatusFail {
			t.Errorf("expected fail, got %s", result.Status)
		}
	})

	t.Run("skips virtual interfaces", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "lo"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "docker0"), 0o755); err != nil {
			t.Fatal(err)
		}

		c := &NICLinkStateCheck{SysNetPath: dir}
		result := c.Run(context.Background())

		if result.Status != StatusPass {
			t.Errorf("expected pass (no physical NICs), got %s", result.Status)
		}
	})
}

func TestThermalStateCheck(t *testing.T) {
	t.Run("normal temperature", func(t *testing.T) {
		dir := t.TempDir()
		zone0 := filepath.Join(dir, "thermal_zone0")
		if err := os.MkdirAll(zone0, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(zone0, "temp"), []byte("45000\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		c := &ThermalStateCheck{SysThermalPath: dir}
		result := c.Run(context.Background())

		if result.Status != StatusPass {
			t.Errorf("expected pass, got %s: %s", result.Status, result.Message)
		}
	})

	t.Run("overheating", func(t *testing.T) {
		dir := t.TempDir()
		zone0 := filepath.Join(dir, "thermal_zone0")
		if err := os.MkdirAll(zone0, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(zone0, "temp"), []byte("98000\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		c := &ThermalStateCheck{SysThermalPath: dir}
		result := c.Run(context.Background())

		if result.Status != StatusFail {
			t.Errorf("expected fail, got %s", result.Status)
		}
	})

	t.Run("no thermal zones", func(t *testing.T) {
		c := &ThermalStateCheck{SysThermalPath: "/nonexistent"}
		result := c.Run(context.Background())

		if result.Status != StatusPass {
			t.Errorf("expected pass (no thermal info), got %s", result.Status)
		}
	})
}

type stubCheck struct {
	name   string
	sev    Severity
	result Status
}

func (s *stubCheck) Name() string      { return s.name }
func (s *stubCheck) Severity() Severity { return s.sev }
func (s *stubCheck) Run(_ context.Context) CheckResult {
	return CheckResult{
		Name:     s.name,
		Status:   s.result,
		Severity: s.sev,
		Message:  "stub",
	}
}
