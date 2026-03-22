//go:build linux

package provision

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
)

const newroot = "/newroot"

// Configurator handles post-image OS configuration.
type Configurator struct {
	disk    *disk.Manager
	rootDir string // allows override for testing (default: /newroot)
}

// NewConfigurator creates a Configurator.
func NewConfigurator(diskMgr *disk.Manager) *Configurator {
	return &Configurator{disk: diskMgr, rootDir: newroot}
}

// SetRootDir overrides the root directory (for testing).
func (c *Configurator) SetRootDir(dir string) { c.rootDir = dir }

// SetHostname writes the hostname to /etc/hostname.
func (c *Configurator) SetHostname(cfg *config.MachineConfig) error {
	path := filepath.Join(c.rootDir, "etc", "hostname")
	slog.Info("Setting hostname", "hostname", cfg.Hostname, "path", path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating hostname dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(cfg.Hostname+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing hostname: %w", err)
	}
	return nil
}

// ConfigureKubelet writes kubelet drop-in configs for provider-id and node labels.
func (c *Configurator) ConfigureKubelet(cfg *config.MachineConfig) error {
	confDir := filepath.Join(c.rootDir, "etc", "kubernetes", "kubelet.conf.d")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		return fmt.Errorf("creating kubelet conf dir: %w", err)
	}

	if cfg.ProviderID != "" {
		content := fmt.Sprintf("KUBELET_EXTRA_ARGS=\"--provider-id=%s\"\n", cfg.ProviderID)
		path := filepath.Join(confDir, "10-caprf-provider-id.conf")
		slog.Info("Writing kubelet provider-id config", "path", path)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("writing provider-id conf: %w", err)
		}
	}

	var labels []string
	if cfg.FailureDomain != "" {
		labels = append(labels, "topology.kubernetes.io/zone="+cfg.FailureDomain)
	}
	if cfg.Region != "" {
		labels = append(labels, "topology.kubernetes.io/region="+cfg.Region)
	}
	if len(labels) > 0 {
		content := fmt.Sprintf("KUBELET_EXTRA_ARGS=\"--node-labels=%s\"\n", strings.Join(labels, ","))
		path := filepath.Join(confDir, "20-caprf-node-labels.conf")
		slog.Info("Writing kubelet node labels config", "path", path)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("writing node-labels conf: %w", err)
		}
	}
	return nil
}

// ConfigureGRUB writes GRUB kernel parameters and runs update-grub via chroot.
func (c *Configurator) ConfigureGRUB(ctx context.Context, cfg *config.MachineConfig) error {
	grubDir := filepath.Join(c.rootDir, "etc", "default", "grub.d")
	if err := os.MkdirAll(grubDir, 0o755); err != nil {
		return fmt.Errorf("creating grub.d dir: %w", err)
	}

	// Detect console: Lenovo uses ttyS1, default ttyS0.
	console := "ttyS0"
	if data, err := os.ReadFile("/sys/class/dmi/id/sys_vendor"); err == nil {
		if strings.Contains(strings.ToLower(string(data)), "lenovo") {
			console = "ttyS1"
		}
	}

	grubLine := fmt.Sprintf("GRUB_CMDLINE_LINUX=\"ds=nocloud console=%s %s\"\n", console, cfg.ExtraKernelParams)
	grubPath := filepath.Join(grubDir, "10-caprf-kernel-params.cfg")
	slog.Info("Writing GRUB config", "path", grubPath, "console", console)
	if err := os.WriteFile(grubPath, []byte(grubLine), 0o644); err != nil {
		return fmt.Errorf("writing grub config: %w", err)
	}

	// Run update-grub in chroot.
	out, err := c.disk.ChrootRun(ctx, c.rootDir, "update-grub")
	if err != nil {
		return fmt.Errorf("update-grub: %s: %w", string(out), err)
	}
	return nil
}

// CopyProvisionerFiles copies files from /deploy/file-system/ to the root.
func (c *Configurator) CopyProvisionerFiles() error {
	return c.copyTreeIntoChroot("/deploy/file-system", "provisioner files")
}

// CopyMachineFiles copies files from /deploy/machine-files/ to the root.
func (c *Configurator) CopyMachineFiles() error {
	return c.copyTreeIntoChroot("/deploy/machine-files", "machine files")
}

