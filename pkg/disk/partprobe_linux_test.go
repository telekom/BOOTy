//go:build linux

package disk

import (
	"errors"
	"os"
	"testing"
)

func snapshotPartprobeFns() func() {
	origOpen := openForReread
	origIoctl := rereadIoctl
	return func() {
		openForReread = origOpen
		rereadIoctl = origIoctl
	}
}

func TestRereadPartitions_UsesReadWriteOpen(t *testing.T) {
	restore := snapshotPartprobeFns()
	defer restore()

	var gotFlag int
	openForReread = func(_ string, flag int, _ os.FileMode) (*os.File, error) {
		gotFlag = flag
		f, err := os.CreateTemp(t.TempDir(), "partprobe-*")
		if err != nil {
			return nil, err
		}
		return f, nil
	}
	rereadIoctl = func(int) error { return nil }

	if err := RereadPartitions("/dev/fake"); err != nil {
		t.Fatalf("RereadPartitions() error: %v", err)
	}
	if gotFlag&os.O_RDWR == 0 {
		t.Fatalf("open flag = %#x, expected O_RDWR", gotFlag)
	}
}

func TestRereadPartitions_IoctlError(t *testing.T) {
	restore := snapshotPartprobeFns()
	defer restore()

	openForReread = func(_ string, _ int, _ os.FileMode) (*os.File, error) {
		f, err := os.CreateTemp(t.TempDir(), "partprobe-*")
		if err != nil {
			return nil, err
		}
		return f, nil
	}
	rereadIoctl = func(int) error { return errors.New("busy") }

	err := RereadPartitions("/dev/fake")
	if err == nil {
		t.Fatal("expected ioctl error")
	}
}
