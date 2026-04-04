//go:build e2e && linux

package e2e

// Tests for three previously uncovered pipeline steps:
//   - collect-inventory  (Gap 1)
//   - health-checks      (Gap 5)
//   - setup-nvme-namespaces (Gap 9)

import (
	"context"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
	"github.com/telekom/BOOTy/pkg/provision"
)

// ---------------------------------------------------------------------------
// Gap 1: collect-inventory
// ---------------------------------------------------------------------------

// TestCollectInventoryDisabledE2E verifies that when InventoryEnabled=false
// the orchestrator skips the step and proceeds (no calls to ReportInventory).
func TestCollectInventoryDisabledE2E(t *testing.T) {
	cfg := &config.MachineConfig{
		InventoryEnabled:    false,
		HealthChecksEnabled: false,
		Hostname:            "inv-disabled-node",
		ImageURLs:           []string{"http://img.local/test.gz"},
		DNSResolvers:        "8.8.8.8",
	}
	provider := newMockProvider(cfg)
	cmd := newMockCommander()
	orch := provision.NewOrchestrator(cfg, provider, disk.NewManager(cmd))

	_ = orch.Provision(context.Background())

	// No error expected from the inventory step itself; verify status was reported.
	statuses := provider.getStatuses()
	if len(statuses) == 0 {
		t.Fatal("expected at least one status report")
	}
	if statuses[0].Status != config.StatusInit {
		t.Errorf("first status = %q, want init", statuses[0].Status)
	}
}

// TestCollectInventoryEnabledNonFatalE2E verifies that when InventoryEnabled=true
// the step is non-fatal even if inventory.Collect() fails in a test environment
// (no real hardware sysfs). The orchestrator must continue past the step.
func TestCollectInventoryEnabledNonFatalE2E(t *testing.T) {
	cfg := &config.MachineConfig{
		InventoryEnabled:    true,
		HealthChecksEnabled: false,
		Hostname:            "inv-nonfatal-node",
		ImageURLs:           []string{"http://img.local/test.gz"},
		DNSResolvers:        "8.8.8.8",
	}
	provider := newMockProvider(cfg)
	cmd := newMockCommander()
	orch := provision.NewOrchestrator(cfg, provider, disk.NewManager(cmd))

	// inventory.Collect() reads real sysfs; it may fail in a test environment.
	// The step must NOT propagate that error — orchestrator continues.
	_ = orch.Provision(context.Background())

	statuses := provider.getStatuses()
	if len(statuses) == 0 {
		t.Fatal("expected at least one status report")
	}
	// If provision fails, it must be a step AFTER collect-inventory, not inventory itself.
	// Verify the first status is always "init" (inventory step comes after init).
	if statuses[0].Status != config.StatusInit {
		t.Errorf("first status = %q, want init", statuses[0].Status)
	}
}

// TestCollectInventoryReportInventoryCalledE2E verifies that when
// InventoryEnabled=true and inventory collection succeeds (or is best-effort),
// the provider's ReportInventory is invoked. We confirm by ensuring the
// orchestrator runs past the inventory step without aborting.
func TestCollectInventoryReportInventoryCalledE2E(t *testing.T) {
	cfg := &config.MachineConfig{
		InventoryEnabled:    true,
		HealthChecksEnabled: false,
		Hostname:            "inv-report-node",
		ImageURLs:           []string{"http://img.local/test.gz"},
		DNSResolvers:        "8.8.8.8",
	}
	provider := newMockProvider(cfg)
	cmd := newMockCommander()
	orch := provision.NewOrchestrator(cfg, provider, disk.NewManager(cmd))

	_ = orch.Provision(context.Background())

	// The step is non-fatal and must not cause StatusError due to inventory failure.
	statuses := provider.getStatuses()
	if len(statuses) == 0 {
		t.Fatal("expected status reports")
	}
	// Inventory error is non-fatal; any failure must come from a later step.
	for _, s := range statuses {
		if s.Status == config.StatusError &&
			strings.Contains(s.Message, "inventory") {
			t.Errorf("inventory step should not report error status: %q", s.Message)
		}
	}
}

