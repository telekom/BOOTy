//go:build e2e

package kvm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/stream"

	imageutil "github.com/telekom/BOOTy/pkg/image"
)

func TestProvisionInitramfsContainsABStreamingTools(t *testing.T) {
	requireProvisionTools(t)

	initramfs := buildProvisionInitramfs(t, map[string]string{
		"MODE":        "provision",
		"IMAGE_MODE":  "ab",
		"DISK_DEVICE": "/dev/vda",
		"IMAGE":       "http://10.0.2.2/image.gz",
	})

	files := listInitramfsFiles(t, initramfs)
	for _, want := range []string{
		"sbin/losetup",
		"sbin/dd",
		"sbin/sfdisk",
		"sbin/partprobe",
		"sbin/sgdisk",
		"sbin/mkfs.ext4",
		"sbin/mkfs.vfat",
		"bin/mdev",
	} {
		if !files[want] && !files["./"+want] {
			t.Fatalf("initramfs missing %s; A/B StreamAB cannot run in VM", want)
		}
	}
}

func TestABProvisionPreloadsSysextsWithoutActivatingVM(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	requireDiskInspectTools(t)

	rawDisk := createChrootCapableTestDiskImage(t, 512)
	gzImage := compressGzip(t, rawDisk)
	sysextPayload := []byte("node tuning sysext vm e2e\n")
	sysextSum := sha256.Sum256(sysextPayload)
	baseURL := startABImageServer(t, gzImage)
	ociPushHost, ociGuestHost := startABOCIRegistry(t)
	pushKVMSysextOCI(t, ociPushHost, "test/node-tuning:v1", sysextPayload)
	sysextURL := "oci://" + ociGuestHost + "/test/node-tuning:v1"
	sysextLayers := fmt.Sprintf(
		`[{"name":"node-tuning","version":"vm-e2e","source":%q,"fileName":"node-tuning.raw","sha256":"sha256:%s","mode":"preload"}]`,
		sysextURL,
		hex.EncodeToString(sysextSum[:]),
	)

	targetDisk := filepath.Join(t.TempDir(), "ab-sysext-target.qcow2")
	run(t, "create target disk", "qemu-img", "create", "-f", "qcow2", targetDisk, "2G")

	initramfs := buildProvisionInitramfs(t, map[string]string{
		"HOSTNAME":                 "ab-sysext-vm",
		"dns_resolver":             "8.8.8.8",
		"MODE":                     "provision",
		"DISK_DEVICE":              "/dev/vda",
		"STATIC_IP":                "10.0.2.15/24",
		"STATIC_GATEWAY":           "10.0.2.2",
		"STATIC_IFACE":             "eth0",
		"IMAGE":                    baseURL + "/image.gz",
		"IMAGE_MODE":               "ab",
		"AB_SCHEME":                "dual-root",
		"AB_TARGET_SLOT":           "inactive",
		"AB_SOURCE_ROOT_PARTITION": "2",
		"AB_BOOT_SIZE_MB":          "64",
		"AB_ROOT_SIZE_MB":          "768",
		"AB_STATE_SIZE_MB":         "64",
		"SYSEXT_ENABLED":           "true",
		"SYSEXT_DEFAULT_MODE":      "preload",
		"SYSEXT_LAYERS":            sysextLayers,
	})

	output := runQEMUNetworkMode(t, findKernel(t), initramfs, targetDisk, 7*time.Minute)
	t.Logf("A/B sysext VM output tail:\n%s", tail(output, 4000))
	assertProvisionSucceeded(t, output)

	rootMount, cleanup := mountQcow2(t, targetDisk)
	defer cleanup()

	hostname := readProvisionedFile(t, rootMount, "etc/hostname")
	if !strings.Contains(hostname, "ab-sysext-vm") {
		t.Fatalf("hostname not written in A/B target root: %q", strings.TrimSpace(hostname))
	}

	catalog := readProvisionedFile(t, rootMount, "usr/lib/tcaas-sysext/preloaded/catalog.json")
	for _, want := range []string{
		`"name": "node-tuning"`,
		`"version": "vm-e2e"`,
		`"path": "/usr/lib/tcaas-sysext/preloaded/node-tuning.raw"`,
		`"digest": "sha256:` + hex.EncodeToString(sysextSum[:]) + `"`,
	} {
		if !strings.Contains(catalog, want) {
			t.Fatalf("catalog missing %s:\n%s", want, catalog)
		}
	}
	if _, err := os.Stat(filepath.Join(rootMount, "usr/lib/tcaas-sysext/preloaded/node-tuning.raw")); err != nil {
		t.Fatalf("preloaded sysext missing: %v", err)
	}
	for _, dir := range []string{
		"etc/extensions",
		"run/extensions",
		"var/lib/extensions",
		"usr/lib/extensions",
	} {
		if _, err := os.Stat(filepath.Join(rootMount, dir, "node-tuning.raw")); !os.IsNotExist(err) {
			t.Fatalf("preloaded sysext must not be active under /%s; stat err=%v", dir, err)
		}
	}
	slotState := readProvisionedFile(t, rootMount, "etc/booty/ab-slot.env")
	if !strings.Contains(slotState, "BOOTY_AB_TARGET_SLOT=a") {
		t.Fatalf("A/B slot state did not record initial target slot a:\n%s", slotState)
	}
	grubDefaults := readProvisionedFile(t, rootMount, "etc/default/grub.d/10-caprf-kernel-params.cfg")
	if !strings.Contains(grubDefaults, "root=PARTLABEL=BOOTY-ROOT-A") {
		t.Fatalf("A/B GRUB defaults did not pin the target root slot:\n%s", grubDefaults)
	}

	cleanup()
	espMount, espCleanup := mountQcow2Partition(t, targetDisk, 1)
	defer espCleanup()
	loader, err := os.ReadFile(filepath.Join(espMount, "EFI", "BOOT", "BOOTX64.EFI"))
	if err != nil {
		t.Fatalf("ESP fallback loader missing: %v", err)
	}
	if string(loader) != testEFIFallbackPayload {
		t.Fatalf("ESP fallback loader payload = %q, want test fixture", string(loader))
	}
	grubCfg := readProvisionedFile(t, espMount, "EFI/BOOT/grub.cfg")
	for _, want := range []string{
		"search --no-floppy --set=booty_root --file /etc/booty/grub-target-",
		"configfile ($booty_root)/boot/grub/grub.cfg",
	} {
		if !strings.Contains(grubCfg, want) {
			t.Fatalf("ESP grub.cfg missing %q:\n%s", want, grubCfg)
		}
	}
}

