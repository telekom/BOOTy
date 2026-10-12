//go:build linux

package provision

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/telekom/BOOTy/pkg/disk"
)

func TestReleaseStaleTargetMounts(t *testing.T) {
	oldMountPoint, oldUnmount := isMountPoint, unmountTargetTree
	t.Cleanup(func() { isMountPoint, unmountTargetTree = oldMountPoint, oldUnmount })

	var unmounted []string
	unmountTargetTree = func(_ *disk.Manager, root string) error {
		unmounted = append(unmounted, root)
		return nil
	}
	o := &Orchestrator{log: slog.Default(), sharedMounts: []string{"/newroot/var/lib/data"}}

	isMountPoint = func(string) bool { return false }
	if err := o.releaseStaleTargetMounts(); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if len(unmounted) != 0 {
		t.Fatalf("first run unmounted %v, want nothing", unmounted)
	}

	isMountPoint = func(path string) bool { return path == newroot }
	if err := o.releaseStaleTargetMounts(); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(unmounted) != 1 || unmounted[0] != newroot {
		t.Fatalf("unmounted = %v, want [%s]", unmounted, newroot)
	}
	if len(o.sharedMounts) != 0 {
		t.Fatalf("sharedMounts = %v, want reset", o.sharedMounts)
	}

	unmountTargetTree = func(*disk.Manager, string) error { return errors.New("busy") }
	if err := o.releaseStaleTargetMounts(); err == nil {
		t.Fatal("releaseStaleTargetMounts() = nil, want error when the target root stays mounted")
	}
}
