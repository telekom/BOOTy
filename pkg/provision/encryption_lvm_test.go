//go:build linux

package provision

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
)

func stubRootFilesystem(t *testing.T, roots ...string) {
	t.Helper()
	old := isRootFilesystem
	isRootFilesystem = func(_ context.Context, _ *Orchestrator, device string) (bool, error) {
		for _, r := range roots {
			if r == device {
				return true, nil
			}
		}
		return false, nil
	}
	t.Cleanup(func() { isRootFilesystem = old })
}

// lvmCalls records the lvm commands of a test; fail makes that subcommand
// fail.
type lvmCalls struct {
	lines []string
	fail  string
}

func stubLVM(t *testing.T, vg string) *lvmCalls {
	t.Helper()
	calls := &lvmCalls{}
	old := runLVM
	runLVM = func(_ context.Context, args ...string) (string, error) {
		calls.lines = append(calls.lines, "lvm "+strings.Join(args, " "))
		switch args[0] {
		case calls.fail:
			return "", errors.New("lvm " + args[0] + ": in use: exit status 5")
		case "pvs":
			return vg, nil
		case "lvs":
			return "/dev/mapper/tcaas-swap\n  /dev/mapper/tcaas-root", nil
		}
		return "", nil
	}
	t.Cleanup(func() { runLVM = old })
	return calls
}

func lvmTestOrchestrator(t *testing.T, vg string) (*Orchestrator, *lvmCalls) {
	t.Helper()
	calls := stubLVM(t, vg)
	o, _ := newTestOrchestratorWithCommander(t, &config.MachineConfig{}, &mockProvider{})
	return o, calls
}

func commandLines(cmd *mockCommander) []string {
	got := make([]string, 0, len(cmd.calls))
	for _, c := range cmd.calls {
		got = append(got, c.name+" "+strings.Join(c.args, " "))
	}
	return got
}

