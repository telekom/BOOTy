//go:build linux

package provision

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
func (p *mockProvider) FetchCommands(_ context.Context) ([]config.Command, error) {
	return nil, nil
}

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
	cmd.setResult("chroot "+c.rootDir, []byte(""), nil)
	changed, err := c.SetupMellanox(context.Background())
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
	cmd.setResult("chroot "+c.rootDir, nil, fmt.Errorf("lspci not found"))
	changed, err := c.SetupMellanox(context.Background())
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
	mgr := disk.NewManager(&sequentialCommander{
		results: []mockResult{
			{output: []byte("15b3:1017 Mellanox ConnectX-5"), err: nil},
			{output: []byte("Applied"), err: nil},
		},
	})
	c := &Configurator{disk: mgr, rootDir: t.TempDir()}
	changed, err := c.SetupMellanox(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Error("expected firmware change when mstconfig succeeds")
	}
}

func TestSetupMellanoxMstconfigFails(t *testing.T) {
	mgr := disk.NewManager(&sequentialCommander{
		results: []mockResult{
			{output: []byte("15b3:1017 Mellanox ConnectX-5"), err: nil},
			{output: []byte("failed"), err: fmt.Errorf("mstconfig: exit 1")},
		},
	})
	c := &Configurator{disk: mgr, rootDir: t.TempDir()}
	changed, err := c.SetupMellanox(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("expected no firmware change when mstconfig fails")
	}
}

func TestSetupEFIBootNoEntries(t *testing.T) {
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)
	cmd.setResult("chroot "+c.rootDir, []byte("BootCurrent: 0001\nBootOrder: 0001\n"), nil)
	if err := c.SetupEFIBoot(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetupEFIBootFailure(t *testing.T) {
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)
	cmd.setResult("chroot "+c.rootDir, nil, fmt.Errorf("efibootmgr not found"))
	if err := c.SetupEFIBoot(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
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
