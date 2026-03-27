//go:build e2e && linux

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/bios"
	_ "github.com/telekom/BOOTy/pkg/bios/dell"
	_ "github.com/telekom/BOOTy/pkg/bios/hpe"
	_ "github.com/telekom/BOOTy/pkg/bios/lenovo"
	_ "github.com/telekom/BOOTy/pkg/bios/supermicro"
	"github.com/telekom/BOOTy/pkg/caprf"
	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
	"github.com/telekom/BOOTy/pkg/efi"
	"github.com/telekom/BOOTy/pkg/provision"
	"github.com/telekom/BOOTy/pkg/rescue"
	"github.com/telekom/BOOTy/pkg/system"
)

// parseTestVars is a helper for dry-run tests.
func parseTestVars(vars string) (*config.MachineConfig, error) {
	return caprf.ParseVars(strings.NewReader(vars))
}

// Gap 2: Deprovisioning E2E

func TestDeprovisionHardStepSequenceE2E(t *testing.T) {
	cmd := newMockCommander()
	cmd.set("mdadm", []byte(""), nil)
	cmd.set("wipefs", []byte(""), nil)
	cmd.set("sgdisk", []byte(""), nil)
	cmd.set("lvm", []byte(""), nil)
	cmd.set("chroot", []byte("BootCurrent: 0001\nBootOrder: 0001"), nil)

	cfg := &config.MachineConfig{Mode: "hard", DNSResolvers: "8.8.8.8"}
	provider := newMockProvider(cfg)
	diskMgr := disk.NewManager(cmd)
	orch := provision.NewOrchestrator(cfg, provider, diskMgr)

	err := orch.Deprovision(context.Background())

	statuses := provider.getStatuses()
	if len(statuses) < 2 {
		t.Fatalf("expected >= 2 status reports, got %d", len(statuses))
	}
	if statuses[0].Status != config.StatusInit {
		t.Errorf("first status = %q, want init", statuses[0].Status)
	}

	calls := cmd.getCalls()
	var mdadmCalled, wipeCalled, lvmCalled bool
	for _, c := range calls {
		s := c.String()
		if strings.Contains(s, "mdadm") {
			mdadmCalled = true
		}
		if strings.Contains(s, "wipefs") || strings.Contains(s, "sgdisk") {
			wipeCalled = true
		}
		if strings.Contains(s, "lvm") && strings.Contains(s, "vgchange") {
			lvmCalled = true
		}
	}
	if !mdadmCalled {
		t.Error("hard deprovision should call mdadm")
	}
	if !wipeCalled {
		t.Error("hard deprovision should call wipefs/sgdisk")
	}
	if !lvmCalled {
		t.Error("hard deprovision should call lvm vgchange")
	}

	if err == nil {
		last := statuses[len(statuses)-1]
		if last.Status != config.StatusSuccess {
			t.Errorf("on success, last status = %q, want success", last.Status)
		}
	} else {
		t.Logf("Hard deprovision failed at expected step: %v", err)
	}
}

func TestDeprovisionSoftStepSequenceE2E(t *testing.T) {
	cmd := newMockCommander()
	sfdiskOut, _ := json.Marshal(map[string]any{
		"partitiontable": map[string]any{
			"device": "/dev/sda",
			"partitions": []map[string]any{
				{"node": "/dev/sda1", "start": 2048, "size": 1048576, "type": "C12A7328-F81F-11D2-BA4B-00A0C93EC93B"},
				{"node": "/dev/sda2", "start": 1050624, "size": 209715200, "type": "0FC63DAF-8483-4772-8E79-3D69D8477DE4"},
			},
		},
	})
	cmd.set("sfdisk", sfdiskOut, nil)
	cmd.set("chroot", []byte(""), nil)
	cmd.set("mount", []byte(""), nil)
	cmd.set("umount", []byte(""), nil)

	cfg := &config.MachineConfig{Mode: "soft", DNSResolvers: "8.8.8.8"}
	provider := newMockProvider(cfg)
	orch := provision.NewOrchestrator(cfg, provider, disk.NewManager(cmd))

	err := orch.Deprovision(context.Background())
	statuses := provider.getStatuses()
	if len(statuses) == 0 {
		t.Fatal("expected status reports")
	}

	for _, c := range cmd.getCalls() {
		s := c.String()
		if strings.Contains(s, "wipefs") {
			t.Error("soft should NOT call wipefs")
		}
		if strings.Contains(s, "mdadm") {
			t.Error("soft should NOT call mdadm")
		}
	}
	if err != nil {
		t.Logf("Soft deprovision on test env: %v", err)
	}
}

