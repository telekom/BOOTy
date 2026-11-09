//go:build e2e

package kvm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Scenario layered-layout (.github/workflows/kvm-matrix.yml) runs
// ^TestLayeredLayout, that is FixtureImage, WholeDiskVM, SystemABVM and
// PartitionLayoutVM, on the fixture of layered_layout_fixture_test.go. The
// TestPendingLayeredLayout* tests name what the fixture is for but BOOTy
// does not do yet; they skip and stay out of the scenario regex (the matrix
// fails on a skipped test).

// TestLayeredLayoutFixtureImage checks the fixture itself, without a VM: the
// content, owners, modes, xattrs, ACLs and file capabilities are in the
// image, and the layout JSON is a valid PARTITION_LAYOUT.
func TestLayeredLayoutFixtureImage(t *testing.T) {
	requireRoot(t)
	requireProvisionTools(t)

	rawDisk := createLayeredLayoutTestDiskImage(t, 512)
	withLoopRoot(t, rawDisk, true, func(root string) {
		assertLayeredEntries(t, root, "", false, layeredCheck{})
		assertLayeredLayoutJSON(t, root)
	})
}

// TestLayeredLayoutWholeDiskVM provisions the fixture in whole-disk mode.
// The image is copied block by block, so every directory keeps its content
// and metadata on the single root.
func TestLayeredLayoutWholeDiskVM(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	requireDiskInspectTools(t)

	baseURL := startImageServer(t, compressGzip(t, createLayeredLayoutTestDiskImage(t, 512)))
	targetDisk := filepath.Join(t.TempDir(), "layered-whole-disk.qcow2")
	run(t, "create layered whole-disk target", "qemu-img", "create", "-f", "qcow2", targetDisk, "2G")

	initramfs := buildProvisionInitramfs(t, map[string]string{
		"HOSTNAME":                  "layered-whole-disk",
		"MODE":                      "provision",
		"DISK_DEVICE":               "/dev/vda",
		"IMAGE_ALLOW_INSECURE_HTTP": "true",
		"IMAGE":                     baseURL + "/image.gz",
	})
	output := runQEMUProvision(t, findKernel(t), initramfs, targetDisk, 7*time.Minute)
	t.Logf("layered whole-disk VM output tail:\n%s", tail(output, 3000))
	assertProvisionSucceeded(t, output)

	root, cleanup := mountQcow2Partition(t, targetDisk, 2)
	defer cleanup()
	if got := strings.TrimSpace(readProvisionedFile(t, root, "etc/hostname")); got != "layered-whole-disk" {
		t.Fatalf("hostname = %q", got)
	}
	assertLayeredEntries(t, root, "", false, layeredCheck{})
	assertLayeredLayoutJSON(t, root)
}

// TestLayeredLayoutSystemABVM provisions the fixture as system-ab with
// /home, /tmp and /var on shared data partitions (AB_DATA_PARTITIONS). It
// asserts the read-only root slot, fstab, and the seeded data partitions
// with content, owners, modes, xattrs, ACLs and file capabilities.
func TestLayeredLayoutSystemABVM(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	requireDiskInspectTools(t)

	baseURL := startABImageServer(t, compressGzip(t, createLayeredLayoutTestDiskImage(t, 512)))
	targetDisk := filepath.Join(t.TempDir(), "layered-system-ab.qcow2")
	run(t, "create layered system-ab target", "qemu-img", "create", "-f", "qcow2", targetDisk, "3G")

	initramfs := buildProvisionInitramfs(t, map[string]string{
		"HOSTNAME":                  "layered-system-ab",
		"dns_resolver":              "8.8.8.8",
		"MODE":                      "provision",
		"DISK_DEVICE":               "/dev/vda",
		"STATIC_IP":                 "10.0.2.15/24",
		"STATIC_GATEWAY":            "10.0.2.2",
		"STATIC_IFACE":              "eth0",
		"IMAGE_ALLOW_INSECURE_HTTP": "true",
		"IMAGE":                     baseURL + "/image.gz",
		"IMAGE_MODE":                "ab",
		"AB_SCHEME":                 "system-ab",
		"AB_TARGET_SLOT":            "a",
		"AB_SOURCE_ROOT_PARTITION":  "2",
		"AB_BOOT_SIZE_MB":           "64",
		"AB_ROOT_SIZE_MB":           "768",
		"AB_DATA_PARTITIONS":        layeredLayoutDataPartitions,
	})
	output := runQEMUNetworkMode(t, findKernel(t), initramfs, targetDisk, 7*time.Minute)
	t.Logf("layered system-ab VM output tail:\n%s", tail(output, 3000))
	assertProvisionSucceeded(t, output)

	slotA, cleanupA := mountQcow2Partition(t, targetDisk, 2)
	fstab := readProvisionedFile(t, slotA, "etc/fstab")
	for _, want := range []string{
		"PARTLABEL=BOOTY-ROOT-A\t/\text4\tro\t0\t1",
		"PARTLABEL=BOOTY-HOME\t/home\text4",
		"PARTLABEL=BOOTY-TMP\t/tmp\text4",
		"PARTLABEL=BOOTY-DATA\t/var\text4",
	} {
		if !strings.Contains(fstab, want) {
			t.Errorf("system-ab fstab missing %q:\n%s", want, fstab)
		}
	}
	// The slot is a block copy of the image root: the files below the data
	// mountpoints stay there, with all metadata, under the mounts.
	assertLayeredEntries(t, slotA, "", false, layeredCheck{})
	assertLayeredLayoutJSON(t, slotA)
	cleanupA()

	for _, data := range []struct {
		part   int
		prefix string
	}{{4, "home/"}, {5, "tmp/"}, {6, "var/"}} {
		mount, cleanup := mountQcow2Partition(t, targetDisk, data.part)
		assertLayeredEntries(t, mount, data.prefix, true, layeredCheck{})
		cleanup()
	}
}

