//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func dockerAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("docker not available")
	}
}

// buildTarget runs docker buildx build for the given Dockerfile target and
// extracts the output to dest. Returns the path to initramfs.cpio.gz.
func buildTarget(t *testing.T, target, dest string) string {
	t.Helper()
	dockerfile := filepath.Join(findRepoRoot(t), "initrd.Dockerfile")
	repoRoot := findRepoRoot(t)

	args := []string{"buildx", "build", "--platform", "linux/amd64"}
	if target != "" {
		args = append(args, "--target", target)
	}
	args = append(args, "--output", "type=local,dest="+dest, "-f", dockerfile, repoRoot)

	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		name := target
		if name == "" {
			name = "default"
		}
		t.Fatalf("docker buildx build --target %s failed: %v", name, err)
	}

	out := filepath.Join(dest, "initramfs.cpio.gz")
	if target == "iso" {
		out = filepath.Join(dest, "booty.iso")
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("expected output %s not found: %v", out, err)
	}
	return out
}

// findRepoRoot walks up from the test file to find the repo root (contains go.mod).
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (go.mod)")
		}
		dir = parent
	}
}

// listCPIOContents uses gzip+cpio to list files in a cpio.gz archive.
func listCPIOContents(t *testing.T, cpioGzPath string) map[string]bool {
	t.Helper()

	// Use: gzip -dc file | cpio -t
	cmd := exec.Command("sh", "-c", "gzip -dc "+cpioGzPath+" | cpio -t 2>/dev/null")
	out, err := cmd.Output()
	if err != nil {
		// Try alternative: use tar if cpio not available (shouldn't happen on Linux CI)
		t.Fatalf("cpio command not available: %v", err)
	}

	files := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "." {
			continue
		}
		// Normalize: strip leading "./"
		line = strings.TrimPrefix(line, "./")
		if line != "" {
			files[line] = true
		}
	}
	return files
}

// assertContains checks that the file set contains the given path.
func assertContains(t *testing.T, files map[string]bool, path, desc string) {
	t.Helper()
	if !files[path] {
		t.Errorf("expected %s (%s) in initramfs, not found", path, desc)
	}
}

// assertNotContains checks that the file set does NOT contain the given path.
func assertNotContains(t *testing.T, files map[string]bool, path, desc string) {
	t.Helper()
	if files[path] {
		t.Errorf("did NOT expect %s (%s) in initramfs, but found it", path, desc)
	}
}

// assertFileSize checks that the cpio.gz file is within the expected size range.
func assertFileSize(t *testing.T, path string, minMB, maxMB float64) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	sizeMB := float64(info.Size()) / (1024 * 1024)
	if sizeMB < minMB || sizeMB > maxMB {
		t.Errorf("initramfs size %.1f MB outside expected range [%.1f, %.1f] MB", sizeMB, minMB, maxMB)
	}
}

// ── Slim build tests ─────────────────────────────────────────────────────

func TestSlimBuildSucceedsE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "slim", dest)

	info, err := os.Stat(cpioGz)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("slim initramfs is empty")
	}
	t.Logf("Slim initramfs size: %.1f MB", float64(info.Size())/(1024*1024))
}

func TestSlimContainsBusyboxE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "slim", dest)
	files := listCPIOContents(t, cpioGz)

	assertContains(t, files, "bin/busybox", "busybox binary")
	assertContains(t, files, "bin/sh", "shell symlink")
}

func TestSlimContainsBootyInitE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "slim", dest)
	files := listCPIOContents(t, cpioGz)

	assertContains(t, files, "init", "BOOTy init binary")
}

func TestSlimContainsNetworkToolsE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "slim", dest)
	files := listCPIOContents(t, cpioGz)

	assertContains(t, files, "bin/ip", "iproute2 ip command")
	assertContains(t, files, "bin/ethtool", "ethtool")
	assertContains(t, files, "bin/curl", "curl")
}

func TestSlimContainsDiskToolsE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "slim", dest)
	files := listCPIOContents(t, cpioGz)

	assertContains(t, files, "bin/partprobe", "partprobe")
	assertContains(t, files, "sbin/e2fsck", "e2fsck")
	assertContains(t, files, "sbin/resize2fs", "resize2fs")
}

