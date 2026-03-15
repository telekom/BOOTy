//go:build linux

package provision

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
	"github.com/telekom/BOOTy/pkg/rescue"
)

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
	return nil, nil
}

func (m *mockCommander) setResult(key string, output []byte, err error) {
	m.results[key] = mockResult{output: output, err: err}
}

type mockProvider struct {
	statuses []statusReport
	configs  []*config.MachineConfig
}

type statusReport struct {
	status  config.Status
	message string
}

func (p *mockProvider) GetConfig(_ context.Context) (*config.MachineConfig, error) {
	if len(p.configs) > 0 {
		return p.configs[0], nil
	}
	return &config.MachineConfig{}, nil
}

func (p *mockProvider) ReportStatus(_ context.Context, status config.Status, message string) error {
	p.statuses = append(p.statuses, statusReport{status: status, message: message})
	return nil
}

func (p *mockProvider) ShipLog(_ context.Context, _ string) error { return nil }
func (p *mockProvider) Heartbeat(_ context.Context) error         { return nil }
func (p *mockProvider) ReportFirmware(_ context.Context, _ []byte) error {
	return nil
}
func (p *mockProvider) FetchCommands(_ context.Context) ([]config.Command, error) {
	return nil, nil
}

func (p *mockProvider) AcknowledgeCommand(_ context.Context, _, _, _ string) error { return nil }

func (p *mockProvider) ReportInventory(_ context.Context, _ []byte) error { return nil }

func newTestConfigurator(t *testing.T, cmd *mockCommander) *Configurator {
	t.Helper()
	root := t.TempDir()
	mgr := disk.NewManager(cmd)
	c := NewConfigurator(mgr)
	c.rootDir = root
	return c
}

func TestSetHostname(t *testing.T) {
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)
	cfg := &config.MachineConfig{Hostname: "test-node"}
	if err := c.SetHostname(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(c.rootDir, "etc", "hostname"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "test-node\n" {
		t.Errorf("hostname = %q, want %q", string(data), "test-node\n")
	}
}

func TestSetHostnameEmpty(t *testing.T) {
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)
	cfg := &config.MachineConfig{Hostname: ""}
	if err := c.SetHostname(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigureKubeletProviderID(t *testing.T) {
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)
	cfg := &config.MachineConfig{ProviderID: "redfish://host/sys/1"}
	if err := c.ConfigureKubelet(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	confPath := filepath.Join(c.rootDir, "etc", "kubernetes", "kubelet.conf.d", "10-caprf-provider-id.conf")
	data, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatal(err)
	}
	expected := "KUBELET_EXTRA_ARGS=\"--provider-id=redfish://host/sys/1\"\n"
	if string(data) != expected {
		t.Errorf("got %q, want %q", string(data), expected)
	}
}

func TestConfigureKubeletNodeLabels(t *testing.T) {
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)
	cfg := &config.MachineConfig{
		FailureDomain: "dc1-az1",
		Region:        "eu-central",
	}
	if err := c.ConfigureKubelet(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	confPath := filepath.Join(c.rootDir, "etc", "kubernetes", "kubelet.conf.d", "20-caprf-node-labels.conf")
	data, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty node labels config")
	}
}

func TestConfigureKubeletNoLabels(t *testing.T) {
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)
	cfg := &config.MachineConfig{}
	if err := c.ConfigureKubelet(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	confPath := filepath.Join(c.rootDir, "etc", "kubernetes", "kubelet.conf.d", "20-caprf-node-labels.conf")
	if _, err := os.Stat(confPath); !os.IsNotExist(err) {
		t.Error("node labels file should not exist when no labels configured")
	}
}

