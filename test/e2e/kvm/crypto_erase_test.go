//go:build e2e

package kvm

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// luksSnapshot is what the host reads from the encrypted partition after
// provisioning.
type luksSnapshot struct {
	start    int64  // partition start in bytes
	metadata int64  // one header copy (hdr_size)
	area     int64  // both header copies and the keyslots area
	backup   string // cryptsetup luksHeaderBackup
}

// TestDiskEncryptionCryptoEraseVM provisions with DISK_ENCRYPTION, then
// deprovisions with WIPE_DEPTH=quick, which leaves the partition contents
// in place: the crypto-erase alone must remove the LUKS2 header and the
// keyslots, so the escrowed recovery key opens nothing, not even with the
// old metadata written back.
func TestDiskEncryptionCryptoEraseVM(t *testing.T) {
	v := newLUKSVM(t)
	escrow := startEscrowServer(t)
	v.provision(map[string]string{
		"HOSTNAME":                         "luks-e2e",
		"IMAGE_ALLOW_INSECURE_HTTP":        "true",
		"IMAGE":                            v.baseURL + "/image.gz",
		"MODE":                             "provision",
		"MIN_DISK_SIZE_GB":                 "1",
		"TOKEN":                            "e2e-token",
		"CA_BUNDLE_FILE":                   "/" + luksCAPath,
		"DISK_ENCRYPTION":                  "luks2-tpm2",
		"DISK_ENCRYPTION_RECOVERY_KEY_URL": escrow.url + "/recovery-key",
	}, map[string]string{luksCAPath: escrow.caPEM})
	key := escrow.recoveryKey(t)
	var body struct {
		LUKSUUID string `json:"luksUUID"`
	}
	if err := json.Unmarshal(escrow.keys[0], &body); err != nil {
		t.Fatal(err)
	}
	snap := snapshotLUKS(t, v.disk, key)

	caprf := startFakeCAPRF(t, "")
	vars := caprf.nativeVars("deprovision")
	vars["SUCCESS_ACTION"] = "poweroff"
	vars["WIPE_DEPTH"] = "quick" // keeps the partition contents: only the crypto-erase touches them
	out := runMirrorQEMU(t, "deprovision", v.args("-kernel", v.kernel, "-initrd", buildProvisionInitramfsWithFiles(t, vars, nil),
		"-append", runnerKernelCmdline+" net.ifnames=0"), 10*time.Minute)
	caprf.requireStatuses(t, "init", "success")
	if msg := caprf.messages["success"]; !strings.Contains(msg, "crypto-erased: /dev/vda") || !strings.Contains(msg, "luksUUID="+body.LUKSUUID) {
		t.Fatalf("success status %q does not name the erased device and luksUUID %s", msg, body.LUKSUUID)
	}
	if !bytes.Contains(out, []byte("LUKS2 keyslots and header destroyed")) {
		t.Fatal("BOOTy did not log the crypto-erase")
	}

	raw := filepath.Join(t.TempDir(), "erased.raw")
	run(t, "convert the deprovisioned disk to raw", "qemu-img", "convert", "-O", "raw", v.disk, raw)
	region := readRegion(t, raw, snap.start, snap.area+1<<20)
	if !bytes.Equal(region[:snap.area], make([]byte, snap.area)) {
		t.Fatal("the header copies and the keyslots area are not zeros")
	}
	if bytes.Equal(region[snap.area:], make([]byte, 1<<20)) {
		t.Fatal("the data area is zeros too: WIPE_DEPTH=quick should have kept it, so this scenario proves nothing")
	}
	erased := writeRegionFile(t, region, nil)
	for _, args := range [][]string{{"isLuks", erased}, {"luksDump", erased}} {
		if out, err := exec.CommandContext(t.Context(), "cryptsetup", args...).CombinedOutput(); err == nil {
			t.Fatalf("cryptsetup %s still finds a header: %s", args[0], out)
		}
	}

	backup, err := os.ReadFile(snap.backup)
	if err != nil {
		t.Fatal(err)
	}
	// Control: the full header backup (with the keyslots) written back opens
	// with the recovery key, so the check below can tell.
	if !recoveryKeyOpens(t, writeRegionFile(t, region, backup), key) {
		t.Fatal("control failed: the recovery key does not open the full header backup")
	}
	// Only the metadata (both header copies, no keyslots area) written back:
	// the header is valid again, but the keyslot material is gone.
	withMetadata := writeRegionFile(t, region, backup[:2*snap.metadata])
	run(t, "the old metadata is a LUKS2 header", "cryptsetup", "isLuks", withMetadata)
	if recoveryKeyOpens(t, withMetadata, key) {
		t.Fatal("the recovery key still opens the old metadata: the keyslots survived the crypto-erase")
	}
}

// snapshotLUKS reads the start of the encrypted partition, its header
// sizes and a header backup, and checks that the recovery key opens it.
func snapshotLUKS(t *testing.T, qcow2 string, key []byte) luksSnapshot {
	t.Helper()
	_, loop := attachDisk(t, qcow2)
	part := luksPartition(t, loop)
	sectors, err := os.ReadFile(filepath.Join("/sys/class/block", filepath.Base(part), "start"))
	if err != nil {
		t.Fatal(err)
	}
	s := luksSnapshot{backup: filepath.Join(t.TempDir(), "header.img")}
	if s.start, err = strconv.ParseInt(strings.TrimSpace(string(sectors)), 10, 64); err != nil {
		t.Fatal(err)
	}
	s.start *= 512
	dump := string(runOutput(t, "luksDump", "cryptsetup", "luksDump", part))
	field := func(name string) int64 {
		m := regexp.MustCompile(name + `:\s+(\d+) \[bytes\]`).FindStringSubmatch(dump)
		if m == nil {
			t.Fatalf("luksDump has no %s:\n%s", name, dump)
		}
		n, _ := strconv.ParseInt(m[1], 10, 64)
		return n
	}
	s.metadata = field("Metadata area")
	s.area = 2*s.metadata + field("Keyslots area")
	run(t, "header backup", "cryptsetup", "luksHeaderBackup", part, "--header-backup-file", s.backup)
	if !recoveryKeyOpens(t, part, key) {
		t.Fatal("the escrowed recovery key does not open the provisioned partition")
	}
	return s
}

func readRegion(t *testing.T, path string, off, n int64) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b := make([]byte, n)
	if _, err := f.ReadAt(b, off); err != nil {
		t.Fatal(err)
	}
	return b
}

// writeRegionFile writes region with head over its start to a new file.
func writeRegionFile(t *testing.T, region, head []byte) string {
	t.Helper()
	b := bytes.Clone(region)
	copy(b, head)
	path := filepath.Join(t.TempDir(), "region.img")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func recoveryKeyOpens(t *testing.T, device string, key []byte) bool {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "cryptsetup", "open", "--test-passphrase", "--key-file", "-", device)
	cmd.Stdin = bytes.NewReader(key)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("cryptsetup open --test-passphrase %s: %v %s", device, err, out)
	}
	return err == nil
}