func TestFlatcarLikeUSRASourceCanProvisionSystemABVM(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	requireDiskInspectTools(t)

	rawDisk := createFlatcarLikeUSRATestDiskImage(t, 512)
	gzImage := compressGzip(t, rawDisk)
	baseURL := startABImageServer(t, gzImage)

	targetDisk := filepath.Join(t.TempDir(), "flatcar-usra-system-ab.qcow2")
	run(t, "create flatcar-like target disk", "qemu-img", "create", "-f", "qcow2", targetDisk, "2G")

	initramfs := buildProvisionInitramfs(t, map[string]string{
		"HOSTNAME":             "flatcar-usra-system-ab",
		"dns_resolver":         "8.8.8.8",
		"MODE":                 "provision",
		"DISK_DEVICE":          "/dev/vda",
		"STATIC_IP":            "10.0.2.15/24",
		"STATIC_GATEWAY":       "10.0.2.2",
		"STATIC_IFACE":         "eth0",
		"IMAGE":                baseURL + "/image.gz",
		"IMAGE_MODE":           "ab",
		"AB_SCHEME":            "system-ab",
		"AB_TARGET_SLOT":       "a",
		"AB_SOURCE_ROOT_LABEL": "USR-A",
		"AB_BOOT_SIZE_MB":      "64",
		"AB_ROOT_SIZE_MB":      "768",
		"AB_STATE_SIZE_MB":     "64",
	})

	output := runQEMUNetworkMode(t, findKernel(t), initramfs, targetDisk, 7*time.Minute)
	t.Logf("Flatcar-like USR-A system-ab VM output tail:\n%s", tail(output, 3000))
	assertProvisionSucceeded(t, output)

	rootMount, cleanup := mountQcow2Partition(t, targetDisk, 2)
	defer cleanup()
	hostname := readProvisionedFile(t, rootMount, "etc/hostname")
	if !strings.Contains(hostname, "flatcar-usra-system-ab") {
		t.Fatalf("hostname not written from Flatcar-like USR-A source: %q", strings.TrimSpace(hostname))
	}
}