// TestLayeredLayoutPartitionLayoutVM provisions the fixture with the CIS-like
// layout that the image ships (PARTITION_LAYOUT_IMAGE_PATH): BOOTy writes the
// image whole-disk, reads the layout from its root, creates the layout and
// writes the image root into it. Each data mountpoint, nested ones included,
// is seeded from the image root with content, owners, modes, xattrs, ACLs
// and file capabilities, and fstab carries the mount options.
func TestLayeredLayoutPartitionLayoutVM(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	requireDiskInspectTools(t)

	baseURL := startImageServer(t, compressGzip(t, createLayeredLayoutTestDiskImage(t, 512)))
	targetDisk := filepath.Join(t.TempDir(), "layered-partition-layout.qcow2")
	run(t, "create layered partition-layout target", "qemu-img", "create", "-f", "qcow2", targetDisk, "2G")

	initramfs := buildProvisionInitramfs(t, map[string]string{
		"HOSTNAME":                    "layered-partition-layout",
		"MODE":                        "provision",
		"DISK_DEVICE":                 "/dev/vda",
		"IMAGE_ALLOW_INSECURE_HTTP":   "true",
		"IMAGE":                       baseURL + "/image.gz",
		"IMAGE_SOURCE_ROOT_PARTITION": "2",
		"PARTITION_LAYOUT_IMAGE_PATH": "/" + filepath.Dir(layeredLayoutJSONPath) + "/*.json",
	})
	output := runQEMUProvision(t, findKernel(t), initramfs, targetDisk, 10*time.Minute)
	t.Logf("layered partition-layout VM output tail:\n%s", tail(output, 3000))
	assertProvisionSucceeded(t, output)
	if !strings.Contains(string(output), "partition layout from image") {
		t.Errorf("BOOTy did not log the layout from the image")
	}

	root, cleanupRoot := mountQcow2Partition(t, targetDisk, 2)
	if got := strings.TrimSpace(readProvisionedFile(t, root, "etc/hostname")); got != "layered-partition-layout" {
		t.Errorf("hostname = %q", got)
	}
	fstab := readProvisionedFile(t, root, "etc/fstab")
	for _, want := range []string{
		"PARTLABEL=ROOT\t/\text4\tdefaults\t0\t1",
		"PARTLABEL=EFI\t/boot/efi\tvfat\tumask=0077\t0\t2",
		"PARTLABEL=AUDIT\t/var/log/audit\text4\tnodev,nosuid,noexec\t0\t2",
		"PARTLABEL=LOG\t/var/log\text4\tnodev,nosuid,noexec\t0\t2",
		"PARTLABEL=VARTMP\t/var/tmp\text4\tnodev,nosuid,noexec\t0\t2",
		"PARTLABEL=VAR\t/var\text4\tnodev,nosuid\t0\t2",
		"PARTLABEL=HOME\t/home\text4\tnodev,nosuid\t0\t2",
		"PARTLABEL=TMP\t/tmp\text4\tnodev,nosuid,noexec\t0\t2",
	} {
		if !strings.Contains(fstab, want) {
			t.Errorf("fstab missing %q:\n%s", want, fstab)
		}
	}
	// The root is a block copy of the image root and keeps everything.
	assertLayeredEntries(t, root, "", false, layeredCheck{})
	cleanupRoot()

	// The layout formats a new ESP; BOOTy puts the fallback loader on it.
	esp, cleanupESP := mountQcow2Partition(t, targetDisk, 1)
	if loader, err := os.ReadFile(filepath.Join(esp, "EFI", "BOOT", "BOOTX64.EFI")); err != nil || string(loader) != testEFIFallbackPayload {
		t.Errorf("ESP fallback loader = %q (%v), want the test payload", loader, err)
	}
	cleanupESP()

	for _, m := range layeredLayoutMounts {
		prefix := strings.TrimPrefix(m.mountpoint, "/") + "/"
		var nested []string
		for _, other := range layeredLayoutMounts {
			if strings.HasPrefix(other.mountpoint, m.mountpoint+"/") {
				nested = append(nested, strings.TrimPrefix(other.mountpoint, "/")+"/")
			}
		}
		mount, cleanup := mountQcow2Partition(t, targetDisk, m.part)
		assertLayeredEntries(t, mount, prefix, true, layeredCheck{exclude: nested})
		// A nested mountpoint is an empty directory in its parent.
		for _, n := range nested {
			rel := strings.TrimPrefix(strings.TrimSuffix(n, "/"), prefix)
			if strings.Contains(rel, "/") {
				continue // below another nested mount
			}
			entries, err := os.ReadDir(filepath.Join(mount, rel))
			if err != nil || len(entries) != 0 {
				t.Errorf("%s: nested mountpoint %s = %d entries (%v), want an empty directory", m.mountpoint, rel, len(entries), err)
			}
		}
		cleanup()
	}
}

// TestPendingLayeredLayoutSignedModuleVM: an out-of-tree kernel module in
// the fixture, signed with a test MOK, loads in the installed OS under
// Secure Boot (OVMF secboot + MOK enrollment).
func TestPendingLayeredLayoutSignedModuleVM(t *testing.T) {
	t.Skip("pending: MOK-signed out-of-tree module in the fixture and MOK enrollment")
}

// TestPendingLayeredLayoutLUKSDataVolumeVM: a data partition of the layered
// layout is a LUKS volume (TPM-bound with swtpm) that the installed OS opens
// from crypttab.
func TestPendingLayeredLayoutLUKSDataVolumeVM(t *testing.T) {
	t.Skip("pending: LUKS-encrypted data partition in the layered layout")
}