// copyTreeIntoChroot copies all files from srcBase into the chroot root.
// If srcBase does not exist, it logs and returns nil.
func (c *Configurator) copyTreeIntoChroot(srcBase, label string) error {
	if _, err := os.Stat(srcBase); os.IsNotExist(err) {
		slog.Info("No directory found", "label", label, "path", srcBase)
		return nil
	}
	slog.Info("Copying files", "label", label, "src", srcBase)
	return copyTree(srcBase, c.rootDir)
}

// copyTree copies all files from srcBase into destRoot, preserving directory structure.
// Symlinks and paths that escape destRoot are rejected to prevent path traversal.
func copyTree(srcBase, destRoot string) error {
	cleanDest, err := filepath.Abs(destRoot)
	if err != nil {
		return fmt.Errorf("resolve dest root: %w", err)
	}
	if err := filepath.WalkDir(srcBase, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk %s: %w", path, walkErr)
		}
		// Reject symlinks to prevent following links that escape the tree.
		if d.Type()&os.ModeSymlink != 0 {
			slog.Warn("Skipping symlink in copy tree", "path", path)
			return nil
		}
		relPath, _ := filepath.Rel(srcBase, path)
		destPath := filepath.Join(cleanDest, relPath)

		// Verify the resolved destination stays within destRoot.
		absDest, err := filepath.Abs(destPath)
		if err != nil || (!strings.HasPrefix(absDest, cleanDest+string(filepath.Separator)) && absDest != cleanDest) {
			return fmt.Errorf("path traversal blocked: %s escapes %s", relPath, cleanDest)
		}

		if d.IsDir() {
			return os.MkdirAll(destPath, 0o755)
		}
		return copyFile(path, destPath)
	}); err != nil {
		return fmt.Errorf("copy tree from %s: %w", srcBase, err)
	}
	return nil
}

// RunMachineCommands executes commands from /deploy/machine-commands/ in chroot.
func (c *Configurator) RunMachineCommands(ctx context.Context) error {
	cmdDir := "/deploy/machine-commands"
	if _, err := os.Stat(cmdDir); os.IsNotExist(err) {
		slog.Info("No machine commands directory found", "path", cmdDir)
		return nil
	}
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		return fmt.Errorf("reading machine-commands dir: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(cmdDir, entry.Name()))
		if err != nil {
			return fmt.Errorf("reading command file %s: %w", entry.Name(), err)
		}
		cmd := strings.TrimSpace(string(data))
		if cmd == "" {
			continue
		}
		slog.Info("Running machine command", "file", entry.Name(), "command", cmd)
		out, err := c.disk.ChrootRun(ctx, c.rootDir, cmd)
		if err != nil {
			return fmt.Errorf("machine command %s: %s: %w", entry.Name(), string(out), err)
		}
	}
	return nil
}

// RunPostProvisionCmds executes custom commands in the chroot after provisioning.
// Each command is run via /bin/bash -c in the chroot environment.
func (c *Configurator) RunPostProvisionCmds(ctx context.Context, cmds []string) error {
	for i, cmd := range cmds {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			continue
		}
		slog.Info("Running post-provision command", "index", i, "command", cmd)
		out, err := c.disk.ChrootRun(ctx, c.rootDir, cmd)
		if err != nil {
			return fmt.Errorf("post-provision cmd %d (%s): %s: %w", i, cmd, string(out), err)
		}
		if len(out) > 0 {
			slog.Debug("Post-provision command output", "index", i, "output", string(out))
		}
	}
	return nil
}

