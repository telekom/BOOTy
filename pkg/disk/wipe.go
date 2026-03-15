package disk

import (
	"fmt"
	"log/slog"
	"os"
)

const wipeSize = 1 << 20 // 1 MiB

// WipeFS removes filesystem and partition-table signatures from a device
// by zeroing the first and last 1 MiB. This covers GPT, MBR, and most
// filesystem superblock locations.
// Equivalent to `wipefs -af <device>`.
func WipeFS(device string) error {
	f, err := os.OpenFile(device, os.O_WRONLY, 0) //nolint:gosec // intentional device write
	if err != nil {
		return fmt.Errorf("open %s for wipe: %w", device, err)
	}
	defer f.Close() //nolint:errcheck // best-effort close on device

	zeros := make([]byte, wipeSize)

	if _, err := f.Write(zeros); err != nil {
		return fmt.Errorf("zero start of %s: %w", device, err)
	}

	stat, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", device, err)
	}

	if stat.Size() > int64(wipeSize) {
		if _, err := f.WriteAt(zeros, stat.Size()-int64(wipeSize)); err != nil {
			return fmt.Errorf("zero end of %s: %w", device, err)
		}
	}

	slog.Info("wiped filesystem signatures", "device", device)
	return nil
}
