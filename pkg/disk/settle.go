//go:build linux

package disk

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// settlePoll is the interval of WaitForStableDisks. Tests shorten it.
var settlePoll = 250 * time.Millisecond

// WaitForStableDisks waits until the set of block devices in sysfs did not
// change for stable, at most maxWait. Storage drivers scan their buses
// asynchronously after the module load (SAS/RAID HBAs can take seconds), so
// a disk can appear after the first look at /sys/block. A timeout is only
// logged: the caller works with the disks there are.
func (m *Manager) WaitForStableDisks(ctx context.Context, stable, maxWait time.Duration) {
	if maxWait <= 0 {
		return
	}
	deadline := time.Now().Add(maxWait)
	last := m.blockDevices()
	since := time.Now()
	for time.Since(since) < stable {
		if time.Now().After(deadline) {
			slog.Warn("block devices still changing, continuing", "waited", maxWait, "devices", last)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(settlePoll):
		}
		if cur := m.blockDevices(); !slices.Equal(cur, last) {
			slog.Info("block devices changed, waiting for them to settle", "devices", cur)
			last, since = cur, time.Now()
		}
	}
	slog.Info("block devices settled", "devices", last)
}

func (m *Manager) blockDevices() []string {
	entries, err := os.ReadDir(filepath.Join(m.sysfsRoot, "block"))
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
