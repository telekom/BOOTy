package kexec

import (
	"os"
	"path/filepath"
	"testing"
)

func createKernel(t *testing.T, root, version string, withInitrd bool) {
	t.Helper()
	bootDir := filepath.Join(root, "boot")
	if err := os.MkdirAll(bootDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", bootDir, err)
	}
	if err := os.WriteFile(filepath.Join(bootDir, "vmlinuz-"+version), []byte("kernel"), 0o644); err != nil {
		t.Fatalf("write vmlinuz-%s: %v", version, err)
	}
	if withInitrd {
		if err := os.WriteFile(filepath.Join(bootDir, "initrd.img-"+version), []byte("initrd"), 0o644); err != nil {
			t.Fatalf("write initrd.img-%s: %v", version, err)
		}
	}
}

func TestDiscoverKernels(t *testing.T) {
	root := t.TempDir()
	createKernel(t, root, "5.15.0-100-generic", true)
	createKernel(t, root, "6.1.0-200-generic", true)
	createKernel(t, root, "5.10.0-50-generic", false)

	kernels, err := DiscoverKernels(root)
	if err != nil {
		t.Fatalf("DiscoverKernels: %v", err)
	}
	if len(kernels) != 3 {
		t.Fatalf("kernels = %d, want 3", len(kernels))
	}
	// Latest first.
	if kernels[0].Version != "6.1.0-200-generic" {
		t.Errorf("first = %q, want 6.1.0-200-generic", kernels[0].Version)
	}
	if kernels[0].InitrdPath == "" {
		t.Errorf("expected initrd for 6.1.0-200-generic, got empty InitrdPath")
	}
}

func TestDiscoverKernels_Empty(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "boot"), 0o755); err != nil {
		t.Fatalf("mkdir boot: %v", err)
	}

	kernels, err := DiscoverKernels(root)
	if err != nil {
		t.Fatalf("DiscoverKernels: %v", err)
	}
	if len(kernels) != 0 {
		t.Errorf("kernels = %d, want 0", len(kernels))
	}
}

func TestSelectKernel_ExplicitPath(t *testing.T) {
	root := t.TempDir()
	m := NewEnhancedManager(nil)
	cfg := &KexecConfig{
		KernelPath: "/boot/vmlinuz-custom",
		InitrdPath: "/boot/initrd-custom.img",
		Cmdline:    "root=/dev/sda1",
	}

	ki, err := m.SelectKernel(root, cfg)
	if err != nil {
		t.Fatalf("SelectKernel: %v", err)
	}
	want := filepath.Join(root, "/boot/vmlinuz-custom")
	if ki.KernelPath != want {
		t.Errorf("KernelPath = %q, want %q", ki.KernelPath, want)
	}
}

func TestSelectKernel_ByVersion(t *testing.T) {
	root := t.TempDir()
	createKernel(t, root, "5.15.0-100", true)
	createKernel(t, root, "6.1.0-200", true)

	m := NewEnhancedManager(nil)
	cfg := &KexecConfig{KernelVersion: "5.15.0-100"}

	ki, err := m.SelectKernel(root, cfg)
	if err != nil {
		t.Fatalf("SelectKernel: %v", err)
	}
	if ki.Version != "5.15.0-100" {
		t.Errorf("version = %q", ki.Version)
	}
}

func TestSelectKernel_Latest(t *testing.T) {
	root := t.TempDir()
	createKernel(t, root, "5.15.0", true)
	createKernel(t, root, "6.1.0", true)

	m := NewEnhancedManager(nil)
	ki, err := m.SelectKernel(root, &KexecConfig{})
	if err != nil {
		t.Fatalf("SelectKernel: %v", err)
	}
	if ki.Version != "6.1.0" {
		t.Errorf("version = %q, want 6.1.0", ki.Version)
	}
}

func TestSelectKernel_NotFound(t *testing.T) {
	root := t.TempDir()
	createKernel(t, root, "5.15.0", true)

	m := NewEnhancedManager(nil)
	_, err := m.SelectKernel(root, &KexecConfig{KernelVersion: "9.9.9"})
	if err == nil {
		t.Error("expected error for missing version")
	}
}

func TestSelectKernel_NoKernels(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "boot"), 0o755); err != nil {
		t.Fatalf("mkdir boot: %v", err)
	}

	m := NewEnhancedManager(nil)
	_, err := m.SelectKernel(root, &KexecConfig{})
	if err == nil {
		t.Error("expected error for empty boot dir")
	}
}

func TestRemoveCmdlineArgs(t *testing.T) {
	tests := []struct {
		cmdline string
		remove  []string
		want    string
	}{
		{"root=/dev/sda1 quiet splash", []string{"quiet", "splash"}, "root=/dev/sda1"},
		{"console=ttyS0 ro", []string{"console"}, "ro"},
		{"root=/dev/sda1", []string{"nonexistent"}, "root=/dev/sda1"},
		{"", []string{"x"}, ""},
	}

	for _, tc := range tests {
		got := RemoveCmdlineArgs(tc.cmdline, tc.remove)
		if got != tc.want {
			t.Errorf("RemoveCmdlineArgs(%q, %v) = %q, want %q", tc.cmdline, tc.remove, got, tc.want)
		}
	}
}

func TestBuildRescueCmdline(t *testing.T) {
	result := BuildRescueCmdline("root=/dev/sda1 quiet splash ro")
	if result != "root=/dev/sda1 ro systemd.unit=rescue.target rd.shell=1" {
		t.Errorf("rescue cmdline = %q", result)
	}
}

func TestKexecModeConstants(t *testing.T) {
	if string(ModeDirect) != "direct" {
		t.Error("ModeDirect wrong")
	}
	if string(ModeChain) != "chain" {
		t.Error("ModeChain wrong")
	}
	if string(ModeRescue) != "rescue" {
		t.Error("ModeRescue wrong")
	}
}

func TestApplyOverrides_CmdlineAppend(t *testing.T) {
	ki := &KernelInfo{Cmdline: "root=/dev/sda1"}
	cfg := &KexecConfig{CmdlineAppend: "console=ttyS0"}
	result := applyOverrides(ki, cfg)
	if result.Cmdline != "root=/dev/sda1 console=ttyS0" {
		t.Errorf("cmdline = %q", result.Cmdline)
	}
}

func TestApplyOverrides_CmdlineReplace(t *testing.T) {
	ki := &KernelInfo{Cmdline: "old"}
	cfg := &KexecConfig{Cmdline: "new"}
	result := applyOverrides(ki, cfg)
	if result.Cmdline != "new" {
		t.Errorf("cmdline = %q", result.Cmdline)
	}
}

func TestDiscoverKernels_InitramfsImg(t *testing.T) {
	root := t.TempDir()
	bootDir := filepath.Join(root, "boot")
	if err := os.MkdirAll(bootDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bootDir, "vmlinuz-5.15.0"), []byte("k"), 0o644); err != nil {
		t.Fatalf("write kernel: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bootDir, "initramfs-5.15.0.img"), []byte("i"), 0o644); err != nil {
		t.Fatalf("write initramfs: %v", err)
	}

	kernels, err := DiscoverKernels(root)
	if err != nil {
		t.Fatalf("DiscoverKernels: %v", err)
	}
	if len(kernels) != 1 {
		t.Fatalf("kernels = %d", len(kernels))
	}
	if kernels[0].InitrdPath == "" {
		t.Error("expected initramfs-.img initrd")
	}
}