func TestABPreserveExistingUpgradeKeepsActiveSlotAndWritesInactiveVM(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	requireDiskInspectTools(t)

	initialRaw := createChrootCapableTestDiskImage(t, 512)
	initialImage := compressGzip(t, initialRaw)
	initialURL := startABImageServer(t, initialImage)

	upgradeRaw := createChrootCapableTestDiskImage(t, 512)
	upgradeImage := compressGzip(t, upgradeRaw)
	upgradeURL := startABImageServer(t, upgradeImage)

	targetDisk := filepath.Join(t.TempDir(), "ab-preserve-target.qcow2")
	run(t, "create preserve target disk", "qemu-img", "create", "-f", "qcow2", targetDisk, "2G")

	initialInitramfs := buildProvisionInitramfs(t, map[string]string{
		"HOSTNAME":                 "ab-preserve-initial",
		"dns_resolver":             "8.8.8.8",
		"MODE":                     "provision",
		"DISK_DEVICE":              "/dev/vda",
		"STATIC_IP":                "10.0.2.15/24",
		"STATIC_GATEWAY":           "10.0.2.2",
		"STATIC_IFACE":             "eth0",
		"IMAGE":                    initialURL + "/image.gz",
		"IMAGE_MODE":               "ab",
		"AB_SCHEME":                "dual-root",
		"AB_TARGET_SLOT":           "a",
		"AB_SOURCE_ROOT_PARTITION": "2",
		"AB_BOOT_SIZE_MB":          "64",
		"AB_ROOT_SIZE_MB":          "768",
		"AB_STATE_SIZE_MB":         "64",
	})

	initialOutput := runQEMUNetworkMode(t, findKernel(t), initialInitramfs, targetDisk, 7*time.Minute)
	t.Logf("A/B initial VM output tail:\n%s", tail(initialOutput, 3000))
	assertProvisionSucceeded(t, initialOutput)

	upgradeInitramfs := buildProvisionInitramfs(t, map[string]string{
		"HOSTNAME":                 "ab-preserve-upgrade",
		"dns_resolver":             "8.8.8.8",
		"MODE":                     "provision",
		"DISK_DEVICE":              "/dev/vda",
		"STATIC_IP":                "10.0.2.15/24",
		"STATIC_GATEWAY":           "10.0.2.2",
		"STATIC_IFACE":             "eth0",
		"IMAGE":                    upgradeURL + "/image.gz",
		"IMAGE_MODE":               "ab",
		"AB_SCHEME":                "dual-root",
		"AB_ACTIVE_SLOT":           "a",
		"AB_TARGET_SLOT":           "inactive",
		"AB_PRESERVE_EXISTING":     "true",
		"AB_SOURCE_ROOT_PARTITION": "2",
		"AB_BOOT_SIZE_MB":          "64",
		"AB_ROOT_SIZE_MB":          "768",
		"AB_STATE_SIZE_MB":         "64",
	})

	upgradeOutput := runQEMUNetworkMode(t, findKernel(t), upgradeInitramfs, targetDisk, 7*time.Minute)
	t.Logf("A/B preserve-existing VM output tail:\n%s", tail(upgradeOutput, 5000))
	assertProvisionSucceeded(t, upgradeOutput)
	upgradeText := string(upgradeOutput)
	for _, want := range []string{
		"A/B preserveExisting enabled, skipping whole-disk wipe",
		"keeping A/B preserve-existing root mounted for kexec",
		"a/b preserveExisting requires kexec; refusing normal reboot",
	} {
		if !strings.Contains(upgradeText, want) {
			t.Fatalf("preserve-existing output missing %q. tail:\n%s", want, tail(upgradeOutput, 5000))
		}
	}

	slotA, cleanupA := mountQcow2Partition(t, targetDisk, 2)
	defer cleanupA()
	hostnameA := readProvisionedFile(t, slotA, "etc/hostname")
	if !strings.Contains(hostnameA, "ab-preserve-initial") {
		t.Fatalf("active slot A hostname changed during preserve-existing upgrade: %q", strings.TrimSpace(hostnameA))
	}
	stateA := readProvisionedFile(t, slotA, "etc/booty/ab-slot.env")
	if !strings.Contains(stateA, "BOOTY_AB_TARGET_SLOT=a") {
		t.Fatalf("slot A state changed unexpectedly:\n%s", stateA)
	}
	cleanupA()

	slotB, cleanupB := mountQcow2Partition(t, targetDisk, 3)
	defer cleanupB()
	hostnameB := readProvisionedFile(t, slotB, "etc/hostname")
	if !strings.Contains(hostnameB, "ab-preserve-upgrade") {
		t.Fatalf("inactive slot B hostname not written by upgrade: %q", strings.TrimSpace(hostnameB))
	}
	stateB := readProvisionedFile(t, slotB, "etc/booty/ab-slot.env")
	for _, want := range []string{
		"BOOTY_AB_TARGET_SLOT=b",
		"BOOTY_AB_BOOTED_SLOT=b",
		"BOOTY_AB_ACTIVE_SLOT=a",
		"BOOTY_AB_PRESERVE_EXISTING=true",
	} {
		if !strings.Contains(stateB, want) {
			t.Fatalf("slot B state missing %q:\n%s", want, stateB)
		}
	}
	cleanupB()

	espMount, espCleanup := mountQcow2Partition(t, targetDisk, 1)
	defer espCleanup()
	loader, err := os.ReadFile(filepath.Join(espMount, "EFI", "BOOT", "BOOTX64.EFI"))
	if err != nil {
		t.Fatalf("ESP fallback loader missing after preserve-existing upgrade: %v", err)
	}
	if string(loader) != testEFIFallbackPayload {
		t.Fatalf("ESP fallback loader was changed during preserve-existing upgrade: %q", string(loader))
	}
}

