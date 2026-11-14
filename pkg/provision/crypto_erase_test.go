//go:build linux

package provision

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
)

func TestCryptoEraseRunsRightBeforeTheWipe(t *testing.T) {
	o := newTestOrchestrator(t, &config.MachineConfig{Mode: "hard"}, &mockProvider{})
	steps := o.deprovisionSteps("hard")
	if erase := requireStepIndex(t, steps, "crypto-erase"); erase != requireStepIndex(t, steps, "stop-raid")+1 ||
		erase != requireStepIndex(t, steps, "wipe-disks")-1 {
		t.Fatalf("crypto-erase at %d: want it between stop-raid and wipe-disks", erase)
	}
	for _, step := range o.deprovisionSteps("soft") {
		if step.Name == "crypto-erase" {
			t.Fatal("soft deprovision wipes nothing and must not crypto-erase")
		}
	}
}

func TestCryptoEraseOffSkipsAndReports(t *testing.T) {
	cfg := &config.MachineConfig{Mode: "hard", Deprovision: config.DeprovisionConfig{CryptoErase: new(false)}}
	o, cmd := newTestOrchestratorWithCommander(t, cfg, &mockProvider{})
	o.disk.SetSysfsRoot(filepath.Join(t.TempDir(), "missing")) // any disk access fails
	if err := o.cryptoErase(context.Background()); err != nil {
		t.Fatalf("cryptoErase() = %v", err)
	}
	if len(cmd.calls) != 0 || !strings.Contains(o.cryptoEraseNote, "crypto-erase off") {
		t.Fatalf("calls %v, note %q", cmd.calls, o.cryptoEraseNote)
	}
}

// A host without LUKS: nothing runs and the status is unchanged. A device
// that cannot be read is named in the status and does not stop the run.
func TestCryptoEraseWithoutLUKS(t *testing.T) {
	o, cmd := newTestOrchestratorWithCommander(t, &config.MachineConfig{Mode: "hard"}, &mockProvider{})
	sysfs := t.TempDir()
	o.disk.SetSysfsRoot(sysfs)
	for _, dir := range []string{"block", "class/block"} {
		if err := os.MkdirAll(filepath.Join(sysfs, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := o.cryptoErase(context.Background()); err != nil || o.cryptoEraseNote != "" || len(cmd.calls) != 0 {
		t.Fatalf("cryptoErase() = %v, note %q, calls %v", err, o.cryptoEraseNote, cmd.calls)
	}
	// A disk whose device node does not exist.
	disk := filepath.Join(sysfs, "block", "bootytestnodev")
	if err := os.MkdirAll(disk, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(disk, "size"), []byte("2048\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(disk, filepath.Join(sysfs, "class", "block", "bootytestnodev")); err != nil {
		t.Fatal(err)
	}
	if err := o.cryptoErase(context.Background()); err != nil || o.cryptoEraseNote != "; crypto-erase could not read: /dev/bootytestnodev" {
		t.Fatalf("cryptoErase() = %v, note %q", err, o.cryptoEraseNote)
	}
}
