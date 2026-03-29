//go:build e2e

package kvm

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// requireProvisionTools fails the test if essential provisioning tools are missing.
func requireProvisionTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"sfdisk", "mkfs.ext4", "qemu-img", "losetup", "dd", "mount", "umount"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s not available", tool)
		}
	}
}

// requireDiskInspectTools fails if tools needed for post-provision inspection are missing.
func requireDiskInspectTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"qemu-nbd", "partprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s not available", tool)
		}
	}
}

// createTestDiskImage creates a minimal raw disk image with a GPT partition table:
// partition 1 = 50 MiB EFI System Partition (FAT32), partition 2 = rest (ext4 root).
// The ext4 root gets a basic /etc directory. Returns path to the raw image.
func createTestDiskImage(t *testing.T, sizeMB int) string {
	t.Helper()
	dir := t.TempDir()
	rawPath := filepath.Join(dir, "test-os.raw")

	run(t, "create raw disk image",
		"dd", "if=/dev/zero", "of="+rawPath, "bs=1M", fmt.Sprintf("count=%d", sizeMB))

	// Create GPT partition table: 50M EFI + rest root.
	// sfdisk reads partition definitions from stdin.
	sfdiskInput := "label: gpt\nsize=50M, type=C12A7328-F81F-11D2-BA4B-00A0C93EC93B\ntype=0FC63DAF-8483-4772-8E79-3D69D8477DE4\n"
	cmd := exec.Command("sfdisk", rawPath)
	cmd.Stdin = strings.NewReader(sfdiskInput)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sfdisk failed: %v\n%s", err, out)
	}

	// Set up loop device with partition scanning.
	loopOut := runOutput(t, "setup loop device", "losetup", "--find", "--show", "--partscan", rawPath)
	loopDev := strings.TrimSpace(string(loopOut))
	t.Cleanup(func() {
		_ = exec.Command("losetup", "-d", loopDev).Run()
	})

	// Wait for partition devices to appear.
	rootDev := loopDev + "p2"
	waitForDevice(t, rootDev, 5*time.Second)

	// Format EFI partition as FAT32 (if mkfs.vfat available).
	efiDev := loopDev + "p1"
	if _, err := exec.LookPath("mkfs.vfat"); err == nil {
		if _, statErr := os.Stat(efiDev); statErr == nil {
			run(t, "format EFI partition", "mkfs.vfat", "-F", "32", efiDev)
		}
	}

	// Format root partition as ext4.
	run(t, "format root partition", "mkfs.ext4", "-F", "-q", rootDev)

	// Mount root and create minimal directory structure.
	mountDir := filepath.Join(dir, "rootmnt")
	if err := os.MkdirAll(mountDir, 0o755); err != nil {
		t.Fatalf("mkdir mountDir: %v", err)
	}
	run(t, "mount root partition", "mount", rootDev, mountDir)
	t.Cleanup(func() {
		_ = exec.Command("umount", mountDir).Run()
	})

	// Create minimal filesystem structure expected by provisioning.
	for _, d := range []string{
		"etc", "etc/default/grub.d", "etc/kubernetes/kubelet.conf.d",
		"boot", "var", "tmp", "bin", "usr/bin",
	} {
		if err := os.MkdirAll(filepath.Join(mountDir, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	// Write pre-provision hostname so we can verify it's overwritten.
	writeFile(t, filepath.Join(mountDir, "etc", "hostname"), "pre-provision\n")

	// Unmount and detach before compressing.
	run(t, "unmount root", "umount", mountDir)
	run(t, "detach loop", "losetup", "-d", loopDev)

	return rawPath
}

// compressGzip gzips the file at src and returns the path to the .gz file.
func compressGzip(t *testing.T, src string) string {
	t.Helper()
	dst := src + ".gz"

	in, err := os.Open(src)
	if err != nil {
		t.Fatalf("open for gzip: %v", err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		t.Fatalf("create gzip file: %v", err)
	}
	defer out.Close()

	gz := gzip.NewWriter(out)
	if _, err := io.Copy(gz, in); err != nil {
		t.Fatalf("gzip copy: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	return dst
}

// startImageServer starts an HTTP server on a random port serving the image file.
// Returns the base URL (e.g. "http://127.0.0.1:PORT").
func startImageServer(t *testing.T, imagePath string) string {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/image.gz", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, imagePath)
	})

	// Listen on all interfaces so QEMU guest can reach us via 10.0.2.2.
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close() })

	// QEMU user-mode networking maps the host to 10.0.2.2.
	return fmt.Sprintf("http://10.0.2.2:%d", listener.Addr().(*net.TCPAddr).Port)
}

// writeDeployVars creates a /deploy/vars file from a map of key=value pairs.
func writeDeployVars(t *testing.T, dir string, vars map[string]string) string {
	t.Helper()
	deployDir := filepath.Join(dir, "deploy")
	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		t.Fatalf("mkdir deploy: %v", err)
	}

	var b strings.Builder
	for k, v := range vars {
		escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
		fmt.Fprintf(&b, "%s=\"%s\"\n", k, escaped)
	}
	varsPath := filepath.Join(deployDir, "vars")
	writeFile(t, varsPath, b.String())
	return varsPath
}

