package disk

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

const wipeSize = 1 << 20 // 1 MiB

// WipeFS removes filesystem and partition-table signatures from a device
// by zeroing the first and last 1 MiB. This covers GPT, MBR, and most
// filesystem superblock locations.
// Note: unlike `wipefs -af`, this zeroes entire 1 MiB regions rather than
// targeting individual signature offsets.
func WipeFS(device string) error {
	f, err := os.OpenFile(device, os.O_WRONLY, 0) //nolint:gosec // intentional device write
	if err != nil {
		return fmt.Errorf("open %s for wipe: %w", device, err)
	}
	defer f.Close() //nolint:errcheck // best-effort close on device

	zeros := make([]byte, wipeSize)

	stat, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", device, err)
	}

	devSize := stat.Size()
	if devSize <= 0 {
		if end, seekErr := f.Seek(0, io.SeekEnd); seekErr == nil {
			devSize = end
		}
	}
	if devSize <= 0 {
		slog.Warn("could not determine device size; tail wipe skipped", "device", device)
	}

	startLen := int64(wipeSize)
	if devSize > 0 && devSize < startLen {
		startLen = devSize
	}
	if err := writeFullAt(f, zeros[:startLen], 0); err != nil {
		return fmt.Errorf("zero start of %s: %w", device, err)
	}

	if devSize > int64(wipeSize) {
		if err := writeFullAt(f, zeros, devSize-int64(wipeSize)); err != nil {
			return fmt.Errorf("zero end of %s: %w", device, err)
		}
	}

	slog.Info("wiped filesystem signatures", "device", device)
	return nil
}

func writeFullAt(f *os.File, data []byte, off int64) error {
	written := 0
	for written < len(data) {
		n, err := f.WriteAt(data[written:], off+int64(written))
		if err != nil {
			return fmt.Errorf("write at offset %d: %w", off+int64(written), err)
		}
		if n == 0 {
			return fmt.Errorf("short write")
		}
		written += n
	}
	return nil
}
