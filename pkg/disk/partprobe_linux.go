//go:build linux

package disk

import (
	"fmt"
	"log/slog"
	"os"

	"golang.org/x/sys/unix"
)

// RereadPartitions triggers a kernel re-read of the partition table.
// Equivalent to `partprobe <device>`.
func RereadPartitions(device string) error {
	f, err := os.Open(device) //nolint:gosec // intentional device access
	if err != nil {
		return fmt.Errorf("open %s: %w", device, err)
	}
	defer f.Close() //nolint:errcheck // best-effort close on device

	if err := unix.IoctlSetInt(int(f.Fd()), unix.BLKRRPART, 0); err != nil { //nolint:gosec // safe fd conversion
		return fmt.Errorf("BLKRRPART ioctl on %s: %w", device, err)
	}

	slog.Info("partition table re-read", "device", device)
	return nil
}
