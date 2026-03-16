//go:build linux

package provision

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
)

type dryRunProvider struct {
	lastStatus  config.Status
	lastMessage string
}

type fakeFileInfo struct{}

func (fakeFileInfo) Name() string       { return "efi" }
func (fakeFileInfo) Size() int64        { return 0 }
func (fakeFileInfo) Mode() os.FileMode  { return os.ModeDir }
func (fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (fakeFileInfo) IsDir() bool        { return true }
func (fakeFileInfo) Sys() any           { return nil }

func (p *dryRunProvider) GetConfig(_ context.Context) (*config.MachineConfig, error) {
	return &config.MachineConfig{}, nil
}
func (p *dryRunProvider) ReportStatus(_ context.Context, s config.Status, msg string) error {
	p.lastStatus = s
	p.lastMessage = msg
	return nil
}
func (p *dryRunProvider) ShipLog(_ context.Context, _ string) error                  { return nil }
func (p *dryRunProvider) Heartbeat(_ context.Context) error                          { return nil }
func (p *dryRunProvider) FetchCommands(_ context.Context) ([]config.Command, error)  { return nil, nil }
func (p *dryRunProvider) AcknowledgeCommand(_ context.Context, _, _, _ string) error { return nil }
func (p *dryRunProvider) ReportInventory(_ context.Context, _ []byte) error          { return nil }
func (p *dryRunProvider) ReportFirmware(_ context.Context, _ []byte) error           { return nil }

func withMockInterfaces(t *testing.T, fn func() ([]net.Interface, error)) {
	t.Helper()
	original := listInterfaces
	listInterfaces = fn
	t.Cleanup(func() {
		listInterfaces = original
	})
}

func withMockStat(t *testing.T, fn func(string) (os.FileInfo, error)) {
	t.Helper()
	original := statPath
	statPath = fn
	t.Cleanup(func() {
		statPath = original
	})
}

func TestDryRunConfigValidation(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *config.MachineConfig
		expect DryRunStatus
	}{
		{
			name:   "no images",
			cfg:    &config.MachineConfig{},
			expect: DryRunFail,
		},
		{
			name:   "no hostname",
			cfg:    &config.MachineConfig{ImageURLs: []string{"http://example.com/img"}},
			expect: DryRunWarn,
		},
		{
			name:   "valid config",
			cfg:    &config.MachineConfig{ImageURLs: []string{"http://example.com/img"}, Hostname: "node1"},
			expect: DryRunPass,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			o := NewOrchestrator(tc.cfg, &dryRunProvider{}, disk.NewManager(nil))
			result := o.dryRunConfigValidation(context.Background())
			if result.Status != tc.expect {
				t.Errorf("got %s, want %s: %s", result.Status, tc.expect, result.Message)
			}
		})
	}
}

func TestDryRunImageReachability(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("expected HEAD, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	o := NewOrchestrator(
		&config.MachineConfig{ImageURLs: []string{srv.URL + "/test.img"}},
		&dryRunProvider{},
		disk.NewManager(nil),
	)
	result := o.dryRunImageReachability(context.Background())
	if result.Status != DryRunPass {
		t.Errorf("got %s, want pass: %s", result.Status, result.Message)
	}
}

func TestDryRunImageUnreachable(t *testing.T) {
	// Use a closed server for fast, deterministic connection failure.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close()

	o := NewOrchestrator(
		&config.MachineConfig{ImageURLs: []string{srv.URL + "/unreachable.img"}},
		&dryRunProvider{},
		disk.NewManager(nil),
	)
	result := o.dryRunImageReachability(context.Background())
	if result.Status != DryRunFail {
		t.Errorf("got %s, want fail: %s", result.Status, result.Message)
	}
}

func TestDryRunHealthChecks(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		expect  DryRunStatus
	}{
		{"disabled", false, DryRunWarn},
		{"enabled", true, DryRunPass},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.MachineConfig{HealthChecksEnabled: tc.enabled}
			o := NewOrchestrator(cfg, &dryRunProvider{}, disk.NewManager(nil))
			result := o.dryRunHealthChecks(context.Background())
			if result.Status != tc.expect {
				t.Errorf("got %s, want %s", result.Status, tc.expect)
			}
		})
	}
}

func TestDryRunDiskDetection_Configured(t *testing.T) {
	// Non-device path should fail device-node check.
	o := NewOrchestrator(
		&config.MachineConfig{DiskDevice: "/tmp"},
		&dryRunProvider{},
		disk.NewManager(nil),
	)
	result := o.dryRunDiskDetection(context.Background())
	if result.Status != DryRunFail {
		t.Errorf("got %s, want fail for non-device path: %s", result.Status, result.Message)
	}
}