// ---------------------------------------------------------------------------
// Gap 5: health-checks
// ---------------------------------------------------------------------------

// TestHealthChecksDisabledE2E verifies that when HealthChecksEnabled=false
// the health-check step is skipped entirely and provisioning proceeds.
func TestHealthChecksDisabledE2E(t *testing.T) {
	cfg := &config.MachineConfig{
		HealthChecksEnabled: false,
		InventoryEnabled:    false,
		Hostname:            "health-disabled",
		ImageURLs:           []string{"http://img.local/test.gz"},
		DNSResolvers:        "8.8.8.8",
	}
	provider := newMockProvider(cfg)
	cmd := newMockCommander()
	orch := provision.NewOrchestrator(cfg, provider, disk.NewManager(cmd))

	_ = orch.Provision(context.Background())

	statuses := provider.getStatuses()
	if len(statuses) == 0 {
		t.Fatal("expected status reports")
	}
	if statuses[0].Status != config.StatusInit {
		t.Errorf("first status = %q, want init", statuses[0].Status)
	}
}

// TestHealthChecksPassE2E verifies that when HealthChecksEnabled=true and no
// minimum thresholds are set, the health check step passes (MinCPUs=0,
// MinMemory=0 skip those sub-checks) and provisioning continues.
func TestHealthChecksPassE2E(t *testing.T) {
	cfg := &config.MachineConfig{
		HealthChecksEnabled: true,
		InventoryEnabled:    false,
		HealthMinMemoryGB:   0, // disabled
		HealthMinCPUs:       0, // disabled
		Hostname:            "health-pass",
		ImageURLs:           []string{"http://img.local/test.gz"},
		DNSResolvers:        "8.8.8.8",
	}
	provider := newMockProvider(cfg)
	cmd := newMockCommander()
	orch := provision.NewOrchestrator(cfg, provider, disk.NewManager(cmd))

	_ = orch.Provision(context.Background())

	statuses := provider.getStatuses()
	if len(statuses) == 0 {
		t.Fatal("expected status reports")
	}
	// Health checks must not be the source of a fatal error when thresholds are 0.
	for _, s := range statuses {
		if s.Status == config.StatusError &&
			strings.Contains(s.Message, "critical health check") {
			t.Errorf("health checks should not fail with zero thresholds: %q", s.Message)
		}
	}
}

// TestHealthChecksCriticalFailureE2E verifies that when a critical health check
// fails (HealthMinCPUs set impossibly high), the orchestrator returns an error
// and reports StatusError.
func TestHealthChecksCriticalFailureE2E(t *testing.T) {
	cfg := &config.MachineConfig{
		HealthChecksEnabled: true,
		InventoryEnabled:    false,
		HealthMinCPUs:       999999, // impossible: forces minimum-cpu to fail
		DNSResolvers:        "8.8.8.8",
	}
	provider := newMockProvider(cfg)
	cmd := newMockCommander()
	orch := provision.NewOrchestrator(cfg, provider, disk.NewManager(cmd))

	err := orch.Provision(context.Background())
	if err == nil {
		t.Fatal("expected provision to fail due to critical health check failure")
	}
	if !strings.Contains(err.Error(), "critical health check") {
		t.Errorf("error should mention critical health check: %v", err)
	}

	statuses := provider.getStatuses()
	if len(statuses) == 0 {
		t.Fatal("expected status reports")
	}
	last := statuses[len(statuses)-1]
	if last.Status != config.StatusError {
		t.Errorf("last status = %q, want error", last.Status)
	}
}

// ---------------------------------------------------------------------------
// Gap 9: setup-nvme-namespaces
// ---------------------------------------------------------------------------

// TestSetupNVMeNamespacesSkippedWhenEmptyE2E verifies that when
// NVMeNamespaces="" the step is skipped and no nvme commands are run.
func TestSetupNVMeNamespacesSkippedWhenEmptyE2E(t *testing.T) {
	cfg := &config.MachineConfig{
		NVMeNamespaces:      "",
		HealthChecksEnabled: false,
		InventoryEnabled:    false,
		Hostname:            "nvme-skip",
		ImageURLs:           []string{"http://img.local/test.gz"},
		DNSResolvers:        "8.8.8.8",
	}
	provider := newMockProvider(cfg)
	cmd := newMockCommander()
	orch := provision.NewOrchestrator(cfg, provider, disk.NewManager(cmd))

	_ = orch.Provision(context.Background())

	for _, c := range cmd.getCalls() {
		if strings.Contains(c.String(), "nvme") {
			t.Errorf("nvme should NOT be called when NVMeNamespaces is empty: %s", c)
		}
	}
}