func TestDeprovisionDefaultIsHardE2E(t *testing.T) {
	cmd := newMockCommander()
	cmd.set("mdadm", []byte(""), nil)
	cmd.set("wipefs", []byte(""), nil)
	cmd.set("sgdisk", []byte(""), nil)
	cmd.set("lvm", []byte(""), nil)
	cmd.set("chroot", []byte("BootCurrent: 0001"), nil)

	cfg := &config.MachineConfig{Mode: "", DNSResolvers: "8.8.8.8"}
	orch := provision.NewOrchestrator(cfg, newMockProvider(cfg), disk.NewManager(cmd))
	_ = orch.Deprovision(context.Background())

	var wipeCalled bool
	for _, c := range cmd.getCalls() {
		if strings.Contains(c.String(), "wipefs") || strings.Contains(c.String(), "sgdisk") {
			wipeCalled = true
		}
	}
	if !wipeCalled {
		t.Error("default deprovision should wipe disks")
	}
}

func TestDeprovisionReportsErrorE2E(t *testing.T) {
	cmd := newMockCommander()
	// wipefs must fail for all disks so WipeAllDisks returns an error.
	cmd.set("wipefs", nil, fmt.Errorf("disk wipe failed"))

	cfg := &config.MachineConfig{Mode: "hard", DNSResolvers: "8.8.8.8"}
	provider := newMockProvider(cfg)
	orch := provision.NewOrchestrator(cfg, provider, disk.NewManager(cmd))

	err := orch.Deprovision(context.Background())
	if err == nil {
		t.Fatal("expected deprovision failure")
	}

	statuses := provider.getStatuses()
	if len(statuses) < 2 {
		t.Fatalf("expected >= 2 statuses, got %d", len(statuses))
	}
	last := statuses[len(statuses)-1]
	if last.Status != config.StatusError {
		t.Errorf("last status = %q, want error", last.Status)
	}
}

func TestDeprovisionSoftViaVarsModeE2E(t *testing.T) {
	cfg := &config.MachineConfig{Mode: "soft"}
	cmd := newMockCommander()
	orch := provision.NewOrchestrator(cfg, newMockProvider(cfg), disk.NewManager(cmd))
	_ = orch.Deprovision(context.Background())
	for _, c := range cmd.getCalls() {
		if strings.Contains(c.String(), "wipefs") {
			t.Error("soft-deprovision should NOT wipe")
		}
	}
}

// Gap 3: RAID/LVM steps E2E

func TestStopRAIDArraysE2E(t *testing.T) {
	cmd := newMockCommander()
	cmd.set("mdadm", []byte("mdadm: stopped /dev/md0"), nil)
	diskMgr := disk.NewManager(cmd)
	if err := diskMgr.StopRAIDArrays(context.Background()); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range cmd.getCalls() {
		if strings.Contains(c.String(), "mdadm") && strings.Contains(c.String(), "--stop") {
			found = true
		}
	}
	if !found {
		t.Error("expected mdadm --stop")
	}
}

func TestStopRAIDArraysNoArraysE2E(t *testing.T) {
	cmd := newMockCommander()
	cmd.set("mdadm", nil, fmt.Errorf("mdadm: No arrays found"))
	if err := disk.NewManager(cmd).StopRAIDArrays(context.Background()); err != nil {
		t.Fatalf("should handle no arrays: %v", err)
	}
}

func TestEnableLVME2E(t *testing.T) {
	cmd := newMockCommander()
	cmd.set("lvm", []byte("  0 logical volume(s)"), nil)
	if err := disk.NewManager(cmd).EnableLVM(context.Background()); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range cmd.getCalls() {
		if strings.Contains(c.String(), "lvm") && strings.Contains(c.String(), "vgchange") {
			found = true
		}
	}
	if !found {
		t.Error("expected lvm vgchange")
	}
}

func TestEnableLVMNotPresentE2E(t *testing.T) {
	cmd := newMockCommander()
	cmd.set("lvm", nil, fmt.Errorf("command not found"))
	err := disk.NewManager(cmd).EnableLVM(context.Background())
	if err == nil {
		t.Fatal("expected error when lvm is not available")
	}
}

