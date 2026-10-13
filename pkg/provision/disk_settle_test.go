//go:build linux

package provision

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/telekom/BOOTy/pkg/config"
)

// The disk selection of every mode waits for the disks to settle: a disk
// that appears after the step starts is seen.
func TestDiskSelectionWaitsForLateDisks(t *testing.T) {
	oldStable, oldPoll := diskSettleStable, diskSettlePoll
	diskSettleStable, diskSettlePoll = 200*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { diskSettleStable, diskSettlePoll = oldStable, oldPoll })
	for _, step := range []string{"detect-disk", "select-deprovision-disk", "decommission-wipe"} {
		t.Run(step, func(t *testing.T) {
			cfg := &config.MachineConfig{}
			cfg.Provision.Disk.Device = filepath.Join(t.TempDir(), "late-disk")
			cfg.Provision.Disk.SettleTimeoutSec = new(5)
			o := newTestOrchestrator(t, cfg, &mockProvider{})
			o.disk.SetSysfsRoot(t.TempDir())
			go func() {
				time.Sleep(100 * time.Millisecond)
				_ = os.WriteFile(cfg.Provision.Disk.Device, nil, 0o600)
			}()
			fn := map[string]func(context.Context) error{
				"detect-disk":             o.detectDisk,
				"select-deprovision-disk": o.selectDeprovisionDisk,
				"decommission-wipe":       o.decommissionWipe,
			}[step]
			_ = fn(context.Background()) // the late "disk" is a file, not a block device
			if _, err := os.Stat(cfg.Provision.Disk.Device); err != nil {
				t.Fatalf("%s returned before the configured disk appeared", step)
			}
		})
	}
}
