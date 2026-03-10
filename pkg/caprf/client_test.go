package caprf

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
)

// testServer is a minimal CAPRF server for testing.
type testServer struct {
	mu       sync.Mutex
	statuses []string
	logs     []string
	debugs   []string
	server   *httptest.Server
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	ts := &testServer{}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /status/init", ts.handleStatus("init"))
	mux.HandleFunc("POST /status/success", ts.handleStatus("success"))
	mux.HandleFunc("POST /status/error", ts.handleStatus("error"))
	mux.HandleFunc("POST /log", ts.handleLog)
	mux.HandleFunc("POST /debug", ts.handleDebug)

	ts.server = httptest.NewServer(mux)
	t.Cleanup(ts.server.Close)
	return ts
}

func (ts *testServer) handleStatus(name string) http.HandlerFunc {
	return func(_ http.ResponseWriter, r *http.Request) {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		ts.statuses = append(ts.statuses, name)
		// Verify auth header.
		if auth := r.Header.Get("Authorization"); auth != "" {
			ts.statuses = append(ts.statuses, "auth:"+auth)
		}
	}
}

func (ts *testServer) handleLog(_ http.ResponseWriter, r *http.Request) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	buf := new(strings.Builder)
	fmt.Fprintf(buf, "%s", r.Header.Get("Authorization"))
	ts.logs = append(ts.logs, buf.String())
}

func (ts *testServer) handleDebug(_ http.ResponseWriter, r *http.Request) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	buf := new(strings.Builder)
	fmt.Fprintf(buf, "%s", r.Header.Get("Authorization"))
	ts.debugs = append(ts.debugs, buf.String())
}

func TestParseVars(t *testing.T) {
	input := `export IMAGE="http://example.com/image1.gz http://example.com/image2.gz"
export HOSTNAME="worker-01"
export TOKEN="test-token-123"
export MACHINE_EXTRA_KERNEL_PARAMS="console=ttyS0 net.ifnames=0"
export FAILURE_DOMAIN="zone-a"
export REGION="eu-central-1"
export PROVIDER_ID="redfish://bmc.example.com/Systems/1"
export MODE="provision"
export MIN_DISK_SIZE_GB="100"
export LOG_URL="http://caprf.example.com/log"
export INIT_URL="http://caprf.example.com/status/init"
export ERROR_URL="http://caprf.example.com/status/error"
export SUCCESS_URL="http://caprf.example.com/status/success"
export DEBUG_URL="http://caprf.example.com/debug"
`

	cfg, err := ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.ImageURLs) != 2 {
		t.Fatalf("expected 2 image URLs, got %d", len(cfg.ImageURLs))
	}
	if cfg.ImageURLs[0] != "http://example.com/image1.gz" {
		t.Fatalf("unexpected image URL 0: %s", cfg.ImageURLs[0])
	}
	if cfg.Hostname != "worker-01" {
		t.Fatalf("unexpected hostname: %s", cfg.Hostname)
	}
	if cfg.Token != "test-token-123" {
		t.Fatalf("unexpected token: %s", cfg.Token)
	}
	if cfg.ExtraKernelParams != "console=ttyS0 net.ifnames=0" {
		t.Fatalf("unexpected kernel params: %s", cfg.ExtraKernelParams)
	}
	if cfg.FailureDomain != "zone-a" {
		t.Fatalf("unexpected failure domain: %s", cfg.FailureDomain)
	}
	if cfg.Region != "eu-central-1" {
		t.Fatalf("unexpected region: %s", cfg.Region)
	}
	if cfg.ProviderID != "redfish://bmc.example.com/Systems/1" {
		t.Fatalf("unexpected provider ID: %s", cfg.ProviderID)
	}
	if cfg.Mode != "provision" {
		t.Fatalf("unexpected mode: %s", cfg.Mode)
	}
	if cfg.MinDiskSizeGB != 100 {
		t.Fatalf("unexpected min disk size: %d", cfg.MinDiskSizeGB)
	}
	if cfg.LogURL != "http://caprf.example.com/log" {
		t.Fatalf("unexpected log URL: %s", cfg.LogURL)
	}
	if cfg.InitURL != "http://caprf.example.com/status/init" {
		t.Fatalf("unexpected init URL: %s", cfg.InitURL)
	}
}