func TestSystemABPreserveExistingKeepsSharedVarVM(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	requireDiskInspectTools(t)

	initialRaw := createChrootCapableTestDiskImage(t, 512)
	initialImage := compressGzip(t, initialRaw)
	initialURL := startABImageServer(t, initialImage)

	upgradeRaw := createChrootCapableTestDiskImage(t, 512)
	upgradeImage := compressGzip(t, upgradeRaw)
	upgradeURL := startABImageServer(t, upgradeImage)

	targetDisk := filepath.Join(t.TempDir(), "system-ab-target.qcow2")
	run(t, "create system-ab target disk", "qemu-img", "create", "-f", "qcow2", targetDisk, "3G")

	initialInitramfs := buildProvisionInitramfs(t, map[string]string{
		"HOSTNAME":                 "system-ab-initial",
		"dns_resolver":             "8.8.8.8",
		"MODE":                     "provision",
		"DISK_DEVICE":              "/dev/vda",
		"STATIC_IP":                "10.0.2.15/24",
		"STATIC_GATEWAY":           "10.0.2.2",
		"STATIC_IFACE":             "eth0",
		"IMAGE":                    initialURL + "/image.gz",
		"IMAGE_MODE":               "ab",
		"AB_SCHEME":                "system-ab",
		"AB_TARGET_SLOT":           "a",
		"AB_SOURCE_ROOT_PARTITION": "2",
		"AB_BOOT_SIZE_MB":          "64",
		"AB_ROOT_SIZE_MB":          "768",
		"AB_STATE_SIZE_MB":         "512",
		"CLOUDINIT_ENABLED":        "true",
	})

	initialOutput := runQEMUNetworkMode(t, findKernel(t), initialInitramfs, targetDisk, 7*time.Minute)
	t.Logf("system-ab initial VM output tail:\n%s", tail(initialOutput, 3000))
	assertProvisionSucceeded(t, initialOutput)

	slotA, cleanupA := mountQcow2Partition(t, targetDisk, 2)
	fstab := readProvisionedFile(t, slotA, "etc/fstab")
	if !strings.Contains(fstab, "PARTLABEL=BOOTY-ROOT-A\t/\text4\tro\t0\t1") {
		t.Fatalf("system-ab fstab missing read-only root:\n%s", fstab)
	}
	if !strings.Contains(fstab, "PARTLABEL=BOOTY-DATA\t/var\text4") {
		t.Fatalf("system-ab fstab missing shared /var:\n%s", fstab)
	}
	cleanupA()

	dataMount, dataCleanup := mountQcow2Partition(t, targetDisk, 4)
	meta := readProvisionedFile(t, dataMount, "lib/cloud/seed/nocloud/meta-data")
	if !strings.Contains(meta, "system-ab-initial") {
		t.Fatalf("initial cloud-init seed missing from shared /var:\n%s", meta)
	}
	dataCleanup()

	upgradeInitramfs := buildProvisionInitramfs(t, map[string]string{
		"HOSTNAME":                 "system-ab-upgrade",
		"dns_resolver":             "8.8.8.8",
		"MODE":                     "provision",
		"DISK_DEVICE":              "/dev/vda",
		"STATIC_IP":                "10.0.2.15/24",
		"STATIC_GATEWAY":           "10.0.2.2",
		"STATIC_IFACE":             "eth0",
		"IMAGE":                    upgradeURL + "/image.gz",
		"IMAGE_MODE":               "ab",
		"AB_SCHEME":                "system-ab",
		"AB_ACTIVE_SLOT":           "a",
		"AB_TARGET_SLOT":           "inactive",
		"AB_PRESERVE_EXISTING":     "true",
		"AB_SOURCE_ROOT_PARTITION": "2",
		"AB_BOOT_SIZE_MB":          "64",
		"AB_ROOT_SIZE_MB":          "768",
		"AB_STATE_SIZE_MB":         "512",
	})

	upgradeOutput := runQEMUNetworkMode(t, findKernel(t), upgradeInitramfs, targetDisk, 7*time.Minute)
	t.Logf("system-ab preserve-existing VM output tail:\n%s", tail(upgradeOutput, 5000))
	assertProvisionSucceeded(t, upgradeOutput)

	slotB, cleanupB := mountQcow2Partition(t, targetDisk, 3)
	hostnameB := readProvisionedFile(t, slotB, "etc/hostname")
	if !strings.Contains(hostnameB, "system-ab-upgrade") {
		t.Fatalf("inactive slot B hostname not written by system-ab upgrade: %q", strings.TrimSpace(hostnameB))
	}
	cleanupB()

	dataMount, dataCleanup = mountQcow2Partition(t, targetDisk, 4)
	meta = readProvisionedFile(t, dataMount, "lib/cloud/seed/nocloud/meta-data")
	if !strings.Contains(meta, "system-ab-initial") {
		t.Fatalf("shared /var was not preserved across system-ab upgrade:\n%s", meta)
	}
	dataCleanup()
}