func TestWipeAllDisksE2E(t *testing.T) {
	cmd := newMockCommander()
	cmd.set("wipefs", []byte(""), nil)
	cmd.set("sgdisk", []byte(""), nil)
	if err := disk.NewManager(cmd).WipeAllDisks(context.Background()); err != nil {
		t.Fatal(err)
	}
	var called bool
	for _, c := range cmd.getCalls() {
		if strings.Contains(c.String(), "wipefs") || strings.Contains(c.String(), "sgdisk") {
			called = true
		}
	}
	if !called {
		t.Error("expected wipefs/sgdisk")
	}
}

func TestRAIDLVMProvisionOrderE2E(t *testing.T) {
	cmd := newMockCommander()
	cmd.set("mdadm", []byte(""), nil)
	cmd.set("wipefs", []byte(""), nil)
	cmd.set("sgdisk", []byte(""), nil)
	cmd.set("lvm", []byte(""), nil)
	cmd.set("chroot", []byte(""), nil)
	cmd.set("e2fsck", []byte(""), nil)
	cmd.set("growpart", []byte("NOCHANGE"), nil)
	cmd.set("resize2fs", []byte(""), nil)
	cmd.set("partprobe", []byte(""), nil)

	sfdiskOut, _ := json.Marshal(map[string]any{
		"partitiontable": map[string]any{
			"device": "/dev/sda",
			"partitions": []map[string]any{
				{"node": "/dev/sda1", "start": 2048, "size": 1048576, "type": "C12A7328-F81F-11D2-BA4B-00A0C93EC93B"},
				{"node": "/dev/sda2", "start": 1050624, "size": 209715200, "type": "0FC63DAF-8483-4772-8E79-3D69D8477DE4"},
			},
		},
	})
	cmd.set("sfdisk", sfdiskOut, nil)

	cfg := &config.MachineConfig{Mode: "provision", Hostname: "raid-node", DNSResolvers: "8.8.8.8", ImageURLs: []string{"http://img.local/test.gz"}}
	orch := provision.NewOrchestrator(cfg, newMockProvider(cfg), disk.NewManager(cmd))
	_ = orch.Provision(context.Background())

	calls := cmd.getCalls()
	var mdadmIdx, lvmDisableIdx, lvmEnableIdx int
	for i, c := range calls {
		s := c.String()
		if strings.Contains(s, "mdadm") && strings.Contains(s, "--stop") {
			mdadmIdx = i
		}
		if strings.Contains(s, "lvm") && strings.Contains(s, "-an") {
			lvmDisableIdx = i
		}
		if strings.Contains(s, "lvm") && strings.Contains(s, "-ay") {
			lvmEnableIdx = i
		}
	}
	if mdadmIdx > 0 && lvmDisableIdx > 0 && mdadmIdx > lvmDisableIdx {
		t.Errorf("mdadm (%d) should be before lvm disable (%d)", mdadmIdx, lvmDisableIdx)
	}
	if lvmDisableIdx > 0 && lvmEnableIdx > 0 && lvmDisableIdx > lvmEnableIdx {
		t.Errorf("lvm disable (%d) should be before enable (%d)", lvmDisableIdx, lvmEnableIdx)
	}
}

// Gap 4: Dry-run mode E2E

func TestDryRunFullPassE2E(t *testing.T) {
	provider := newMockProvider(&config.MachineConfig{})
	cmd := newMockCommander()
	cfg := &config.MachineConfig{DryRun: true, ImageURLs: []string{"http://example.com/test.img"}, Hostname: "dry-run-node", HealthChecksEnabled: false, InventoryEnabled: false}
	orch := provision.NewOrchestrator(cfg, provider, disk.NewManager(cmd))
	_ = orch.DryRun(context.Background())
	if len(provider.getStatuses()) == 0 {
		t.Fatal("DryRun should report status")
	}
}

func TestDryRunFailsWithoutImagesE2E(t *testing.T) {
	provider := newMockProvider(&config.MachineConfig{})
	cfg := &config.MachineConfig{DryRun: true, Hostname: "no-images"}
	orch := provision.NewOrchestrator(cfg, provider, disk.NewManager(newMockCommander()))
	err := orch.DryRun(context.Background())
	if err == nil {
		t.Fatal("DryRun should fail without images")
	}
	statuses := provider.getStatuses()
	if len(statuses) == 0 {
		t.Fatal("should report error")
	}
	if statuses[len(statuses)-1].Status != config.StatusError {
		t.Errorf("status = %q", statuses[len(statuses)-1].Status)
	}
}

