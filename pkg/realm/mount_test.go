//go:build linux

package realm

import (
	"os"
	"strings"
	"testing"
)

func TestIsMountedProc(t *testing.T) {
	// /proc is always mounted on Linux.
	if !isMounted("/proc") {
		t.Fatal("expected /proc to be mounted")
	}
}

func TestIsMountedNonexistent(t *testing.T) {
	if isMounted("/nonexistent/mountpoint/12345") {
		t.Fatal("expected /nonexistent/mountpoint/12345 not to be mounted")
	}
}

func TestIsMountedPartialMatch(t *testing.T) {
	// Ensure partial path matches don't false-positive.
	// /proc/sys is typically not a separate mount (it's inside /proc).
	// But /sys IS a mount. Use a path that won't be mounted.
	if isMounted("/pro") {
		t.Fatal("partial path /pro should not match /proc mount")
	}
}

func TestDefaultMountsContainsExpected(t *testing.T) {
	mounts := DefaultMounts()
	expected := []string{"bin", "dev", "etc", "home", "mnt", "proc", "sys", "tmp", "usr"}
	if len(mounts.Mount) != len(expected) {
		t.Fatalf("expected %d mounts, got %d", len(expected), len(mounts.Mount))
	}
	for i, name := range expected {
		if mounts.Mount[i].Name != name {
			t.Errorf("mount[%d]: expected %q, got %q", i, name, mounts.Mount[i].Name)
		}
	}
}

func TestGetMount(t *testing.T) {
	mounts := DefaultMounts()
	m := mounts.GetMount("dev")
	if m == nil {
		t.Fatal("expected to find mount 'dev'")
	}
	if m.Path != "/dev" {
		t.Errorf("expected path /dev, got %s", m.Path)
	}
	if m.FSType != "devtmpfs" {
		t.Errorf("expected fstype devtmpfs, got %s", m.FSType)
	}
}

func TestGetMountNotFound(t *testing.T) {
	mounts := DefaultMounts()
	if mounts.GetMount("nonexistent") != nil {
		t.Fatal("expected nil for nonexistent mount")
	}
}

func TestMountNamedSkipsAlreadyMounted(t *testing.T) {
	// /proc is already mounted. Create a mount entry for it,
	// call MountNamed — it should NOT error (skip because already mounted).
	mounts := &Mounts{
		Mount: []Mount{
			{Name: "proc", Source: "proc", Path: "/proc", FSType: "proc", EnableMount: true},
		},
	}
	if err := mounts.MountNamed("proc", false); err != nil {
		t.Fatalf("expected no error for already-mounted /proc, got: %v", err)
	}
}

func TestMountNamedRemovesAfterSkip(t *testing.T) {
	mounts := &Mounts{
		Mount: []Mount{
			{Name: "proc", Source: "proc", Path: "/proc", FSType: "proc", EnableMount: true},
		},
	}
	if err := mounts.MountNamed("proc", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mounts.Mount) != 0 {
		t.Fatal("expected mount to be removed from list after skip")
	}
}

func TestMountNamedNoMatchReturnsNil(t *testing.T) {
	mounts := DefaultMounts()
	if err := mounts.MountNamed("nonexistent", false); err != nil {
		t.Fatalf("expected nil for non-matching name, got: %v", err)
	}
}

func TestCreateFolder(t *testing.T) {
	dir := t.TempDir()
	mounts := &Mounts{
		Mount: []Mount{
			{Name: "test", Path: dir + "/sub/deep", Mode: 0o755, CreateMount: true},
		},
	}
	if err := mounts.CreateFolder(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	info, err := os.Stat(dir + "/sub/deep")
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected directory")
	}
}

func TestCreateFolderSkipsNonCreate(t *testing.T) {
	mounts := &Mounts{
		Mount: []Mount{
			{Name: "skip", Path: "/tmp/should-not-create-" + t.Name(), Mode: 0o755, CreateMount: false},
		},
	}
	if err := mounts.CreateFolder(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat("/tmp/should-not-create-" + t.Name()); err == nil {
		t.Fatal("directory should not have been created")
		os.Remove("/tmp/should-not-create-" + t.Name())
	}
}

func TestMountAllSkipsAlreadyMounted(t *testing.T) {
	// Use /proc which is already mounted. MountAll should skip it, not fail.
	mounts := &Mounts{
		Mount: []Mount{
			{Name: "proc", Source: "proc", Path: "/proc", FSType: "proc", EnableMount: true},
		},
	}
	if err := mounts.MountAll(); err != nil {
		t.Fatalf("expected no error for already-mounted /proc, got: %v", err)
	}
}

func TestMountAllSkipsDisabled(t *testing.T) {
	mounts := &Mounts{
		Mount: []Mount{
			{Name: "skip", Source: "none", Path: "/nonexistent", FSType: "tmpfs", EnableMount: false},
		},
	}
	// Should silently skip disabled mount entries.
	if err := mounts.MountAll(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProcMountsFormat(t *testing.T) {
	// Validate that /proc/mounts is readable and has expected format.
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		t.Fatalf("cannot read /proc/mounts: %v", err)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 2 {
		t.Fatal("expected at least 2 lines in /proc/mounts")
	}
	// Each line should have at least 4 fields: device mountpoint fstype options.
	fields := strings.Fields(lines[0])
	if len(fields) < 4 {
		t.Fatalf("expected at least 4 fields in /proc/mounts line, got %d: %q", len(fields), lines[0])
	}
}
