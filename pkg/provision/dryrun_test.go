//go:build linux

package provision

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
)

type dryRunProvider struct {
	lastStatus  config.Status
	lastMessage string
}

func (p *dryRunProvider) GetConfig(_ context.Context) (*config.MachineConfig, error) {
	return &config.MachineConfig{}, nil
}
func (p *dryRunProvider) ReportStatus(_ context.Context, s config.Status, msg string) error {
	p.lastStatus = s
	p.lastMessage = msg
	return nil
}
func (p *dryRunProvider) ShipLog(_ context.Context, _ string) error                 { return nil }
func (p *dryRunProvider) Heartbeat(_ context.Context) error                         { return nil }
func (p *dryRunProvider) FetchCommands(_ context.Context) ([]config.Command, error) { return nil, nil }
func (p *dryRunProvider) ReportInventory(_ context.Context, _ []byte) error         { return nil }
func (p *dryRunProvider) ReportFirmware(_ context.Context, _ []byte) error          { return nil }

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
