//go:build linux

package provision

import (
	"context"
	"errors"
	"fmt"
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

type fakeFileInfo struct {
	name string
	mode os.FileMode
}

func (f fakeFileInfo) Name() string {
	if f.name != "" {
		return f.name
	}
	return "mock"
}

func (fakeFileInfo) Size() int64 { return 0 }

func (f fakeFileInfo) Mode() os.FileMode {
	if f.mode != 0 {
		return f.mode
	}
	return os.ModeDir
}

func (fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool      { return f.Mode().IsDir() }
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

func withMockReadPath(t *testing.T, fn func(string) ([]byte, error)) {
	t.Helper()
	original := readPath
	readPath = fn
	t.Cleanup(func() {
		readPath = original
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

func TestDryRunImageReachability_InvalidURL(t *testing.T) {
	o := NewOrchestrator(
		&config.MachineConfig{ImageURLs: []string{"http://%zz"}},
		&dryRunProvider{},
		disk.NewManager(nil),
	)
	result := o.dryRunImageReachability(context.Background())
	if result.Status != DryRunFail {
		t.Errorf("got %s, want fail for invalid URL: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "invalid image URL") {
		t.Errorf("expected invalid URL error, got %q", result.Message)
	}
}

func TestDryRunImageReachability_UppercaseScheme(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	upperURL := strings.Replace(srv.URL, "http://", "HTTP://", 1)
	o := NewOrchestrator(
		&config.MachineConfig{ImageURLs: []string{upperURL + "/image.raw"}},
		&dryRunProvider{},
		disk.NewManager(nil),
	)
	result := o.dryRunImageReachability(context.Background())
	if result.Status != DryRunPass {
		t.Errorf("got %s, want pass for uppercase scheme: %s", result.Status, result.Message)
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
		{"uppercase type", "abc123", "SHA512", DryRunPass},
		{"trimmed uppercase type", "abc123", " SHA256 ", DryRunPass},
		{"empty type defaults to sha256", "abc123", "", DryRunPass},
		{"unsupported type", "abc123", "md5", DryRunFail},
	}
	for _, tc := range tests {
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
		t.Run(tc.name, func(t *testing.T) {
			withMockReadPath(t, func(string) ([]byte, error) {
				return []byte("1\n"), nil
			})

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

func TestDryRunNetworkLink_CarrierDown(t *testing.T) {
	withMockInterfaces(t, func() ([]net.Interface, error) {
		return []net.Interface{{Name: "eth0", Flags: net.FlagUp}}, nil
	})
	withMockReadPath(t, func(path string) ([]byte, error) {
		if strings.HasSuffix(path, "/carrier") {
			return []byte("0\n"), nil
		}
		return nil, os.ErrNotExist
	})

	o := NewOrchestrator(&config.MachineConfig{}, &dryRunProvider{}, disk.NewManager(nil))
	result := o.dryRunNetworkLink(context.Background())
	if result.Status != DryRunFail {
		t.Errorf("got %s, want fail when carrier is down: %s", result.Status, result.Message)
	}
}

func TestInterfaceHasCarrier(t *testing.T) {
	tests := []struct {
		name     string
		readPath func(string) ([]byte, error)
		expected bool
	}{
		{
			name: "carrier up",
			readPath: func(path string) ([]byte, error) {
				if strings.HasSuffix(path, "/carrier") {
					return []byte("1\n"), nil
				}
				return nil, os.ErrNotExist
			},
			expected: true,
		},
		{
			name: "carrier down",
			readPath: func(path string) ([]byte, error) {
				if strings.HasSuffix(path, "/carrier") {
					return []byte("0\n"), nil
				}
				return nil, os.ErrNotExist
			},
			expected: false,
		},
		{
			name: "fallback operstate up",
			readPath: func(path string) ([]byte, error) {
				if strings.HasSuffix(path, "/carrier") {
					return nil, os.ErrNotExist
				}
				if strings.HasSuffix(path, "/operstate") {
					return []byte("up\n"), nil
				}
				return nil, os.ErrNotExist
			},
			expected: true,
		},
		{
			name: "fallback operstate down",
			readPath: func(path string) ([]byte, error) {
				if strings.HasSuffix(path, "/carrier") {
					return nil, os.ErrNotExist
				}
				if strings.HasSuffix(path, "/operstate") {
					return []byte("down\n"), nil
				}
				return nil, os.ErrNotExist
			},
			expected: false,
		},
		{
			name: "both probes unavailable",
			readPath: func(string) ([]byte, error) {
				return nil, os.ErrNotExist
			},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withMockReadPath(t, tc.readPath)
			if got := interfaceHasCarrier("eth0"); got != tc.expected {
				t.Errorf("interfaceHasCarrier() = %v, want %v", got, tc.expected)
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

func TestDryRunInventoryProbe_Enabled(t *testing.T) {
	cfg := &config.MachineConfig{InventoryEnabled: true}
	o := NewOrchestrator(cfg, &dryRunProvider{}, disk.NewManager(nil))

	t.Run("dmi accessible", func(t *testing.T) {
		withMockStat(t, func(_ string) (os.FileInfo, error) { return os.Stat(os.DevNull) })
		result := o.dryRunInventoryProbe(context.Background())
		if result.Status != DryRunPass {
			t.Errorf("expected pass when DMI accessible, got %s: %s", result.Status, result.Message)
		}
	})

	t.Run("dmi not accessible", func(t *testing.T) {
		withMockStat(t, func(_ string) (os.FileInfo, error) { return nil, os.ErrNotExist })
		result := o.dryRunInventoryProbe(context.Background())
		if result.Status != DryRunWarn {
			t.Errorf("expected warn when DMI not accessible, got %s: %s", result.Status, result.Message)
		}
	})
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

	// DryRun with a reachable image server and valid hostname, but /dev/null
	// as DiskDevice (a char device, not a block device) so disk check warns/fails.
	// Verifies aggregation and provider status reporting run without panic.
	o := NewOrchestrator(
		&config.MachineConfig{
			ImageURLs:  []string{srv.URL + "/image.raw"},
			Hostname:   "test-host",
			DiskDevice: "/dev/null",
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

func TestDryRunAggregation_WarningsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	provider := &dryRunProvider{}

	withMockStat(t, func(path string) (os.FileInfo, error) {
		switch path {
		case "/dev/mock0":
			return fakeFileInfo{name: "mock0", mode: os.ModeDevice}, nil
		case "/sys/firmware/efi":
			return fakeFileInfo{name: "efi", mode: os.ModeDir}, nil
		default:
			return nil, os.ErrNotExist
		}
	})
	withMockInterfaces(t, func() ([]net.Interface, error) {
		return []net.Interface{{Name: "eth0", Flags: net.FlagUp}}, nil
	})
	withMockReadPath(t, func(path string) ([]byte, error) {
		if strings.HasSuffix(path, "/carrier") {
			return []byte("1\n"), nil
		}
		return nil, os.ErrNotExist
	})

	o := NewOrchestrator(
		&config.MachineConfig{
			ImageURLs:           []string{srv.URL + "/image.raw"},
			Hostname:            "test-host",
			DiskDevice:          "/dev/mock0",
			HealthChecksEnabled: false,
			InventoryEnabled:    false,
			ImageChecksum:       "",
			ImageChecksumType:   "",
		},
		provider,
		disk.NewManager(nil),
	)

	err := o.DryRun(context.Background())
	if err != nil {
		t.Fatalf("expected nil error with warnings only, got %v", err)
	}
	if provider.lastStatus != config.StatusSuccess {
		t.Fatalf("expected StatusSuccess, got %s", provider.lastStatus)
	}
	if !strings.Contains(provider.lastMessage, "warning(s)") {
		t.Fatalf("expected warning summary, got %q", provider.lastMessage)
	}
}

func TestDryRun_AllPass(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	provider := &dryRunProvider{}

	withMockStat(t, func(path string) (os.FileInfo, error) {
		switch path {
		case "/dev/mock0":
			return fakeFileInfo{name: "mock0", mode: os.ModeDevice}, nil
		case "/sys/firmware/efi":
			return fakeFileInfo{name: "efi", mode: os.ModeDir}, nil
		case "/sys/class/dmi/id/sys_vendor":
			return fakeFileInfo{name: "sys_vendor", mode: os.ModeDir}, nil
		default:
			return nil, os.ErrNotExist
		}
	})
	withMockInterfaces(t, func() ([]net.Interface, error) {
		return []net.Interface{{Name: "eth0", Flags: net.FlagUp}}, nil
	})
	withMockReadPath(t, func(path string) ([]byte, error) {
		if strings.HasSuffix(path, "/carrier") {
			return []byte("1\n"), nil
		}
		return nil, os.ErrNotExist
	})

	o := NewOrchestrator(
		&config.MachineConfig{
			ImageURLs:           []string{srv.URL + "/image.raw"},
			Hostname:            "test-host",
			DiskDevice:          "/dev/mock0",
			HealthChecksEnabled: true,
			InventoryEnabled:    true,
			ImageChecksum:       "abc123",
			ImageChecksumType:   "sha256",
		},
		provider,
		disk.NewManager(nil),
	)

	err := o.DryRun(context.Background())
	if err != nil {
		t.Fatalf("expected dry-run to pass, got %v", err)
	}
	if provider.lastStatus != config.StatusSuccess {
		t.Fatalf("expected StatusSuccess, got %s", provider.lastStatus)
	}
	if !strings.Contains(provider.lastMessage, "passed all checks") {
		t.Fatalf("expected pass summary, got %q", provider.lastMessage)
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
		t.Run(tc.name, func(t *testing.T) {
			got := redactImageURL(tc.in)
			if got != tc.want {
				t.Errorf("redactImageURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRedactURLError(t *testing.T) {
	raw := "https://user:secret@example.com/image.raw?token=abc"
	err := fmt.Errorf("request failed for %s", raw)

	redacted := redactURLError(err, raw)
	if strings.Contains(redacted, "secret") || strings.Contains(redacted, "token=abc") {
		t.Fatalf("redacted error leaked sensitive data: %q", redacted)
	}
	if !strings.Contains(redacted, "https://example.com/image.raw") {
		t.Fatalf("expected redacted URL in error, got %q", redacted)
	}
}
