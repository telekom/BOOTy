//go:build linux

package image

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// punchHole deallocates length bytes at off of f. On a block device the
// kernel sends WRITE ZEROES without a fallback to writing zeros
// (BLKDEV_ZERO_NOFALLBACK, Linux 4.12 and later), which the device may carry
// out by deallocating, and fails when the device cannot guarantee zeros; on
// success the range reads back as zeros. On a regular file a punch past the
// end does not extend the file.
func punchHole(f *os.File, off, length int64) error {
	if err := unix.Fallocate(int(f.Fd()), unix.FALLOC_FL_PUNCH_HOLE|unix.FALLOC_FL_KEEP_SIZE, off, length); err != nil {
		return fmt.Errorf("punching %d bytes at offset %d: %w", length, off, err)
	}
	return nil
}

// dropCachedBlocks writes back and drops the page cache of the block device
// f (BLKFLSBUF), so that later reads come from the device: an erase through
// a passthrough tool does not invalidate cached blocks. Regular files need
// nothing.
func dropCachedBlocks(f *os.File) error {
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", f.Name(), err)
	}
	if fi.Mode()&os.ModeDevice == 0 {
		return nil
	}
	if err := unix.IoctlSetInt(int(f.Fd()), unix.BLKFLSBUF, 0); err != nil {
		return fmt.Errorf("dropping the page cache of %s: %w", f.Name(), err)
	}
	return nil
}
