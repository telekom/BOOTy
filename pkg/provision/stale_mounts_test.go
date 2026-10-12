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

	// The root probe can miss a stale child mount or fail to read the mount
	// table, so the recursive cleanup must run anyway.
	isMountPoint = func(string) bool { return false }
	if err := o.releaseStaleTargetMounts(); err != nil {
		t.Fatalf("releaseStaleTargetMounts() = %v", err)
	}
	if len(unmounted) != 1 || unmounted[0] != newroot {
		t.Fatalf("unmounted = %v, want [%s]", unmounted, newroot)
	}
	if len(o.sharedMounts) != 0 {
		t.Fatalf("sharedMounts = %v, want reset", o.sharedMounts)
	}

	unmountTargetTree = func(*disk.Manager, string) error { return errors.New("read mounts: permission denied") }
	if err := o.releaseStaleTargetMounts(); err == nil {
		t.Fatal("releaseStaleTargetMounts() = nil, want error when the target root may stay mounted")
	}
}