func TestSlimExcludesFRRE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "slim", dest)
	files := listCPIOContents(t, cpioGz)

	assertNotContains(t, files, "sbin/bgpd", "FRR bgpd")
	assertNotContains(t, files, "sbin/zebra", "FRR zebra")
	assertNotContains(t, files, "sbin/bfdd", "FRR bfdd")
	assertNotContains(t, files, "bin/vtysh", "FRR vtysh")
	assertNotContains(t, files, "sbin/watchfrr", "FRR watchfrr")
}

func TestSlimExcludesLVME2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "slim", dest)
	files := listCPIOContents(t, cpioGz)

	assertNotContains(t, files, "sbin/lvm", "LVM tooling")
	assertNotContains(t, files, "bin/sfdisk", "sfdisk")
}

func TestSlimSizeSmallerThanDefaultE2E(t *testing.T) {
	dockerAvailable(t)

	// Build both default and slim, compare sizes
	defaultDest := t.TempDir()
	slimDest := t.TempDir()

	defaultCpio := buildTarget(t, "", defaultDest)
	slimCpio := buildTarget(t, "slim", slimDest)

	defaultInfo, _ := os.Stat(defaultCpio)
	slimInfo, _ := os.Stat(slimCpio)

	t.Logf("Default size: %.1f MB, Slim size: %.1f MB",
		float64(defaultInfo.Size())/(1024*1024),
		float64(slimInfo.Size())/(1024*1024))

	if slimInfo.Size() >= defaultInfo.Size() {
		t.Errorf("slim (%d bytes) should be smaller than default (%d bytes)",
			slimInfo.Size(), defaultInfo.Size())
	}
}

// ── Micro build tests ────────────────────────────────────────────────────

func TestMicroBuildSucceedsE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "micro", dest)

	info, err := os.Stat(cpioGz)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("micro initramfs is empty")
	}
	t.Logf("Micro initramfs size: %.1f MB", float64(info.Size())/(1024*1024))
}

func TestMicroContainsOnlyInitE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "micro", dest)
	files := listCPIOContents(t, cpioGz)

	assertContains(t, files, "init", "BOOTy init binary")
	assertContains(t, files, "etc/ssl/certs/ca-certificates.crt", "CA certificates")

	// Should NOT contain any external tools
	assertNotContains(t, files, "bin/busybox", "busybox")
	assertNotContains(t, files, "bin/sh", "shell")
	assertNotContains(t, files, "sbin/bgpd", "FRR bgpd")
	assertNotContains(t, files, "sbin/lvm", "LVM")
	assertNotContains(t, files, "bin/sfdisk", "sfdisk")
	assertNotContains(t, files, "bin/ip", "iproute2")
	assertNotContains(t, files, "bin/curl", "curl")
}

func TestMicroHasMinimalDirsE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "micro", dest)
	files := listCPIOContents(t, cpioGz)

	// Verify minimal directory structure for Linux init
	for _, dir := range []string{"dev", "proc", "sys", "tmp", "etc"} {
		assertContains(t, files, dir, "required directory")
	}
}

func TestMicroSizeSmallerThanSlimE2E(t *testing.T) {
	dockerAvailable(t)

	slimDest := t.TempDir()
	microDest := t.TempDir()

	slimCpio := buildTarget(t, "slim", slimDest)
	microCpio := buildTarget(t, "micro", microDest)

	slimInfo, _ := os.Stat(slimCpio)
	microInfo, _ := os.Stat(microCpio)

	t.Logf("Slim size: %.1f MB, Micro size: %.1f MB",
		float64(slimInfo.Size())/(1024*1024),
		float64(microInfo.Size())/(1024*1024))

	if microInfo.Size() >= slimInfo.Size() {
		t.Errorf("micro (%d bytes) should be smaller than slim (%d bytes)",
			microInfo.Size(), slimInfo.Size())
	}
}

func TestMicroIsPureGoE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "micro", dest)

	// Micro should be very small — the pure-Go binary + certs + dirs only
	// Expected: under 20 MB compressed (compared to ~50+ MB for default)
	assertFileSize(t, cpioGz, 0.1, 20.0)
}

// ── Cross-flavour comparison test ────────────────────────────────────────

