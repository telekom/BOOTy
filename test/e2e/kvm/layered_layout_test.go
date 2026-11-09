//go:build e2e

package kvm

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Scenario layered-layout (.github/workflows/kvm-matrix.yml) runs
// ^TestLayeredLayout, that is FixtureImage, WholeDiskVM and SystemABVM, on
// the fixture of layered_layout_fixture_test.go. The TestPendingLayeredLayout* tests name
// what the fixture is for but BOOTy does not do yet; they skip and stay out
// of the scenario regex (the matrix fails on a skipped test).

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
// /home, /tmp and /var on shared data partitions (AB_DATA_PARTITIONS).
// Asserted today: the read-only root slot keeps all metadata (block copy),
// fstab mounts the slot read-only and the data partitions, and the seeded
// data partitions have the content, owners and modes.
//
// Known gap (strict expected failure): seeding copies files without their
// xattrs, ACLs and file capabilities. The test fails once seeding keeps any
// of them, so that change turns the check into an assertion.
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
		assertLayeredEntries(t, mount, data.prefix, true, layeredCheck{skipXattrs: true})
		kept, lost := extendedMetadataKept(t, mount, data.prefix)
		if len(kept) > 0 {
			t.Errorf("seeded /%s keeps %s now: assert the metadata (layeredCheck{}) and drop the known gap", data.prefix, strings.Join(kept, ", "))
		}
		t.Logf("known gap: seeded /%s lost %s", data.prefix, strings.Join(lost, ", "))
		cleanup()
	}
}

// TestPendingLayeredLayoutPartitionLayoutSeedingVM: whole-disk provisioning
// with the multi-mountpoint layout of the image (PARTITION_LAYOUT, or the
// layout JSON read from the image), with /var, /home and /tmp seeded from
// the image root with xattrs, ACLs, file capabilities and mount options.
// Today a non-A/B layout supports only / and /boot/efi.
func TestPendingLayeredLayoutPartitionLayoutSeedingVM(t *testing.T) {
	t.Skip("pending: non-A/B PARTITION_LAYOUT with data mountpoints and metadata-preserving seeding")
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