func createFlatcarLikeUSRATestDiskImage(t *testing.T, sizeMB int) string {
	t.Helper()
	rawDisk := createChrootCapableTestDiskImage(t, sizeMB)
	run(t, "label flatcar-like source partitions",
		"sgdisk",
		"--change-name=1:EFI-SYSTEM",
		"--change-name=2:USR-A",
		rawDisk,
	)
	return rawDisk
}

func assertProvisionSucceeded(t *testing.T, output []byte) {
	t.Helper()
	text := string(output)
	for _, bad := range []string{
		"provisioning step failed",
		"provisioning failed",
		"mode exited with error",
		"panic:",
	} {
		if strings.Contains(text, bad) {
			t.Fatalf("QEMU provisioning output contains failure marker %q. tail:\n%s", bad, tail(output, 4000))
		}
	}
	for _, good := range []string{
		"provisioning step\" component=provision step=report-success",
		"CAPRF run complete",
	} {
		if strings.Contains(text, good) {
			return
		}
	}
	t.Fatalf("QEMU provisioning output did not contain a success marker. tail:\n%s", tail(output, 4000))
}

func listInitramfsFiles(t *testing.T, path string) map[string]bool {
	t.Helper()

	gzipCmd := exec.Command("gzip", "-dc", path)
	cpioCmd := exec.Command("cpio", "-t")
	pipe, err := gzipCmd.StdoutPipe()
	if err != nil {
		t.Fatalf("gzip stdout pipe: %v", err)
	}
	cpioCmd.Stdin = pipe
	var out bytes.Buffer
	var errOut bytes.Buffer
	cpioCmd.Stdout = &out
	cpioCmd.Stderr = &errOut

	if err := cpioCmd.Start(); err != nil {
		t.Fatalf("start cpio: %v", err)
	}
	if err := gzipCmd.Start(); err != nil {
		t.Fatalf("start gzip: %v", err)
	}
	if err := gzipCmd.Wait(); err != nil {
		t.Fatalf("gzip initramfs: %v", err)
	}
	if err := cpioCmd.Wait(); err != nil {
		t.Fatalf("cpio initramfs: %v\n%s", err, errOut.String())
	}

	files := map[string]bool{}
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files[line] = true
		}
	}
	return files
}

func startABImageServer(t *testing.T, imagePath string) string {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/image.gz", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, imagePath)
	})

	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close() })

	return fmt.Sprintf("http://10.0.2.2:%d", listener.Addr().(*net.TCPAddr).Port)
}

func startABOCIRegistry(t *testing.T) (pushHost, guestHost string) {
	t.Helper()

	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen OCI registry: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: registry.New(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close() })

	return fmt.Sprintf("127.0.0.1:%d", port), fmt.Sprintf("10.0.2.2:%d", port)
}

func pushKVMSysextOCI(t *testing.T, registryHost, repoTag string, data []byte) {
	t.Helper()

	layer := stream.NewLayer(
		io.NopCloser(bytes.NewReader(data)),
		stream.WithMediaType(imageutil.SystemdSysextMediaType),
	)
	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		t.Fatalf("append sysext OCI layer: %v", err)
	}

	ref, err := name.ParseReference(registryHost + "/" + repoTag)
	if err != nil {
		t.Fatalf("parse sysext OCI ref: %v", err)
	}
	if err := remote.Write(ref, img); err != nil {
		t.Fatalf("push sysext OCI ref: %v", err)
	}
}