func TestBuildFlavourSizeOrderE2E(t *testing.T) {
	dockerAvailable(t)

	defaultDest := t.TempDir()
	slimDest := t.TempDir()
	microDest := t.TempDir()

	defaultCpio := buildTarget(t, "", defaultDest)
	slimCpio := buildTarget(t, "slim", slimDest)
	microCpio := buildTarget(t, "micro", microDest)

	defaultInfo, _ := os.Stat(defaultCpio)
	slimInfo, _ := os.Stat(slimCpio)
	microInfo, _ := os.Stat(microCpio)

	t.Logf("Size order: micro (%.1f MB) < slim (%.1f MB) < default (%.1f MB)",
		float64(microInfo.Size())/(1024*1024),
		float64(slimInfo.Size())/(1024*1024),
		float64(defaultInfo.Size())/(1024*1024))

	if microInfo.Size() >= slimInfo.Size() {
		t.Error("micro should be smaller than slim")
	}
	if slimInfo.Size() >= defaultInfo.Size() {
		t.Error("slim should be smaller than default")
	}
}

// ── Default build composition tests ──────────────────────────────────────

func TestDefaultBuildSucceedsE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "", dest)

	info, err := os.Stat(cpioGz)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("default initramfs is empty")
	}
	t.Logf("Default initramfs size: %.1f MB", float64(info.Size())/(1024*1024))
}

func TestDefaultContainsFRRE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "", dest)
	files := listCPIOContents(t, cpioGz)

	assertContains(t, files, "sbin/bgpd", "FRR bgpd")
	assertContains(t, files, "sbin/zebra", "FRR zebra")
	assertContains(t, files, "sbin/bfdd", "FRR bfdd")
	assertContains(t, files, "bin/vtysh", "FRR vtysh")
	assertContains(t, files, "sbin/watchfrr", "FRR watchfrr")
}

func TestDefaultContainsLVME2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "", dest)
	files := listCPIOContents(t, cpioGz)

	assertContains(t, files, "sbin/lvm", "LVM tooling")
	assertContains(t, files, "bin/sfdisk", "sfdisk partitioner")
}

func TestDefaultContainsDiskToolsE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "", dest)
	files := listCPIOContents(t, cpioGz)

	assertContains(t, files, "bin/wipefs", "wipefs")
	assertContains(t, files, "sbin/mdadm", "mdadm RAID")
	assertContains(t, files, "sbin/resize2fs", "resize2fs")
	assertContains(t, files, "sbin/e2fsck", "e2fsck")
	assertContains(t, files, "bin/parted", "parted")
	assertContains(t, files, "bin/sgdisk", "sgdisk GPT")
	assertContains(t, files, "bin/partprobe", "partprobe")
	assertContains(t, files, "bin/efibootmgr", "efibootmgr")
}

func TestDefaultContainsNetworkAndSSHE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "", dest)
	files := listCPIOContents(t, cpioGz)

	assertContains(t, files, "bin/ip", "iproute2 ip")
	assertContains(t, files, "bin/bridge", "iproute2 bridge")
	assertContains(t, files, "bin/ethtool", "ethtool")
	assertContains(t, files, "bin/curl", "curl")
	assertContains(t, files, "bin/dropbear", "dropbear SSH")
	assertContains(t, files, "bin/dropbearkey", "dropbearkey")
	assertContains(t, files, "bin/lldpcli", "LLDP client")
	assertContains(t, files, "sbin/lldpd", "LLDP daemon")
}

func TestDefaultContainsKernelModulesE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "", dest)
	files := listCPIOContents(t, cpioGz)

	// At least one .ko or .ko.zst file should exist under lib/modules/
	hasModules := false
	for path := range files {
		if strings.HasPrefix(path, "modules/") &&
			(strings.HasSuffix(path, ".ko") || strings.HasSuffix(path, ".ko.zst") ||
				strings.HasSuffix(path, ".ko.xz") || strings.HasSuffix(path, ".ko.gz")) {
			hasModules = true
			break
		}
	}
	if !hasModules {
		t.Error("no kernel modules found under modules/ in default initramfs")
	}
}

// ── GoBGP build composition tests ────────────────────────────────────────

func TestGoBGPBuildSucceedsE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "gobgp", dest)

	info, err := os.Stat(cpioGz)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("gobgp initramfs is empty")
	}
	t.Logf("GoBGP initramfs size: %.1f MB", float64(info.Size())/(1024*1024))
}