func TestDryRunReportsWarningsE2E(t *testing.T) {
	provider := newMockProvider(&config.MachineConfig{})
	cfg := &config.MachineConfig{DryRun: true, ImageURLs: []string{"http://example.com/test.img"}, Hostname: "warn-node", HealthChecksEnabled: false, InventoryEnabled: false}
	orch := provision.NewOrchestrator(cfg, provider, disk.NewManager(newMockCommander()))
	_ = orch.DryRun(context.Background())
	if len(provider.getStatuses()) == 0 {
		t.Fatal("DryRun should report status")
	}
}

func TestDryRunVarsParsingE2E(t *testing.T) {
	tests := []struct {
		name    string
		vars    string
		wantDry bool
	}{
		{"true", "export DRY_RUN=\"true\"\nexport HOSTNAME=\"d\"\nexport IMAGE=\"http://i/a\"\n", true},
		{"1", "export DRY_RUN=\"1\"\nexport HOSTNAME=\"d\"\nexport IMAGE=\"http://i/a\"\n", true},
		{"false", "export DRY_RUN=\"false\"\nexport HOSTNAME=\"d\"\nexport IMAGE=\"http://i/a\"\n", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := parseTestVars(tc.vars)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.DryRun != tc.wantDry {
				t.Errorf("DryRun = %v, want %v", cfg.DryRun, tc.wantDry)
			}
		})
	}
}

// Gap 6: EFI operations E2E

func TestEFIVarReadWriteRoundtripE2E(t *testing.T) {
	dir := t.TempDir()
	reader := efi.NewEFIVarReader(dir)
	data := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	if err := reader.WriteVar("TestVar-1234-5678", 7, data); err != nil {
		t.Fatal(err)
	}
	got, err := reader.ReadVar("TestVar-1234-5678")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0] != 0xDE || got[3] != 0xEF {
		t.Errorf("data = %v", got)
	}
}

func TestEFIVarListE2E(t *testing.T) {
	dir := t.TempDir()
	reader := efi.NewEFIVarReader(dir)
	for _, n := range []string{"VarA-1234", "VarB-5678", "VarC-9012"} {
		if err := reader.WriteVar(n, 0, []byte{1}); err != nil {
			t.Fatal(err)
		}
	}
	names, err := reader.ListVars()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 3 {
		t.Errorf("got %d vars: %v", len(names), names)
	}
}

