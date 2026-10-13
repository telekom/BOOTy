//go:build linux

package provision

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
)

// withEFIFirmware points /sys/firmware/efi at a temporary path that exists
// (UEFI boot) or not (BIOS boot). efivarfs is never mounted there.
func withEFIFirmware(t *testing.T, uefi bool) {
	t.Helper()
	oldFirmwarePath, oldVarsPath := efiFirmwarePath, efiVarsPath
	efiFirmwarePath = filepath.Join(t.TempDir(), "efi")
	efiVarsPath = filepath.Join(efiFirmwarePath, "efivars")
	if uefi {
		if err := os.MkdirAll(efiFirmwarePath, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { efiFirmwarePath, efiVarsPath = oldFirmwarePath, oldVarsPath })
}

func withHostCommands(t *testing.T, fn func(name string, args ...string) ([]byte, error)) {
	t.Helper()
	old := hostCommandCombinedOutput
	hostCommandCombinedOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		return fn(name, args...)
	}
	t.Cleanup(func() { hostCommandCombinedOutput = old })
}

// The legacy scripts ran mount -t efivarfs and efibootmgr with set -e on
// UEFI, so a failure stopped the run. On BIOS they skipped the EFI steps.
func TestEFIBootEntryStepsFailOnUEFI(t *testing.T) {
	efibootmgr := func(out string, listErr, removeErr error) func(string, ...string) ([]byte, error) {
		return func(name string, args ...string) ([]byte, error) {
			switch {
			case name == "efibootmgr" && len(args) == 0:
				return []byte(out), listErr
			case name == "efibootmgr":
				return []byte("Could not delete variable"), removeErr
			}
			return []byte("modprobe: not found"), os.ErrNotExist
		}
	}
	listing := "BootOrder: 0001,0002\nBoot0001* ubuntu\tHD(1,GPT,x)/File(\\EFI\\ubuntu\\shimx64.efi)\nBoot0002* PXE\n"
	tests := []struct {
		name     string
		uefi     bool
		required *bool
		step     func(*Orchestrator) error
		commands func(string, ...string) ([]byte, error)
		efiReady func() (bool, string)
		wantErr  string
	}{
		{name: "mount fails on UEFI", uefi: true, step: mountStep, commands: efibootmgr("", nil, nil), wantErr: "mount efivarfs"},
		{name: "mount fails on UEFI, not required", uefi: true, required: new(false), step: mountStep, commands: efibootmgr("", nil, nil)},
		{name: "mount skipped on BIOS", step: mountStep, commands: efibootmgr("", nil, nil)},
		{name: "list fails on UEFI", uefi: true, step: removeStep, commands: efibootmgr("", errors.New("EFI variables are not supported"), nil), wantErr: "efibootmgr list"},
		{name: "remove fails on UEFI", uefi: true, step: removeStep, commands: efibootmgr(listing, nil, errors.New("exit 5")), wantErr: "efibootmgr remove Boot0001"},
		{name: "remove fails on UEFI, not required", uefi: true, required: new(false), step: removeStep, commands: efibootmgr(listing, nil, errors.New("exit 5"))},
		{name: "remove works on UEFI", uefi: true, step: removeStep, commands: efibootmgr(listing, nil, nil)},
		{name: "remove skipped on BIOS", step: removeStep, commands: efibootmgr("", errors.New("EFI variables are not supported"), nil)},
		{name: "create without efivarfs on UEFI", uefi: true, step: createStep, commands: efibootmgr("", nil, nil),
			efiReady: func() (bool, string) { return false, "efivarfs not mounted" }, wantErr: "efivarfs not mounted"},
		{name: "create without efivarfs on UEFI, not required", uefi: true, required: new(false), step: createStep, commands: efibootmgr("", nil, nil),
			efiReady: func() (bool, string) { return false, "efivarfs not mounted" }},
		{name: "create skipped on BIOS", step: createStep, commands: efibootmgr("", nil, nil),
			efiReady: func() (bool, string) { return false, efiNotBootedReason }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withEFIFirmware(t, tt.uefi)
			withHostCommands(t, tt.commands)
			oldMount := mountEFIVarFS
			mountEFIVarFS = func(string, string, string, uintptr, string) error { return syscall.ENODEV }
			t.Cleanup(func() { mountEFIVarFS = oldMount })
			if tt.efiReady != nil {
				old := efiRuntimeReady
				efiRuntimeReady = tt.efiReady
				t.Cleanup(func() { efiRuntimeReady = old })
			}
			cfg := &config.MachineConfig{}
			cfg.Provision.EFIBootEntryRequired = tt.required
			o := newTestOrchestrator(t, cfg, &mockProvider{})
			o.targetDisk, o.bootPartition = "/dev/sda", "/dev/sda1"
			err := tt.step(o)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("step error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("step error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func mountStep(o *Orchestrator) error  { return o.mountEFIVars(context.Background()) }
func removeStep(o *Orchestrator) error { return o.removeEFIBootEntries(context.Background()) }
func createStep(o *Orchestrator) error { return o.createEFIBootEntry(context.Background()) }

// MODE=decommission keeps going without EFI variables, like decommission_wipe.
func TestDecommissionMountsEFIVarsBestEffort(t *testing.T) {
	withEFIFirmware(t, true)
	withHostCommands(t, func(string, ...string) ([]byte, error) { return nil, os.ErrNotExist })
	oldMount := mountEFIVarFS
	mountEFIVarFS = func(string, string, string, uintptr, string) error { return syscall.ENODEV }
	t.Cleanup(func() { mountEFIVarFS = oldMount })
	o := newTestOrchestrator(t, &config.MachineConfig{}, &mockProvider{})
	for _, step := range o.deprovisionSteps("decommission") {
		if step.Name != "mount-efivarfs" {
			continue
		}
		if err := step.Fn(context.Background()); err != nil {
			t.Fatalf("decommission mount-efivarfs = %v, want nil", err)
		}
		return
	}
	t.Fatal("decommission has no mount-efivarfs step")
}
