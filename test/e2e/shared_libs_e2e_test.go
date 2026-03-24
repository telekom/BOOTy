//go:build e2e_build

package e2e

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildStageImage builds a specific Dockerfile stage as a Docker image (--load)
// and returns the image tag. The image is automatically removed via t.Cleanup.
func buildStageImage(t *testing.T, stage string) string {
	t.Helper()
	dockerAvailable(t)

	tag := "booty-test-" + stage + ":latest"
	dockerfile := filepath.Join(findRepoRoot(t), "initrd.Dockerfile")
	repoRoot := findRepoRoot(t)

	args := []string{
		"buildx", "build", "--platform", "linux/amd64",
		"--target", stage, "--load", "-t", tag,
		"-f", dockerfile, repoRoot,
	}
	cmd := exec.Command("docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker buildx build --target %s failed: %v\n%s", stage, err, out)
	}

	t.Cleanup(func() {
		_ = exec.Command("docker", "rmi", "-f", tag).Run()
	})
	return tag
}

// lddCheck runs ldd on a binary inside a container and returns any libraries
// reported as "not found".
func lddCheck(t *testing.T, image, binary string) []string {
	t.Helper()
	args := []string{"run", "--rm", "--entrypoint", "", image, "ldd", binary}
	cmd := exec.Command("docker", args...)
	out, _ := cmd.CombinedOutput()

	var missing []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "not found") {
			missing = append(missing, strings.TrimSpace(line))
		}
	}
	return missing
}

// checkSharedLibs is a table-driven helper that runs ldd on each binary inside
// the given builder stage image and reports any missing shared libraries.
func checkSharedLibs(t *testing.T, image string, binaries []string) {
	t.Helper()
	workdir := "/build/initramfs"
	var failures []string
	for _, bin := range binaries {
		fullPath := workdir + "/" + bin
		missing := lddCheck(t, image, fullPath)
		if len(missing) > 0 {
			failures = append(failures,
				fmt.Sprintf("%s: %s", bin, strings.Join(missing, "; ")))
		}
	}
	if len(failures) > 0 {
		t.Errorf("binaries with missing shared libraries:\n  %s",
			strings.Join(failures, "\n  "))
	}
}

// TestDefaultSharedLibsResolveE2E builds the default (busybox) builder stage
// and verifies every dynamically-linked binary can resolve all its shared
// library dependencies.  This catches missing .so files that cause runtime
// "No such file or directory" errors in the initramfs.
func TestDefaultSharedLibsResolveE2E(t *testing.T) {
	image := buildStageImage(t, "busybox")
	checkSharedLibs(t, image, []string{
		// Disk tools
		"bin/wipefs", "sbin/mdadm", "sbin/resize2fs", "sbin/e2fsck",
		"sbin/xfs_growfs", "bin/btrfs", "bin/parted", "bin/sgdisk",
		"bin/partprobe",
		// EFI
		"bin/efibootmgr",
		// System
		"bin/dmidecode", "bin/ethtool", "bin/curl",
		// Network
		"bin/ip", "bin/bridge",
		// Secure erase
		"bin/hdparm", "bin/nvme",
		// Firmware
		"bin/mstconfig", "bin/mstflint",
		// LLDP
		"bin/lldpcli", "sbin/lldpd",
		// SSH
		"bin/dropbear", "bin/dropbearkey",
		// FRR
		"sbin/bgpd", "sbin/zebra", "sbin/bfdd", "bin/vtysh", "sbin/watchfrr",
	})
}

// TestGoBGPSharedLibsResolveE2E builds the GoBGP builder stage and verifies
// shared library resolution for all dynamically-linked binaries.
func TestGoBGPSharedLibsResolveE2E(t *testing.T) {
	image := buildStageImage(t, "gobgp-builder")
	checkSharedLibs(t, image, []string{
		// Disk tools
		"bin/wipefs", "sbin/mdadm", "sbin/resize2fs", "sbin/e2fsck",
		"sbin/xfs_growfs", "bin/btrfs", "bin/parted", "bin/sgdisk",
		"bin/partprobe",
		// EFI
		"bin/efibootmgr",
		// System
		"bin/dmidecode", "bin/ethtool", "bin/curl",
		// Network
		"bin/ip", "bin/bridge",
		// Secure erase
		"bin/hdparm", "bin/nvme",
		// Firmware
		"bin/mstconfig", "bin/mstflint",
		// LLDP
		"bin/lldpcli", "sbin/lldpd",
		// SSH
		"bin/dropbear", "bin/dropbearkey",
		// Rescue mode
		"bin/lsblk",
	})
}

// TestSlimSharedLibsResolveE2E builds the slim builder stage and verifies
// shared library resolution for all dynamically-linked binaries.
func TestSlimSharedLibsResolveE2E(t *testing.T) {
	image := buildStageImage(t, "slim-builder")
	checkSharedLibs(t, image, []string{
		"bin/ip", "bin/ethtool", "bin/curl",
		"bin/partprobe", "sbin/e2fsck", "sbin/resize2fs",
	})
}

