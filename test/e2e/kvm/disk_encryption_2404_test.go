//go:build e2e

package kvm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	// luksSwitchScriptPath is run by luksCheckScript when it exists.
	luksSwitchScriptPath = "/usr/local/lib/booty-e2e-initramfs-tools.sh"
	luksSwitchDebDir     = "var/lib/booty-e2e"
	// luksCryptrootPrompt is what cryptsetup-initramfs (initramfs-tools)
	// asks for the passphrase.
	luksCryptrootPrompt = "Please unlock disk"
)

// luksSwitchScript switches the initramfs generator of the installed
// system, once per state (` + luksSwitchDebDir + `/state):
//
//   - after phase 1 (bound, dracut): to initramfs-tools with the stock
//     cryptsetup-initramfs, built with mkinitramfs, so that the next boot
//     (phase 2) cannot use the TPM2;
//   - after that boot (initramfs-tools): back to dracut, so that the boot
//     after it is phase 2 with the TPM2.
//
// It runs in the installed system, so the cryptroot hook sees the real root.
const luksSwitchScript = `#!/bin/sh
k=$(uname -r)
d=/` + luksSwitchDebDir + `
state=$(cat "$d/state" 2>/dev/null)
log=$d/switch.log
swap() { mv "/boot/initrd.img-$k.new" "/boot/initrd.img-$k"; }
case $state in
"")
	echo "INITRAMFS=dracut"
	[ -e /etc/booty/luks-tpm2.bound ] || exit 0
	if dpkg --remove --force-depends dracut dracut-core >"$log" 2>&1 &&
		dpkg -i "$d"/initramfs-tools_*.deb >>"$log" 2>&1 &&
		mkinitramfs -o "/boot/initrd.img-$k.new" "$k" >>"$log" 2>&1 && swap; then
		echo initramfs-tools >"$d/state"
		echo "SWITCH=initramfs-tools"
	else
		echo "SWITCH=failed"
	fi
	sed 's/^/SWITCH /' "$log"
	t=$(mktemp -d)
	unmkinitramfs "/boot/initrd.img-$k" "$t" >/dev/null 2>&1
	echo "CRYPTROOT_CRYPTTAB=$(find "$t" -path '*cryptroot/crypttab' -exec cat {} + 2>/dev/null)"
	;;
initramfs-tools)
	echo "INITRAMFS=initramfs-tools"
	if dpkg --remove --force-depends initramfs-tools >"$log" 2>&1 &&
		env INITRD=No dpkg -i "$d"/dracut/*.deb >>"$log" 2>&1 &&
		dracut --force "/boot/initrd.img-$k.new" "$k" >>"$log" 2>&1 && swap; then
		echo dracut >"$d/state"
		echo "SWITCH=dracut"
	else
		echo "SWITCH=failed"
	fi
	sed 's/^/SWITCH /' "$log"
	;;
*)
	echo "INITRAMFS=dracut"
	;;
esac
# Diagnostics of the unlock: what systemd-cryptsetup did in the initramfs
# and which TPM2 pieces the dracut initramfs has.
journalctl -b -o cat -u 'systemd-cryptsetup@*' --no-pager 2>/dev/null | tail -n 20 | sed 's/^/CRYPTSETUP /'
lsinitrd "/boot/initrd.img-$k" 2>/dev/null | grep -E 'tss2|tpm|cryptsetup|crypttab' | awk '{print "INITRD " $NF}'
`

// stageInitramfsToolsSwitch puts luksSwitchScript and the initramfs-tools
// deb of DISK_ENCRYPTION_INITRAMFS_TOOLS_DEB into the image root.
func stageInitramfsToolsSwitch(t *testing.T, mnt string) {
	t.Helper()
	deb := os.Getenv("DISK_ENCRYPTION_INITRAMFS_TOOLS_DEB")
	data, err := os.ReadFile(deb)
	if deb == "" || err != nil {
		failOrSkipUnsupportedHost(t, "DISK_ENCRYPTION_INITRAMFS_TOOLS_DEB: %q %v", deb, err)
	}
	luksWriteImageFile(t, mnt, filepath.Join(luksSwitchDebDir, filepath.Base(deb)), data, 0o644)
	// The dracut debs of DISK_ENCRYPTION_DEBS, for the way back.
	for _, deb := range strings.Fields(os.Getenv("DISK_ENCRYPTION_DEBS")) {
		if !strings.HasPrefix(filepath.Base(deb), "dracut") {
			continue
		}
		data, err := os.ReadFile(deb)
		if err != nil {
			t.Fatal(err)
		}
		luksWriteImageFile(t, mnt, filepath.Join(luksSwitchDebDir, "dracut", filepath.Base(deb)), data, 0o644)
	}
	luksWriteImageFile(t, mnt, luksSwitchScriptPath[1:], []byte(luksSwitchScript), 0o755)
}