func TestDryRunDiskDetection_Missing(t *testing.T) {
	o := NewOrchestrator(
		&config.MachineConfig{DiskDevice: "/dev/nonexistent-disk-xyz"},
		&dryRunProvider{},
		disk.NewManager(nil),
	)
	result := o.dryRunDiskDetection(context.Background())
	if result.Status != DryRunFail {
		t.Errorf("got %s, want fail for missing device: %s", result.Status, result.Message)
	}
}

func TestDryRunImageReachability_NoURLs(t *testing.T) {
	o := NewOrchestrator(
		&config.MachineConfig{},
		&dryRunProvider{},
		disk.NewManager(nil),
	)
	result := o.dryRunImageReachability(context.Background())
	if result.Status != DryRunFail {
		t.Errorf("got %s, want fail for empty URLs: %s", result.Status, result.Message)
	}
}

func TestDryRunImageReachability_OCI(t *testing.T) {
	o := NewOrchestrator(
		&config.MachineConfig{ImageURLs: []string{"oci://registry.example.com/image:latest"}},
		&dryRunProvider{},
		disk.NewManager(nil),
	)
	result := o.dryRunImageReachability(context.Background())
	if result.Status != DryRunWarn {
		t.Errorf("got %s, want warn for OCI URLs: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "skipped") {
		t.Errorf("expected skipped message, got %q", result.Message)
	}
}

func TestDryRunImageReachability_MixedHTTPAndOCI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	o := NewOrchestrator(
		&config.MachineConfig{ImageURLs: []string{srv.URL + "/image.raw", "oci://registry.example.com/image:latest"}},
		&dryRunProvider{},
		disk.NewManager(nil),
	)
	result := o.dryRunImageReachability(context.Background())
	if result.Status != DryRunWarn {
		t.Errorf("got %s, want warn for mixed URLs: %s", result.Status, result.Message)
	}
}

func TestDryRunImageReachability_UnsupportedScheme(t *testing.T) {
	o := NewOrchestrator(
		&config.MachineConfig{ImageURLs: []string{"ftp://example.com/img"}},
		&dryRunProvider{},
		disk.NewManager(nil),
	)
	result := o.dryRunImageReachability(context.Background())
	if result.Status != DryRunFail {
		t.Errorf("got %s, want fail for unsupported scheme: %s", result.Status, result.Message)
	}
}

func TestDryRunImageChecksum(t *testing.T) {
	tests := []struct {
		name         string
		checksum     string
		checksumType string
		expect       DryRunStatus
	}{
		{"no checksum", "", "", DryRunWarn},
		{"sha256", "abc123", "sha256", DryRunPass},
		{"sha512", "abc123", "sha512", DryRunPass},
		{"empty type defaults to sha256", "abc123", "", DryRunPass},
		{"unsupported type", "abc123", "md5", DryRunFail},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.MachineConfig{
				ImageChecksum:     tc.checksum,
				ImageChecksumType: tc.checksumType,
			}
			o := NewOrchestrator(cfg, &dryRunProvider{}, disk.NewManager(nil))
			result := o.dryRunImageChecksum(context.Background())
			if result.Status != tc.expect {
				t.Errorf("got %s, want %s: %s", result.Status, tc.expect, result.Message)
			}
		})
	}
}

func TestDryRunNetworkLink(t *testing.T) {
	tests := []struct {
		name       string
		ifaces     []net.Interface
		err        error
		expect     DryRunStatus
		wantSubstr string
	}{
		{
			name:       "physical interface up",
			ifaces:     []net.Interface{{Name: "eth0", Flags: net.FlagUp}},
			expect:     DryRunPass,
			wantSubstr: "interfaces up",
		},
		{
			name:       "only loopback up",
			ifaces:     []net.Interface{{Name: "lo", Flags: net.FlagUp | net.FlagLoopback}},
			expect:     DryRunFail,
			wantSubstr: "no physical non-loopback interfaces are up",
		},
		{
			name:       "only virtual interfaces up",
			ifaces:     []net.Interface{{Name: "docker0", Flags: net.FlagUp}},
			expect:     DryRunFail,
			wantSubstr: "no physical non-loopback interfaces are up",
		},
		{
			name:       "interface enumeration error",
			err:        errors.New("boom"),
			expect:     DryRunFail,
			wantSubstr: "cannot list interfaces",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			withMockInterfaces(t, func() ([]net.Interface, error) {
				if tc.err != nil {
					return nil, tc.err
				}
				return tc.ifaces, nil
			})

			o := NewOrchestrator(&config.MachineConfig{}, &dryRunProvider{}, disk.NewManager(nil))
			result := o.dryRunNetworkLink(context.Background())
			if result.Status != tc.expect {
				t.Errorf("got %s, want %s: %s", result.Status, tc.expect, result.Message)
			}
			if !strings.Contains(result.Message, tc.wantSubstr) {
				t.Errorf("message %q does not contain %q", result.Message, tc.wantSubstr)
			}
		})
	}
}