func TestOpenEncryptedVG(t *testing.T) {
	stubRootFilesystem(t, "/dev/mapper/tcaas-root")
	o, cmd := lvmTestOrchestrator(t, "tcaas")
	st := &encryptionState{device: "/dev/vda1", lvm: true, isRoot: true}
	if err := o.openEncryptedVG(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if st.rootLV != "/dev/mapper/tcaas-root" || !st.isRoot {
		t.Fatalf("root LV %q isRoot %v", st.rootLV, st.isRoot)
	}
	want := []string{
		"lvm pvs --devices /dev/mapper/booty_crypt --noheadings -o vg_name /dev/mapper/booty_crypt",
		"lvm pvresize --devices /dev/mapper/booty_crypt /dev/mapper/booty_crypt",
		"lvm vgchange --devices /dev/mapper/booty_crypt -ay tcaas",
		"lvm vgmknodes --devices /dev/mapper/booty_crypt tcaas",
		"lvm lvs --devices /dev/mapper/booty_crypt --noheadings -o lv_dm_path tcaas",
	}
	if got := cmd.lines; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("commands:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestOpenEncryptedVGRefusesOtherVG(t *testing.T) {
	stubRootFilesystem(t, "/dev/mapper/tcaas-root")
	o, cmd := lvmTestOrchestrator(t, "other")
	err := o.openEncryptedVG(context.Background(), &encryptionState{device: "/dev/vda1", lvm: true, isRoot: true})
	if err == nil || !strings.Contains(err.Error(), `VG "other"`) {
		t.Fatalf("err = %v", err)
	}
	if len(cmd.lines) != 1 {
		t.Fatalf("LVM changed after the VG check failed: %v", cmd.lines)
	}
}

func TestOpenEncryptedVGWithoutRoot(t *testing.T) {
	stubRootFilesystem(t)
	o, _ := lvmTestOrchestrator(t, "tcaas")
	if err := o.openEncryptedVG(context.Background(), &encryptionState{device: "/dev/vda1", lvm: true, isRoot: true}); err == nil {
		t.Fatal("DISK_ENCRYPTION_TARGET=root accepted a VG without a root")
	}
}

// A numbered target is a data VG even when an LV looks like a root (a
// backup OS): the selected plain root stays the root.
func TestOpenEncryptedVGNumberedIsData(t *testing.T) {
	stubRootFilesystem(t, "/dev/mapper/tcaas-root")
	o, _ := lvmTestOrchestrator(t, "tcaas")
	st := &encryptionState{device: "/dev/vda3", lvm: true}
	if err := o.openEncryptedVG(context.Background(), st); err != nil || st.isRoot || st.rootLV != "" {
		t.Fatalf("data VG: isRoot %v rootLV %q err %v", st.isRoot, st.rootLV, err)
	}
}

func TestOpenEncryptedVGRootSelectorConflicts(t *testing.T) {
	stubRootFilesystem(t, "/dev/mapper/tcaas-root")
	o, _ := lvmTestOrchestrator(t, "tcaas")
	o.cfg.Provision.Disk.RootPartitionNumber = 2
	st := &encryptionState{device: "/dev/vda1", lvm: true, isRoot: true}
	if err := o.openEncryptedVG(context.Background(), st); err == nil || st.rootLV != "" {
		t.Fatalf("root selector ignored: rootLV %q err %v", st.rootLV, err)
	}
}

func TestOpenEncryptedVGProbeCanceled(t *testing.T) {
	o, _ := lvmTestOrchestrator(t, "tcaas")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := o.openEncryptedVG(ctx, &encryptionState{device: "/dev/vda1", lvm: true, isRoot: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the cancellation", err)
	}
}

// Two disks with a VG tcaas: enable-lvm must not activate the other one.
func TestEnableLVMSkippedForEncryptedVG(t *testing.T) {
	o, cmd := newTestOrchestratorWithCommander(t, &config.MachineConfig{}, &mockProvider{})
	o.encryption = &encryptionState{device: "/dev/vda1", lvm: true}
	if err := o.enableLVM(context.Background()); err != nil || len(cmd.calls) != 0 {
		t.Fatalf("enable-lvm with an encrypted VG: err %v commands %v", err, commandLines(cmd))
	}
	o.encryption = &encryptionState{device: "/dev/vda1"}
	if err := o.enableLVM(context.Background()); err != nil || len(cmd.calls) != 1 {
		t.Fatalf("enable-lvm without LVM encryption: err %v commands %v", err, commandLines(cmd))
	}
}

func TestOpenEncryptedVGProbeError(t *testing.T) {
	old := isRootFilesystem
	isRootFilesystem = func(context.Context, *Orchestrator, string) (bool, error) { return false, errors.New("busy") }
	t.Cleanup(func() { isRootFilesystem = old })
	o, _ := lvmTestOrchestrator(t, "tcaas")
	if err := o.openEncryptedVG(context.Background(), &encryptionState{lvm: true, isRoot: true}); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("err = %v", err)
	}
}

// Without LVM the dracut and GRUB files are those of #939, byte for byte.
func TestEncryptionFilesWithoutLVMUnchanged(t *testing.T) {
	libs := []string{"/usr/lib/a.so", "/usr/lib/b.so"}
	if got := luksDracutConfText(libs, false); got != "# Written by BOOTy: unlock the LUKS2 volume in the initramfs (docs/disk-encryption.md).\n"+
		"hostonly=\"no\"\nadd_dracutmodules+=\" crypt systemd-cryptsetup \"\ninstall_items+=\" /etc/crypttab /usr/lib/a.so /usr/lib/b.so \"\n" {
		t.Fatalf("dracut conf:\n%s", got)
	}
	if got := luksGrubDefaultsText(&encryptionState{luksUUID: "u"}); got != "# Written by BOOTy (docs/disk-encryption.md).\nGRUB_FORCE_PARTUUID=\nGRUB_DISABLE_RECOVERY=true\n" {
		t.Fatalf("GRUB defaults:\n%s", got)
	}
}

func TestEncryptionFilesLVM(t *testing.T) {
	root := encryptionTargetRoot(t)
	st := &encryptionState{luksUUID: "11111111-2222-4333-8444-555555555555", enrollKey: []byte("k"), lvm: true, isRoot: true}
	if err := writeEncryptionFiles(root, st, &encryptionExtras{libs: luksTargetLibs(root), grubHash: "h"}); err != nil {
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
	if d := read(luksDracutConf); !strings.Contains(d, "add_dracutmodules+=\" crypt systemd-cryptsetup lvm \"\n") {
		t.Errorf("dracut conf without the lvm module:\n%s", d)
	}
	if g := read(luksGrubConf); !strings.HasSuffix(g, "GRUB_CMDLINE_LINUX=\"$GRUB_CMDLINE_LINUX rd.luks.uuid=11111111-2222-4333-8444-555555555555 rd.lvm.vg=tcaas\"\n") {
		t.Errorf("GRUB defaults:\n%s", g)
	}
	// The volume, its key and the PCR binding are those of a partition.
	if c := read("etc/crypttab"); c != "booty_crypt UUID=11111111-2222-4333-8444-555555555555 /etc/booty/luks-enroll.key luks,x-initrd.attach\n" {
		t.Errorf("crypttab:\n%s", c)
	}
	if !strings.Contains(read(luksEnvPath), "TPM2_PCRS='7+14'\n") || read(luksScriptPath) != luksEnrollScript {
		t.Error("the enrollment differs with LVM")
	}
	if err := checkEncryptionTarget(root, false, true); err == nil || !strings.Contains(err.Error(), "/usr/sbin/lvm") {
		t.Fatalf("image without lvm: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "usr/sbin/lvm"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkEncryptionTarget(root, false, true); err != nil {
		t.Fatalf("image with lvm: %v", err)
	}
}

// close-encrypted-disk deactivates VG tcaas on the mapping before it closes
// the mapping, and never runs a file system tool on the physical volume.
func TestCloseEncryptedDiskLVM(t *testing.T) {
	calls := stubCryptsetup(t, "")
	lvm := stubLVM(t, "tcaas")
	o, cmd := newTestOrchestratorWithCommander(t, &config.MachineConfig{}, &mockProvider{})
	o.encryption = &encryptionState{device: "/dev/vda1", lvm: true, isRoot: true, rootLV: "/dev/mapper/tcaas-root"}
	if err := o.closeEncryptedDisk(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(lvm.lines) != 1 || lvm.lines[0] != "lvm vgchange --devices /dev/mapper/booty_crypt -an tcaas" || len(cmd.calls) != 0 {
		t.Fatalf("commands %v %v", lvm.lines, commandLines(cmd))
	}
	if len(*calls) != 1 || strings.Join((*calls)[0].args, " ") != "close booty_crypt" {
		t.Fatalf("cryptsetup calls %v", *calls)
	}

	calls = stubCryptsetup(t, "")
	lvm.fail = "vgchange"
	o, _ = newTestOrchestratorWithCommander(t, &config.MachineConfig{}, &mockProvider{})
	o.encryption = &encryptionState{device: "/dev/vda1", lvm: true}
	if err := o.closeEncryptedDisk(context.Background()); err == nil || len(*calls) != 0 {
		t.Fatalf("failed deactivation: err=%v cryptsetup=%v", err, *calls)
	}
}