// TestDiskEncryptionUbuntu2404VM decides the Ubuntu 24.04 initramfs
// question on the stock cloud image (docs/disk-encryption.md#requirements)
// and checks that phase 2 keeps key slot 1 when the initramfs did not
// unlock with the TPM2 (#963):
//
//   - with dracut instead of initramfs-tools (DISK_ENCRYPTION_DEBS,
//     DISK_ENCRYPTION_REMOVE_PKGS) the volume binds to the TPM2;
//   - switched to initramfs-tools with the stock cryptsetup-initramfs, the
//     same volume with the same TPM2 token and PCRs stops at the
//     passphrase prompt: cryptsetup-initramfs cannot unlock with the TPM2.
//     The recovery key opens it, and phase 2 keeps slot 1 although the
//     TPM2 unseals from the booted system;
//   - back on dracut, the TPM2 unlocks without a prompt and phase 2
//     removes slot 1.
//
//nolint:funlen // one VM life cycle, phase by phase
func TestDiskEncryptionUbuntu2404VM(t *testing.T) {
	v := newLUKSVMWith(t, luksInitramfsToolsSwitch)
	escrow := startEscrowServer(t)
	logs := make([][]byte, 0, 4)

	// 1. Provision with Secure Boot off.
	logs = append(logs, v.provision(map[string]string{
		"HOSTNAME":                         "luks-e2e",
		"IMAGE_ALLOW_INSECURE_HTTP":        "true",
		"IMAGE":                            v.baseURL + "/image.gz",
		"MODE":                             "provision",
		"MIN_DISK_SIZE_GB":                 "1",
		"TOKEN":                            "e2e-token",
		"CA_BUNDLE_FILE":                   "/" + luksCAPath,
		"DISK_ENCRYPTION":                  "luks2-tpm2",
		"DISK_ENCRYPTION_RECOVERY_KEY_URL": escrow.url + "/recovery-key",
		"DISK_ENCRYPTION_STATUS_URL":       escrow.url + "/status",
		"DISK_ENCRYPTION_STATUS_TOKEN":     luksStatusToken,
	}, map[string]string{luksCAPath: escrow.caPEM}))
	key := escrow.recoveryKey(t)

	// 2. Secure Boot on, phase 1 (dracut): bound to PCR 7+14. Then the
	// check switches the system to initramfs-tools.
	setLUKSSecureBoot(t, v.vars, true)
	out := runMirrorQEMU(t, "phase 1", v.args(), 10*time.Minute)
	logs = append(logs, out)
	report := luksCheckReport(t, "phase 1", out)
	for _, l := range []string{"SECUREBOOT=1", "ROOT_TYPE=crypt", "TPM2_TOKENS=1", "BOUND=yes", "INITRD_KEY=0", "INITRAMFS=dracut", "SWITCH=initramfs-tools"} {
		luksRequireLine(t, report, l)
	}
	if luksField(report, "CRYPTROOT_CRYPTTAB") == "" {
		t.Fatal("the initramfs-tools initramfs has no cryptroot entry for the root")
	}
	escrow.requireStatus(t, "tpm2-bound")
	pcr7, pcr14 := luksField(report, "PCR7"), luksField(report, "PCR14")
	if pcr7 == "" || pcr14 == "" {
		t.Fatalf("no PCR 7 or 14 reading: %q %q", pcr7, pcr14)
	}

	// 3. Same Secure Boot state, TPM2 token and PCRs, initramfs-tools: the
	// expected failure, a passphrase prompt. The recovery key opens it,
	// and phase 2 keeps slot 1. Then the check switches back to dracut.
	out, prompted := runQEMUAnswerPrompt(t, "initramfs-tools", v.args(), luksCryptrootPrompt, key, 10*time.Minute)
	logs = append(logs, out)
	if !prompted {
		t.Fatal("initramfs-tools: no passphrase prompt; cryptsetup-initramfs unlocked without it, update docs/disk-encryption.md")
	}
	report = luksCheckReport(t, "initramfs-tools", out)
	for _, l := range []string{"SECUREBOOT=1", "ROOT_TYPE=crypt", "TPM2_TOKENS=1", "INITRAMFS=initramfs-tools", "INITRD_TPM2_UNLOCK=no",
		"ENROLL_KEY=present", "BOUND=yes", "UNIT=enabled", "SWITCH=dracut"} {
		luksRequireLine(t, report, l)
	}
	if !strings.HasPrefix(luksField(report, "KEYSLOTS"), "0,1,") {
		t.Fatalf("initramfs-tools: key slot 1 removed after a recovery key boot: key slots %q", luksField(report, "KEYSLOTS"))
	}
	escrow.requireStatus(t, "tpm2-initrd-unlock-failed")
	if luksField(report, "PCR7") != pcr7 || luksField(report, "PCR14") != pcr14 {
		t.Fatalf("PCRs changed with the initramfs: PCR7 %q -> %q, PCR14 %q -> %q", pcr7, luksField(report, "PCR7"), pcr14, luksField(report, "PCR14"))
	}

	// 4. Phase 2 (dracut): the TPM2 unlocks without a prompt, slot 1 goes.
	out, prompted = runQEMUAnswerPrompt(t, "phase 2 dracut", v.args(), luksPrompt, key, 10*time.Minute)
	logs = append(logs, out)
	report = luksCheckReport(t, "phase 2 dracut", out)
	if prompted {
		t.Fatal("dracut: the TPM2 did not unlock, passphrase prompt with Secure Boot on")
	}
	for _, l := range []string{"SECUREBOOT=1", "ROOT_TYPE=crypt", "TPM2_TOKENS=1", "INITRAMFS=dracut", "INITRD_TPM2_UNLOCK=yes",
		"ENROLL_KEY=absent", "BOUND=no", "UNIT=disabled"} {
		luksRequireLine(t, report, l)
	}
	if slots := luksField(report, "KEYSLOTS"); !strings.HasPrefix(slots, "0,") || strings.HasPrefix(slots, "0,1,") {
		t.Fatalf("key slots %q: want the recovery slot 0 and the TPM2 slot, not slot 1", slots)
	}
	escrow.requireStatus(t, "slot1-removed")
	requireNoRecoveryKey(t, logs, key)
}
