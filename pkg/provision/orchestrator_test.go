//go:build linux

package provision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/telekom/BOOTy/pkg/cloudinit"
	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
)

// newTestOrchestrator builds an Orchestrator with a mock provider and disk manager
// suitable for unit testing individual steps.
func newTestOrchestrator(t *testing.T, cfg *config.MachineConfig, provider *mockProvider) *Orchestrator {
	t.Helper()
	o, _ := newTestOrchestratorWithCommander(t, cfg, provider)
	return o
}

func newTestOrchestratorWithCommander(t *testing.T, cfg *config.MachineConfig, provider *mockProvider) (*Orchestrator, *mockCommander) {
	t.Helper()
	cmd := newMockCommander()
	mgr := disk.NewManager(cmd)
	o := NewOrchestrator(cfg, provider, mgr)
	o.config.rootDir = t.TempDir()
	return o, cmd
}

func withProcCmdline(t *testing.T, cmdline string) {
	t.Helper()
	previous := readProcCmdline
	readProcCmdline = func() ([]byte, error) {
		return []byte(cmdline), nil
	}
	t.Cleanup(func() {
		readProcCmdline = previous
	})
}

func sfdiskJSON(t *testing.T, parts []disk.Partition) []byte {
	t.Helper()
	var out struct {
		PartitionTable struct {
			Partitions []disk.Partition `json:"partitions"`
		} `json:"partitiontable"`
	}
	out.PartitionTable.Partitions = parts
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal sfdisk json: %v", err)
	}
	return data
}

func hasCommandName(calls []mockCall, name string) bool {
	for _, call := range calls {
		if call.name == name {
			return true
		}
	}
	return false
}