// ConfigureDNS writes resolv.conf to the chroot.
func (c *Configurator) ConfigureDNS(cfg *config.MachineConfig) error {
	if cfg.DNSResolvers == "" {
		return nil
	}
	path := filepath.Join(c.rootDir, "etc", "resolv.conf")
	slog.Info("Configuring DNS", "resolvers", cfg.DNSResolvers)
	var lines []string
	for _, r := range strings.Split(cfg.DNSResolvers, ",") {
		r = strings.TrimSpace(r)
		if r != "" {
			lines = append(lines, "nameserver "+r)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing resolv.conf: %w", err)
	}
	return nil
}

// MountEFIVars loads the efivarfs kernel module and mounts the efivarfs
// filesystem at /sys/firmware/efi/efivars if not already mounted.
// This is required before any efibootmgr operations.
func (c *Configurator) MountEFIVars(ctx context.Context) error {
	// Load the efivarfs module (best-effort — may already be built-in).
	if out, err := exec.CommandContext(ctx, "modprobe", "efivarfs").CombinedOutput(); err != nil { //nolint:gosec // fixed command
		slog.Info("modprobe efivarfs failed (may be built-in)", "output", strings.TrimSpace(string(out)))
	}

	efiPath := "/sys/firmware/efi/efivars"

	// Check if already mounted.
	if isMountPoint(efiPath) {
		slog.Info("efivarfs already mounted")
		return nil
	}

	// On non-EFI systems /sys/firmware/efi does not exist; skip gracefully.
	if _, err := os.Stat("/sys/firmware/efi"); os.IsNotExist(err) {
		slog.Info("non-EFI system detected, skipping efivarfs mount")
		return nil
	}

	if err := os.MkdirAll(efiPath, 0o755); err != nil {
		slog.Warn("create efivarfs mountpoint failed, continuing without EFI variable access", "error", err, "path", efiPath)
		return nil
	}
	if err := syscall.Mount("efivarfs", efiPath, "efivarfs", 0, ""); err != nil {
		slog.Warn("mount efivarfs failed, continuing without EFI variable access", "error", err, "path", efiPath)
		return nil
	}
	slog.Info("mounted efivarfs", "path", efiPath)
	return nil
}

// isMountPoint checks whether a path is already a mount point by reading /proc/mounts.
func isMountPoint(path string) bool {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == path {
			return true
		}
	}
	return false
}

// RemoveEFIBootEntries removes old EFI boot entries matching "ubuntu".
// Runs efibootmgr directly on the host (not in chroot) since it operates
// on the host's EFI variables via /sys/firmware/efi/efivars.
func (c *Configurator) RemoveEFIBootEntries(ctx context.Context) error {
	slog.Info("Removing old EFI boot entries")
	out, err := exec.CommandContext(ctx, "efibootmgr").CombinedOutput() //nolint:gosec // fixed command
	if err != nil {
		slog.Warn("efibootmgr list failed (non-EFI system?)", "output", string(out), "error", err)
		return nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(strings.ToLower(line), "ubuntu") {
			continue
		}
		if len(line) > 8 && strings.HasPrefix(line, "Boot") {
			bootNum := line[4:8]
			slog.Info("Removing EFI boot entry", "entry", bootNum)
			if out, err := exec.CommandContext(ctx, "efibootmgr", "-b", bootNum, "-B").CombinedOutput(); err != nil { //nolint:gosec // boot entry ID from efibootmgr output
				slog.Warn("Failed to remove EFI entry", "entry", bootNum, "output", string(out))
			}
		}
	}
	return nil
}

// CreateEFIBootEntry creates a new EFI boot entry for the installed OS.
func (c *Configurator) CreateEFIBootEntry(ctx context.Context, diskDev, bootPart string) error {
	if bootPart == "" {
		slog.Warn("No EFI partition found, skipping EFI boot entry creation")
		return nil
	}
	slog.Info("Creating EFI boot entry", "disk", diskDev, "partition", bootPart)

	// Detect EFI loader path — architecture-aware shimx64/shimaa64 with grub fallback.
	loader, err := efiLoaderPath(c.rootDir, runtime.GOARCH)
	if err != nil {
		return fmt.Errorf("detect EFI loader: %w", err)
	}

	// Determine partition number from the partition device path.
	partNum := partNumberFromDevice(bootPart)

	cmd := fmt.Sprintf("efibootmgr -c -d %s -p %s -L ubuntu -l %s", diskDev, partNum, loader)
	out, err := c.disk.ChrootRun(ctx, c.rootDir, cmd)
	if err != nil {
		return fmt.Errorf("efibootmgr create: %s: %w", string(out), err)
	}
	slog.Info("EFI boot entry created", "output", string(out))
	return nil
}

// efiLoaderNames returns the shim and grub EFI binary names for the given architecture.
func efiLoaderNames(arch string) (shimName, grubName string, err error) {
	switch arch {
	case "amd64":
		return "shimx64.efi", "grubx64.efi", nil
	case "arm64":
		return "shimaa64.efi", "grubaa64.efi", nil
	default:
		return "", "", fmt.Errorf("unsupported architecture: %s", arch)
	}
}

