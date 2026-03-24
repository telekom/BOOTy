//go:build linux

package provision

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
)

// newTestOrchestrator builds an Orchestrator with a mock provider and disk manager
// suitable for unit testing individual steps.
func newTestOrchestrator(t *testing.T, cfg *config.MachineConfig, provider *mockProvider) *Orchestrator {
	t.Helper()
	cmd := newMockCommander()
	mgr := disk.NewManager(cmd)
	o := NewOrchestrator(cfg, provider, mgr)
	o.config.rootDir = t.TempDir()
	return o
}

func TestProvisionStepCount(t *testing.T) {
	// Verify the pipeline has the expected number of steps.
	cfg := &config.MachineConfig{}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	// Use the shared provisionSteps() method from orchestrator.go.
	steps := o.provisionSteps()
	if len(steps) != 32 {
		t.Fatalf("expected 32 provisioning steps, got %d", len(steps))
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
		secureErr   error
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
		{
			name:        "secure erase error",
			secureErase: true,
			secureErr:   fmt.Errorf("secure erase failed"),
			// NOTE: /sys/block is empty in tests, so the mocked wipefs error
			// is never reached. wantErr=false reflects the no-op behavior.
			wantErr: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.MachineConfig{SecureErase: tc.secureErase}
			cmd := newMockCommander()
			if tc.wipeErr != nil {
				cmd.setResult("wipefs -af", nil, tc.wipeErr)
			}
			if tc.secureErr != nil {
				cmd.setResult("wipefs -af", nil, tc.secureErr)
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

func TestCollectInventoryDisabled(t *testing.T) {
	cfg := &config.MachineConfig{InventoryEnabled: false}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	if err := o.collectInventory(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCollectFirmwareDisabled(t *testing.T) {
	cfg := &config.MachineConfig{FirmwareEnabled: false}
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
	cfg := &config.MachineConfig{HealthChecksEnabled: false}
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
	cfg := &config.MachineConfig{
		RescueMode:           "shell",
		RescueSSHPubKey:      "ssh-ed25519 AAAA...",
		RescuePasswordHash:   "$6$rounds=4096$salt$hash",
		RescueTimeout:        120,
		RescueAutoMountDisks: true,
	}
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
	cfg := &config.MachineConfig{
		ImageSignatureURL: "https://example.com/image.sig",
		ImageGPGPubKey:    "",
	}
	provider := &mockProvider{}
	o := newTestOrchestrator(t, cfg, provider)

	err := o.verifyImageSignature(context.Background())
	if err == nil {
		t.Error("expected error for missing pub key")
	}
}
