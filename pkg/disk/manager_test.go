//go:build linux

package disk

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// mockCommander records calls and returns preset results.
type mockCommander struct {
	calls   []mockCall
	results map[string]mockResult
}

type mockCall struct {
	name string
	args []string
}

type mockResult struct {
	output []byte
	err    error
}

func newMockCommander() *mockCommander {
	return &mockCommander{results: make(map[string]mockResult)}
}

func (m *mockCommander) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	m.calls = append(m.calls, mockCall{name: name, args: args})
	key := name
	if len(args) > 0 {
		key = name + " " + args[0]
	}
	if r, ok := m.results[key]; ok {
		return r.output, r.err
	}
	// Default: success with empty output.
	return nil, nil
}

func (m *mockCommander) setResult(key string, output []byte, err error) {
	m.results[key] = mockResult{output: output, err: err}
}

func TestNewManagerDefault(t *testing.T) {
	mgr := NewManager(nil)
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
	// Should use ExecCommander by default.
	if _, ok := mgr.cmd.(*ExecCommander); !ok {
		t.Fatal("expected ExecCommander as default")
	}
}

func TestNewManagerCustomCommander(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)
	if mgr.cmd != cmd {
		t.Fatal("expected custom commander")
	}
}

func TestStopRAIDArrays(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	// Should succeed even if mdadm fails (no arrays).
	cmd.setResult("mdadm --stop", nil, fmt.Errorf("exec mdadm: exit 1"))
	if err := mgr.StopRAIDArrays(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmd.calls) != 1 || cmd.calls[0].name != "mdadm" {
		t.Fatalf("expected mdadm call, got %v", cmd.calls)
	}
}

func TestParsePartitions(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	sfdisk := sfdiskOutput{}
	sfdisk.PartitionTable.Partitions = []Partition{
		{Node: "/dev/sda1", Type: EFISystemPartitionGUID},
		{Node: "/dev/sda2", Type: LinuxFilesystemGUID},
	}
	data, _ := json.Marshal(sfdisk)
	cmd.setResult("sfdisk --json", data, nil)

	parts, err := mgr.ParsePartitions(context.Background(), "/dev/sda")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("expected 2 partitions, got %d", len(parts))
	}
	if parts[0].Node != "/dev/sda1" {
		t.Errorf("expected /dev/sda1, got %s", parts[0].Node)
	}
}