// efiLoaderPath determines the EFI loader path, preferring shim with grub fallback.
func efiLoaderPath(rootDir, arch string) (string, error) {
	shimName, grubName, err := efiLoaderNames(arch)
	if err != nil {
		return "", fmt.Errorf("resolve efi loader names: %w", err)
	}
	shimPath := filepath.Join(rootDir, "boot", "efi", "EFI", "ubuntu", shimName)
	grubPath := filepath.Join(rootDir, "boot", "efi", "EFI", "ubuntu", grubName)
	_, err = os.Stat(shimPath)
	if err == nil {
		return `\EFI\ubuntu\` + shimName, nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat shim %s: %w", shimPath, err)
	}
	_, err = os.Stat(grubPath)
	if err == nil {
		return `\EFI\ubuntu\` + grubName, nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat grub %s: %w", grubPath, err)
	}
	return "", fmt.Errorf("no EFI loader found: checked %s and %s", shimPath, grubPath)
}

// partNumberFromDevice extracts the partition number from a device path.
// e.g. "/dev/sda1" → "1", "/dev/nvme0n1p2" → "2".
func partNumberFromDevice(dev string) string {
	for i := len(dev) - 1; i >= 0; i-- {
		if dev[i] < '0' || dev[i] > '9' {
			return dev[i+1:]
		}
	}
	return "1"
}

// SetupMellanox detects and configures Mellanox ConnectX NICs.
// Returns true if firmware values were changed (requiring a hard reboot for reinit).
func (c *Configurator) SetupMellanox(ctx context.Context, numVFs int) (bool, error) {
	slog.Info("Checking for Mellanox NICs")

	// Detect Mellanox NICs via sysfs (vendor 0x15b3) instead of lspci.
	found, err := hasPCIVendorFunc("15b3")
	if err != nil {
		slog.Info("PCI enumeration failed, skipping Mellanox setup", "error", err)
		return false, nil
	}
	if !found {
		slog.Info("No Mellanox NICs found")
		return false, nil
	}

	if numVFs <= 0 {
		numVFs = 32
	}

	// Enumerate all Mellanox mst pciconf devices dynamically.
	listOut, err := c.disk.ChrootRun(ctx, c.rootDir, "ls /dev/mst/")
	if err != nil {
		slog.Warn("Cannot list /dev/mst/ devices", "error", err)
		return false, nil
	}

	changed := false
	for _, entry := range strings.Fields(string(listOut)) {
		if !strings.Contains(entry, "pciconf") {
			continue
		}
		// Validate device name with allowlist to prevent shell injection.
		if !isSafeDeviceName(entry) {
			slog.Warn("Skipping mst device with invalid characters", "entry", entry)
			continue
		}
		devPath := "/dev/mst/" + entry
		cmd := fmt.Sprintf("mstconfig -d %s set NUM_OF_VFS=%d", devPath, numVFs)
		slog.Info("Configuring Mellanox SR-IOV", "device", devPath, "numVFs", numVFs)
		out, err := c.disk.ChrootRun(ctx, c.rootDir, cmd)
		if err != nil {
			slog.Warn("mstconfig failed", "device", devPath, "output", string(out), "error", err)
			continue
		}
		changed = true
	}

	if changed {
		slog.Info("Mellanox firmware values changed, hard reboot required")
	}
	return changed, nil
}

// isSafeDeviceName validates that a device name contains only safe characters
// (letters, digits, dots, underscores, hyphens).
func isSafeDeviceName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

// hasPCIVendorFunc is the PCI vendor check function, replaceable in tests.
var hasPCIVendorFunc = hasPCIVendor

// SetPCIVendorCheckFunc overrides the PCI vendor detection for testing.
// Returns a restore function that resets to the original implementation.
func SetPCIVendorCheckFunc(fn func(string) (bool, error)) func() {
	old := hasPCIVendorFunc
	hasPCIVendorFunc = fn
	return func() { hasPCIVendorFunc = old }
}

// hasPCIVendor checks if any PCI device with the given vendor ID exists via sysfs.
func hasPCIVendor(vendorID string) (bool, error) {
	entries, err := os.ReadDir("/sys/bus/pci/devices")
	if err != nil {
		return false, fmt.Errorf("reading PCI devices: %w", err)
	}
	target := "0x" + vendorID
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join("/sys/bus/pci/devices", entry.Name(), "vendor")) //nolint:gocritic // absolute sysfs path is correct
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == target {
			return true, nil
		}
	}
	return false, nil
}

// copyFile copies a file preserving permissions.
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", dst, err)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return fmt.Errorf("open dest %s: %w", dst, err)
	}
	defer func() { _ = out.Close() }()

	if _, err = io.Copy(out, in); err != nil {
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	return nil
}