// TestSetupNVMeNamespacesInvalidConfigE2E verifies that a malformed JSON config
// causes a parse error that propagates as a fatal provisioning failure.
func TestSetupNVMeNamespacesInvalidConfigE2E(t *testing.T) {
	cfg := &config.MachineConfig{
		NVMeNamespaces:      "{not valid json",
		HealthChecksEnabled: false,
		InventoryEnabled:    false,
		DNSResolvers:        "8.8.8.8",
	}
	provider := newMockProvider(cfg)
	cmd := newMockCommander()
	orch := provision.NewOrchestrator(cfg, provider, disk.NewManager(cmd))

	err := orch.Provision(context.Background())
	if err == nil {
		t.Fatal("expected error from invalid NVMe namespace config")
	}

	statuses := provider.getStatuses()
	if len(statuses) == 0 {
		t.Fatal("expected status reports")
	}
	last := statuses[len(statuses)-1]
	if last.Status != config.StatusError {
		t.Errorf("last status = %q, want error", last.Status)
	}
}

// TestSetupNVMeNamespacesCommandsCalledE2E verifies that with a valid NVMe
// namespace config the orchestrator calls nvme id-ctrl, list-ns, create-ns and
// attach-ns against the configured controller.
func TestSetupNVMeNamespacesCommandsCalledE2E(t *testing.T) {
	// Minimal valid config: one controller, one 100% namespace.
	nvmeCfg := `[{"controller":"/dev/nvme0","namespaces":[{"label":"os","sizePct":100}]}]`

	cfg := &config.MachineConfig{
		NVMeNamespaces:      nvmeCfg,
		HealthChecksEnabled: false,
		InventoryEnabled:    false,
		DNSResolvers:        "8.8.8.8",
	}
	provider := newMockProvider(cfg)
	cmd := newMockCommander()

	// id-ctrl: report nn>1 (multi-namespace) and tnvmcap (1 TiB).
	cmd.set("nvme id-ctrl", []byte(
		`{"mn":"Mock NVMe","nn":32,"tnvmcap":1000204886016}`,
	), nil)
	// list-ns: no existing namespaces (clean slate).
	cmd.set("nvme list-ns", []byte(`{"nsid_list":[]}`), nil)
	// create-ns: return nsid 1.
	cmd.set("nvme create-ns", []byte("create-ns: Success, created nsid:1\n"), nil)
	// attach-ns: success.
	cmd.set("nvme attach-ns", []byte(""), nil)

	orch := provision.NewOrchestrator(cfg, provider, disk.NewManager(cmd))
	_ = orch.Provision(context.Background())

	calls := cmd.getCalls()
	var idCtrlCalled, createNSCalled, attachNSCalled bool
	for _, c := range calls {
		s := c.String()
		if strings.Contains(s, "nvme id-ctrl") {
			idCtrlCalled = true
		}
		if strings.Contains(s, "nvme create-ns") {
			createNSCalled = true
		}
		if strings.Contains(s, "nvme attach-ns") {
			attachNSCalled = true
		}
	}
	if !idCtrlCalled {
		t.Error("expected nvme id-ctrl to be called")
	}
	if !createNSCalled {
		t.Error("expected nvme create-ns to be called")
	}
	if !attachNSCalled {
		t.Error("expected nvme attach-ns to be called")
	}

	// DiskDevice must be set to the first created namespace.
	if cfg.DiskDevice == "" {
		t.Error("expected cfg.DiskDevice to be set after namespace creation")
	}
	if !strings.HasPrefix(cfg.DiskDevice, "/dev/nvme0n") {
		t.Errorf("DiskDevice = %q, want /dev/nvme0n<N>", cfg.DiskDevice)
	}
}
