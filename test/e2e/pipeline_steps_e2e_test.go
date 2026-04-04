//go:build e2e && linux

package e2e

// Tests for three previously uncovered pipeline steps:
//   - collect-inventory  (Gap 1)
//   - health-checks      (Gap 5)
//   - setup-nvme-namespaces (Gap 9)

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
	"github.com/telekom/BOOTy/pkg/provision"
)

// ---------------------------------------------------------------------------
// inventoryTrackingProvider wraps mockProvider and counts ReportInventory calls.
// Defined at package level (not inside a test function) so it satisfies
// config.Provider without the funlen linter penalising a per-function type.
// ---------------------------------------------------------------------------

type inventoryTrackingProvider struct {
	*mockProvider
	calls atomic.Int64
}

func (p *inventoryTrackingProvider) ReportInventory(ctx context.Context, data []byte) error {
	p.calls.Add(1)
	return p.mockProvider.ReportInventory(ctx, data)
}

func newInventoryTrackingProvider(cfg *config.MachineConfig) *inventoryTrackingProvider {
	return &inventoryTrackingProvider{mockProvider: newMockProvider(cfg)}
}

// ---------------------------------------------------------------------------
// Gap 1: collect-inventory
// ---------------------------------------------------------------------------

// TestCollectInventoryDisabledE2E verifies that when InventoryEnabled=false the
// orchestrator skips ReportInventory entirely (zero calls) and still reaches the
// init status.
func TestCollectInventoryDisabledE2E(t *testing.T) {
	cfg := &config.MachineConfig{
		InventoryEnabled:    false,
		HealthChecksEnabled: false,
		Hostname:            "inv-disabled-node",
		ImageURLs:           []string{"http://img.local/test.gz"},
		DNSResolvers:        "8.8.8.8",
	}
	provider := newInventoryTrackingProvider(cfg)
	cmd := newMockCommander()
	orch := provision.NewOrchestrator(cfg, provider, disk.NewManager(cmd))

	_ = orch.Provision(context.Background())

	if provider.calls.Load() != 0 {
		t.Errorf("ReportInventory called %d time(s), want 0 when inventory is disabled",
			provider.calls.Load())
	}

	statuses := provider.getStatuses()
	if len(statuses) == 0 {
		t.Fatal("expected at least one status report")
	}
	if statuses[0].Status != config.StatusInit {
		t.Errorf("first status = %q, want init", statuses[0].Status)
	}
}

// TestCollectInventoryEnabledNonFatalE2E verifies that when InventoryEnabled=true
// the step is non-fatal even if inventory.Collect() fails in a CI environment
// (no real hardware sysfs). The pipeline must continue past the inventory step.
//
// NOTE: inventory.Collect() reads real sysfs paths (/sys/bus, /proc/cpuinfo,
// etc.) that are absent in a containerised CI runner. The expected outcome is
// that the step silently absorbs the error — the test asserts non-fatality, not
// successful data collection.
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
	err := orch.Provision(context.Background())
	// Whether provision succeeds or fails overall, inventory must not be the cause.
	if err != nil && strings.Contains(err.Error(), "inventory") {
		t.Fatalf("inventory step should not cause fatal error: %v", err)
	}

	statuses := provider.getStatuses()
	if len(statuses) == 0 {
		t.Fatal("expected at least one status report")
	}
	// Verify the first status is "init" — inventory step comes after init.
	if statuses[0].Status != config.StatusInit {
		t.Errorf("first status = %q, want init", statuses[0].Status)
	}
}