// buildProvisionInitramfs builds an initramfs containing BOOTy, busybox, essential
// provisioning tools (copied from host with shared libraries), and /deploy/vars.
// Returns path to the cpio.gz file.
func buildProvisionInitramfs(t *testing.T, vars map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	rootDir := filepath.Join(dir, "initramfs")

	// Create directory structure.
	for _, d := range []string{
		"bin", "sbin", "dev", "proc", "sys", "etc", "tmp", "mnt",
		"usr/bin", "usr/sbin", "lib", "lib64", "modules", "deploy",
		"newroot",
	} {
		if err := os.MkdirAll(filepath.Join(rootDir, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	// Copy BOOTy binary.
	bootyBin := envOrDefault("BOOTY_BINARY", "")
	if bootyBin == "" {
		// Build BOOTy if not provided.
		bootyBin = filepath.Join(dir, "booty")
		buildBooty(t, bootyBin)
	}
	copyBinary(t, bootyBin, filepath.Join(rootDir, "booty"))

	// Copy busybox.
	busyboxBin := findBusybox(t)
	copyBinary(t, busyboxBin, filepath.Join(rootDir, "bin", "busybox"))
	for _, applet := range []string{
		"sh", "mount", "umount", "ls", "cat", "echo", "sleep",
		"mkdir", "cp", "mv", "rm", "ln", "chmod", "chown",
		"mknod", "insmod", "modprobe", "setsid", "cttyhack",
		"chroot", "bash", "ash", "ip", "ifconfig", "udhcpc",
		"find", "xargs", "grep", "awk", "sed",
	} {
		link := filepath.Join(rootDir, "bin", applet)
		_ = os.Symlink("busybox", link)
	}

	// Copy essential provisioning tools from host with their shared libraries.
	essentialTools := []string{
		"partprobe", "sfdisk", "e2fsck", "resize2fs", "wipefs", "lvm",
	}
	for _, tool := range essentialTools {
		toolPath, err := exec.LookPath(tool)
		if err != nil {
			t.Logf("tool %s not found, skipping", tool)
			continue
		}
		destBin := filepath.Join(rootDir, "sbin", tool)
		copyBinary(t, toolPath, destBin)
		copySharedLibs(t, toolPath, rootDir)
	}

	// Copy ld-linux dynamic linker if present.
	for _, ld := range []string{"/lib64/ld-linux-x86-64.so.2", "/lib/ld-linux-x86-64.so.2"} {
		if _, err := os.Stat(ld); err == nil {
			destDir := filepath.Join(rootDir, filepath.Dir(ld))
			if err := os.MkdirAll(destDir, 0o755); err == nil {
				copyBinary(t, ld, filepath.Join(rootDir, ld))
			}
			break
		}
	}

	// Write /deploy/vars.
	writeDeployVars(t, rootDir, vars)

	// Write /deploy/machine-files if any custom files needed.
	// (subtest-specific; left empty by default)

	// Write /init script.
	initScript := "#!/bin/sh\nexport PATH=/bin:/sbin:/usr/bin:/usr/sbin\nexec /booty\n"
	writeFile(t, filepath.Join(rootDir, "init"), initScript)
	run(t, "chmod init", "chmod", "+x", filepath.Join(rootDir, "init"))

	// Create device nodes.
	for _, dev := range []struct {
		name       string
		major, min int
	}{
		{"dev/console", 5, 1},
		{"dev/ttyS0", 4, 64},
		{"dev/null", 1, 3},
	} {
		devPath := filepath.Join(rootDir, dev.name)
		cmd := exec.Command("mknod", devPath, "c",
			fmt.Sprintf("%d", dev.major), fmt.Sprintf("%d", dev.min))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Logf("mknod %s: %v (%s)", dev.name, err, out)
		}
	}

	// Package as cpio.gz using explicit pipes instead of shell concatenation.
	cpioPath := filepath.Join(dir, "provision-initramfs.cpio.gz")

	findCmd := exec.Command("find", ".", "-print0")
	findCmd.Dir = rootDir

	cpioCmd := exec.Command("cpio", "--null", "-ov", "--format=newc")
	cpioCmd.Dir = rootDir
	cpioCmd.Stderr = nil

	gzipCmd := exec.Command("gzip")

	cpioCmd.Stdin, _ = findCmd.StdoutPipe()
	gzipCmd.Stdin, _ = cpioCmd.StdoutPipe()

	outFile, err := os.Create(cpioPath)
	if err != nil {
		t.Fatalf("create cpio.gz: %v", err)
	}
	gzipCmd.Stdout = outFile

	for _, c := range []*exec.Cmd{gzipCmd, cpioCmd, findCmd} {
		if err := c.Start(); err != nil {
			_ = outFile.Close()
			t.Fatalf("start %s: %v", c.Path, err)
		}
	}
	for _, c := range []*exec.Cmd{findCmd, cpioCmd, gzipCmd} {
		if err := c.Wait(); err != nil {
			_ = outFile.Close()
			t.Fatalf("wait %s: %v", c.Path, err)
		}
	}
	_ = outFile.Close()

	return cpioPath
}

// runQEMUProvision launches QEMU for a full provisioning run.
// Returns the serial output. QEMU exits when BOOTy reboots.
func runQEMUProvision(t *testing.T, kernel, initramfs, disk string, timeoutDur time.Duration) []byte {
	t.Helper()

	args := []string{
		"-m", "1024",
		"-nographic",
		"-no-reboot",
		"-kernel", kernel,
		"-initrd", initramfs,
		"-drive", fmt.Sprintf("file=%s,format=qcow2,if=virtio", disk),
		"-net", "nic,model=e1000,macaddr=52:54:00:12:34:56",
		"-net", "user",
		"-append", "console=ttyS0 panic=1",
	}
	args = append(args, splitExtraArgs(os.Getenv("QEMU_EXTRA_ARGS"))...)

	ctx, cancel := context.WithTimeout(context.Background(), timeoutDur)
	defer cancel()

	cmd := exec.CommandContext(ctx, "qemu-system-x86_64", args...)
	out, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		t.Logf("QEMU provision timed out after %v. tail:\n%s", timeoutDur, tail(out, 2000))
	} else if err != nil {
		// Exit code from -no-reboot is expected when BOOTy calls reboot.
		t.Logf("QEMU provision exited: %v (expected on reboot)", err)
	}

	return out
}

// mountQcow2 mounts a qcow2 disk image via qemu-nbd and returns the root mount path
// and a cleanup function. The caller must defer cleanup.
func mountQcow2(t *testing.T, qcow2Path string) (rootMount string, cleanup func()) {
	t.Helper()

	// Find an available nbd device.
	run(t, "load nbd module", "modprobe", "nbd", "max_part=8")

	nbdDev := ""
	for i := 0; i < 16; i++ {
		dev := fmt.Sprintf("/dev/nbd%d", i)
		// Check if device is free by trying to connect.
		cmd := exec.Command("qemu-nbd", "--connect="+dev, qcow2Path)
		if out, err := cmd.CombinedOutput(); err == nil {
			nbdDev = dev
			break
		} else {
			t.Logf("nbd%d busy: %s", i, out)
		}
	}
	if nbdDev == "" {
		t.Fatal("no free nbd device found")
	}

	// Register disconnect immediately so it runs even if partprobe/mount fail.
	t.Cleanup(func() {
		_ = exec.Command("qemu-nbd", "--disconnect", nbdDev).Run()
	})

	// Wait for partitions after qemu-nbd attach.
	run(t, "partprobe nbd", "partprobe", nbdDev)
	rootPart := nbdDev + "p2"
	waitForDevice(t, rootPart, 10*time.Second)

	// Mount root partition.
	mountDir := t.TempDir()
	run(t, "mount provisioned root", "mount", "-o", "ro", rootPart, mountDir)

	cleanup = func() {
		_ = exec.Command("umount", mountDir).Run()
	}

	return mountDir, cleanup
}

// --- Low-level helpers ---

func run(t *testing.T, desc string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %s %v failed: %v\n%s", desc, name, args, err, out)
	}
}

