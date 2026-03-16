//go:build linux

package disk

import (
	"fmt"
	"log/slog"
	"os"

	"golang.org/x/sys/unix"
)

var (
	openForReread = os.OpenFile
	rereadIoctl   = func(fd int) error {
		return unix.IoctlSetInt(fd, unix.BLKRRPART, 0) //nolint:gosec // safe fd conversion
	}
)

// RereadPartitions triggers a kernel re-read of the partition table.
// Equivalent to `partprobe <device>`.
func RereadPartitions(device string) error {
	f, err := openForReread(device, os.O_RDWR, 0) //nolint:gosec // intentional device access
	if err != nil {
		return fmt.Errorf("open %s: %w", device, err)
	}
	defer f.Close() //nolint:errcheck // best-effort close on device

	if err := rereadIoctl(int(f.Fd())); err != nil {
		return fmt.Errorf("BLKRRPART ioctl on %s: %w", device, err)
	}

	slog.Info("partition table re-read", "device", device)
	return nil
}