func TestParseVarsEmpty(t *testing.T) {
	cfg, err := ParseVars(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hostname != "" {
		t.Fatalf("expected empty hostname, got %s", cfg.Hostname)
	}
}

func TestParseVarsComments(t *testing.T) {
	input := `# This is a comment
export HOSTNAME="test-host"
# Another comment
`
	cfg, err := ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hostname != "test-host" {
		t.Fatalf("expected test-host, got %s", cfg.Hostname)
	}
}

func TestClientReportStatus(t *testing.T) {
	ts := newTestServer(t)

	cfg := &config.MachineConfig{
		Token:      "my-token",
		InitURL:    ts.server.URL + "/status/init",
		SuccessURL: ts.server.URL + "/status/success",
		ErrorURL:   ts.server.URL + "/status/error",
		LogURL:     ts.server.URL + "/log",
		DebugURL:   ts.server.URL + "/debug",
	}

	client := NewFromConfig(cfg)
	ctx := context.Background()

	// Report init.
	if err := client.ReportStatus(ctx, config.StatusInit, "starting"); err != nil {
		t.Fatal(err)
	}

	// Report success.
	if err := client.ReportStatus(ctx, config.StatusSuccess, "done"); err != nil {
		t.Fatal(err)
	}

	// Report error.
	if err := client.ReportStatus(ctx, config.StatusError, "failed"); err != nil {
		t.Fatal(err)
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()

	// Check statuses: each status + auth header.
	expectedStatuses := []string{"init", "auth:Bearer my-token", "success", "auth:Bearer my-token", "error", "auth:Bearer my-token"}
	if len(ts.statuses) != len(expectedStatuses) {
		t.Fatalf("expected %d status entries, got %d: %v", len(expectedStatuses), len(ts.statuses), ts.statuses)
	}
	for i, exp := range expectedStatuses {
		if ts.statuses[i] != exp {
			t.Errorf("status[%d] = %q, want %q", i, ts.statuses[i], exp)
		}
	}
}

func TestClientShipLog(t *testing.T) {
	ts := newTestServer(t)

	cfg := &config.MachineConfig{
		Token:  "log-token",
		LogURL: ts.server.URL + "/log",
	}

	client := NewFromConfig(cfg)
	if err := client.ShipLog(context.Background(), "test log line"); err != nil {
		t.Fatal(err)
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()

	if len(ts.logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(ts.logs))
	}
	if ts.logs[0] != "Bearer log-token" {
		t.Fatalf("expected auth header, got %s", ts.logs[0])
	}
}

func TestClientHeartbeatNoop(t *testing.T) {
	client := NewFromConfig(&config.MachineConfig{})
	if err := client.Heartbeat(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestClientFetchCommandsNoop(t *testing.T) {
	client := NewFromConfig(&config.MachineConfig{})
	cmds, err := client.FetchCommands(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cmds != nil {
		t.Fatalf("expected nil commands, got %v", cmds)
	}
}

func TestClientNoURLSkips(t *testing.T) {
	client := NewFromConfig(&config.MachineConfig{})

	// ShipLog with no URL should be a no-op.
	if err := client.ShipLog(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}

	// ShipDebug with no URL should be a no-op.
	if err := client.ShipDebug(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}

	// ReportStatus with no URL should warn but not error.
	if err := client.ReportStatus(context.Background(), config.StatusInit, "test"); err != nil {
		t.Fatal(err)
	}
}

func TestParseVarsWithoutExport(t *testing.T) {
	input := `HOSTNAME="bare-host"
TOKEN="bare-token"
`
	cfg, err := ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hostname != "bare-host" {
		t.Fatalf("expected bare-host, got %s", cfg.Hostname)
	}
	if cfg.Token != "bare-token" {
		t.Fatalf("expected bare-token, got %s", cfg.Token)
	}
}

// Compile-time check that Client implements config.Provider.
var _ config.Provider = (*Client)(nil)
