//go:build linux

package disk

import (
	"errors"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func TestUnmountOrRemountReadOnly(t *testing.T) {
	oldRead, oldRemount := readMountsFile, remountReadOnlyFunc
	t.Cleanup(func() { readMountsFile, remountReadOnlyFunc = oldRead, oldRemount })
	readMountsFile = func(string) ([]byte, error) {
		return []byte(`rootfs / rootfs rw 0 0
/dev/vda2 /newroot ext4 rw 0 0
/dev/vda3 /newroot/boot ext4 rw 0 0
/dev/vda1 /newroot/boot/efi vfat rw 0 0
devtmpfs /newroot/dev devtmpfs rw 0 0
proc /newroot/proc proc rw 0 0
/dev/vdb1 /newrootx ext4 rw 0 0
`), nil
	}
	var unmounted, remounted []string
	restore := SetUnmountFuncForTest(func(target string, _ int) error {
		switch target {
		case "/newroot/boot", "/newroot/dev":
			return syscall.EBUSY
		case "/newroot/proc":
			return syscall.EINVAL
		}
		unmounted = append(unmounted, target)
		return nil
	})
	t.Cleanup(restore)
	remountReadOnlyFunc = func(target string) error {
		remounted = append(remounted, target)
		return nil
	}

	if err := NewManager(nil).UnmountOrRemountReadOnly("/newroot"); err != nil {
		t.Fatalf("UnmountOrRemountReadOnly() = %v", err)
	}
	if want := []string{"/newroot/boot/efi", "/newroot"}; !reflect.DeepEqual(unmounted, want) {
		t.Errorf("unmounted %v, want %v (deepest first, nothing outside the root)", unmounted, want)
	}
	// The busy /boot is a disk filesystem; the busy /dev bind is not remounted.
	if want := []string{"/newroot/boot"}; !reflect.DeepEqual(remounted, want) {
		t.Errorf("remounted read-only %v, want %v", remounted, want)
	}

	remountReadOnlyFunc = func(string) error { return errors.New("busy too") }
	err := NewManager(nil).UnmountOrRemountReadOnly("/newroot")
	if err == nil || !strings.Contains(err.Error(), "remount read-only") {
		t.Fatalf("UnmountOrRemountReadOnly() = %v, want the remount error", err)
	}
}
