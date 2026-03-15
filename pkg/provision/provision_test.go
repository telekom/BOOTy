//go:build linux

package provision

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
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

func TestRemoveEFIBootEntriesNoEntries(t *testing.T) {
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)
	cmd.setResult("chroot "+c.rootDir, []byte("BootCurrent: 0001\nBootOrder: 0001\n"), nil)
	if err := c.RemoveEFIBootEntries(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRemoveEFIBootEntriesFailure(t *testing.T) {
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)
	cmd.setResult("chroot "+c.rootDir, nil, fmt.Errorf("efibootmgr not found"))
	if err := c.RemoveEFIBootEntries(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
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

	// Create the shim EFI loader file so it's detected.
	efiDir := filepath.Join(c.rootDir, "boot", "efi", "EFI", "ubuntu")
	if err := os.MkdirAll(efiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(efiDir, "shimx64.efi"), []byte("shim"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd.setResult("chroot "+c.rootDir, []byte("Boot0001* ubuntu"), nil)

	err := c.CreateEFIBootEntry(context.Background(), "/dev/sda", "/dev/sda1")
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

// mockBIOSProvider embeds mockProvider and implements BIOSSettingsReporter.
type mockBIOSProvider struct {
	mockProvider
	biosData []byte
	biosErr  error
}

func (p *mockBIOSProvider) ReportBIOSSettings(_ context.Context, data []byte) error {
	p.biosData = data
	return p.biosErr
}

func TestReportBIOSSettings(t *testing.T) {
	tests := []struct {
		name         string
		settings     string
		provider     config.Provider
		wantReport   bool
		wantContains string
	}{
		{
			name:     "empty settings skips",
			settings: "",
			provider: &mockProvider{},
		},
		{
			name:     "invalid JSON skips",
			settings: "not-json",
			provider: &mockProvider{},
		},
		{
			name:     "provider without BIOSSettingsReporter skips",
			settings: `{"BootMode":"UEFI"}`,
			provider: &mockProvider{},
		},
		{
			name:         "valid settings reported",
			settings:     `{"BootMode":"UEFI"}`,
			provider:     &mockBIOSProvider{},
			wantReport:   true,
			wantContains: "UEFI",
		},
		{
			name:     "report error is non-fatal",
			settings: `{"BootMode":"UEFI"}`,
			provider: &mockBIOSProvider{biosErr: fmt.Errorf("network error")},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := &Orchestrator{
				cfg:      &config.MachineConfig{BIOSSettings: tc.settings},
				provider: tc.provider,
				log:      slog.Default(),
			}
			if err := o.reportBIOSSettings(context.Background()); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantReport {
				bp, ok := tc.provider.(*mockBIOSProvider)
				if !ok || bp.biosData == nil {
					t.Fatal("expected BIOS data to be reported")
				}
				if tc.wantContains != "" {
					if got := string(bp.biosData); !strings.Contains(got, tc.wantContains) {
						t.Errorf("report = %q, want to contain %q", got, tc.wantContains)
					}
				}
			}
		})
	}
}