func TestConfigureDNS(t *testing.T) {
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)
	// Create etc/ directory (in production it exists from the mounted root).
	if err := os.MkdirAll(filepath.Join(c.rootDir, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.MachineConfig{DNSResolvers: "8.8.8.8,1.1.1.1"}
	if err := c.ConfigureDNS(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(c.rootDir, "etc", "resolv.conf"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if content != "nameserver 8.8.8.8\nnameserver 1.1.1.1\n" {
		t.Errorf("resolv.conf = %q", content)
	}
}

func TestConfigureDNSEmpty(t *testing.T) {
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)
	cfg := &config.MachineConfig{DNSResolvers: ""}
	if err := c.ConfigureDNS(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	path := filepath.Join(c.rootDir, "etc", "resolv.conf")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("resolv.conf should not exist when no resolvers configured")
	}
}

func TestConfigureGRUB(t *testing.T) {
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)
	cfg := &config.MachineConfig{ExtraKernelParams: "quiet splash"}
	if err := c.ConfigureGRUB(context.Background(), cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(c.rootDir, "etc", "default", "grub.d", "10-caprf-kernel-params.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty grub config")
	}
}

func TestCopyProvisionerFilesNotExist(t *testing.T) {
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)
	if err := c.CopyProvisionerFiles(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCopyMachineFilesNotExist(t *testing.T) {
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)
	if err := c.CopyMachineFiles(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunMachineCommandsNotExist(t *testing.T) {
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)
	if err := c.RunMachineCommands(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetupMellanoxNoNICs(t *testing.T) {
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)
	restore := SetPCIVendorCheckFunc(func(string) (bool, error) { return false, nil })
	defer restore()
	changed, err := c.SetupMellanox(context.Background(), 32)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected no firmware change")
	}
}

func TestSetupMellanoxLspciFailure(t *testing.T) {
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)
	restore := SetPCIVendorCheckFunc(func(string) (bool, error) { return false, fmt.Errorf("sysfs not available") })
	defer restore()
	changed, err := c.SetupMellanox(context.Background(), 32)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected no firmware change on lspci failure")
	}
}

type sequentialCommander struct {
	results []mockResult
	idx     int
}

func (s *sequentialCommander) Run(_ context.Context, _ string, _ ...string) ([]byte, error) {
	if s.idx >= len(s.results) {
		return nil, nil
	}
	r := s.results[s.idx]
	s.idx++
	return r.output, r.err
}

func TestSetupMellanoxFirmwareChanged(t *testing.T) {
	restore := SetPCIVendorCheckFunc(func(string) (bool, error) { return true, nil })
	defer restore()
	mgr := disk.NewManager(&sequentialCommander{
		results: []mockResult{
			{output: []byte("mt4125_pciconf0\n"), err: nil},
			{output: []byte("Applied"), err: nil},
		},
	})
	c := &Configurator{disk: mgr, rootDir: t.TempDir()}
	changed, err := c.SetupMellanox(context.Background(), 32)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected firmware change when mstconfig succeeds")
	}
}

func TestSetupMellanoxMstconfigFails(t *testing.T) {
	restore := SetPCIVendorCheckFunc(func(string) (bool, error) { return true, nil })
	defer restore()
	mgr := disk.NewManager(&sequentialCommander{
		results: []mockResult{
			{output: []byte("mt4125_pciconf0\n"), err: nil},
			{output: []byte("failed"), err: fmt.Errorf("mstconfig: exit 1")},
		},
	})
	c := &Configurator{disk: mgr, rootDir: t.TempDir()}
	changed, err := c.SetupMellanox(context.Background(), 32)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected no firmware change when mstconfig fails")
	}
}

func TestRemoveEFIBootEntriesGracefulOnMissing(t *testing.T) {
	// RemoveEFIBootEntries runs efibootmgr directly on the host.
	// Skip when running as root to avoid modifying real EFI variables.
	if os.Getuid() == 0 {
		t.Skip("skipping under root to avoid touching real EFI boot entries")
	}
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)
	if err := c.RemoveEFIBootEntries(context.Background()); err != nil {
		t.Fatalf("expected nil error when efibootmgr unavailable, got: %v", err)
	}
}

func TestMountEFIVarsReturnsNilOnHost(t *testing.T) {
	// MountEFIVars calls modprobe + syscall.Mount directly (not via Commander).
	// Skip when running as root to avoid side effects on the host.
	if os.Getuid() == 0 {
		t.Skip("skipping under root to avoid mounting efivarfs on the host")
	}
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)
	err := c.MountEFIVars(context.Background())
	if err != nil {
		t.Fatalf("expected nil error on non-EFI test system, got: %v", err)
	}
}

func TestIsMountPoint(t *testing.T) {
	// /proc should be a mount point on Linux.
	if !isMountPoint("/proc") {
		t.Error("/proc should be a mount point")
	}
	// Nonexistent path should not be a mount point.
	if isMountPoint("/nonexistent/path/12345") {
		t.Error("/nonexistent/path/12345 should not be a mount point")
	}
}

func TestPartNumberFromDevice(t *testing.T) {
	tests := []struct {
		dev  string
		want string
	}{
		{"/dev/sda1", "1"},
		{"/dev/sda2", "2"},
		{"/dev/sda15", "15"},
		{"/dev/nvme0n1p1", "1"},
		{"/dev/nvme0n1p2", "2"},
		{"/dev/nvme0n1p15", "15"},
		{"/dev/vda3", "3"},
	}
	for _, tt := range tests {
		t.Run(tt.dev, func(t *testing.T) {
			if got := partNumberFromDevice(tt.dev); got != tt.want {
				t.Errorf("partNumberFromDevice(%q) = %q, want %q", tt.dev, got, tt.want)
			}
		})
	}
}