func runOutput(t *testing.T, desc string, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %s %v failed: %v\n%s", desc, name, args, err, out)
	}
	return out
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// waitForDevice polls for a device node to appear, with a timeout.
func waitForDevice(t *testing.T, devPath string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(devPath); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("device %s did not appear within %s", devPath, timeout)
}

func buildBooty(t *testing.T, output string) {
	t.Helper()
	cmd := exec.Command("go", "build",
		"-ldflags", "-linkmode external -extldflags '-static' -s -w",
		"-o", output, "github.com/telekom/BOOTy")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1", "GOOS=linux", "GOARCH=amd64")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build booty: %v\n%s", err, out)
	}
}

func findBusybox(t *testing.T) string {
	t.Helper()
	for _, p := range []string{
		"/usr/bin/busybox",
		"/bin/busybox",
		"/usr/local/bin/busybox",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("busybox"); err == nil {
		return p
	}
	t.Fatal("busybox not found")
	return ""
}

func copyBinary(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatalf("open %s: %v", src, err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatalf("create %s: %v", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		t.Fatalf("copy %s -> %s: %v", src, dst, err)
	}
}

// copySharedLibs copies shared library dependencies of a binary into the initramfs.
func copySharedLibs(t *testing.T, binary, rootDir string) {
	t.Helper()
	cmd := exec.Command("ldd", binary)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Statically linked — no libs needed.
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		// Parse lines like: libext2fs.so.2 => /lib/x86_64-linux-gnu/libext2fs.so.2 (0x...)
		parts := strings.Fields(line)
		for i, p := range parts {
			if p == "=>" && i+1 < len(parts) {
				libPath := parts[i+1]
				if libPath == "" || libPath == "not" {
					continue
				}
				destDir := filepath.Join(rootDir, filepath.Dir(libPath))
				destPath := filepath.Join(rootDir, libPath)
				if _, err := os.Stat(destPath); err == nil {
					continue // already copied
				}
				if err := os.MkdirAll(destDir, 0o755); err != nil {
					continue
				}
				// Resolve symlinks to copy the actual file.
				realPath, err := filepath.EvalSymlinks(libPath)
				if err != nil {
					continue
				}
				copyBinary(t, realPath, destPath)
			}
		}
	}
}
