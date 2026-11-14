//go:build linux

package provision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
)

func TestNewRecoveryKeyFormat(t *testing.T) {
	a, err := newRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newRecoveryKey()
	if !regexp.MustCompile(`^[cbdefghijklnrtuv]{8}(-[cbdefghijklnrtuv]{8}){7}$`).Match(a) {
		t.Fatalf("recovery key %q is not 8 groups of 8 modhex characters", a)
	}
	if bytes.Equal(a, b) {
		t.Fatal("two recovery keys are equal")
	}
}

func TestRecoveryKeyBody(t *testing.T) {
	st := &encryptionState{device: "/dev/vda1", luksUUID: "u-1"}
	body, err := recoveryKeyBody(`host"1`, st, []byte("cccc-dddd"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body %s: %v", body, err)
	}
	want := map[string]any{"hostname": `host"1`, "device": "/dev/vda1", "luksUUID": "u-1", "keySlot": float64(0), "recoveryKey": "cccc-dddd"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

func encryptionTargetRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range append(luksTargetTools, "usr/lib/x86_64-linux-gnu/libtss2-esys.so.0",
		"usr/lib/x86_64-linux-gnu/libtss2-rc.so.0", "usr/lib/x86_64-linux-gnu/libtss2-mu.so.0",
		"usr/lib/x86_64-linux-gnu/libtss2-tcti-device.so.0",
		"usr/lib/x86_64-linux-gnu/cryptsetup/libcryptsetup-token-systemd-tpm2.so") {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestCheckEncryptionTarget(t *testing.T) {
	root := encryptionTargetRoot(t)
	if err := checkEncryptionTarget(root, false); err != nil {
		t.Fatalf("complete image: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "usr/lib/x86_64-linux-gnu/libtss2-rc.so.0")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "usr/bin/dracut")); err != nil {
		t.Fatal(err)
	}
	err := checkEncryptionTarget(root, false)
	if err == nil || !strings.Contains(err.Error(), "libtss2-rc.so.0") || !strings.Contains(err.Error(), "/usr/bin/dracut") {
		t.Fatalf("missing libtss2-rc and dracut: %v", err)
	}
}

func TestWriteEncryptionFiles(t *testing.T) {
	root := encryptionTargetRoot(t)
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc/crypttab"), []byte("# old\ndata UUID=x none luks\nbooty_crypt UUID=old none luks\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &encryptionState{luksUUID: "11111111-2222-4333-8444-555555555555", enrollKey: []byte("enroll-key")}
	extras := &encryptionExtras{libs: luksTargetLibs(root), hostname: "h1", statusURL: "https://p/status", token: "tok", ca: []byte("CA"), grubHash: "grub.pbkdf2.sha512.10000.AA.BB"}
	if err := writeEncryptionFiles(root, st, extras); err != nil {
		t.Fatal(err)
	}
	read := func(rel string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	crypttab := read("etc/crypttab")
	wantLine := "booty_crypt UUID=11111111-2222-4333-8444-555555555555 /etc/booty/luks-enroll.key luks,x-initrd.attach\n"
	if crypttab != "# old\ndata UUID=x none luks\n"+wantLine {
		t.Fatalf("crypttab:\n%s", crypttab)
	}
	if fi, err := os.Stat(filepath.Join(root, luksEnrollKeyPath)); err != nil || fi.Mode().Perm() != 0o400 {
		t.Fatalf("enrollment key mode: %v %v", fi, err)
	}
	if read(luksEnrollKeyPath) != "enroll-key" {
		t.Fatal("enrollment key content")
	}
	dracut := read(luksDracutConf)
	for _, want := range []string{"hostonly=\"no\"", "crypt systemd-cryptsetup", "/etc/crypttab", "/usr/lib/x86_64-linux-gnu/libtss2-rc.so.0", "/usr/lib/x86_64-linux-gnu/libtss2-tcti-device.so.0",
		"/usr/lib/x86_64-linux-gnu/cryptsetup/libcryptsetup-token-systemd-tpm2.so"} {
		if !strings.Contains(dracut, want) {
			t.Errorf("dracut conf lacks %q:\n%s", want, dracut)
		}
	}
	if !strings.Contains(read(luksDracutKeyConf), luksEnrollKeyPath) {
		t.Error("enrollment key dracut conf does not include the key")
	}
	if g := read(luksGrubConf); !strings.Contains(g, "GRUB_FORCE_PARTUUID=\n") || !strings.Contains(g, "GRUB_DISABLE_RECOVERY=true\n") {
		t.Errorf("GRUB defaults:\n%s", g)
	}
	if fi, err := os.Stat(filepath.Join(root, luksGrubLock)); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("GRUB lock script mode: %v %v", fi, err)
	}
	if auth := read(luksStatusAuth); auth != "Authorization: Bearer tok\n" {
		t.Errorf("status auth %q", auth)
	}
	if fi, _ := os.Stat(filepath.Join(root, luksStatusAuth)); fi.Mode().Perm() != 0o600 {
		t.Errorf("status auth mode %v", fi.Mode())
	}
	if read(luksStatusCA) != "CA" {
		t.Error("status CA not written")
	}
	if !strings.Contains(read("/etc/systemd/system/"+luksUnitName), "Wants=network-online.target") {
		t.Error("unit with a status URL does not wait for the network")
	}
	if env := read(luksEnvPath); !strings.Contains(env, "TPM2_PCRS='7+14'\n") || !strings.Contains(env, "LUKS_ENROLL_SLOT='1'\n") ||
		!strings.Contains(env, "STATUS_URL='https://p/status'\n") || !strings.Contains(env, "STATUS_CA='"+luksStatusCA+"'\n") {
		t.Errorf("env:\n%s", env)
	}
	if fi, err := os.Stat(filepath.Join(root, luksScriptPath)); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("script mode: %v %v", fi, err)
	}
	link, err := os.Readlink(filepath.Join(root, "etc/systemd/system/multi-user.target.wants", luksUnitName))
	if err != nil || link != "/etc/systemd/system/"+luksUnitName {
		t.Fatalf("unit not enabled: %q %v", link, err)
	}
	// A second run replaces the entry instead of adding one.
	if err := writeEncryptionFiles(root, st, &encryptionExtras{}); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(read("etc/crypttab"), "booty_crypt"); n != 1 {
		t.Fatalf("crypttab has %d booty_crypt lines", n)
	}
}

func TestGrubLockScript(t *testing.T) {
	h1, err := unusableGrubHash()
	if err != nil {
		t.Fatal(err)
	}
	h2, _ := unusableGrubHash()
	if h1 == h2 || !regexp.MustCompile(`^grub\.pbkdf2\.sha512\.10000\.[0-9A-F]{128}\.[0-9A-F]{128}$`).MatchString(h1) {
		t.Fatalf("GRUB hash %q", h1)
	}
	got := luksGrubLockScript(h1)
	for _, want := range []string{
		"#!/bin/sh\n", "cat <<'EOF'\n", "set superusers=\"booty-locked\"\n",
		"password_pbkdf2 booty-locked " + h1 + "\n",
		// Every grub-mkconfig entry carries $menuentry_id_option: they stay bootable.
		"set menuentry_id_option=\"--unrestricted $menuentry_id_option\"\n", "EOF\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("GRUB lock lacks %q:\n%s", want, got)
		}
	}
}

func TestEnrollUnitAndScript(t *testing.T) {
	unit := luksEnrollUnitText(false)
	for _, want := range []string{"ConditionSecurity=uefi-secureboot", "ConditionPathExists=" + luksEnrollKeyPath} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit lacks %q", want)
		}
	}
	if strings.Contains(unit, "network-online") {
		t.Error("unit without a status URL waits for the network")
	}
	// Phase 2 (bound) wipes slot 1 only after checking the booted initramfs
	// and the TPM2; phase 1 never wipes it.
	p2 := luksEnrollScript[strings.Index(luksEnrollScript, `if [ -e "$bound" ]`):strings.Index(luksEnrollScript, "step=start")]
	p1 := luksEnrollScript[strings.Index(luksEnrollScript, "step=start"):]
	inOrder(t, p2, `lsinitrd "/boot/initrd.img-$(uname -r)"`, "*luks-enroll.key*", "--token-only", "luksKillSlot", "shred -u", "systemctl disable", "status slot1-removed")
	inOrder(t, p1, "trap restore EXIT", "--tpm2-pcrs=", "--token-only", "none luks,x-initrd.attach,tpm2-device=auto", "lsinitrd", `mv "$img.booty-new"`, `: >"$bound"`, "trap - EXIT", "status tpm2-bound")
	if strings.Contains(p1, "luksKillSlot") || strings.Contains(p1, "shred") {
		t.Error("phase 1 destroys the enrollment key")
	}
	if !strings.Contains(luksEnrollScript, `[ "$(efivar SetupMode)" != 0 ]`) {
		t.Error("the script does not check SetupMode")
	}
	if strings.Contains(luksEnrollScript, "crypttab.enroll") {
		t.Error("the script copies crypttab instead of rebuilding the line")
	}
}

// A MOK change waiting for MokManager changes PCR 14 on a later boot, so
// phase 1 must not bind before it is applied.
func TestEnrollScriptWaitsForPendingMOKChange(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	efivars := filepath.Join(dir, "efivars")
	env := filepath.Join(dir, "env")
	if err := os.MkdirAll(efivars, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env, []byte("LUKS_UUID=u\nSTATUS_URL=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]byte{"SecureBoot": 1, "SetupMode": 0} {
		if err := os.WriteFile(filepath.Join(efivars, name+"-"+efiGlobalGUID), []byte{6, 0, 0, 0, v}, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	head := luksEnrollScript[:strings.Index(luksEnrollScript, "step=start")] + "echo phase1\n"
	head = strings.ReplaceAll(head, "/sys/firmware/efi/efivars", efivars)
	head = strings.ReplaceAll(head, luksEnvPath, env)
	head = strings.ReplaceAll(head, "/etc/booty/luks-tpm2.bound", filepath.Join(dir, "bound"))
	run := func() string {
		out, err := exec.Command("sh", "-c", head).CombinedOutput()
		if err != nil {
			t.Fatalf("script: %v: %s", err, out)
		}
		return string(out)
	}
	if out := run(); !strings.Contains(out, "phase1") {
		t.Fatalf("no pending MOK change: phase 1 does not run: %s", out)
	}
	if err := os.WriteFile(filepath.Join(efivars, "MokNew-"+shimLockGUID), []byte{7, 0, 0, 0, 1}, 0o600); err != nil {
		t.Fatal(err)
	}
	if out := run(); strings.Contains(out, "phase1") || !strings.Contains(out, "MokNew") {
		t.Fatalf("pending MokNew: phase 1 runs: %s", out)
	}
}

func inOrder(t *testing.T, s string, parts ...string) {
	t.Helper()
	last := -1
	for _, p := range parts {
		i := strings.Index(s, p)
		if i < 0 || i < last {
			t.Fatalf("%q missing or out of order", p)
		}
		last = i
	}
}

func TestSecureBootUserMode(t *testing.T) {
	dir := t.TempDir()
	if err := secureBootUserMode(dir); err == nil {
		t.Fatal("no SecureBoot variable accepted")
	}
	write := func(name string, v byte) {
		if err := os.WriteFile(filepath.Join(dir, name+"-"+efiGlobalGUID), []byte{7, 0, 0, 0, v}, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("SecureBoot", 0)
	write("SetupMode", 1)
	if err := secureBootUserMode(dir); err == nil || !strings.Contains(err.Error(), "setup mode") {
		t.Fatalf("setup mode: %v", err)
	}
	write("SetupMode", 0)
	if err := secureBootUserMode(dir); err != nil {
		t.Fatalf("user mode with Secure Boot off: %v", err)
	}
}

func TestEncryptionStepsOnlyWhenEnabled(t *testing.T) {
	names := func(cfg *config.MachineConfig) []string {
		steps := newTestOrchestrator(t, cfg, &mockProvider{}).provisionSteps()
		n := make([]string, 0, len(steps))
		for _, s := range steps {
			n = append(n, s.Name)
		}
		return n
	}
	off := names(&config.MachineConfig{})
	for _, n := range off {
		if strings.Contains(n, "encrypt") {
			t.Fatalf("step %s without DISK_ENCRYPTION", n)
		}
	}
	cfg := &config.MachineConfig{}
	cfg.Provision.DiskEncryption.Mode = config.DiskEncryptionLUKS2TPM2
	on := names(cfg)
	if len(on) != len(off)+2 {
		t.Fatalf("%d steps with encryption, %d without", len(on), len(off))
	}
	joined := strings.Join(on, ",")
	for _, pair := range []string{"prepare-disk-encryption,configure-grub", "teardown-chroot,close-encrypted-disk,report-success"} {
		if !strings.Contains(joined, pair) {
			t.Errorf("steps lack %s", pair)
		}
	}
}

type escrowProvider struct {
	mockProvider
	bodies [][]byte
	err    error
}

func (p *escrowProvider) EscrowRecoveryKey(_ context.Context, body []byte) error {
	p.bodies = append(p.bodies, append([]byte(nil), body...))
	return p.err
}

func (p *escrowProvider) CurrentToken() string { return "tok" }

type cryptsetupCall struct {
	args  []string
	stdin []byte
}

func stubCryptsetup(t *testing.T, fail string) *[]cryptsetupCall {
	t.Helper()
	var calls []cryptsetupCall
	old, oldWait, oldSize := runCryptsetup, waitForBlockDevice, blockDeviceSize
	runCryptsetup = func(_ context.Context, stdin []byte, _ *os.File, args ...string) error {
		calls = append(calls, cryptsetupCall{args: args, stdin: append([]byte(nil), stdin...)})
		if args[0] == "isLuks" || args[0] == fail {
			return errors.New("exit status 1")
		}
		return nil
	}
	waitForBlockDevice = func(context.Context, string) error { return nil }
	blockDeviceSize = func(string) (int64, error) { return 1 << 30, nil }
	t.Cleanup(func() { runCryptsetup, waitForBlockDevice, blockDeviceSize = old, oldWait, oldSize })
	return &calls
}

func formatTestOrchestrator(t *testing.T, provider config.Provider) (*Orchestrator, *mockCommander, *encryptionState) {
	t.Helper()
	o, cmd := newTestOrchestratorWithCommander(t, &config.MachineConfig{Hostname: "h1"}, &mockProvider{})
	o.provider = provider
	o.targetDisk = "/dev/vda"
	return o, cmd, &encryptionState{luksUUID: "u-1", enrollKey: []byte("enroll"), isRoot: true}
}

func TestFormatEncryptedPartition(t *testing.T) {
	calls := stubCryptsetup(t, "")
	p := &escrowProvider{}
	o, cmd, st := formatTestOrchestrator(t, p)
	mapped, err := o.formatEncryptedPartition(context.Background(), st, 1, 1<<29)
	if err != nil {
		t.Fatal(err)
	}
	if mapped != "/dev/mapper/booty_crypt" || st.device != "/dev/vda1" {
		t.Fatalf("mapped %q device %q", mapped, st.device)
	}
	if len(cmd.calls) < 2 || cmd.calls[0].name != "partprobe" || cmd.calls[1].name != "growpart" {
		t.Fatalf("no partprobe and growpart before the format: %+v", cmd.calls)
	}
	if len(p.bodies) != 1 {
		t.Fatalf("escrow calls = %d", len(p.bodies))
	}
	var body struct{ RecoveryKey string }
	if err := json.Unmarshal(p.bodies[0], &body); err != nil || body.RecoveryKey == "" {
		t.Fatalf("escrow body %s: %v", p.bodies[0], err)
	}
	ops := make([]string, 0, len(*calls))
	for _, c := range *calls {
		ops = append(ops, c.args[0])
		if strings.Contains(strings.Join(c.args, " "), body.RecoveryKey) {
			t.Fatalf("recovery key in cryptsetup arguments: %v", c.args)
		}
		if c.args[0] != "isLuks" && string(c.stdin) != body.RecoveryKey {
			t.Errorf("cryptsetup %s stdin is not the escrowed recovery key", c.args[0])
		}
	}
	if strings.Join(ops, ",") != "isLuks,luksFormat,luksAddKey,open" {
		t.Fatalf("cryptsetup calls %v", ops)
	}
	format := strings.Join((*calls)[1].args, " ")
	for _, want := range []string{"--type luks2", "--uuid u-1", "--key-slot 0", "--key-file -", "/dev/vda1"} {
		if !strings.Contains(format, want) {
			t.Errorf("luksFormat %q lacks %q", format, want)
		}
	}
	if add := strings.Join((*calls)[2].args, " "); !strings.Contains(add, "--key-slot 1") || !strings.HasSuffix(add, "/dev/vda1 /proc/self/fd/3") {
		t.Errorf("luksAddKey %q", add)
	}
	if open := strings.Join((*calls)[3].args, " "); !strings.HasSuffix(open, "/dev/vda1 booty_crypt") {
		t.Errorf("open %q", open)
	}
}

func TestFormatEncryptedPartitionEscrowFailureFormatsNothing(t *testing.T) {
	calls := stubCryptsetup(t, "")
	o, _, st := formatTestOrchestrator(t, &escrowProvider{err: errors.New("503")})
	if _, err := o.formatEncryptedPartition(context.Background(), st, 1, 1<<29); err == nil || !strings.Contains(err.Error(), "recovery key") {
		t.Fatalf("err = %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("cryptsetup ran after a failed escrow: %v", *calls)
	}
}

func TestFormatEncryptedPartitionTooSmall(t *testing.T) {
	stubCryptsetup(t, "")
	blockDeviceSize = func(string) (int64, error) { return 100, nil }
	o, _, st := formatTestOrchestrator(t, &escrowProvider{})
	if _, err := o.formatEncryptedPartition(context.Background(), st, 1, 1<<29); err == nil || !strings.Contains(err.Error(), "too small") {
		t.Fatalf("err = %v", err)
	}
}

func TestUseEncryptedRoot(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.DiskEncryption.Mode = config.DiskEncryptionLUKS2TPM2
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	if err := o.useEncryptedRoot(); err == nil || !strings.Contains(err.Error(), "resume") {
		t.Fatalf("resume without keys: %v", err)
	}
	o.encryptionSkipped = true
	if err := o.useEncryptedRoot(); err != nil {
		t.Fatalf("allow-failure run: %v", err)
	}
	o.encryption = &encryptionState{device: "/dev/vda1", isRoot: true}
	o.rootPartition = "/dev/vda2"
	if err := o.useEncryptedRoot(); err == nil {
		t.Fatal("root on another partition accepted")
	}
	o.rootPartition = "/dev/vda1"
	if err := o.useEncryptedRoot(); err != nil || o.rootPartition != "/dev/mapper/booty_crypt" {
		t.Fatalf("root %q, %v", o.rootPartition, err)
	}
	off := newTestOrchestrator(t, &config.MachineConfig{}, &mockProvider{})
	off.rootPartition = "/dev/vda1"
	if err := off.useEncryptedRoot(); err != nil || off.rootPartition != "/dev/vda1" {
		t.Fatalf("encryption off changed the root: %q %v", off.rootPartition, err)
	}
}

func TestEncryptionPreflightFailsWithoutEscrow(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.DiskEncryption = config.DiskEncryptionConfig{Mode: config.DiskEncryptionLUKS2TPM2, Target: "x"}
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	if _, err := o.encryptionPreflight(); err == nil {
		t.Fatal("bad target accepted")
	}
}

func TestRewriteFstabSources(t *testing.T) {
	in := "# /dev/vda3 comment\nLABEL=cloudimg-rootfs / ext4 defaults 0 1\n/dev/vda3 /data ext4 defaults 0 2\nPARTUUID=abc /srv ext4 defaults 0 2\nUUID=fs /x ext4 defaults 0 2\n"
	enc := map[string]bool{"/dev/vda3": true, "PARTUUID=abc": true, "UUID=fs": true}
	out, changed := rewriteFstabSources(in, func(s string) bool { return enc[s] })
	want := "# /dev/vda3 comment\nLABEL=cloudimg-rootfs / ext4 defaults 0 1\n/dev/mapper/booty_crypt /data ext4 defaults 0 2\n" +
		"/dev/mapper/booty_crypt /srv ext4 defaults 0 2\nUUID=fs /x ext4 defaults 0 2\n"
	if !changed || out != want {
		t.Fatalf("fstab:\n%s", out)
	}
}

func TestEncryptionPreflightNeedsTPM2(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.DiskEncryption = config.DiskEncryptionConfig{Mode: config.DiskEncryptionLUKS2TPM2}
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	o.provider = &escrowProvider{}
	dir := t.TempDir()
	old := sysTPMDir
	sysTPMDir = dir
	t.Cleanup(func() { sysTPMDir = old })
	if err := os.WriteFile(filepath.Join(dir, "tpm_version_major"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := o.encryptionPreflight(); !errors.Is(err, errNoTPM2) {
		t.Fatalf("TPM 1.2: err = %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "tpm_version_major")); err != nil {
		t.Fatal(err)
	}
	if _, err := o.encryptionPreflight(); !errors.Is(err, errNoTPM2) {
		t.Fatalf("no TPM: err = %v", err)
	}
}

func TestUseEncryptedRootByNumber(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.DiskEncryption = config.DiskEncryptionConfig{Mode: config.DiskEncryptionLUKS2TPM2, Target: "1"}
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	o.encryption = &encryptionState{device: "/dev/vda1"}
	o.rootPartition = "/dev/vda1"
	if err := o.useEncryptedRoot(); err != nil || !o.encryption.isRoot || o.rootPartition != "/dev/mapper/booty_crypt" {
		t.Fatalf("numbered root: root %q isRoot %v err %v", o.rootPartition, o.encryption.isRoot, err)
	}
}

func TestEnrollScriptKeepsOriginalImagesUntilCommit(t *testing.T) {
	p1 := luksEnrollScript[strings.Index(luksEnrollScript, "step=start"):]
	inOrder(t, p1, "restore() {", `ln -f "$img" "$img.booty-old"`, `mv "$img.booty-new" "$img"`, "trap - EXIT", "rm -f /boot/initrd.img-*.booty-old")
	restore := p1[strings.Index(p1, "restore() {"):strings.Index(p1, "trap restore EXIT")]
	if !strings.Contains(restore, "restore_images") || !strings.Contains(luksEnrollScript, `if [ "$old" -ef "$img" ]; then rm -f "$old"; else mv -f "$old" "$img"; fi`) {
		t.Fatal("restore does not bring the original images back")
	}
}

func TestCloseEncryptedDisk(t *testing.T) {
	calls := stubCryptsetup(t, "")
	o, cmd := newTestOrchestratorWithCommander(t, &config.MachineConfig{}, &mockProvider{})
	o.encryption = &encryptionState{device: "/dev/vda3"}
	if err := o.closeEncryptedDisk(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(cmd.calls))
	for _, c := range cmd.calls {
		got = append(got, c.name+" "+strings.Join(c.args, " "))
	}
	joined := strings.Join(got, "|")
	// The data file system grows offline through the mapping, never on the
	// LUKS partition.
	if !strings.Contains(joined, "e2fsck -p -f /dev/mapper/booty_crypt") || !strings.Contains(joined, "resize2fs /dev/mapper/booty_crypt") || strings.Contains(joined, "/dev/vda3") {
		t.Fatalf("data partition commands: %v", got)
	}
	if len(*calls) != 1 || strings.Join((*calls)[0].args, " ") != "close booty_crypt" {
		t.Fatalf("cryptsetup calls %v", *calls)
	}

	// A failed check keeps the mapping open and fails the step.
	calls = stubCryptsetup(t, "")
	o, cmd = newTestOrchestratorWithCommander(t, &config.MachineConfig{}, &mockProvider{})
	cmd.setResult("e2fsck -p", nil, &exec.ExitError{})
	o.encryption = &encryptionState{device: "/dev/vda3"}
	if err := o.closeEncryptedDisk(context.Background()); err == nil || len(*calls) != 0 {
		t.Fatalf("failed e2fsck: err=%v cryptsetup=%v", err, *calls)
	}

	// The root grows online in resize-filesystem; close only closes.
	calls = stubCryptsetup(t, "")
	o, cmd = newTestOrchestratorWithCommander(t, &config.MachineConfig{}, &mockProvider{})
	o.encryption = &encryptionState{device: "/dev/vda1", isRoot: true}
	if err := o.closeEncryptedDisk(context.Background()); err != nil || len(cmd.calls) != 0 || len(*calls) != 1 {
		t.Fatalf("root: err=%v cmds=%v cryptsetup=%v", err, cmd.calls, *calls)
	}
}