func TestDryRunEFIBoot(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		expect DryRunStatus
	}{
		{name: "efi present", err: nil, expect: DryRunPass},
		{name: "efi missing", err: os.ErrNotExist, expect: DryRunWarn},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			withMockStat(t, func(string) (os.FileInfo, error) {
				if tc.err != nil {
					return nil, tc.err
				}
				return fakeFileInfo{}, nil
			})

			o := NewOrchestrator(&config.MachineConfig{}, &dryRunProvider{}, disk.NewManager(nil))
			result := o.dryRunEFIBoot(context.Background())
			if result.Status != tc.expect {
				t.Errorf("got %s, want %s: %s", result.Status, tc.expect, result.Message)
			}
		})
	}
}

func TestDryRunInventoryProbe(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		expect  DryRunStatus
	}{
		{"disabled", false, DryRunWarn},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.MachineConfig{InventoryEnabled: tc.enabled}
			o := NewOrchestrator(cfg, &dryRunProvider{}, disk.NewManager(nil))
			result := o.dryRunInventoryProbe(context.Background())
			if result.Status != tc.expect {
				t.Errorf("got %s, want %s: %s", result.Status, tc.expect, result.Message)
			}
		})
	}
}

func TestIsVirtualInterface(t *testing.T) {
	tests := []struct {
		name    string
		virtual bool
	}{
		{"eth0", false},
		{"eno1", false},
		{"enp3s0", false},
		{"veth123abc", true},
		{"docker0", true},
		{"br-abc123", true},
		{"virbr0", true},
		{"cni0", true},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := isVirtualInterface(tc.name); got != tc.virtual {
				t.Errorf("isVirtualInterface(%q) = %v, want %v", tc.name, got, tc.virtual)
			}
		})
	}
}

func TestDryRunDiskDetection_CharDevice(t *testing.T) {
	// /dev/null is a character device and should fail the block device check.
	o := NewOrchestrator(
		&config.MachineConfig{DiskDevice: "/dev/null"},
		&dryRunProvider{},
		disk.NewManager(nil),
	)
	result := o.dryRunDiskDetection(context.Background())
	if result.Status != DryRunFail {
		t.Errorf("got %s, want fail for char device: %s", result.Status, result.Message)
	}
}

func TestDryRunImageReachability_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	o := NewOrchestrator(
		&config.MachineConfig{ImageURLs: []string{srv.URL + "/missing.img"}},
		&dryRunProvider{},
		disk.NewManager(nil),
	)
	result := o.dryRunImageReachability(context.Background())
	if result.Status != DryRunFail {
		t.Errorf("got %s, want fail for 404: %s", result.Status, result.Message)
	}
}

func TestDryRunAggregation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	provider := &dryRunProvider{}

	// All checks should pass with valid config and reachable image server.
	o := NewOrchestrator(
		&config.MachineConfig{
			ImageURLs: []string{srv.URL + "/image.raw"},
			Hostname:  "test-host",
		},
		provider,
		disk.NewManager(nil),
	)

	_ = o.DryRun(context.Background())
	// Some checks may warn/fail in test environments (e.g. no EFI, no disk),
	// but the aggregation and status reporting must not panic.
	if provider.lastStatus == "" {
		t.Error("DryRun did not report status to provider")
	}

	// Verify that a fully missing config fails with error.
	provFail := &dryRunProvider{}
	oFail := NewOrchestrator(
		&config.MachineConfig{},
		provFail,
		disk.NewManager(nil),
	)
	err := oFail.DryRun(context.Background())
	if err == nil {
		t.Error("expected DryRun to fail with empty config")
	}
	if provFail.lastStatus != config.StatusError {
		t.Errorf("expected StatusError, got %s", provFail.lastStatus)
	}
}

func TestRedactImageURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "removes credentials and query",
			in:   "https://user:secret@example.com/image.raw?token=abc#frag",
			want: "https://example.com/image.raw",
		},
		{
			name: "invalid URL is unchanged",
			in:   "::://bad-url",
			want: "::://bad-url",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := redactImageURL(tc.in)
			if got != tc.want {
				t.Errorf("redactImageURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