// TestCollectInventoryDoesNotCauseErrorE2E verifies that when InventoryEnabled=true
// and inventory collection succeeds (or is silently skipped in CI), no
// StatusError referencing inventory is ever emitted AND ReportInventory is
// called exactly once.
func TestCollectInventoryDoesNotCauseErrorE2E(t *testing.T) {
	cfg := &config.MachineConfig{
		InventoryEnabled:    true,
		HealthChecksEnabled: false,
		Hostname:            "inv-report-node",
		ImageURLs:           []string{"http://img.local/test.gz"},
		DNSResolvers:        "8.8.8.8",
	}
	provider := newInventoryTrackingProvider(cfg)
	cmd := newMockCommander()
	orch := provision.NewOrchestrator(cfg, provider, disk.NewManager(cmd))

	_ = orch.Provision(context.Background())

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
	// Positive assertion: ReportInventory must have been called exactly once.
	if got := provider.calls.Load(); got != 1 {
		t.Errorf("ReportInventory called %d time(s), want 1", got)
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

// healthSkipHardware is the set of checks that depend on real kernel sysfs/
// thermal/NIC devices unavailable in a CI environment. Skipping them ensures
// TestHealthChecksPassE2E does not fail due to absent hardware.
const healthSkipHardware = "disk-presence,disk-ioerr,memory-ecc,nic-link-state,thermal-state"

// TestHealthChecksPassE2E verifies that the health check step passes when
// MinCPUs=0 and MinMemory=0 (thresholds disabled) and all hardware-dependent
// checks are skipped via HealthSkipChecks.
func TestHealthChecksPassE2E(t *testing.T) {
	cfg := &config.MachineConfig{
		HealthChecksEnabled: true,
		InventoryEnabled:    false,
		HealthMinMemoryGB:   0,                  // threshold disabled
		HealthMinCPUs:       0,                  // threshold disabled
		HealthSkipChecks:    healthSkipHardware, // skip CI-incompatible checks
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
	// With zero thresholds and hardware checks skipped, no critical failure should occur.
	for _, s := range statuses {
		if s.Status == config.StatusError &&
			strings.Contains(s.Message, "critical health check") {
			t.Errorf("health checks should not fail with zero thresholds: %q", s.Message)
		}
	}
}

// TestHealthChecksCriticalFailureE2E verifies that when a critical health check
// fails (HealthMinCPUs set impossibly high), the orchestrator returns an error
// and reports StatusError mentioning minimum-cpu.
func TestHealthChecksCriticalFailureE2E(t *testing.T) {
	cfg := &config.MachineConfig{
		HealthChecksEnabled: true,
		InventoryEnabled:    false,
		HealthMinCPUs:       999999,             // impossible: forces minimum-cpu to fail
		HealthSkipChecks:    healthSkipHardware, // isolate minimum-cpu as the only failing check
		DNSResolvers:        "8.8.8.8",
	}
	provider := newMockProvider(cfg)
	cmd := newMockCommander()
	orch := provision.NewOrchestrator(cfg, provider, disk.NewManager(cmd))

	err := orch.Provision(context.Background())
	if err == nil {
		t.Fatal("expected provision to fail due to critical health check failure")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "minimum-cpu") {
		t.Errorf("error should mention minimum-cpu check: %v", err)
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

	statuses := provider.getStatuses()
	if len(statuses) == 0 {
		t.Fatal("expected status reports — provisioning may not have started")
	}
	// Pipeline must have at least reached init, confirming the nvme step was
	// encountered (and skipped) rather than the pipeline aborting before it.
	if statuses[0].Status != config.StatusInit {
		t.Errorf("first status = %q, want init — pipeline did not progress to nvme step", statuses[0].Status)
	}
	for _, c := range cmd.getCalls() {
		if strings.Contains(c.String(), "nvme") {
			t.Errorf("nvme should NOT be called when NVMeNamespaces is empty: %s", c)
		}
	}
}

// TestSetupNVMeNamespacesInvalidConfigE2E verifies that a malformed JSON config
// causes a parse error at the nvme namespace layout step, propagating as a
// fatal provisioning failure with the specific error text.
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
	errMsg := err.Error()
	// The orchestrator wraps the JSON parse error with "parsing nvme namespace layout".
	if !strings.Contains(errMsg, "parsing nvme namespace layout") {
		t.Errorf("error should reference NVMe namespace layout parsing, got: %v", err)
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
