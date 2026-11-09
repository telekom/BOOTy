//go:build !linux

package image

import (
	"errors"
	"fmt"
	"os"
)

func punchHole(*os.File, int64, int64) error {
	return errors.ErrUnsupported
}

// dropCachedBlocks cannot bypass the page cache of a block device here, so
// it fails for one and skipUnchanged turns off. Regular files need nothing.
func dropCachedBlocks(f *os.File) error {
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", f.Name(), err)
	}
	if fi.Mode()&os.ModeDevice != 0 {
		return errors.ErrUnsupported
	}
	return nil
}