func TestCreateEFIBootEntry(t *testing.T) {
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)

	// Create the arch-specific shim EFI loader file so it's detected.
	efiDir := filepath.Join(c.rootDir, "boot", "efi", "EFI", "ubuntu")
	if err := os.MkdirAll(efiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shimName, _, err := efiLoaderNames(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(efiDir, shimName), []byte("shim"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd.setResult("chroot "+c.rootDir, []byte("Boot0001* ubuntu"), nil)

	err = c.CreateEFIBootEntry(context.Background(), "/dev/sda", "/dev/sda1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateEFIBootEntryEmptyPartition(t *testing.T) {
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)

	// Empty boot partition should skip without error.
	err := c.CreateEFIBootEntry(context.Background(), "/dev/sda", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunPostProvisionCmds(t *testing.T) {
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)

	cmd.setResult("chroot "+c.rootDir, []byte("ok"), nil)

	cmds := []string{"apt update", "systemctl enable foo", ""}
	err := c.RunPostProvisionCmds(context.Background(), cmds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunPostProvisionCmdsError(t *testing.T) {
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)

	cmd.setResult("chroot "+c.rootDir, nil, fmt.Errorf("command failed"))

	cmds := []string{"failing-command"}
	err := c.RunPostProvisionCmds(context.Background(), cmds)
	if err == nil {
		t.Fatal("expected error when command fails")
	}
}

func TestNewOrchestrator(t *testing.T) {
	cfg := &config.MachineConfig{Mode: "provision"}
	provider := &mockProvider{}
	mgr := disk.NewManager(newMockCommander())
	orch := NewOrchestrator(cfg, provider, mgr)
	if orch == nil {
		t.Fatal("expected non-nil orchestrator")
	}
}

func TestProvisionStepsContainEFIVars(t *testing.T) {
	cfg := &config.MachineConfig{}
	orch := NewOrchestrator(cfg, &mockProvider{}, disk.NewManager(newMockCommander()))
	steps := orch.provisionSteps()

	// Verify mount-efivarfs appears before remove-efi-entries.
	mountIdx, removeIdx := -1, -1
	for i, step := range steps {
		switch step.Name {
		case "mount-efivarfs":
			mountIdx = i
		case "remove-efi-entries":
			removeIdx = i
		}
	}
	if mountIdx == -1 || removeIdx == -1 {
		t.Fatal("mount-efivarfs and remove-efi-entries not found in steps")
	}
	if mountIdx >= removeIdx {
		t.Errorf("mount-efivarfs (idx %d) must come before remove-efi-entries (idx %d)", mountIdx, removeIdx)
	}

	// Verify total step count is 31 (30 original + mount-efivarfs).
	if len(steps) != 31 {
		t.Errorf("expected 31 provisioning steps, got %d", len(steps))
	}
}

func TestFirmwareChangedDefault(t *testing.T) {
	cfg := &config.MachineConfig{}
	orch := NewOrchestrator(cfg, &mockProvider{}, disk.NewManager(newMockCommander()))
	if orch.FirmwareChanged() {
		t.Error("expected firmwareChanged=false by default")
	}
}

func TestDeprovisionReportsInit(t *testing.T) {
	provider := &mockProvider{}
	cmd := newMockCommander()
	cfg := &config.MachineConfig{Mode: "hard"}
	orch := NewOrchestrator(cfg, provider, disk.NewManager(cmd))
	_ = orch.Deprovision(context.Background())
	if len(provider.statuses) == 0 {
		t.Fatal("expected at least one status report")
	}
	if provider.statuses[0].status != config.StatusInit {
		t.Errorf("expected first status to be init, got %s", provider.statuses[0].status)
	}
}

func TestDeprovisionSoftMode(t *testing.T) {
	provider := &mockProvider{}
	cmd := newMockCommander()
	cfg := &config.MachineConfig{Mode: "soft"}
	orch := NewOrchestrator(cfg, provider, disk.NewManager(cmd))
	err := orch.Deprovision(context.Background())
	if err == nil {
		return
	}
	found := false
	for _, s := range provider.statuses {
		if s.status == config.StatusError {
			found = true
		}
	}
	if !found {
		t.Error("expected error status to be reported on failure")
	}
}

func TestRedactURLs(t *testing.T) {
	tests := []struct {
		name string
		urls []string
		want []string
	}{
		{
			name: "no credentials",
			urls: []string{"http://example.com/image.gz"},
			want: []string{"http://example.com/image.gz"},
		},
		{
			name: "with credentials",
			urls: []string{"http://user:pass@registry.example.com/image:tag"},
			want: []string{"http://REDACTED@registry.example.com/image:tag"},
		},
		{
			name: "oci with credentials",
			urls: []string{"oci://user:pass@registry/repo:v1"},
			want: []string{"oci://REDACTED@registry/repo:v1"},
		},
		{
			name: "empty",
			urls: []string{},
			want: []string{},
		},
		{
			name: "mixed",
			urls: []string{
				"http://example.com/plain.gz",
				"http://admin:secret@example.com/private.gz",
			},
			want: []string{
				"http://example.com/plain.gz",
				"http://REDACTED@example.com/private.gz",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := redactURLs(tc.urls)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestRescueAction(t *testing.T) {
	tests := []struct {
		name       string
		rescueMode string
		wantType   rescue.Mode
	}{
		{"empty defaults to reboot", "", rescue.ModeReboot},
		{"invalid defaults to reboot", "bogus", rescue.ModeReboot},
		{"reboot mode", "reboot", rescue.ModeReboot},
		{"retry mode", "retry", rescue.ModeRetry},
		{"shell mode", "shell", rescue.ModeShell},
		{"wait mode", "wait", rescue.ModeWait},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			orch := &Orchestrator{
				cfg: &config.MachineConfig{RescueMode: tc.rescueMode},
				log: slog.Default(),
			}
			state := &rescue.RetryState{}
			action := orch.RescueAction(state)
			if action.Type != tc.wantType {
				t.Errorf("RescueAction() type = %q, want %q", action.Type, tc.wantType)
			}
		})
	}
}

func TestIsSafeDeviceName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"sda", true},
		{"nvme0n1", true},
		{"vd.a", true},
		{"disk-0", true},
		{"DISK_A", true},
		{"", false},
		{"sd a", false},
		{"/dev/sda", false},
		{"sda;rm", false},
		{"sd$a", false},
		{"name`cmd`", false},
		{"../etc", false},
		{"sda\n", false},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("%q", tc.name), func(t *testing.T) {
			if got := isSafeDeviceName(tc.name); got != tc.want {
				t.Errorf("isSafeDeviceName(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestCopyTreeNormalFiles(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// Create a simple file tree: dir/file.txt
	subDir := filepath.Join(src, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree() unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dst, "sub", "file.txt"))
	if err != nil {
		t.Fatalf("expected copied file: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("file content = %q, want %q", data, "hello")
	}
}

func TestCopyTreeSymlinksSkipped(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// Create a real file and a symlink pointing to it.
	realFile := filepath.Join(src, "real.txt")
	if err := os.WriteFile(realFile, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realFile, filepath.Join(src, "link.txt")); err != nil {
		t.Fatal(err)
	}

	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree() unexpected error: %v", err)
	}

	// Real file should exist, symlink should be skipped.
	if _, err := os.Stat(filepath.Join(dst, "real.txt")); err != nil {
		t.Errorf("expected real.txt to be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "link.txt")); !os.IsNotExist(err) {
		t.Error("expected symlink to be skipped, but it exists")
	}
}

func TestCopyTreePathTraversal(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// Create a normal file under src.
	if err := os.WriteFile(filepath.Join(src, "ok.txt"), []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Normal copy should succeed.
	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree() on clean tree should succeed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "ok.txt")); err != nil {
		t.Errorf("expected ok.txt to be copied: %v", err)
	}

	// Create a symlink inside src that points outside.
	outsideFile := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("escape"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(src, "escape")); err != nil {
		t.Fatal(err)
	}

	dst2 := t.TempDir()
	// Copy should skip the symlink (no error, but symlink not followed).
	if err := copyTree(src, dst2); err != nil {
		t.Fatalf("copyTree() should skip symlinks without error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst2, "escape")); !os.IsNotExist(err) {
		t.Error("expected symlink target to not be copied")
	}
}

func TestRescueAction_RetryExhausted(t *testing.T) {
	orch := &Orchestrator{
		cfg: &config.MachineConfig{RescueMode: "retry"},
		log: slog.Default(),
	}
	state := &rescue.RetryState{Attempts: 3, MaxRetries: 3}
	action := orch.RescueAction(state)
	if action.Type != rescue.ModeReboot {
		t.Errorf("exhausted retry should reboot, got %q", action.Type)
	}
}