func TestParsePartitionsError(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	cmd.setResult("sfdisk --json", nil, fmt.Errorf("exec sfdisk: exit 1"))
	_, err := mgr.ParsePartitions(context.Background(), "/dev/sda")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParsePartitionsNoPartitionTable(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	// sfdisk reports "does not contain a recognized partition table" for empty disks.
	// ParsePartitions should return nil (empty list), not an error.
	cmd.setResult("sfdisk --json",
		[]byte("sfdisk: /dev/loop0: does not contain a recognized partition table"),
		fmt.Errorf("exec sfdisk: exit status 1"),
	)
	parts, err := mgr.ParsePartitions(context.Background(), "/dev/loop0")
	if err != nil {
		t.Fatalf("expected nil error for empty partition table, got: %v", err)
	}
	if len(parts) != 0 {
		t.Errorf("expected 0 partitions, got %d", len(parts))
	}
}

func TestParsePartitionsInvalidJSON(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	cmd.setResult("sfdisk --json", []byte("not json"), nil)
	_, err := mgr.ParsePartitions(context.Background(), "/dev/sda")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParsePartitionsWithGPTWarningPrefix(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	sfdisk := sfdiskOutput{}
	sfdisk.PartitionTable.Partitions = []Partition{
		{Node: "/dev/sda1", Type: EFISystemPartitionGUID},
	}
	data, _ := json.Marshal(sfdisk)
	// Simulate sfdisk stderr warning merged before JSON via CombinedOutput.
	prefixed := append([]byte("GPT PMBR size mismatch (7340031 != 488397167) will be corrected by write.\n"), data...)
	cmd.setResult("sfdisk --json", prefixed, nil)

	parts, err := mgr.ParsePartitions(context.Background(), "/dev/sda")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(parts) != 1 || parts[0].Node != "/dev/sda1" {
		t.Errorf("expected 1 partition /dev/sda1, got %v", parts)
	}
}

func TestFindBootPartition(t *testing.T) {
	mgr := NewManager(newMockCommander())
	parts := []Partition{
		{Node: "/dev/sda1", Type: EFISystemPartitionGUID},
		{Node: "/dev/sda2", Type: LinuxFilesystemGUID},
	}
	boot, err := mgr.FindBootPartition(parts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if boot.Node != "/dev/sda1" {
		t.Errorf("expected /dev/sda1, got %s", boot.Node)
	}
}

func TestFindBootPartitionNotFound(t *testing.T) {
	mgr := NewManager(newMockCommander())
	parts := []Partition{
		{Node: "/dev/sda1", Type: LinuxFilesystemGUID},
	}
	_, err := mgr.FindBootPartition(parts)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFindRootPartition(t *testing.T) {
	mgr := NewManager(newMockCommander())
	parts := []Partition{
		{Node: "/dev/sda1", Type: EFISystemPartitionGUID},
		{Node: "/dev/sda2", Type: LinuxFilesystemGUID},
	}
	root, err := mgr.FindRootPartition(parts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if root.Node != "/dev/sda2" {
		t.Errorf("expected /dev/sda2, got %s", root.Node)
	}
}

func TestFindRootPartitionNotFound(t *testing.T) {
	mgr := NewManager(newMockCommander())
	parts := []Partition{
		{Node: "/dev/sda1", Type: EFISystemPartitionGUID},
	}
	_, err := mgr.FindRootPartition(parts)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGrowPartitionSuccess(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	if err := mgr.GrowPartition(context.Background(), "/dev/sda", 2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmd.calls) != 1 || cmd.calls[0].name != "growpart" {
		t.Fatalf("expected growpart call, got %v", cmd.calls)
	}
}

func TestGrowPartitionNoChange(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	cmd.setResult("growpart /dev/sda", []byte("NOCHANGE: partition already fills disk"), fmt.Errorf("exit 1"))
	if err := mgr.GrowPartition(context.Background(), "/dev/sda", 2); err != nil {
		t.Fatalf("unexpected error for NOCHANGE: %v", err)
	}
}

func TestChrootRunSuccess(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	cmd.setResult("chroot /newroot", []byte("hello"), nil)
	out, err := mgr.ChrootRun(context.Background(), "/newroot", "echo hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "hello" {
		t.Errorf("expected 'hello', got %q", string(out))
	}
}

func TestChrootRunCommandError(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	cmd.setResult("chroot /newroot", []byte("error output"), fmt.Errorf("exec chroot: exit 1"))
	_, err := mgr.ChrootRun(context.Background(), "/newroot", "false")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "chroot exec") {
		t.Errorf("expected 'chroot exec' in error, got: %v", err)
	}
}

func TestChrootRunFallbackOnNotFound(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	// Simulate chroot binary not found.
	cmd.setResult("chroot /nonexistent", nil, fmt.Errorf("exec chroot: %w", exec.ErrNotFound))

	// The fallback will try to exec /bin/bash with SysProcAttr.Chroot.
	// In test context this will fail because /nonexistent doesn't exist,
	// but we verify the fallback path is triggered (not the "chroot exec" error).
	_, err := mgr.ChrootRun(context.Background(), "/nonexistent", "echo hi")
	if err == nil {
		t.Fatal("expected error (nonexistent root)")
	}
	// Should be a syscall fallback error, not "chroot exec" error.
	if strings.Contains(err.Error(), "chroot exec") {
		t.Error("expected syscall fallback error, got chroot exec error")
	}
}

func TestIsExecNotFound(t *testing.T) {
	if !isExecNotFound(exec.ErrNotFound) {
		t.Error("expected true for exec.ErrNotFound")
	}
	if !isExecNotFound(fmt.Errorf("exec chroot: %w", exec.ErrNotFound)) {
		t.Error("expected true for wrapped exec.ErrNotFound")
	}
	if !isExecNotFound(fmt.Errorf("executable file not found in $PATH")) {
		t.Error("expected true for message-based detection")
	}
	if isExecNotFound(fmt.Errorf("some other error")) {
		t.Error("expected false for unrelated error")
	}
}

func TestIsBashNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"bash missing in chroot", fmt.Errorf("exec chroot: exit status 127 [output: chroot: can't execute '/bin/bash': No such file or directory]"), true},
		{"exit 127 without no such file", fmt.Errorf("exit status 127"), false},
		{"no such file without 127", fmt.Errorf("No such file or directory"), false},
		{"normal error", fmt.Errorf("exec chroot: exit status 1"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBashNotFound(tt.err); got != tt.want {
				t.Errorf("isBashNotFound() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestChrootRunBashNotFoundFallsBackToSh(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	// Simulate bash not found inside chroot (exit status 127).
	bashErr := fmt.Errorf("exec chroot: exit status 127 [output: chroot: can't execute '/bin/bash': No such file or directory]")
	cmd.setResult("chroot /newroot", nil, bashErr)

	// The fallback /bin/sh call uses the same mock key, so it also errors.
	// We verify that isBashNotFound triggers and /bin/sh is attempted.
	_, _ = mgr.ChrootRun(context.Background(), "/newroot", "ls /dev/mst/")

	// Verify both /bin/bash and /bin/sh were attempted.
	var bashCall, shCall bool
	for _, c := range cmd.calls {
		if c.name == "chroot" && len(c.args) >= 2 {
			switch c.args[1] {
			case "/bin/bash":
				bashCall = true
			case "/bin/sh":
				shCall = true
			}
		}
	}
	if !bashCall {
		t.Error("expected /bin/bash attempt")
	}
	if !shCall {
		t.Error("expected /bin/sh fallback attempt after bash not found")
	}
}

func TestGrowPartitionError(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	cmd.setResult("growpart /dev/sda", []byte("error"), fmt.Errorf("exit 1"))
	if err := mgr.GrowPartition(context.Background(), "/dev/sda", 2); err == nil {
		t.Fatal("expected error")
	}
}

func TestResizeFilesystemExt4(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	if err := mgr.ResizeFilesystem(context.Background(), "/dev/sda2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmd.calls) != 1 || cmd.calls[0].name != "resize2fs" {
		t.Fatalf("expected resize2fs call, got %v", cmd.calls)
	}
}

func TestResizeFilesystemXFS(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	cmd.setResult("resize2fs /dev/sda2", nil, fmt.Errorf("not ext4"))
	if err := mgr.ResizeFilesystem(context.Background(), "/dev/sda2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmd.calls) != 2 {
		t.Fatalf("expected 2 calls (resize2fs, xfs_growfs), got %d", len(cmd.calls))
	}
	if cmd.calls[1].name != "xfs_growfs" {
		t.Errorf("expected xfs_growfs, got %s", cmd.calls[1].name)
	}
}

func TestResizeFilesystemBothFail(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	cmd.setResult("resize2fs /dev/sda2", nil, fmt.Errorf("not ext4"))
	cmd.setResult("xfs_growfs /dev/sda2", nil, fmt.Errorf("not xfs"))
	cmd.setResult("btrfs filesystem", nil, fmt.Errorf("not btrfs"))
	if err := mgr.ResizeFilesystem(context.Background(), "/dev/sda2"); err == nil {
		t.Fatal("expected error when all resize methods fail")
	}
}

func TestPartProbe(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	if err := mgr.PartProbe(context.Background(), "/dev/sda"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPartProbeError(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	// Both partprobe and blockdev fallback fail → error expected.
	cmd.setResult("partprobe /dev/sda", nil, fmt.Errorf("exit 1"))
	cmd.setResult("blockdev --rereadpt", nil, fmt.Errorf("exit 1"))
	if err := mgr.PartProbe(context.Background(), "/dev/sda"); err == nil {
		t.Fatal("expected error")
	}
}

func TestPartProbeFallback(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	// partprobe fails but blockdev --rereadpt succeeds → no error.
	cmd.setResult("partprobe /dev/sda", nil, fmt.Errorf("exit 1"))
	if err := mgr.PartProbe(context.Background(), "/dev/sda"); err != nil {
		t.Fatalf("expected fallback to succeed: %v", err)
	}
}

func TestCheckFilesystem(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	// e2fsck always returns nil even on error (by design).
	cmd.setResult("e2fsck -fy", nil, fmt.Errorf("exit 1"))
	if err := mgr.CheckFilesystem(context.Background(), "/dev/sda2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnableLVM(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	if err := mgr.EnableLVM(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmd.calls) == 0 || cmd.calls[0].name != "lvm" {
		t.Fatal("expected lvm call")
	}
}

func TestEnableLVMError(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	cmd.setResult("lvm vgchange", nil, fmt.Errorf("no vg found"))
	if err := mgr.EnableLVM(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestChrootRun(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	cmd.setResult("chroot /newroot", []byte("output"), nil)
	out, err := mgr.ChrootRun(context.Background(), "/newroot", "echo hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "output" {
		t.Errorf("expected 'output', got %q", string(out))
	}
}

func TestIsVirtualDisk(t *testing.T) {
	tests := []struct {
		name   string
		expect bool
	}{
		{"loop0", true},
		{"sr0", true},
		{"ram0", true},
		{"dm-0", true},
		{"zram0", true},
		{"md0", true},
		{"zd0", true},
		{"nbd0", true},
		{"sda", false},
		{"nvme0n1", false},
		{"vda", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isVirtualDisk(tt.name); got != tt.expect {
				t.Errorf("isVirtualDisk(%q) = %v, want %v", tt.name, got, tt.expect)
			}
		})
	}
}

func TestFindPartitionsCaseInsensitive(t *testing.T) {
	mgr := NewManager(newMockCommander())
	// Test that GUID matching is case-insensitive.
	parts := []Partition{
		{Node: "/dev/sda1", Type: "c12a7328-f81f-11d2-ba4b-00a0c93ec93b"}, // lowercase
		{Node: "/dev/sda2", Type: "0fc63daf-8483-4772-8e79-3d69d8477de4"}, // lowercase
	}
	boot, err := mgr.FindBootPartition(parts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if boot.Node != "/dev/sda1" {
		t.Errorf("expected /dev/sda1, got %s", boot.Node)
	}
	root, err := mgr.FindRootPartition(parts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if root.Node != "/dev/sda2" {
		t.Errorf("expected /dev/sda2, got %s", root.Node)
	}
}

func TestDisableLVM(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	// DisableLVM should succeed even if lvm fails (no LVM present).
	cmd.setResult("lvm vgchange", nil, fmt.Errorf("exec lvm: exit 5"))
	if err := mgr.DisableLVM(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmd.calls) != 1 || cmd.calls[0].name != "lvm" {
		t.Fatalf("expected lvm call, got %v", cmd.calls)
	}
	if cmd.calls[0].args[0] != "vgchange" || cmd.calls[0].args[1] != "-an" {
		t.Errorf("expected vgchange -an, got %v", cmd.calls[0].args)
	}
}

func TestDisableLVMSuccess(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	if err := mgr.DisableLVM(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateRAIDArray(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	err := mgr.CreateRAIDArray(context.Background(), "md0", 1, []string{"/dev/sda", "/dev/sdb"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmd.calls) != 1 || cmd.calls[0].name != "mdadm" {
		t.Fatalf("expected mdadm call, got %v", cmd.calls)
	}
}

func TestCreateRAIDArrayTooFewDevices(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	err := mgr.CreateRAIDArray(context.Background(), "md0", 1, []string{"/dev/sda"})
	if err == nil {
		t.Fatal("expected error for single device RAID")
	}
}

func TestCreateRAIDArrayError(t *testing.T) {
	cmd := newMockCommander()
	mgr := NewManager(cmd)

	cmd.setResult("mdadm --create", nil, fmt.Errorf("mdadm: exit 1"))
	err := mgr.CreateRAIDArray(context.Background(), "md0", 1, []string{"/dev/sda", "/dev/sdb"})
	if err == nil {
		t.Fatal("expected error when mdadm fails")
	}
}