func hasCommandCall(calls []mockCall, name string, args ...string) bool {
	for _, call := range calls {
		if call.name != name || len(call.args) != len(args) {
			continue
		}
		match := true
		for i := range args {
			if call.args[i] != args[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestClassifyImageStreamErrorChecksumMismatchIsPermanent(t *testing.T) {
	err := classifyImageStreamError("http://images.local/node.raw", errors.New("checksum mismatch: computed=bad want=good"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !isPermanent(err) {
		t.Fatalf("checksum mismatch should be permanent: %T %[1]v", err)
	}
	if !strings.Contains(err.Error(), "streaming http://images.local/node.raw") {
		t.Fatalf("error should include image URL context, got %q", err.Error())
	}
}

func TestClassifyImageStreamErrorNonChecksumRemainsRetryable(t *testing.T) {
	err := classifyImageStreamError("http://images.local/node.raw", errors.New("connection reset by peer"))
	if err == nil {
		t.Fatal("expected error")
	}
	if isPermanent(err) {
		t.Fatalf("transient stream failure should not be permanent: %T %[1]v", err)
	}
}

func TestProvisionStepCount(t *testing.T) {
	// Verify the pipeline has the expected number of steps.
	cfg := &config.MachineConfig{}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	// Use the shared provisionSteps() method from orchestrator.go.
	steps := o.provisionSteps()
	if len(steps) != 38 {
		t.Fatalf("expected 38 provisioning steps, got %d", len(steps))
	}
}

func TestMountBootStepIsBetweenRootMountAndBootloaderWork(t *testing.T) {
	cfg := &config.MachineConfig{}
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	steps := o.provisionSteps()

	indices := map[string]int{}
	for i, step := range steps {
		indices[step.Name] = i
	}
	for _, name := range []string{"mount-root", "mount-boot", "configure-grub", "create-efi-boot-entry", "teardown-chroot"} {
		if _, ok := indices[name]; !ok {
			t.Fatalf("missing step %q", name)
		}
	}
	if indices["mount-root"] >= indices["mount-boot"] ||
		indices["mount-boot"] >= indices["configure-grub"] ||
		indices["mount-boot"] >= indices["create-efi-boot-entry"] ||
		indices["create-efi-boot-entry"] >= indices["teardown-chroot"] {
		t.Fatalf("unexpected boot mount ordering: %#v", indices)
	}
}

func TestMountBootSkipsWhenNoBootPartition(t *testing.T) {
	cfg := &config.MachineConfig{}
	o := newTestOrchestrator(t, cfg, &mockProvider{})

	if err := o.mountBoot(context.Background()); err != nil {
		t.Fatalf("mountBoot without boot partition: %v", err)
	}
}

func TestMountBootSkipsWhenEFIRuntimeUnavailable(t *testing.T) {
	old := efiRuntimeReady
	efiRuntimeReady = func() (bool, string) { return false, "unit test" }
	t.Cleanup(func() { efiRuntimeReady = old })

	cfg := &config.MachineConfig{}
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	o.bootPartition = "/dev/does-not-exist"

	if err := o.mountBoot(context.Background()); err != nil {
		t.Fatalf("mountBoot without EFI runtime: %v", err)
	}
}

func TestBootEFIMountPointUsesMountedRoot(t *testing.T) {
	if got, want := bootEFIMountPoint(), filepath.Join(newroot, "boot", "efi"); got != want {
		t.Fatalf("bootEFIMountPoint = %q, want %q", got, want)
	}
}

func TestSetupNVMeNamespaces_NoOp(t *testing.T) {
	cfg := &config.MachineConfig{}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	if err := o.setupNVMeNamespaces(context.Background()); err != nil {
		t.Fatalf("setupNVMeNamespaces no-op: %v", err)
	}
	if cfg.Provision.Disk.Device != "" {
		t.Fatalf("DiskDevice = %q, want empty", cfg.Provision.Disk.Device)
	}
}

func TestSetupNVMeNamespaces_InvalidJSON(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Disk.NVMeNamespaces = `{bad}`
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	err := o.setupNVMeNamespaces(context.Background())
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parsing nvme namespace layout") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetupNVMeNamespaces_HappyPathSetsDiskDevice(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Disk.NVMeNamespaces = `[{"controller":"/dev/nvme0","namespaces":[{"label":"os","sizePct":100}]}]`
	provider := &mockProvider{}
	o, cmd := newTestOrchestratorWithCommander(t, cfg, provider)

	cmd.setResult("nvme id-ctrl", []byte(`{"nn":32,"tnvmcap":1024000}`), nil)
	cmd.setResult("nvme create-ns", []byte("create-ns: Success, created nsid:5\n"), nil)

	if err := o.setupNVMeNamespaces(context.Background()); err != nil {
		t.Fatalf("setupNVMeNamespaces: %v", err)
	}
	if cfg.Provision.Disk.Device != "/dev/nvme0n5" {
		t.Fatalf("DiskDevice = %q, want /dev/nvme0n5", cfg.Provision.Disk.Device)
	}
}

func TestProvisionReportsErrorOnStepFailure(t *testing.T) {
	cfg := &config.MachineConfig{}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	// Override the pipeline with a single step that always fails.
	testErr := fmt.Errorf("simulated failure")
	steps := []Step{
		{"report-init", o.reportInit},
		{"failing-step", func(_ context.Context) error { return testErr }},
	}

	var gotErr error
	for _, step := range steps {
		if err := step.Fn(context.Background()); err != nil {
			gotErr = err
			break
		}
	}
	if gotErr == nil {
		t.Fatal("expected error from failing step")
	}
	if !errors.Is(gotErr, testErr) {
		t.Errorf("expected simulated failure, got %v", gotErr)
	}
	// Verify init was still reported before the failure.
	if len(provider.statuses) != 1 || provider.statuses[0].status != config.StatusInit {
		t.Error("expected StatusInit before failure")
	}
}

func TestReportInit(t *testing.T) {
	cfg := &config.MachineConfig{}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	if err := o.reportInit(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(provider.statuses) != 1 {
		t.Fatalf("expected 1 status report, got %d", len(provider.statuses))
	}
	if provider.statuses[0].status != config.StatusInit {
		t.Errorf("expected StatusInit, got %v", provider.statuses[0].status)
	}
}

func TestReportSuccess(t *testing.T) {
	cfg := &config.MachineConfig{}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	if err := o.reportSuccess(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(provider.statuses) != 1 {
		t.Fatalf("expected 1 status report, got %d", len(provider.statuses))
	}
	if provider.statuses[0].status != config.StatusSuccess {
		t.Errorf("expected StatusSuccess, got %v", provider.statuses[0].status)
	}
}

func TestWipeOrSecureEraseDisks(t *testing.T) {
	tests := []struct {
		name        string
		secureErase bool
		wipeErr     error
		wantErr     bool
	}{
		{
			name:        "quick erase (default)",
			secureErase: false,
		},
		{
			name:        "secure erase enabled",
			secureErase: true,
			// NOTE: SecureEraseAllDisks reads /sys/block which is empty in test,
			// so this only verifies the function is called without panic.
			// Full coverage requires integration tests with real or mock disks.
		},
		{
			name:        "quick erase error",
			secureErase: false,
			wipeErr:     fmt.Errorf("wipe failed"),
			wantErr:     true, // WipeAllDisks returns error when all disk wipes fail
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.MachineConfig{}
			cfg.Provision.Disk.SecureErase = tc.secureErase
			cmd := newMockCommander()
			if tc.wipeErr != nil {
				cmd.setResult("wipefs -af", nil, tc.wipeErr)
			}
			provider := &mockProvider{}
			mgr := disk.NewManager(cmd)
			o := NewOrchestrator(cfg, provider, mgr)

			err := o.wipeOrSecureEraseDisks(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got err=%v", tc.wantErr, err)
			}
		})
	}
}

func TestWipeOrSecureEraseDisksAllowsPartitionLayoutWithImageURLsInDeprovisionMode(t *testing.T) {
	cfg := &config.MachineConfig{
		Mode: "deprovision",
	}
	cfg.Provision.Image.URLs = []string{"http://images.local/node.img.zst"}
	cfg.Provision.Disk.PartitionLayout = &config.PartitionLayout{
		Table: "gpt",
		Partitions: []config.Partition{
			{Label: "root", Mountpoint: "/"},
		},
	}
	o := newTestOrchestrator(t, cfg, &mockProvider{})

	err := o.wipeOrSecureEraseDisks(context.Background())
	if err != nil {
		t.Fatalf("expected no error in deprovision mode, got: %v", err)
	}
}

func TestWipeOrSecureEraseDisksRejectsPartitionLayoutWithImageURLsInProvisionMode(t *testing.T) {
	cfg := &config.MachineConfig{
		Mode: "provision",
	}
	cfg.Provision.Image.URLs = []string{"http://images.local/node.img.zst"}
	cfg.Provision.Disk.PartitionLayout = &config.PartitionLayout{
		Table: "gpt",
		Partitions: []config.Partition{
			{Label: "root", Mountpoint: "/"},
		},
	}
	o := newTestOrchestrator(t, cfg, &mockProvider{})

	err := o.wipeOrSecureEraseDisks(context.Background())
	if err == nil {
		t.Fatal("expected error when partition layout is combined with image urls in provision mode")
	}
	if !strings.Contains(err.Error(), "partition layout provisioning is not supported yet") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWipeOrSecureEraseDisksRejectsPartitionLayoutWithoutImageURLsInProvisionMode(t *testing.T) {
	cfg := &config.MachineConfig{
		Mode: "provision",
	}
	cfg.Provision.Disk.PartitionLayout = &config.PartitionLayout{
		Table: "gpt",
		Partitions: []config.Partition{
			{Label: "root", Mountpoint: "/"},
		},
	}
	o := newTestOrchestrator(t, cfg, &mockProvider{})

	err := o.wipeOrSecureEraseDisks(context.Background())
	if err == nil {
		t.Fatal("expected error when partition layout is set without image urls in provision mode")
	}
	if !strings.Contains(err.Error(), "partition layout provisioning is not supported yet") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWipeOrSecureEraseDisksRejectsConflictingDeviceOverrides(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Disk.Device = "/dev/sda"
	cfg.Provision.Disk.PartitionLayout = &config.PartitionLayout{
		Device: "/dev/sdb",
		Table:  "gpt",
		Partitions: []config.Partition{
			{Label: "root", Mountpoint: "/"},
		},
	}
	o := newTestOrchestrator(t, cfg, &mockProvider{})

	err := o.wipeOrSecureEraseDisks(context.Background())
	if err == nil {
		t.Fatal("expected error for conflicting disk device overrides")
	}
	if !strings.Contains(err.Error(), "disk device conflict") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCollectInventoryDisabled(t *testing.T) {
	cfg := &config.MachineConfig{}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	if err := o.collectInventory(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCollectFirmwareDisabled(t *testing.T) {
	cfg := &config.MachineConfig{}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	if err := o.collectFirmware(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOrchestratorSetHostnameEmpty(t *testing.T) {
	cfg := &config.MachineConfig{Hostname: ""}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	if err := o.setHostname(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHealthChecksDisabled(t *testing.T) {
	cfg := &config.MachineConfig{}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	if err := o.runHealthChecks(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPostProvisionCmdsEmpty(t *testing.T) {
	cfg := &config.MachineConfig{}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	if err := o.runPostProvisionCmds(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFirmwareChanged(t *testing.T) {
	cfg := &config.MachineConfig{}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	if o.FirmwareChanged() {
		t.Error("expected no firmware change initially")
	}
	o.firmwareChanged = true
	if !o.FirmwareChanged() {
		t.Error("expected firmware change after setting flag")
	}
}

func TestCheckpointResume_SkipsCompleted(t *testing.T) {
	// Steps: first two are marked done in checkpoint; only the third should run.
	dir := t.TempDir()
	cpPath := dir + "/checkpoint.json"

	// Pre-create a checkpoint with the first two steps completed.
	cp := &Checkpoint{
		CompletedSteps: []string{"step-one", "step-two"},
		persist:        true,
		path:           cpPath,
	}
	if err := cp.Save(); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	loadedCP, err := LoadCheckpointFrom(cpPath)
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}

	var ran []string
	steps := []Step{
		{"step-one", func(_ context.Context) error { ran = append(ran, "step-one"); return nil }},
		{"step-two", func(_ context.Context) error { ran = append(ran, "step-two"); return nil }},
		{"step-three", func(_ context.Context) error { ran = append(ran, "step-three"); return nil }},
	}

	stateSteps := map[string]struct{}{}
	for _, step := range steps {
		_, mustRun := stateSteps[step.Name]
		if loadedCP.IsCompleted(step.Name) && !mustRun {
			continue
		}
		if err := step.Fn(context.Background()); err != nil {
			t.Fatalf("step %s failed: %v", step.Name, err)
		}
	}

	if len(ran) != 1 || ran[0] != "step-three" {
		t.Errorf("expected only step-three to run on resume, got %v", ran)
	}
}

func TestCheckpointResume_StateStepsAlwaysRun(t *testing.T) {
	// stateSteps (setup-mellanox, detect-disk, parse-partitions) must re-run
	// even if marked complete because they rebuild runtime in-memory state.
	dir := t.TempDir()
	cpPath := dir + "/checkpoint.json"

	cp := &Checkpoint{
		CompletedSteps: []string{"setup-mellanox", "detect-disk", "parse-partitions", "stream-image", "configure-ssh"},
		persist:        true,
		path:           cpPath,
	}
	if err := cp.Save(); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	loadedCP, err := LoadCheckpointFrom(cpPath)
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}

	stateSteps := resumeStateSteps()

	var ran []string
	steps := []Step{
		{"setup-mellanox", func(_ context.Context) error { ran = append(ran, "setup-mellanox"); return nil }},
		{"detect-disk", func(_ context.Context) error { ran = append(ran, "detect-disk"); return nil }},
		{"parse-partitions", func(_ context.Context) error { ran = append(ran, "parse-partitions"); return nil }},
		{"stream-image", func(_ context.Context) error { ran = append(ran, "stream-image"); return nil }},
		{"configure-ssh", func(_ context.Context) error { ran = append(ran, "configure-ssh"); return nil }},
	}

	for _, step := range steps {
		_, mustRun := stateSteps[step.Name]
		if loadedCP.IsCompleted(step.Name) && !mustRun {
			continue
		}
		if err := step.Fn(context.Background()); err != nil {
			t.Fatalf("step %s failed: %v", step.Name, err)
		}
	}

	// setup-mellanox, detect-disk, and parse-partitions re-run (stateSteps);
	// stream-image and configure-ssh skip (completed, non-state).
	if len(ran) != 3 {
		t.Errorf("expected 3 runs (setup-mellanox, detect-disk, parse-partitions), got %v", ran)
	}
	for _, name := range []string{"setup-mellanox", "detect-disk", "parse-partitions"} {
		found := false
		for _, r := range ran {
			if r == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %s to re-run on resume", name)
		}
	}
}

func TestCheckpoint_FailureCountIncrements(t *testing.T) {
	cfg := &config.MachineConfig{}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	cp := &Checkpoint{}
	testErr := fmt.Errorf("simulated transient failure")
	step := Step{"failing-step", func(_ context.Context) error { return testErr }}

	_ = o.executeStep(context.Background(), step, cp)

	if cp.FailureCount != 1 {
		t.Errorf("expected FailureCount=1, got %d", cp.FailureCount)
	}
	if len(cp.Errors) != 1 {
		t.Errorf("expected 1 error recorded, got %d", len(cp.Errors))
	}
}

func TestLoadOrCreateCheckpoint(t *testing.T) {
	tests := []struct {
		name        string
		envValue    string
		wantPersist bool
	}{
		{name: "unset env returns non-persistent", envValue: "", wantPersist: false},
		{name: "true enables persistence", envValue: "true", wantPersist: true},
		{name: "1 enables persistence", envValue: "1", wantPersist: true},
		{name: "false disables persistence", envValue: "false", wantPersist: false},
		{name: "0 disables persistence", envValue: "0", wantPersist: false},
		{name: "random string disables persistence", envValue: "notabool", wantPersist: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.MachineConfig{}
			provider := &mockProvider{}
			o := newTestOrchestrator(t, cfg, provider)

			if tc.envValue != "" {
				t.Setenv("BOOTY_RESUME", tc.envValue)
			} else {
				t.Setenv("BOOTY_RESUME", "")
			}

			cp := o.loadOrCreateCheckpoint()
			if cp == nil {
				t.Fatal("expected non-nil checkpoint")
			}
			if cp.persist != tc.wantPersist {
				t.Errorf("persist = %v, want %v", cp.persist, tc.wantPersist)
			}
		})
	}
}

func TestRescueConfig_WiresAllFields(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Rescue.Mode = "shell"
	cfg.Rescue.SSHPubKey = "ssh-ed25519 AAAA..."
	cfg.Rescue.PasswordHash = "$6$rounds=4096$salt$hash"
	cfg.Rescue.Timeout = 120
	cfg.Rescue.AutoMountDisks = true
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)
	rc := o.RescueConfig()

	if rc.Mode != "shell" {
		t.Errorf("Mode = %q, want shell", rc.Mode)
	}
	if len(rc.SSHKeys) != 1 || rc.SSHKeys[0] != "ssh-ed25519 AAAA..." {
		t.Errorf("SSHKeys = %v, want [ssh-ed25519 AAAA...]", rc.SSHKeys)
	}
	if rc.PasswordHash != "$6$rounds=4096$salt$hash" {
		t.Errorf("PasswordHash = %q", rc.PasswordHash)
	}
	if rc.ShellTimeout.Seconds() != 120 {
		t.Errorf("ShellTimeout = %v, want 2m", rc.ShellTimeout)
	}
	if !rc.AutoMountDisks {
		t.Error("AutoMountDisks should be true")
	}
}

func TestRescueConfig_DefaultsApplied(t *testing.T) {
	cfg := &config.MachineConfig{}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)
	rc := o.RescueConfig()

	if rc.Mode != "reboot" {
		t.Errorf("Mode = %q, want reboot", rc.Mode)
	}
	if rc.RetryDelay == 0 {
		t.Error("RetryDelay should have a default")
	}
	if rc.ShellTimeout == 0 {
		t.Error("ShellTimeout should have a default")
	}
}

func TestVerifyImageSignature_Skipped(t *testing.T) {
	cfg := &config.MachineConfig{}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	// No signature URL → should skip without error.
	if err := o.verifyImageSignature(context.Background()); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestVerifyImageSignature_MissingPubKey(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Image.SignatureURL = "https://example.com/image.sig"
	cfg.Provision.Image.GPGPubKey = ""
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	err := o.verifyImageSignature(context.Background())
	if err == nil {
		t.Error("expected error for missing pub key")
	}
}

func TestDryRunImageMode(t *testing.T) {
	tests := []struct {
		name   string
		mode   string
		status DryRunStatus
	}{
		{"default empty", "", DryRunPass},
		{"whole-disk", "whole-disk", DryRunPass},
		{"partition", "partition", DryRunPass},
		{"PARTITION caps", "PARTITION", DryRunPass},
		{"ab", "ab", DryRunPass},
		{"invalid", "invalid-mode", DryRunFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.MachineConfig{}
			cfg.Provision.Image.Mode = tt.mode
			provider := &mockProvider{}
			o := newTestOrchestrator(t, cfg, provider)
			result := o.dryRunImageMode(context.Background())
			if result.Status != tt.status {
				t.Errorf("dryRunImageMode(%q) status = %s, want %s", tt.mode, result.Status, tt.status)
			}
		})
	}
}

func TestEnsureABPartitionLayoutTargetsInactiveSlot(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Image.Mode = config.ImageModeAB
	cfg.Provision.AB.ActiveSlot = config.ABSlotA
	cfg.Provision.AB.TargetSlot = config.ABTargetInactive
	cfg.Provision.AB.RootSizeMB = 8192
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	o.targetDisk = "/dev/sda"

	if err := o.ensureABPartitionLayout(); err != nil {
		t.Fatalf("ensureABPartitionLayout: %v", err)
	}
	layout := cfg.Provision.Disk.PartitionLayout
	if layout == nil {
		t.Fatal("expected partition layout")
	}
	if layout.Partitions[1].Mountpoint != "" {
		t.Fatalf("slot A mountpoint = %q, want empty", layout.Partitions[1].Mountpoint)
	}
	if layout.Partitions[2].Mountpoint != "/" {
		t.Fatalf("slot B mountpoint = %q, want /", layout.Partitions[2].Mountpoint)
	}
}

func TestParsePartitionsEnsuresABLayoutOnResume(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Image.Mode = config.ImageModeAB
	cfg.Provision.AB.ActiveSlot = config.ABSlotA
	cfg.Provision.AB.TargetSlot = config.ABTargetInactive
	cfg.Provision.AB.RootSizeMB = 8192
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	o.targetDisk = "/dev/sda"

	if err := o.parsePartitions(context.Background()); err != nil {
		t.Fatalf("parsePartitions: %v", err)
	}
	if cfg.Provision.Disk.PartitionLayout == nil {
		t.Fatal("expected generated A/B partition layout")
	}
	if o.bootPartition != "/dev/sda1" {
		t.Fatalf("bootPartition = %q, want /dev/sda1", o.bootPartition)
	}
	if o.rootPartition != "/dev/sda3" {
		t.Fatalf("rootPartition = %q, want inactive slot /dev/sda3", o.rootPartition)
	}
}

func TestABStreamTargetsPreserveExistingSkipsSharedBoot(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Image.Mode = config.ImageModeAB
	cfg.Provision.AB.PreserveExisting = true
	cfg.Provision.AB.SourceRootLabel = "rootfs"
	cfg.Provision.AB.SourceRootPartition = 0
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	o.targetDisk = "/dev/sda"
	o.bootPartition = "/dev/sda1"
	o.rootPartition = "/dev/sda3"

	targets := o.abStreamTargets()
	if targets.Disk != "/dev/sda" {
		t.Fatalf("Disk = %q, want /dev/sda", targets.Disk)
	}
	if targets.RootPartition != "/dev/sda3" {
		t.Fatalf("RootPartition = %q, want /dev/sda3", targets.RootPartition)
	}
	if targets.BootPartition != "" {
		t.Fatalf("BootPartition = %q, want empty when preserving existing A/B boot assets", targets.BootPartition)
	}
	if targets.SourceRootLabel != "rootfs" {
		t.Fatalf("SourceRootLabel = %q, want rootfs", targets.SourceRootLabel)
	}
}

func TestShouldPreserveABBootEntries(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		preserve bool
		want     bool
	}{
		{name: "A/B preserve existing", mode: config.ImageModeAB, preserve: true, want: true},
		{name: "A/B fresh install", mode: config.ImageModeAB, preserve: false, want: false},
		{name: "whole disk preserve flag ignored", mode: config.ImageModeWholeDisk, preserve: true, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.MachineConfig{}
			cfg.Provision.Image.Mode = tt.mode
			cfg.Provision.AB.PreserveExisting = tt.preserve
			o := newTestOrchestrator(t, cfg, &mockProvider{})

			if got := o.shouldPreserveABBootEntries(); got != tt.want {
				t.Fatalf("shouldPreserveABBootEntries() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestWriteABSlotState(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Image.Mode = config.ImageModeAB
	cfg.Provision.AB.ActiveSlot = config.ABSlotB
	cfg.Provision.AB.TargetSlot = config.ABTargetInactive
	cfg.Provision.AB.PreserveExisting = true
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	o.rootPartition = "/dev/sda2"

	if err := o.writeABSlotState(); err != nil {
		t.Fatalf("writeABSlotState: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(o.config.rootDir, "etc", "booty", "ab-slot.env"))
	if err != nil {
		t.Fatalf("read ab-slot.env: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"BOOTY_AB_TARGET_SLOT=a",
		"BOOTY_AB_BOOTED_SLOT=a",
		"BOOTY_AB_ACTIVE_SLOT=b",
		"BOOTY_AB_PRESERVE_EXISTING=true",
		"BOOTY_AB_ROOT_PARTITION=/dev/sda2",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("ab-slot.env missing %q in:\n%s", want, content)
		}
	}
}

func TestReadABSlotStateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ab-slot.env")
	if err := os.WriteFile(path, []byte("BOOTY_AB_BOOTED_SLOT='b'\nBOOTY_AB_TARGET_SLOT=a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := readABSlotStateFile(path)
	if err != nil {
		t.Fatalf("readABSlotStateFile: %v", err)
	}
	if state["BOOTY_AB_BOOTED_SLOT"] != "b" {
		t.Fatalf("BOOTY_AB_BOOTED_SLOT = %q, want b", state["BOOTY_AB_BOOTED_SLOT"])
	}
}

func TestDetectBootedABSlotFromCmdline(t *testing.T) {
	tests := []struct {
		name     string
		cmdline  string
		disk     string
		wantSlot string
	}{
		{name: "partlabel slot a", cmdline: "quiet root=PARTLABEL=BOOTY-ROOT-A", disk: "/dev/sda", wantSlot: config.ABSlotA},
		{name: "by partlabel slot b", cmdline: "root=/dev/disk/by-partlabel/BOOTY-ROOT-B ro", disk: "/dev/sda", wantSlot: config.ABSlotB},
		{name: "scsi partition slot a", cmdline: "root=/dev/sda2", disk: "/dev/sda", wantSlot: config.ABSlotA},
		{name: "nvme partition slot b", cmdline: "root=/dev/nvme0n1p3", disk: "/dev/nvme0n1", wantSlot: config.ABSlotB},
		{name: "unknown root", cmdline: "root=UUID=abcd", disk: "/dev/sda", wantSlot: ""},
		{name: "no root", cmdline: "console=ttyS0", disk: "/dev/sda", wantSlot: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withProcCmdline(t, tt.cmdline)
			got, err := detectBootedABSlotFromCmdline(tt.disk)
			if err != nil {
				t.Fatalf("detectBootedABSlotFromCmdline: %v", err)
			}
			if got != tt.wantSlot {
				t.Fatalf("detectBootedABSlotFromCmdline() = %q, want %q", got, tt.wantSlot)
			}
		})
	}
}

func TestABSlotPartitionDevice(t *testing.T) {
	tests := []struct {
		slot string
		want string
	}{
		{slot: config.ABSlotA, want: "/dev/sda2"},
		{slot: config.ABSlotB, want: "/dev/sda3"},
	}
	for _, tt := range tests {
		got, err := abSlotPartitionDevice("/dev/sda", tt.slot)
		if err != nil {
			t.Fatalf("abSlotPartitionDevice(%q): %v", tt.slot, err)
		}
		if got != tt.want {
			t.Fatalf("abSlotPartitionDevice(%q) = %q, want %q", tt.slot, got, tt.want)
		}
	}
}

func TestWriteABSlotStateRejectsSymlinkEscape(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Image.Mode = config.ImageModeAB
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(o.config.rootDir, "etc")); err != nil {
		t.Fatal(err)
	}

	err := o.writeABSlotState()
	if err == nil {
		t.Fatal("expected symlink escape error")
	}
	if !strings.Contains(err.Error(), "target escapes provisioned root") {
		t.Fatalf("error = %v, want target escape", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "booty", "ab-slot.env")); !os.IsNotExist(err) {
		t.Fatalf("A/B slot state followed symlink into %s", outside)
	}
}

func TestWipeOrSecureEraseDisksSkipsWholeDiskWipeForABPreserveExisting(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Image.Mode = config.ImageModeAB
	cfg.Provision.AB.PreserveExisting = true
	o, cmd := newTestOrchestratorWithCommander(t, cfg, &mockProvider{})

	if err := o.wipeOrSecureEraseDisks(context.Background()); err != nil {
		t.Fatalf("wipeOrSecureEraseDisks: %v", err)
	}
	if len(cmd.calls) != 0 {
		t.Fatalf("expected no wipe commands, got %#v", cmd.calls)
	}
}

func TestABPreserveExistingValidatesExistingLayoutBeforeWipe(t *testing.T) {
	withProcCmdline(t, "console=ttyS0 root=PARTLABEL=BOOTY-ROOT-A")

	cfg := &config.MachineConfig{}
	cfg.Provision.Image.Mode = config.ImageModeAB
	cfg.Provision.AB.ActiveSlot = config.ABSlotA
	cfg.Provision.AB.TargetSlot = config.ABTargetInactive
	cfg.Provision.AB.PreserveExisting = true
	o, cmd := newTestOrchestratorWithCommander(t, cfg, &mockProvider{})
	o.targetDisk = "/dev/sda"
	cmd.setResult("sfdisk --json", sfdiskJSON(t, []disk.Partition{
		{Node: "/dev/sda1", Type: disk.EFISystemPartitionGUID, Name: "BOOTY-EFI"},
		{Node: "/dev/sda2", Type: disk.LinuxFilesystemGUID, Name: "BOOTY-ROOT-A"},
		{Node: "/dev/sda3", Type: disk.LinuxFilesystemGUID, Name: "BOOTY-ROOT-B"},
		{Node: "/dev/sda4", Type: disk.LinuxFilesystemGUID, Name: "BOOTY-STATE"},
	}), nil)

	if err := o.ensureABPartitionLayout(); err != nil {
		t.Fatalf("ensureABPartitionLayout: %v", err)
	}
	if err := o.parsePartitionsFromLayout(context.Background()); err != nil {
		t.Fatalf("parsePartitionsFromLayout: %v", err)
	}
	if err := o.validateABPreserveExistingLayout(context.Background()); err != nil {
		t.Fatalf("validateABPreserveExistingLayout: %v", err)
	}
	if err := o.prepareABTargetSlot(context.Background()); err != nil {
		t.Fatalf("prepareABTargetSlot: %v", err)
	}
	if !hasCommandCall(cmd.calls, "wipefs", "-af", "/dev/sda3") {
		t.Fatalf("expected wipefs only on inactive root /dev/sda3, calls: %#v", cmd.calls)
	}
}

func TestABPreserveExistingRejectsStaleActiveSlotFromCmdline(t *testing.T) {
	withProcCmdline(t, "console=ttyS0 root=PARTLABEL=BOOTY-ROOT-B")

	cfg := &config.MachineConfig{}
	cfg.Provision.Image.Mode = config.ImageModeAB
	cfg.Provision.AB.ActiveSlot = config.ABSlotA
	cfg.Provision.AB.TargetSlot = config.ABTargetInactive
	cfg.Provision.AB.PreserveExisting = true
	o, cmd := newTestOrchestratorWithCommander(t, cfg, &mockProvider{})
	o.targetDisk = "/dev/sda"
	cmd.setResult("sfdisk --json", sfdiskJSON(t, []disk.Partition{
		{Node: "/dev/sda1", Type: disk.EFISystemPartitionGUID, Name: "BOOTY-EFI"},
		{Node: "/dev/sda2", Type: disk.LinuxFilesystemGUID, Name: "BOOTY-ROOT-A"},
		{Node: "/dev/sda3", Type: disk.LinuxFilesystemGUID, Name: "BOOTY-ROOT-B"},
		{Node: "/dev/sda4", Type: disk.LinuxFilesystemGUID, Name: "BOOTY-STATE"},
	}), nil)

	if err := o.ensureABPartitionLayout(); err != nil {
		t.Fatalf("ensureABPartitionLayout: %v", err)
	}
	if err := o.parsePartitionsFromLayout(context.Background()); err != nil {
		t.Fatalf("parsePartitionsFromLayout: %v", err)
	}
	err := o.validateABPreserveExistingLayout(context.Background())
	if err == nil {
		t.Fatal("expected stale active slot rejection")
	}
	if !strings.Contains(err.Error(), `kernel cmdline reports booted A/B slot "b", config declares active slot "a"`) {
		t.Fatalf("error = %v", err)
	}
	if hasCommandName(cmd.calls, "wipefs") {
		t.Fatalf("stale active slot validation must fail before wipefs, calls: %#v", cmd.calls)
	}
}

func TestABPreserveExistingRejectsUnexpectedLayoutBeforeWipe(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Image.Mode = config.ImageModeAB
	cfg.Provision.AB.ActiveSlot = config.ABSlotA
	cfg.Provision.AB.TargetSlot = config.ABTargetInactive
	cfg.Provision.AB.PreserveExisting = true
	o, cmd := newTestOrchestratorWithCommander(t, cfg, &mockProvider{})
	o.targetDisk = "/dev/sda"
	cmd.setResult("sfdisk --json", sfdiskJSON(t, []disk.Partition{
		{Node: "/dev/sda1", Type: disk.EFISystemPartitionGUID, Name: "SYSTEM"},
		{Node: "/dev/sda2", Type: disk.LinuxFilesystemGUID, Name: "BOOTY-ROOT-B"},
		{Node: "/dev/sda3", Type: disk.LinuxFilesystemGUID, Name: "BOOTY-ROOT-A"},
		{Node: "/dev/sda4", Type: disk.LinuxFilesystemGUID, Name: "BOOTY-STATE"},
	}), nil)

	err := o.streamABImage(context.Background(), "http://image.example/os.raw", nil)
	if err == nil {
		t.Fatal("expected unexpected preserved A/B layout to fail")
	}
	if !strings.Contains(err.Error(), `existing A/B partition 1 label = "SYSTEM", want "BOOTY-EFI"`) {
		t.Fatalf("error = %v", err)
	}
	if hasCommandName(cmd.calls, "wipefs") {
		t.Fatalf("preserve layout validation must fail before wipefs, calls: %#v", cmd.calls)
	}
}

func TestResolveRootFromLayoutPrefersLVMRoot(t *testing.T) {
	cfg := &config.MachineConfig{}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)
	o.targetDisk = "/dev/sda"

	layout := &config.PartitionLayout{
		Table: "gpt",
		Partitions: []config.Partition{
			{Label: "pv", Mountpoint: "/var"},
		},
		LVM: &config.LVMConfig{
			VolumeGroup: "sysvg",
			Volumes: []config.LVVolume{
				{Name: "root", Mountpoint: "/"},
			},
		},
	}

	if err := o.resolveRootFromLayout(layout); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if o.rootPartition != "/dev/sysvg/root" {
		t.Errorf("rootPartition = %q, want /dev/sysvg/root", o.rootPartition)
	}
}

func TestDetectDiskUsesPartitionLayoutDeviceOverride(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Disk.PartitionLayout = &config.PartitionLayout{
		Device: "/dev/disk/by-id/test-disk",
		Table:  "gpt",
		Partitions: []config.Partition{
			{Label: "root", Mountpoint: "/"},
		},
	}
	o := newTestOrchestrator(t, cfg, &mockProvider{})

	if err := o.detectDisk(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if o.targetDisk != "/dev/disk/by-id/test-disk" {
		t.Fatalf("targetDisk = %q, want /dev/disk/by-id/test-disk", o.targetDisk)
	}
}

func TestDetectDiskTrimsPartitionLayoutDeviceOverride(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Disk.PartitionLayout = &config.PartitionLayout{
		Device: "  /dev/disk/by-id/test-disk  ",
		Table:  "gpt",
		Partitions: []config.Partition{
			{Label: "root", Mountpoint: "/"},
		},
	}
	o := newTestOrchestrator(t, cfg, &mockProvider{})

	if err := o.detectDisk(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if o.targetDisk != "/dev/disk/by-id/test-disk" {
		t.Fatalf("targetDisk = %q, want /dev/disk/by-id/test-disk", o.targetDisk)
	}
}

func TestResolveRootFromLayoutFallsBackToPartitionRoot(t *testing.T) {
	cfg := &config.MachineConfig{}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)
	o.targetDisk = "/dev/sda"

	layout := &config.PartitionLayout{
		Table: "gpt",
		Partitions: []config.Partition{
			{Label: "data", Mountpoint: "/var"},
			{Label: "root", Mountpoint: "/"},
		},
		LVM: &config.LVMConfig{
			VolumeGroup: "sysvg",
			Volumes: []config.LVVolume{
				{Name: "var", Mountpoint: "/var/lib"},
			},
		},
	}

	if err := o.resolveRootFromLayout(layout); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if o.rootPartition != "/dev/sda2" {
		t.Errorf("rootPartition = %q, want /dev/sda2", o.rootPartition)
	}
}

func TestResolveRootFromLayoutMissingRoot(t *testing.T) {
	cfg := &config.MachineConfig{}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)
	o.targetDisk = "/dev/sda"

	layout := &config.PartitionLayout{
		Table: "gpt",
		Partitions: []config.Partition{
			{Label: "data", Mountpoint: "/var"},
		},
		LVM: &config.LVMConfig{
			VolumeGroup: "sysvg",
			Volumes: []config.LVVolume{
				{Name: "data", Mountpoint: "/data"},
			},
		},
	}

	err := o.resolveRootFromLayout(layout)
	if err == nil {
		t.Fatal("expected error when no root mountpoint is defined")
	}
	if !strings.Contains(err.Error(), "mountpoint \"/\"") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStreamImagePartitionLayoutFailsWithoutImages(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Disk.PartitionLayout = &config.PartitionLayout{
		Table: "gpt",
		Partitions: []config.Partition{
			{Label: "root", Mountpoint: "/"},
		},
	}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)
	o.targetDisk = "/dev/sda"

	err := o.streamImage(context.Background())
	if err == nil {
		t.Fatal("expected error when partition layout is used without image urls")
	}
	if !strings.Contains(err.Error(), "partition layout provisioning is not supported yet") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStreamImagePartitionLayoutRejectsImageURLs(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Image.URLs = []string{"http://images.local/node.img.zst"}
	cfg.Provision.Disk.PartitionLayout = &config.PartitionLayout{
		Table: "gpt",
		Partitions: []config.Partition{
			{Label: "root", Mountpoint: "/"},
		},
	}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)
	o.targetDisk = "/dev/sda"

	err := o.streamImage(context.Background())
	if err == nil {
		t.Fatal("expected error when partition layout is combined with image urls")
	}
	if !strings.Contains(err.Error(), "partition layout provisioning is not supported yet") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParsePartitionsFromLayoutNoBootEFIMountpoint(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Disk.PartitionLayout = &config.PartitionLayout{
		Table: "gpt",
		Partitions: []config.Partition{
			{Label: "data", Filesystem: "vfat", Mountpoint: "/boot"},
			{Label: "root", Filesystem: "ext4", Mountpoint: "/"},
		},
	}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)
	o.targetDisk = "/dev/sda"

	err := o.parsePartitionsFromLayout(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if o.rootPartition != "/dev/sda2" {
		t.Errorf("rootPartition = %q, want /dev/sda2", o.rootPartition)
	}
	if o.bootPartition != "" {
		t.Errorf("bootPartition = %q, want empty when /boot/efi is not declared", o.bootPartition)
	}
}

func TestGrowPartitionSkippedForPartitionLayout(t *testing.T) {
	cmd := newMockCommander()
	cfg := &config.MachineConfig{}
	cfg.Provision.Disk.PartitionLayout = &config.PartitionLayout{Table: "gpt", Partitions: []config.Partition{{Label: "root", Mountpoint: "/"}}}
	o := NewOrchestrator(
		cfg,
		&mockProvider{},
		disk.NewManager(cmd),
	)
	o.targetDisk = "/dev/sda"
	o.rootPartition = "/dev/sda1"

	if err := o.growPartition(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmd.calls) != 0 {
		t.Fatalf("expected no commands when grow-partition is skipped, got %d", len(cmd.calls))
	}
}

func TestResizeFilesystemSkippedForPartitionLayout(t *testing.T) {
	cmd := newMockCommander()
	cfg := &config.MachineConfig{}
	cfg.Provision.Disk.PartitionLayout = &config.PartitionLayout{Table: "gpt", Partitions: []config.Partition{{Label: "root", Mountpoint: "/"}}}
	o := NewOrchestrator(
		cfg,
		&mockProvider{},
		disk.NewManager(cmd),
	)
	o.rootPartition = "/dev/sda1"

	if err := o.resizeFilesystem(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmd.calls) != 0 {
		t.Fatalf("expected no commands when resize-filesystem is skipped, got %d", len(cmd.calls))
	}
}

func TestResizeFilesystemRunsForABPartitionLayout(t *testing.T) {
	cmd := newMockCommander()
	cfg := &config.MachineConfig{}
	cfg.Provision.Image.Mode = config.ImageModeAB
	cfg.Provision.Disk.PartitionLayout = &config.PartitionLayout{Table: "gpt", Partitions: []config.Partition{{Label: "root", Mountpoint: "/"}}}
	o := NewOrchestrator(
		cfg,
		&mockProvider{},
		disk.NewManager(cmd),
	)
	o.rootPartition = "/dev/sda3"

	if err := o.resizeFilesystem(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmd.calls) != 1 || cmd.calls[0].name != "resize2fs" || strings.Join(cmd.calls[0].args, " ") != "/dev/sda3" {
		t.Fatalf("expected resize2fs /dev/sda3 when resizing A/B root slot, got %#v", cmd.calls)
	}
}

func TestInjectCloudInit_Disabled(t *testing.T) {
	cfg := &config.MachineConfig{}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	if err := o.injectCloudInit(context.Background()); err != nil {
		t.Fatalf("expected no error when CloudInit disabled, got %v", err)
	}
}

func TestInjectCloudInit_UnsupportedDatasource(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.CloudInit.Enabled = true
	cfg.Provision.CloudInit.Datasource = "openstack"
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	err := o.injectCloudInit(context.Background())
	if err == nil {
		t.Fatal("expected error for unsupported datasource")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("expected 'unsupported' in error, got %v", err)
	}
}

func TestInjectCloudInit_NoCloudInject(t *testing.T) {
	cfg := &config.MachineConfig{
		Hostname: "test-host",
	}
	cfg.Provision.CloudInit.Enabled = true
	cfg.Provision.CloudInit.Datasource = "nocloud"
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	if err := o.injectCloudInit(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the seed files were created under rootDir.
	seedDir := filepath.Join(o.config.rootDir, "var", "lib", "cloud", "seed", "nocloud")
	for _, name := range []string{"user-data", "meta-data", "network-config"} {
		if _, err := os.Stat(filepath.Join(seedDir, name)); err != nil {
			t.Errorf("expected seed file %s to exist: %v", name, err)
		}
	}
}

func TestInjectCloudInit_DefaultDatasourceAndStableInstanceID(t *testing.T) {
	cfg := &config.MachineConfig{
		Hostname: "test-host",
	}
	cfg.Provision.CloudInit.Enabled = true
	cfg.Provision.ProviderID = "redfish://bmc.example/Systems/1"
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	if err := o.injectCloudInit(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	metaPath := filepath.Join(o.config.rootDir, "var", "lib", "cloud", "seed", "nocloud", "meta-data")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read meta-data: %v", err)
	}
	if !strings.Contains(string(data), "instance-id: redfish://bmc.example/Systems/1") {
		t.Fatalf("meta-data missing stable instance-id, got: %s", string(data))
	}
}

func TestInjectCloudInit_DatasourceCaseInsensitiveAndTrimmed(t *testing.T) {
	cfg := &config.MachineConfig{
		Hostname: "test-host",
	}
	cfg.Provision.CloudInit.Enabled = true
	cfg.Provision.CloudInit.Datasource = " NoCloud "
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	if err := o.injectCloudInit(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInjectCloudInit_TrimmedBondAndDNSValues(t *testing.T) {
	cfg := &config.MachineConfig{
		Hostname: "test-host",
	}
	cfg.Provision.CloudInit.Enabled = true
	cfg.Provision.CloudInit.Datasource = "nocloud"
	cfg.Network.Static.IP = "10.0.0.10/24"
	cfg.Network.Static.Gateway = "10.0.0.1"
	cfg.Network.Bond.Interfaces = "eth0, eth1, ,"
	cfg.Network.Bond.Mode = "802.3ad"
	cfg.Network.DNSResolvers = "8.8.8.8, 1.1.1.1, ,"
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	if err := o.injectCloudInit(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	netPath := filepath.Join(o.config.rootDir, "var", "lib", "cloud", "seed", "nocloud", "network-config")
	data, err := os.ReadFile(netPath)
	if err != nil {
		t.Fatalf("read network-config: %v", err)
	}

	var nc cloudinit.NetworkConfig
	if err := yaml.Unmarshal(data, &nc); err != nil {
		t.Fatalf("unmarshal network-config: %v", err)
	}

	bond, ok := nc.Bonds["bond0"]
	if !ok {
		t.Fatal("expected bond0 in network-config")
	}
	if len(bond.Interfaces) != 2 || bond.Interfaces[0] != "eth0" || bond.Interfaces[1] != "eth1" {
		t.Fatalf("unexpected bond interfaces: %v", bond.Interfaces)
	}
	if bond.Nameservers == nil || len(bond.Nameservers.Addresses) != 2 {
		t.Fatalf("unexpected nameservers: %+v", bond.Nameservers)
	}
	for _, addr := range bond.Nameservers.Addresses {
		if strings.TrimSpace(addr) != addr {
			t.Fatalf("nameserver has whitespace: %q", addr)
		}
	}
}

// TestDetectDisk_CharDeviceRejected verifies that detectDisk rejects a character
// device when DiskDevice is explicitly configured. Both the validatePartitionLayoutConfig
// and detectDisk code paths must reject character devices consistently.
func TestDetectDisk_CharDeviceRejected(t *testing.T) {
	// /dev/null is always a character device on Linux; use it as a stand-in for
	// a misconfigured char device path.
	charDevice := "/dev/null"
	info, err := os.Stat(charDevice)
	if err != nil {
		t.Skipf("cannot stat %s: %v", charDevice, err)
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		t.Skipf("%s is not a character device on this host", charDevice)
	}

	cfg := &config.MachineConfig{}
	cfg.Provision.Disk.Device = charDevice
	o := newTestOrchestrator(t, cfg, &mockProvider{})

	err = o.detectDisk(context.Background())
	if err == nil {
		t.Fatal("expected error when DiskDevice is a character device, got nil")
	}
	if !strings.Contains(err.Error(), "not a block device") {
		t.Fatalf("expected 'not a block device' in error, got: %v", err)
	}
}

func TestIsSensitiveEnvKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"AUTH_TOKEN", true},
		{"SECRET_VALUE", true},
		{"DB_PASSWORD", true},
		{"API_KEY", true},
		{"MY_CREDENTIAL", true},
		{"HOME", false},
		{"PATH", false},
		{"NETWORK_MODE", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := isSensitiveEnvKey(tt.key); got != tt.want {
				t.Errorf("isSensitiveEnvKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestStreamImagePartitionModeWipesDiskFirst(t *testing.T) {
	// Verify that WipeDisk is called before StreamPartitions in partition mode.
	// This ensures a clean slate on any retry and prevents data corruption.
	cmd := newMockCommander()
	wipeErr := fmt.Errorf("wipe failed")
	cmd.setResult("wipefs -af", nil, wipeErr)

	cfg := &config.MachineConfig{}
	cfg.Provision.Image.Mode = "partition"
	o := NewOrchestrator(cfg, &mockProvider{}, disk.NewManager(cmd))
	o.targetDisk = "/dev/sda"
	o.bestImageURL = "http://images.local/node.img.zst"

	err := o.streamImage(context.Background())
	if err == nil {
		t.Fatal("expected error when wipefs fails, got nil")
	}
	if !strings.Contains(err.Error(), "wiping disk before partition stream") {
		t.Fatalf("expected 'wiping disk before partition stream' in error, got: %v", err)
	}

	// sgdisk is called first by WipeDisk (before wipefs), so it must appear
	// as the first recorded command.
	if len(cmd.calls) < 1 {
		t.Fatal("expected at least one command to be recorded")
	}
	if cmd.calls[0].name != "sgdisk" {
		t.Fatalf("expected first command to be 'sgdisk' (WipeDisk), got %q", cmd.calls[0].name)
	}
}

func TestStopRAID(t *testing.T) {
	cfg := &config.MachineConfig{}
	o, cmd := newTestOrchestratorWithCommander(t, cfg, &mockProvider{})
	cmd.setResult("mdadm --stop", nil, nil)
	if err := o.stopRAID(context.Background()); err != nil {
		t.Fatalf("stopRAID: %v", err)
	}
}

func TestStopRAIDError(t *testing.T) {
	cfg := &config.MachineConfig{}
	o, cmd := newTestOrchestratorWithCommander(t, cfg, &mockProvider{})
	cmd.setResult("mdadm --stop", nil, fmt.Errorf("stop failed"))
	if err := o.stopRAID(context.Background()); err == nil {
		t.Fatal("expected error from stopRAID")
	}
}

func TestDisableLVMStep(t *testing.T) {
	cfg := &config.MachineConfig{}
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	if err := o.disableLVM(context.Background()); err != nil {
		t.Fatalf("disableLVM: %v", err)
	}
}

func TestEnableLVMStep(t *testing.T) {
	cfg := &config.MachineConfig{}
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	if err := o.enableLVM(context.Background()); err != nil {
		t.Fatalf("enableLVM: %v", err)
	}
}

func TestPartprobe(t *testing.T) {
	cfg := &config.MachineConfig{}
	o, cmd := newTestOrchestratorWithCommander(t, cfg, &mockProvider{})
	o.targetDisk = "/dev/sda"
	cmd.setResult("partprobe /dev/sda", nil, nil)
	if err := o.partprobe(context.Background()); err != nil {
		t.Fatalf("partprobe: %v", err)
	}
}

func TestPartprobeError(t *testing.T) {
	cfg := &config.MachineConfig{}
	o, cmd := newTestOrchestratorWithCommander(t, cfg, &mockProvider{})
	o.targetDisk = "/dev/sda"
	cmd.setResult("partprobe /dev/sda", nil, fmt.Errorf("partprobe: device busy"))
	cmd.setResult("blockdev --rereadpt", nil, fmt.Errorf("blockdev: device busy"))
	if err := o.partprobe(context.Background()); err == nil {
		t.Fatal("expected error from partprobe when both partprobe and blockdev fail")
	}
}

func TestCheckFilesystem(t *testing.T) {
	cfg := &config.MachineConfig{}
	o, cmd := newTestOrchestratorWithCommander(t, cfg, &mockProvider{})
	o.rootPartition = "/dev/sda2"
	cmd.setResult("blkid -o", nil, nil)
	cmd.setResult("e2fsck -fy", nil, nil)
	if err := o.checkFilesystem(context.Background()); err != nil {
		t.Fatalf("checkFilesystem: %v", err)
	}
}

func TestTeardownChrootReturnsJoinedErrors(t *testing.T) {
	cfg := &config.MachineConfig{}
	o, _ := newTestOrchestratorWithCommander(t, cfg, &mockProvider{})
	err := o.teardownChroot(context.Background())
	if err == nil {
		t.Fatal("expected error from teardownChroot when unmount fails on non-root host")
	}
}

func TestTeardownChrootKeepsABPreserveExistingMountedForKexec(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Image.Mode = config.ImageModeAB
	cfg.Provision.AB.PreserveExisting = true
	o, _ := newTestOrchestratorWithCommander(t, cfg, &mockProvider{})

	if err := o.teardownChroot(context.Background()); err != nil {
		t.Fatalf("teardownChroot should keep /newroot mounted for preserve-existing kexec: %v", err)
	}
}
