//go:build !linux

package image

import (
	"errors"
	"os"
)

func punchHole(*os.File, int64, int64) error {
	return errors.ErrUnsupported
}

func dropCachedBlocks(*os.File) error {
	return nil
}
