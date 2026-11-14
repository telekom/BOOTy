//go:build linux

package provision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
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
	o := newTestOrchestrator(t, &config.MachineConfig{}, &mockProvider{})
	if err := o.checkEncryptionTarget(root); err != nil {
		t.Fatalf("complete image: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "usr/lib/x86_64-linux-gnu/libtss2-rc.so.0")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "usr/bin/dracut")); err != nil {
		t.Fatal(err)
	}
	err := o.checkEncryptionTarget(root)
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
	if err := writeEncryptionFiles(root, st, luksTargetLibs(root)); err != nil {
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
	if !strings.Contains(read(luksGrubConf), "GRUB_FORCE_PARTUUID=\n") {
		t.Error("GRUB override does not clear GRUB_FORCE_PARTUUID")
	}
	if env := read(luksEnvPath); !strings.Contains(env, "TPM2_PCRS=7+14\n") || !strings.Contains(env, "LUKS_ENROLL_SLOT=1\n") {
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
	if err := writeEncryptionFiles(root, st, nil); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(read("etc/crypttab"), "booty_crypt"); n != 1 {
		t.Fatalf("crypttab has %d booty_crypt lines", n)
	}
}

func TestEnrollUnitAndScript(t *testing.T) {
	for _, want := range []string{"ConditionSecurity=uefi-secureboot", "ConditionPathExists=" + luksEnrollKeyPath} {
		if !strings.Contains(luksEnrollUnit(luksEnrollKeyPath), want) {
			t.Errorf("unit lacks %q", want)
		}
	}
	// The key slot is wiped only after the TPM2 test, the initramfs checks
	// and the switch to the new initramfs.
	order := []string{"--tpm2-pcrs=", "--token-only", "none luks,x-initrd.attach,tpm2-device=auto", "lsinitrd", "mv \"$img.booty-new\"", "luksKillSlot", "shred -u", "systemctl disable"}
	last := -1
	for _, s := range order {
		i := strings.Index(luksEnrollScript, s)
		if i < 0 || i < last {
			t.Fatalf("%q missing or out of order in the enrollment script", s)
		}
		last = i
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

type cryptsetupCall struct {
	args  []string
	stdin []byte
}

func stubCryptsetup(t *testing.T, fail string) *[]cryptsetupCall {
	t.Helper()
	var calls []cryptsetupCall
	old := runCryptsetup
	runCryptsetup = func(_ context.Context, stdin []byte, _ *os.File, args ...string) error {
		calls = append(calls, cryptsetupCall{args: args, stdin: append([]byte(nil), stdin...)})
		if args[0] == "isLuks" || args[0] == fail {
			return errors.New("exit status 1")
		}
		return nil
	}
	t.Cleanup(func() { runCryptsetup = old })
	return &calls
}

func encryptTestOrchestrator(t *testing.T, provider config.Provider) (*Orchestrator, *mockCommander) {
	t.Helper()
	o, cmd := newTestOrchestratorWithCommander(t, &config.MachineConfig{Hostname: "h1"}, &mockProvider{})
	o.provider = provider
	sys := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sys, "vda1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sys, "vda1", "size"), []byte("2097152\n"), 0o644); err != nil { // 1 GiB
		t.Fatal(err)
	}
	old := sysBlockRoot
	sysBlockRoot = sys
	t.Cleanup(func() { sysBlockRoot = old })
	o.encryption = &encryptionState{device: "/dev/vda1", luksUUID: "u-1", enrollKey: []byte("enroll")}
	return o, cmd
}

func TestEncryptDisk(t *testing.T) {
	calls := stubCryptsetup(t, "")
	p := &escrowProvider{}
	o, cmd := encryptTestOrchestrator(t, p)
	enrollKey := o.encryption.enrollKey
	if err := o.encryptDisk(context.Background()); err != nil {
		t.Fatal(err)
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
	if strings.Join(ops, ",") != "isLuks,reencrypt,luksAddKey,open" {
		t.Fatalf("cryptsetup calls %v", ops)
	}
	reencrypt := strings.Join((*calls)[1].args, " ")
	for _, want := range []string{"--encrypt", "--type luks2", "--reduce-device-size 32M", "--uuid u-1", "--key-slot 0", "/dev/vda1"} {
		if !strings.Contains(reencrypt, want) {
			t.Errorf("reencrypt %q lacks %q", reencrypt, want)
		}
	}
	if add := strings.Join((*calls)[2].args, " "); !strings.Contains(add, "--key-slot 1") || !strings.HasSuffix(add, "/dev/vda1 /proc/self/fd/3") {
		t.Errorf("luksAddKey %q", add)
	}
	// 1 GiB - 32 MiB = 1015808 KiB, after e2fsck -p -f.
	var fsck, resize bool
	for _, c := range cmd.calls {
		fsck = fsck || c.name == "e2fsck"
		if c.name == "resize2fs" {
			resize = fsck && strings.Join(c.args, " ") == "/dev/vda1 1015808K"
		}
	}
	if !resize {
		t.Fatalf("no e2fsck then resize2fs to 1015808K: %+v", cmd.calls)
	}
	if !bytes.Equal(enrollKey, make([]byte, len(enrollKey))) {
		t.Fatal("enrollment key not zeroed")
	}
}

func TestEncryptDiskEscrowFailureChangesNothing(t *testing.T) {
	calls := stubCryptsetup(t, "")
	o, cmd := encryptTestOrchestrator(t, &escrowProvider{err: errors.New("503")})
	if err := o.encryptDisk(context.Background()); err == nil || !strings.Contains(err.Error(), "recovery key") {
		t.Fatalf("err = %v", err)
	}
	if len(*calls) != 1 || len(cmd.calls) != 0 {
		t.Fatalf("disk changed after a failed escrow: %v %+v", *calls, cmd.calls)
	}
}

func TestEncryptDiskWithoutEscrowProvider(t *testing.T) {
	stubCryptsetup(t, "")
	o, _ := encryptTestOrchestrator(t, &mockProvider{})
	if err := o.encryptDisk(context.Background()); err == nil {
		t.Fatal("encrypted without a way to hand over the recovery key")
	}
}

func TestEncryptDiskFailsOnReencryptError(t *testing.T) {
	stubCryptsetup(t, "reencrypt")
	o, _ := encryptTestOrchestrator(t, &escrowProvider{})
	if err := o.encryptDisk(context.Background()); err == nil {
		t.Fatal("reencrypt error ignored")
	}
}

func TestEncryptDiskSkippedWithoutPreparation(t *testing.T) {
	calls := stubCryptsetup(t, "")
	o, _ := encryptTestOrchestrator(t, &escrowProvider{})
	o.encryption = nil
	if err := o.encryptDisk(context.Background()); err != nil || len(*calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, *calls)
	}
}

func TestPrepareDiskEncryptionAllowFailure(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.DiskEncryption = config.DiskEncryptionConfig{Mode: config.DiskEncryptionLUKS2TPM2, RecoveryKeyURL: "https://x", Target: "x"}
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	if err := o.prepareDiskEncryption(context.Background()); err == nil {
		t.Fatal("failed preflight did not fail the step")
	}
	cfg.Provision.DiskEncryption.AllowFailure = true
	if err := o.prepareDiskEncryption(context.Background()); err != nil || o.encryption != nil {
		t.Fatalf("allow failure: err=%v state=%v", err, o.encryption)
	}
}
