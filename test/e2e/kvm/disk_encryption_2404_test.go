//go:build e2e

package kvm

import (
	"os"
	"path/filepath"
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

// luksSwitchScript replaces dracut with initramfs-tools and the stock
// cryptsetup-initramfs once the TPM2 enrollment is complete (unit disabled,
// enrollment key gone), and builds the initramfs with mkinitramfs. It runs
// once, in the installed system, so the cryptroot hook sees the real root.
const luksSwitchScript = `#!/bin/sh
k=$(uname -r)
if [ -e /` + luksSwitchDebDir + `/switched ]; then
	echo "INITRAMFS=initramfs-tools"
	exit 0
fi
echo "INITRAMFS=dracut"
# Diagnostics of the dracut unlock: what systemd-cryptsetup did in the
# initramfs and which TPM2 pieces the initramfs has.
journalctl -b -o cat -u 'systemd-cryptsetup@*' --no-pager 2>/dev/null | tail -n 20 | sed 's/^/CRYPTSETUP /'
lsinitrd "/boot/initrd.img-$k" 2>/dev/null | grep -E 'tss2|tpm|cryptsetup|crypttab' | awk '{print "INITRD " $NF}'
if [ -e /etc/booty/luks-enroll.key ] || systemctl is-enabled --quiet booty-luks-tpm2-enroll.service; then
	exit 0
fi
log=/` + luksSwitchDebDir + `/switch.log
if dpkg --remove --force-depends dracut dracut-core >"$log" 2>&1 &&
	dpkg -i /` + luksSwitchDebDir + `/initramfs-tools_*.deb >>"$log" 2>&1 &&
	mkinitramfs -o "/boot/initrd.img-$k.new" "$k" >>"$log" 2>&1 &&
	mv "/boot/initrd.img-$k.new" "/boot/initrd.img-$k"; then
	touch /` + luksSwitchDebDir + `/switched
	echo "SWITCH=ok"
else
	echo "SWITCH=failed"
fi
sed 's/^/SWITCH /' "$log"
d=$(mktemp -d)
unmkinitramfs "/boot/initrd.img-$k" "$d" >/dev/null 2>&1
echo "CRYPTROOT_CRYPTTAB=$(find "$d" -path '*cryptroot/crypttab' -exec cat {} + 2>/dev/null)"
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
	luksWriteImageFile(t, mnt, luksSwitchScriptPath[1:], []byte(luksSwitchScript), 0o755)
}

// TestDiskEncryptionUbuntu2404VM decides the Ubuntu 24.04 initramfs
// question on the stock cloud image (docs/disk-encryption.md#requirements):
//
//   - with dracut instead of initramfs-tools (DISK_ENCRYPTION_DEBS,
//     DISK_ENCRYPTION_REMOVE_PKGS) the volume binds to the TPM2 and the
//     TPM2 unlocks it without a prompt;
//   - after the switch back to initramfs-tools with the stock
//     cryptsetup-initramfs, the same volume with the same TPM2 token and
//     PCRs stops at the passphrase prompt: cryptsetup-initramfs cannot
//     unlock with the TPM2. The recovery key opens it there.
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
	}, map[string]string{luksCAPath: escrow.caPEM}))
	key := escrow.recoveryKey(t)

	// 2. Secure Boot on, phase 1 (dracut): bound to PCR 7+14.
	setLUKSSecureBoot(t, v.vars, true)
	out := runMirrorQEMU(t, "phase 1", v.args(), 10*time.Minute)
	logs = append(logs, out)
	report := luksCheckReport(t, "phase 1", out)
	for _, l := range []string{"SECUREBOOT=1", "ROOT_TYPE=crypt", "TPM2_TOKENS=1", "BOUND=yes", "INITRD_KEY=0", "INITRAMFS=dracut"} {
		luksRequireLine(t, report, l)
	}

	// 3. Phase 2 (dracut): the TPM2 unlocks without a prompt, slot 1 goes.
	// Then the check switches the system to initramfs-tools.
	out, prompted := runQEMUAnswerPrompt(t, "phase 2 dracut", v.args(), luksPrompt, key, 10*time.Minute)
	logs = append(logs, out)
	report = luksCheckReport(t, "phase 2 dracut", out)
	if prompted {
		t.Fatal("dracut: the TPM2 did not unlock, passphrase prompt with Secure Boot on")
	}
	for _, l := range []string{"SECUREBOOT=1", "ROOT_TYPE=crypt", "TPM2_TOKENS=1", "ENROLL_KEY=absent", "UNIT=disabled", "SWITCH=ok"} {
		luksRequireLine(t, report, l)
	}
	if luksField(report, "CRYPTROOT_CRYPTTAB") == "" {
		t.Fatal("the initramfs-tools initramfs has no cryptroot entry for the root")
	}
	pcr7, pcr14 := luksField(report, "PCR7"), luksField(report, "PCR14")

	// 4. Same Secure Boot state, TPM2 token and PCRs, initramfs-tools: the
	// expected failure, a passphrase prompt. The recovery key opens it.
	out, prompted = runQEMUAnswerPrompt(t, "initramfs-tools", v.args(), luksCryptrootPrompt, key, 10*time.Minute)
	logs = append(logs, out)
	if !prompted {
		t.Fatal("initramfs-tools: no passphrase prompt; cryptsetup-initramfs unlocked without it, update docs/disk-encryption.md")
	}
	report = luksCheckReport(t, "initramfs-tools", out)
	for _, l := range []string{"SECUREBOOT=1", "ROOT_TYPE=crypt", "TPM2_TOKENS=1", "INITRAMFS=initramfs-tools"} {
		luksRequireLine(t, report, l)
	}
	if luksField(report, "PCR7") != pcr7 || luksField(report, "PCR14") != pcr14 {
		t.Fatalf("PCRs changed with the initramfs: PCR7 %q -> %q, PCR14 %q -> %q", pcr7, luksField(report, "PCR7"), pcr14, luksField(report, "PCR14"))
	}
	requireNoRecoveryKey(t, logs, key)
}
