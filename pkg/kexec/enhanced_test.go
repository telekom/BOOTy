//go:build linux

package kexec

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverKernels(t *testing.T) {
	root := t.TempDir()
	boot := filepath.Join(root, "boot")
	if err := os.MkdirAll(boot, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create fake kernels
	for _, name := range []string{
		"vmlinuz-5.15.0-generic",
		"vmlinuz-6.1.0-generic",
		"vmlinuz-5.15.0-rescue",
		"initrd.img-5.15.0-generic",
		"initrd.img-6.1.0-generic",
	} {
		if err := os.WriteFile(filepath.Join(boot, name), []byte("stub"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mgr := NewEnhancedManager(root)
	kernels, err := mgr.DiscoverKernels()
	if err != nil {
		t.Fatal(err)
	}
	if len(kernels) != 3 {
		t.Fatalf("got %d kernels, want 3", len(kernels))
	}
	// Newest non-rescue kernel should be first
	if kernels[0].Version != "6.1.0-generic" {
		t.Errorf("first kernel = %s, want 6.1.0-generic", kernels[0].Version)
	}
	// Rescue should be last
	if !kernels[2].IsRescue {
		t.Error("last kernel should be rescue")
	}
}

func TestLatestKernel(t *testing.T) {
	root := t.TempDir()
	boot := filepath.Join(root, "boot")
	if err := os.MkdirAll(boot, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"vmlinuz-5.4.0-generic", "vmlinuz-5.15.0-generic"} {
		if err := os.WriteFile(filepath.Join(boot, name), []byte("stub"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mgr := NewEnhancedManager(root)
	latest, err := mgr.LatestKernel()
	if err != nil {
		t.Fatal(err)
	}
	if latest.Version != "5.15.0-generic" {
		t.Errorf("latest = %s, want 5.15.0-generic", latest.Version)
	}
}

func TestLatestKernelNone(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "boot"), 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := NewEnhancedManager(root)
	_, err := mgr.LatestKernel()
	if err == nil {
		t.Error("expected error for empty boot dir")
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"5.15.0", "5.15.0", 0},
		{"6.1.0", "5.15.0", 1},
		{"5.15.0", "6.1.0", -1},
		{"5.15.1", "5.15.0", 1},
	}
	for _, tc := range tests {
		got := compareVersions(tc.a, tc.b)
		if (tc.want > 0 && got <= 0) || (tc.want < 0 && got >= 0) || (tc.want == 0 && got != 0) {
			t.Errorf("compareVersions(%q, %q) = %d, want sign %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestRemoveCmdlineArgs(t *testing.T) {
	result := RemoveCmdlineArgs("root=/dev/sda1 ro quiet splash", "quiet", "splash")
	if result != "root=/dev/sda1 ro" {
		t.Errorf("result = %q", result)
	}
}

func TestBuildRescueCmdline(t *testing.T) {
	result := BuildRescueCmdline("root=/dev/sda1 ro quiet splash")
	if result != "root=/dev/sda1 ro single" {
		t.Errorf("result = %q", result)
	}
}