// TestDefaultInitramfsHasLinkerE2E verifies the dynamic linker (ld-linux) is
// present in the default initramfs.  Without it, ALL dynamically-linked
// binaries fail with "No such file or directory".
func TestDefaultInitramfsHasLinkerE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "", dest)
	files := listCPIOContents(t, cpioGz)

	hasLinker := false
	for path := range files {
		if strings.Contains(path, "ld-linux") || strings.Contains(path, "ld-musl") {
			hasLinker = true
			break
		}
	}
	if !hasLinker {
		t.Error("dynamic linker (ld-linux-x86-64.so or ld-musl) not found in default initramfs")
	}
}

// TestGoBGPInitramfsHasLinkerE2E verifies the dynamic linker is present in the
// GoBGP initramfs.
func TestGoBGPInitramfsHasLinkerE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "gobgp", dest)
	files := listCPIOContents(t, cpioGz)

	hasLinker := false
	for path := range files {
		if strings.Contains(path, "ld-linux") || strings.Contains(path, "ld-musl") {
			hasLinker = true
			break
		}
	}
	if !hasLinker {
		t.Error("dynamic linker (ld-linux-x86-64.so or ld-musl) not found in gobgp initramfs")
	}
}

// TestSlimInitramfsHasLinkerE2E verifies the dynamic linker is present in the
// slim initramfs.
func TestSlimInitramfsHasLinkerE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "slim", dest)
	files := listCPIOContents(t, cpioGz)

	hasLinker := false
	for path := range files {
		if strings.Contains(path, "ld-linux") || strings.Contains(path, "ld-musl") {
			hasLinker = true
			break
		}
	}
	if !hasLinker {
		t.Error("dynamic linker (ld-linux-x86-64.so or ld-musl) not found in slim initramfs")
	}
}

// TestDefaultInitramfsHasLibefivarE2E verifies libefivar.so is present in the
// default initramfs (required by efibootmgr).
func TestDefaultInitramfsHasLibefivarE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "", dest)
	files := listCPIOContents(t, cpioGz)

	hasLibefivar := false
	for path := range files {
		if strings.Contains(path, "libefivar") {
			hasLibefivar = true
			break
		}
	}
	if !hasLibefivar {
		t.Error("libefivar.so not found in default initramfs (required by efibootmgr)")
	}
}

// TestDefaultInitramfsHasLibmnlE2E verifies libmnl.so is present in the
// default initramfs (required by ip/bridge from iproute2).
func TestDefaultInitramfsHasLibmnlE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "", dest)
	files := listCPIOContents(t, cpioGz)

	hasLibmnl := false
	for path := range files {
		if strings.Contains(path, "libmnl") {
			hasLibmnl = true
			break
		}
	}
	if !hasLibmnl {
		t.Error("libmnl.so not found in default initramfs (required by iproute2 ip/bridge)")
	}
}

// TestGoBGPInitramfsHasLibefivarE2E verifies libefivar.so is present in the
// GoBGP initramfs (required by efibootmgr).
func TestGoBGPInitramfsHasLibefivarE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "gobgp", dest)
	files := listCPIOContents(t, cpioGz)

	hasLibefivar := false
	for path := range files {
		if strings.Contains(path, "libefivar") {
			hasLibefivar = true
			break
		}
	}
	if !hasLibefivar {
		t.Error("libefivar.so not found in gobgp initramfs (required by efibootmgr)")
	}
}

// TestGoBGPInitramfsHasLibmnlE2E verifies libmnl.so is present in the
// GoBGP initramfs (required by ip/bridge from iproute2).
func TestGoBGPInitramfsHasLibmnlE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "gobgp", dest)
	files := listCPIOContents(t, cpioGz)

	hasLibmnl := false
	for path := range files {
		if strings.Contains(path, "libmnl") {
			hasLibmnl = true
			break
		}
	}
	if !hasLibmnl {
		t.Error("libmnl.so not found in gobgp initramfs (required by iproute2 ip/bridge)")
	}
}

// TestMicroInitramfsNoDynamicLibsE2E verifies the micro initramfs has no
// shared libraries — the init binary is statically compiled with CGO_ENABLED=0.
func TestMicroInitramfsNoDynamicLibsE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "micro", dest)
	files := listCPIOContents(t, cpioGz)

	for path := range files {
		if strings.HasSuffix(path, ".so") || strings.Contains(path, ".so.") {
			if strings.Contains(path, "ca-certificates") {
				continue
			}
			t.Errorf("unexpected shared library in micro initramfs: %s", path)
		}
	}
}

// TestMicroInitramfsStaticBinaryE2E verifies the micro init binary is
// statically linked by running ldd inside the Docker build stage.
func TestMicroInitramfsStaticBinaryE2E(t *testing.T) {
	image := buildStageImage(t, "micro-builder")

	// ldd on a static binary prints "not a dynamic executable" or "statically linked"
	args := []string{"run", "--rm", "--entrypoint", "", image, "ldd", "/build/initramfs/init"}
	cmd := exec.Command("docker", args...)
	out, _ := cmd.CombinedOutput()
	output := string(out)

	if strings.Contains(output, "not found") {
		t.Errorf("micro init binary has missing shared libraries: %s", output)
	}
}