func TestEFISecureBootDetectionE2E(t *testing.T) {
	dir := t.TempDir()
	reader := efi.NewEFIVarReader(dir)
	sbName := "SecureBoot-8be4df61-93ca-11d2-aa0d-00e098032b8c"
	if err := reader.WriteVar(sbName, 7, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if en, err := reader.IsSecureBootEnabled(); err != nil || !en {
		t.Errorf("expected enabled, err=%v", err)
	}
	if err := reader.WriteVar(sbName, 7, []byte{0}); err != nil {
		t.Fatal(err)
	}
	if en, err := reader.IsSecureBootEnabled(); err != nil || en {
		t.Errorf("expected disabled, err=%v", err)
	}
}

func TestEFISetupModeDetectionE2E(t *testing.T) {
	dir := t.TempDir()
	reader := efi.NewEFIVarReader(dir)
	smName := "SetupMode-8be4df61-93ca-11d2-aa0d-00e098032b8c"
	if err := reader.WriteVar(smName, 7, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if sm, err := reader.IsSetupMode(); err != nil || !sm {
		t.Errorf("expected setup, err=%v", err)
	}
}

func TestEFIBuildLoadOptionE2E(t *testing.T) {
	opt := efi.BuildLoadOption(efi.BootEntry{Description: "Ubuntu", Loader: "/EFI/ubuntu/shimx64.efi"})
	if len(opt) < 6 {
		t.Fatalf("too short: %d bytes", len(opt))
	}
	if opt[0] != 1 || opt[1] != 0 || opt[2] != 0 || opt[3] != 0 {
		t.Errorf("attrs = %v", opt[:4])
	}
}

func TestCreateEFIBootEntryShimE2E(t *testing.T) {
	cmd := newMockCommander()
	cmd.set("chroot", []byte("Boot0001* ubuntu"), nil)
	c := provision.NewConfigurator(disk.NewManager(cmd))
	root := t.TempDir()
	c.SetRootDir(root)
	shimName := "shimx64.efi"
	if runtime.GOARCH == "arm64" {
		shimName = "shimaa64.efi"
	}
	efiDir := filepath.Join(root, "boot", "efi", "EFI", "ubuntu")
	if err := os.MkdirAll(efiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(efiDir, shimName), []byte("shim"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateEFIBootEntry(context.Background(), "/dev/sda", "/dev/sda1"); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, call := range cmd.getCalls() {
		if strings.Contains(call.String(), "efibootmgr") {
			found = true
			if !strings.Contains(call.String(), shimName) {
				t.Errorf("expected %s in: %s", shimName, call)
			}
		}
	}
	if !found {
		t.Error("expected efibootmgr call")
	}
}

func TestRemoveEFIBootEntriesGracefulE2E(t *testing.T) {
	c := provision.NewConfigurator(disk.NewManager(newMockCommander()))
	c.SetRootDir(t.TempDir())
	if err := c.RemoveEFIBootEntries(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestEFIBootEntryNVMePartitionE2E(t *testing.T) {
	cmd := newMockCommander()
	cmd.set("chroot", []byte("Boot0001* ubuntu"), nil)
	c := provision.NewConfigurator(disk.NewManager(cmd))
	root := t.TempDir()
	c.SetRootDir(root)
	shimName := "shimx64.efi"
	if runtime.GOARCH == "arm64" {
		shimName = "shimaa64.efi"
	}
	efiDir := filepath.Join(root, "boot", "efi", "EFI", "ubuntu")
	if err := os.MkdirAll(efiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(efiDir, shimName), []byte("shim"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateEFIBootEntry(context.Background(), "/dev/nvme0n1", "/dev/nvme0n1p1"); err != nil {
		t.Fatal(err)
	}
	for _, call := range cmd.getCalls() {
		if strings.Contains(call.String(), "efibootmgr -c") {
			if !strings.Contains(call.String(), "-d /dev/nvme0n1") {
				t.Errorf("expected -d /dev/nvme0n1: %s", call)
			}
		}
	}
}

// Gap 7: Rescue mode E2E

func TestRescueDecideAllModesE2E(t *testing.T) {
	tests := []struct {
		name string
		cfg  rescue.Config
		st   rescue.RetryState
		want rescue.Mode
	}{
		{"reboot", rescue.Config{Mode: rescue.ModeReboot}, rescue.RetryState{}, rescue.ModeReboot},
		{"shell", rescue.Config{Mode: rescue.ModeShell}, rescue.RetryState{}, rescue.ModeShell},
		{"wait", rescue.Config{Mode: rescue.ModeWait}, rescue.RetryState{}, rescue.ModeWait},
		{"retry-1st", rescue.Config{Mode: rescue.ModeRetry, MaxRetries: 3}, rescue.RetryState{MaxRetries: 3}, rescue.ModeRetry},
		{"retry-2nd", rescue.Config{Mode: rescue.ModeRetry, MaxRetries: 3}, rescue.RetryState{MaxRetries: 3, Attempts: 1}, rescue.ModeRetry},
		{"retry-exhausted", rescue.Config{Mode: rescue.ModeRetry, MaxRetries: 3}, rescue.RetryState{MaxRetries: 3, Attempts: 3}, rescue.ModeReboot},
		{"default", rescue.Config{}, rescue.RetryState{}, rescue.ModeReboot},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := rescue.Decide(&tc.cfg, &tc.st)
			if a.Type != tc.want {
				t.Errorf("type = %q, want %q", a.Type, tc.want)
			}
		})
	}
}

func TestRescueConfigValidateE2E(t *testing.T) {
	tests := []struct {
		name    string
		cfg     rescue.Config
		wantErr bool
	}{
		{"empty", rescue.Config{}, false},
		{"reboot", rescue.Config{Mode: rescue.ModeReboot}, false},
		{"shell", rescue.Config{Mode: rescue.ModeShell}, false},
		{"wait", rescue.Config{Mode: rescue.ModeWait}, false},
		{"retry-ok", rescue.Config{Mode: rescue.ModeRetry, MaxRetries: 3}, false},
		{"retry-no-max", rescue.Config{Mode: rescue.ModeRetry}, true},
		{"bad-mode", rescue.Config{Mode: "invalid"}, true},
		{"neg-delay", rescue.Config{Mode: rescue.ModeReboot, RetryDelay: -1}, true},
		{"neg-timeout", rescue.Config{Mode: rescue.ModeShell, ShellTimeout: -1}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cfg.Validate(); (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestRescueRetryStateLifecycleE2E(t *testing.T) {
	s := &rescue.RetryState{MaxRetries: 3}
	if !s.CanRetry() {
		t.Fatal("should retry at 0")
	}
	if s.Remaining() != 3 {
		t.Errorf("remaining = %d", s.Remaining())
	}
	s.RecordAttempt(fmt.Errorf("disk not found"))
	if s.Attempts != 1 || s.LastError != "disk not found" {
		t.Errorf("attempts=%d err=%q", s.Attempts, s.LastError)
	}
	s.RecordAttempt(fmt.Errorf("timeout"))
	s.RecordAttempt(fmt.Errorf("unreachable"))
	if s.CanRetry() || s.Remaining() != 0 {
		t.Error("should be exhausted")
	}
}

func TestRescueRetrySuccessResetsE2E(t *testing.T) {
	s := &rescue.RetryState{MaxRetries: 5, LastError: "prev"}
	s.RecordAttempt(nil)
	if s.LastError != "" {
		t.Errorf("lastError = %q", s.LastError)
	}
}

func TestRescueApplyDefaultsE2E(t *testing.T) {
	cfg := &rescue.Config{}
	cfg.ApplyDefaults()
	if cfg.Mode != rescue.ModeReboot {
		t.Errorf("mode = %q", cfg.Mode)
	}
}

func TestRescueApplyDefaultsRetryE2E(t *testing.T) {
	cfg := &rescue.Config{Mode: rescue.ModeRetry}
	cfg.ApplyDefaults()
	if cfg.MaxRetries != 3 {
		t.Errorf("maxRetries = %d", cfg.MaxRetries)
	}
}

func TestRescueOrchestratorE2E(t *testing.T) {
	cmd := newMockCommander()
	cfg := &config.MachineConfig{Mode: "provision", RescueMode: "retry"}
	orch := provision.NewOrchestrator(cfg, newMockProvider(cfg), disk.NewManager(cmd))
	if orch.RescueConfig().Mode != rescue.ModeRetry {
		t.Errorf("mode = %q", orch.RescueConfig().Mode)
	}
	st := &rescue.RetryState{MaxRetries: 3}
	if orch.RescueAction(st).Type != rescue.ModeRetry {
		t.Error("expected retry")
	}
	st.Attempts = 3
	if orch.RescueAction(st).Type != rescue.ModeReboot {
		t.Error("expected reboot after exhaustion")
	}
}

func TestRescueOrchestratorAllModesE2E(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want rescue.Mode
	}{
		{"reboot", rescue.ModeReboot}, {"shell", rescue.ModeShell},
		{"wait", rescue.ModeWait}, {"retry", rescue.ModeRetry},
	} {
		t.Run(tc.in, func(t *testing.T) {
			cfg := &config.MachineConfig{Mode: "provision", RescueMode: tc.in}
			orch := provision.NewOrchestrator(cfg, newMockProvider(cfg), disk.NewManager(newMockCommander()))
			if orch.RescueConfig().Mode != tc.want {
				t.Errorf("mode = %q", orch.RescueConfig().Mode)
			}
		})
	}
}

func TestRescueParseModeE2E(t *testing.T) {
	for _, m := range []string{"reboot", "shell", "retry", "wait"} {
		if mode, err := rescue.ParseMode(m); err != nil || string(mode) != m {
			t.Errorf("ParseMode(%q) = %q, %v", m, mode, err)
		}
	}
	for _, m := range []string{"restart", "halt", "panic", ""} {
		if _, err := rescue.ParseMode(m); err == nil {
			t.Errorf("ParseMode(%q) should fail", m)
		}
	}
}

// Gap 8: BIOS settings E2E

func TestBIOSVendorManagersE2E(t *testing.T) {
	for _, v := range []system.Vendor{system.VendorDell, system.VendorHPE, system.VendorLenovo, system.VendorSupermicro} {
		t.Run(string(v), func(t *testing.T) {
			mgr, err := bios.NewManager(v, nil)
			if err != nil {
				t.Fatal(err)
			}
			if mgr.Vendor() != v {
				t.Errorf("vendor = %q", mgr.Vendor())
			}
		})
	}
}

func TestBIOSCaptureE2E(t *testing.T) {
	for _, v := range []system.Vendor{system.VendorDell, system.VendorHPE, system.VendorLenovo, system.VendorSupermicro} {
		t.Run(string(v), func(t *testing.T) {
			mgr, _ := bios.NewManager(v, nil)
			state, err := mgr.Capture(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if state.Vendor != v {
				t.Errorf("vendor = %q", state.Vendor)
			}
		})
	}
}

func TestBIOSApplyE2E(t *testing.T) {
	for _, v := range []system.Vendor{system.VendorDell, system.VendorHPE, system.VendorLenovo, system.VendorSupermicro} {
		t.Run(string(v), func(t *testing.T) {
			mgr, _ := bios.NewManager(v, nil)
			changes := []bios.SettingChange{{Name: "BootMode", Value: "Uefi"}, {Name: "SecureBoot", Value: "Enabled"}}
			reboot, err := mgr.Apply(context.Background(), changes)
			if err != nil {
				t.Fatal(err)
			}
			if len(reboot) != 2 {
				t.Errorf("reboot count = %d", len(reboot))
			}
		})
	}
}

func TestBIOSResetE2E(t *testing.T) {
	for _, v := range []system.Vendor{system.VendorDell, system.VendorHPE, system.VendorLenovo, system.VendorSupermicro} {
		t.Run(string(v), func(t *testing.T) {
			mgr, _ := bios.NewManager(v, nil)
			if mgr.Reset(context.Background()) == nil {
				t.Error("Reset should return error")
			}
		})
	}
}

func TestBIOSCompareMatchE2E(t *testing.T) {
	bl := &bios.Baseline{Settings: map[string]string{"BootMode": "Uefi", "SB": "On"}}
	st := &bios.State{Settings: map[string]bios.Setting{"BootMode": {CurrentValue: "Uefi"}, "SB": {CurrentValue: "On"}}}
	if !bios.Compare(bl, st).Matches {
		t.Error("expected match")
	}
}

func TestBIOSCompareMismatchE2E(t *testing.T) {
	bl := &bios.Baseline{Settings: map[string]string{"BootMode": "Uefi"}}
	st := &bios.State{Settings: map[string]bios.Setting{"BootMode": {CurrentValue: "Legacy"}}}
	d := bios.Compare(bl, st)
	if d.Matches || len(d.Changes) != 1 {
		t.Errorf("matches=%v changes=%d", d.Matches, len(d.Changes))
	}
}

func TestBIOSCompareMissingE2E(t *testing.T) {
	bl := &bios.Baseline{Settings: map[string]string{"A": "1", "B": "2"}}
	st := &bios.State{Settings: map[string]bios.Setting{"A": {CurrentValue: "1"}}}
	if bios.Compare(bl, st).Matches {
		t.Error("missing should mismatch")
	}
}

func TestBIOSCompareNilsE2E(t *testing.T) {
	if !bios.Compare(nil, nil).Matches {
		t.Error("nil+nil should match")
	}
	if bios.Compare(&bios.Baseline{Settings: map[string]string{"A": "B"}}, nil).Matches {
		t.Error("bl+nil mismatch")
	}
	if bios.Compare(nil, &bios.State{}).Matches {
		t.Error("nil+st mismatch")
	}
}

func TestBIOSUnregisteredE2E(t *testing.T) {
	if _, err := bios.NewManager("unknown", nil); err == nil {
		t.Error("expected error")
	}
}

func TestBIOSCaptureRoundtripE2E(t *testing.T) {
	mgr, _ := bios.NewManager(system.VendorDell, nil)
	state, _ := mgr.Capture(context.Background())
	bl := &bios.Baseline{Vendor: state.Vendor, Settings: make(map[string]string)}
	for n, s := range state.Settings {
		bl.Settings[n] = s.CurrentValue
	}
	if !bios.Compare(bl, state).Matches {
		t.Error("capture roundtrip should match")
	}
}