func TestGoBGPExcludesFRRE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "gobgp", dest)
	files := listCPIOContents(t, cpioGz)

	assertNotContains(t, files, "sbin/bgpd", "FRR bgpd")
	assertNotContains(t, files, "sbin/zebra", "FRR zebra")
	assertNotContains(t, files, "sbin/bfdd", "FRR bfdd")
	assertNotContains(t, files, "bin/vtysh", "FRR vtysh")
	assertNotContains(t, files, "sbin/watchfrr", "FRR watchfrr")
}

func TestGoBGPContainsLVME2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "gobgp", dest)
	files := listCPIOContents(t, cpioGz)

	assertContains(t, files, "sbin/lvm", "LVM tooling")
	assertContains(t, files, "bin/sfdisk", "sfdisk partitioner")
}

func TestGoBGPContainsDiskToolsE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "gobgp", dest)
	files := listCPIOContents(t, cpioGz)

	assertContains(t, files, "bin/wipefs", "wipefs")
	assertContains(t, files, "sbin/mdadm", "mdadm RAID")
	assertContains(t, files, "bin/efibootmgr", "efibootmgr")
	assertContains(t, files, "bin/lsblk", "lsblk for rescue mode")
}

func TestGoBGPContainsNetworkAndSSHE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "gobgp", dest)
	files := listCPIOContents(t, cpioGz)

	assertContains(t, files, "bin/ip", "iproute2 ip")
	assertContains(t, files, "bin/bridge", "iproute2 bridge")
	assertContains(t, files, "bin/ethtool", "ethtool")
	assertContains(t, files, "bin/curl", "curl")
	assertContains(t, files, "bin/dropbear", "dropbear SSH")
	assertContains(t, files, "bin/lldpcli", "LLDP client")
}

func TestGoBGPContainsKernelModulesE2E(t *testing.T) {
	dockerAvailable(t)
	dest := t.TempDir()
	cpioGz := buildTarget(t, "gobgp", dest)
	files := listCPIOContents(t, cpioGz)

	hasModules := false
	for path := range files {
		if strings.HasPrefix(path, "modules/") &&
			(strings.HasSuffix(path, ".ko") || strings.HasSuffix(path, ".ko.zst") ||
				strings.HasSuffix(path, ".ko.xz") || strings.HasSuffix(path, ".ko.gz")) {
			hasModules = true
			break
		}
	}
	if !hasModules {
		t.Error("no kernel modules found under modules/ in gobgp initramfs")
	}
}

// ── Cross-flavour comparison with GoBGP ──────────────────────────────────

func TestBuildFlavourSizeOrderWithGoBGPE2E(t *testing.T) {
	dockerAvailable(t)

	defaultDest := t.TempDir()
	gobgpDest := t.TempDir()
	slimDest := t.TempDir()
	microDest := t.TempDir()

	defaultCpio := buildTarget(t, "", defaultDest)
	gobgpCpio := buildTarget(t, "gobgp", gobgpDest)
	slimCpio := buildTarget(t, "slim", slimDest)
	microCpio := buildTarget(t, "micro", microDest)

	defaultInfo, _ := os.Stat(defaultCpio)
	gobgpInfo, _ := os.Stat(gobgpCpio)
	slimInfo, _ := os.Stat(slimCpio)
	microInfo, _ := os.Stat(microCpio)

	t.Logf("Size order: micro (%.1f MB) < slim (%.1f MB) < gobgp (%.1f MB) <= default (%.1f MB)",
		float64(microInfo.Size())/(1024*1024),
		float64(slimInfo.Size())/(1024*1024),
		float64(gobgpInfo.Size())/(1024*1024),
		float64(defaultInfo.Size())/(1024*1024))

	if microInfo.Size() >= slimInfo.Size() {
		t.Error("micro should be smaller than slim")
	}
	if slimInfo.Size() >= gobgpInfo.Size() {
		t.Error("slim should be smaller than gobgp")
	}
	// GoBGP may be similar size to default (no FRR, but same tools).
	// Only check that GoBGP is not vastly larger than default.
	if gobgpInfo.Size() > defaultInfo.Size()*2 {
		t.Error("gobgp should not be more than 2x the default size")
	}
}
