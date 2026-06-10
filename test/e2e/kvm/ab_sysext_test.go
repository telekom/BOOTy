//go:build e2e

package kvm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	sysextPath := filepath.Join(t.TempDir(), "node-tuning.raw")
	sysextPayload := []byte("node tuning sysext vm e2e\n")
	if err := os.WriteFile(sysextPath, sysextPayload, 0o600); err != nil {
		t.Fatalf("write sysext: %v", err)
	}
	sysextSum := sha256.Sum256(sysextPayload)
	baseURL := startABImageServer(t, gzImage, sysextPath)
	sysextURL := baseURL + "/node-tuning.raw"
	sysextLayers := fmt.Sprintf(
		`[{"name":"node-tuning","version":"vm-e2e","source":%q,"fileName":"node-tuning.raw","sha256":"sha256:%s","mode":"preload"}]`,
		sysextURL,
		hex.EncodeToString(sysextSum[:]),
	)

	targetDisk := filepath.Join(t.TempDir(), "ab-sysext-target.qcow2")
	run(t, "create target disk", "qemu-img", "create", "-f", "qcow2", targetDisk, "2G")

	initramfs := buildProvisionInitramfs(t, map[string]string{
		"HOSTNAME":                   "ab-sysext-vm",
		"dns_resolver":               "8.8.8.8",
		"MODE":                       "provision",
		"DISK_DEVICE":                "/dev/vda",
		"STATIC_IP":                  "10.0.2.15/24",
		"STATIC_GATEWAY":             "10.0.2.2",
		"STATIC_IFACE":               "eth0",
		"IMAGE":                      baseURL + "/image.gz",
		"IMAGE_MODE":                 "ab",
		"AB_SCHEME":                  "dual-root",
		"AB_TARGET_SLOT":             "inactive",
		"AB_BOOT_SIZE_MB":            "64",
		"AB_ROOT_SIZE_MB":            "768",
		"AB_STATE_SIZE_MB":           "64",
		"SYSEXT_ENABLED":             "true",
		"SYSEXT_ALLOW_INSECURE_HTTP": "true",
		"SYSEXT_DEFAULT_MODE":        "preload",
		"SYSEXT_LAYERS":              sysextLayers,
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

func startABImageServer(t *testing.T, imagePath, sysextPath string) string {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/image.gz", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, imagePath)
	})
	mux.HandleFunc("/node-tuning.raw", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, sysextPath)
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
