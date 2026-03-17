package caprf

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/health"
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

func TestClientReportInventory(t *testing.T) {
	var receivedBody []byte
	var receivedContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewFromConfig(&config.MachineConfig{
		Token:        "test-token",
		InventoryURL: srv.URL + "/inventory",
	})

	data := []byte(`{"system":{"vendor":"Dell"}}`)
	if err := client.ReportInventory(context.Background(), data); err != nil {
		t.Fatalf("ReportInventory() error: %v", err)
	}
	if receivedContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", receivedContentType)
	}
	if string(receivedBody) != string(data) {
		t.Errorf("body = %q, want %q", receivedBody, data)
	}
}

func TestClientReportInventoryNoURL(t *testing.T) {
	client := NewFromConfig(&config.MachineConfig{})
	// Should be a no-op when no URL is configured.
	if err := client.ReportInventory(context.Background(), []byte(`{}`)); err != nil {
		t.Fatalf("ReportInventory() with no URL should not error: %v", err)
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

func TestNew(t *testing.T) {
	// Create a temporary vars file.
	f, err := os.CreateTemp(t.TempDir(), "vars-*")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`export HOSTNAME="test-via-file"
export TOKEN="file-token"
export MODE="provision"
`)
	f.Close()

	client, err := New(f.Name())
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := client.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hostname != "test-via-file" {
		t.Fatalf("expected test-via-file, got %s", cfg.Hostname)
	}
	if cfg.Token != "file-token" {
		t.Fatalf("expected file-token, got %s", cfg.Token)
	}
}

func TestNewFileNotFound(t *testing.T) {
	_, err := New("/nonexistent/path/to/vars")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestParseVarsNetworkTuning(t *testing.T) {
	input := `export asn_server="64497"
export provision_vni="2001000"
export underlay_subnet="10.50.0.0/16"
export overlay_subnet="fd21:0cc2:0981::/64"
export vrf_table_id="10"
export bgp_keepalive="30"
export bgp_hold="90"
export bfd_transmit_ms="150"
export bfd_receive_ms="150"
export dcgw_ips="10.10.10.1,10.10.10.2"
export vpn_rt="64497:1000"
`
	cfg, err := ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ASN != 64497 {
		t.Errorf("ASN = %d, want 64497", cfg.ASN)
	}
	if cfg.ProvisionVNI != 2001000 {
		t.Errorf("ProvisionVNI = %d, want 2001000", cfg.ProvisionVNI)
	}
	if cfg.VRFTableID != 10 {
		t.Errorf("VRFTableID = %d, want 10", cfg.VRFTableID)
	}
	if cfg.BGPKeepalive != 30 {
		t.Errorf("BGPKeepalive = %d, want 30", cfg.BGPKeepalive)
	}
	if cfg.BGPHold != 90 {
		t.Errorf("BGPHold = %d, want 90", cfg.BGPHold)
	}
	if cfg.BFDTransmitMS != 150 {
		t.Errorf("BFDTransmitMS = %d, want 150", cfg.BFDTransmitMS)
	}
	if cfg.BFDReceiveMS != 150 {
		t.Errorf("BFDReceiveMS = %d, want 150", cfg.BFDReceiveMS)
	}
	if cfg.DCGWIPs != "10.10.10.1,10.10.10.2" {
		t.Errorf("DCGWIPs = %q, want %q", cfg.DCGWIPs, "10.10.10.1,10.10.10.2")
	}
	if cfg.VPNRT != "64497:1000" {
		t.Errorf("VPNRT = %q, want %q", cfg.VPNRT, "64497:1000")
	}
}

func TestGetConfig(t *testing.T) {
	cfg := &config.MachineConfig{
		Hostname: "config-host",
		Mode:     "provision",
	}
	client := NewFromConfig(cfg)
	got, err := client.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != cfg {
		t.Fatal("expected same config pointer")
	}
}

func TestClientShipDebug(t *testing.T) {
	ts := newTestServer(t)

	cfg := &config.MachineConfig{
		Token:    "debug-token",
		DebugURL: ts.server.URL + "/debug",
	}

	client := NewFromConfig(cfg)
	if err := client.ShipDebug(context.Background(), "debug message"); err != nil {
		t.Fatal(err)
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()

	if len(ts.debugs) != 1 {
		t.Fatalf("expected 1 debug entry, got %d", len(ts.debugs))
	}
	if ts.debugs[0] != "Bearer debug-token" {
		t.Fatalf("expected auth header, got %s", ts.debugs[0])
	}
}

func TestReportStatusUnknown(t *testing.T) {
	client := NewFromConfig(&config.MachineConfig{})
	err := client.ReportStatus(context.Background(), config.Status("invalid"), "msg")
	if err == nil {
		t.Fatal("expected error for unknown status")
	}
	if !strings.Contains(err.Error(), "unknown status") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPostWithAuthNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	cfg := &config.MachineConfig{
		InitURL: srv.URL + "/status/init",
	}
	client := NewFromConfig(cfg)
	err := client.ReportStatus(context.Background(), config.StatusInit, "test")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseVarsInvalidDiskSize(t *testing.T) {
	input := `export MIN_DISK_SIZE_GB="notanumber"
export HOSTNAME="host"
`
	cfg, err := ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	// Invalid number should be silently ignored (stays 0).
	if cfg.MinDiskSizeGB != 0 {
		t.Fatalf("expected 0 for invalid disk size, got %d", cfg.MinDiskSizeGB)
	}
	if cfg.Hostname != "host" {
		t.Fatalf("expected host, got %s", cfg.Hostname)
	}
}

func TestParseVarsLineWithoutEquals(t *testing.T) {
	input := `export NOSEPARATOR
export HOSTNAME="works"
`
	cfg, err := ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hostname != "works" {
		t.Fatalf("expected works, got %s", cfg.Hostname)
	}
}

// Compile-time check that Client implements config.Provider.
var _ config.Provider = (*Client)(nil)

func TestParseVarsNetworkFields(t *testing.T) {
	input := `underlay_subnet="192.168.4.0/24"
underlay_ip="10.50.12.13"
overlay_subnet="2a01:598:40a:5481::/64"
ipmi_subnet="172.30.0.0/24"
asn_server="65188"
provision_vni="2002002"
provision_ip="10.100.0.42/24"
dns_resolver="2003:0:af08:1005::1000"
dcgw_ips="10.10.10.1,10.10.10.2"
leaf_asn="65500"
local_asn="65501"
overlay_aggregate="2a01:598:40a:5481::/64"
vpn_rt="65188:2002"
`
	cfg, err := ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UnderlaySubnet != "192.168.4.0/24" {
		t.Errorf("UnderlaySubnet = %q", cfg.UnderlaySubnet)
	}
	if cfg.UnderlayIP != "10.50.12.13" {
		t.Errorf("UnderlayIP = %q", cfg.UnderlayIP)
	}
	if cfg.OverlaySubnet != "2a01:598:40a:5481::/64" {
		t.Errorf("OverlaySubnet = %q", cfg.OverlaySubnet)
	}
	if cfg.IPMISubnet != "172.30.0.0/24" {
		t.Errorf("IPMISubnet = %q", cfg.IPMISubnet)
	}
	if cfg.ASN != 65188 {
		t.Errorf("ASN = %d", cfg.ASN)
	}
	if cfg.ProvisionVNI != 2002002 {
		t.Errorf("ProvisionVNI = %d", cfg.ProvisionVNI)
	}
	if cfg.ProvisionIP != "10.100.0.42/24" {
		t.Errorf("ProvisionIP = %q", cfg.ProvisionIP)
	}
	if cfg.DNSResolvers != "2003:0:af08:1005::1000" {
		t.Errorf("DNSResolvers = %q", cfg.DNSResolvers)
	}
	if cfg.DCGWIPs != "10.10.10.1,10.10.10.2" {
		t.Errorf("DCGWIPs = %q", cfg.DCGWIPs)
	}
	if cfg.LeafASN != 65500 {
		t.Errorf("LeafASN = %d", cfg.LeafASN)
	}
	if cfg.LocalASN != 65501 {
		t.Errorf("LocalASN = %d", cfg.LocalASN)
	}
	if cfg.OverlayAggregate != "2a01:598:40a:5481::/64" {
		t.Errorf("OverlayAggregate = %q", cfg.OverlayAggregate)
	}
	if cfg.VPNRT != "65188:2002" {
		t.Errorf("VPNRT = %q", cfg.VPNRT)
	}
}

func TestParseVarsNetworkMode(t *testing.T) {
	input := `NETWORK_MODE="gobgp"
`
	cfg, err := ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NetworkMode != "gobgp" {
		t.Errorf("NetworkMode = %q, want gobgp", cfg.NetworkMode)
	}
}

func TestParseVarsBGPPeering(t *testing.T) {
	input := `BGP_PEER_MODE="dual"
BGP_NEIGHBORS="10.0.0.1,10.0.0.2"
bgp_remote_asn="65100"
`
	cfg, err := ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BGPPeerMode != "dual" {
		t.Errorf("BGPPeerMode = %q, want dual", cfg.BGPPeerMode)
	}
	if cfg.BGPNeighbors != "10.0.0.1,10.0.0.2" {
		t.Errorf("BGPNeighbors = %q, want 10.0.0.1,10.0.0.2", cfg.BGPNeighbors)
	}
	if cfg.BGPRemoteASN != 65100 {
		t.Errorf("BGPRemoteASN = %d, want 65100", cfg.BGPRemoteASN)
	}
}

func TestClientHeartbeat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewFromConfig(&config.MachineConfig{HeartbeatURL: srv.URL})
	if err := client.Heartbeat(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestClientFetchCommands(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"ID":"cmd-1","Type":"provision","Payload":null}]`))
	}))
	defer srv.Close()

	client := NewFromConfig(&config.MachineConfig{CommandsURL: srv.URL})
	cmds, err := client.FetchCommands(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	if cmds[0].ID != "cmd-1" || cmds[0].Type != "provision" {
		t.Errorf("unexpected command: %+v", cmds[0])
	}
}

func TestClientFetchCommandsNoContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := NewFromConfig(&config.MachineConfig{CommandsURL: srv.URL})
	cmds, err := client.FetchCommands(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cmds != nil {
		t.Errorf("expected nil commands on 204, got %v", cmds)
	}
}

func TestClientFetchCommandsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewFromConfig(&config.MachineConfig{CommandsURL: srv.URL})
	_, err := client.FetchCommands(context.Background())
	if err == nil {
		t.Error("expected error on 500")
	}
}

func TestParseVarsAgentURLs(t *testing.T) {
	input := `HOSTNAME="standby-host"
HEARTBEAT_URL="http://server/status/heartbeat"
COMMANDS_URL="http://server/commands"
`
	cfg, err := ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HeartbeatURL != "http://server/status/heartbeat" {
		t.Errorf("HeartbeatURL = %q", cfg.HeartbeatURL)
	}
	if cfg.CommandsURL != "http://server/commands" {
		t.Errorf("CommandsURL = %q", cfg.CommandsURL)
	}
}

func TestParseVarsStaticNetworking(t *testing.T) {
	input := `STATIC_IP="10.0.0.5/24"
STATIC_GATEWAY="10.0.0.1"
STATIC_IFACE="eth0"
`
	cfg, err := ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StaticIP != "10.0.0.5/24" {
		t.Errorf("StaticIP = %q, want %q", cfg.StaticIP, "10.0.0.5/24")
	}
	if cfg.StaticGateway != "10.0.0.1" {
		t.Errorf("StaticGateway = %q, want %q", cfg.StaticGateway, "10.0.0.1")
	}
	if cfg.StaticIface != "eth0" {
		t.Errorf("StaticIface = %q, want %q", cfg.StaticIface, "eth0")
	}
}

func TestParseVarsBondConfig(t *testing.T) {
	input := `BOND_INTERFACES="eth0,eth1"
BOND_MODE="802.3ad"
`
	cfg, err := ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BondInterfaces != "eth0,eth1" {
		t.Errorf("BondInterfaces = %q, want %q", cfg.BondInterfaces, "eth0,eth1")
	}
	if cfg.BondMode != "802.3ad" {
		t.Errorf("BondMode = %q, want %q", cfg.BondMode, "802.3ad")
	}
}

func TestParseVarsSecureErase(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{`SECURE_ERASE="true"`, true},
		{`SECURE_ERASE="1"`, true},
		{`SECURE_ERASE="yes"`, true},
		{`SECURE_ERASE="false"`, false},
		{`SECURE_ERASE="0"`, false},
	}
	for _, tt := range tests {
		cfg, err := ParseVars(strings.NewReader(tt.input))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.SecureErase != tt.want {
			t.Errorf("SecureErase for %q = %v, want %v", tt.input, cfg.SecureErase, tt.want)
		}
	}
}

func TestParseVarsPostProvisionCmds(t *testing.T) {
	input := `POST_PROVISION_CMDS="apt update;systemctl enable foo;echo done"`
	cfg, err := ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.PostProvisionCmds) != 3 {
		t.Fatalf("PostProvisionCmds len = %d, want 3", len(cfg.PostProvisionCmds))
	}
	if cfg.PostProvisionCmds[0] != "apt update" {
		t.Errorf("cmd[0] = %q, want %q", cfg.PostProvisionCmds[0], "apt update")
	}
	if cfg.PostProvisionCmds[2] != "echo done" {
		t.Errorf("cmd[2] = %q, want %q", cfg.PostProvisionCmds[2], "echo done")
	}
}

func TestParseVarsImageChecksum(t *testing.T) {
	input := `IMAGE_CHECKSUM="abc123def456"
IMAGE_CHECKSUM_TYPE="sha256"
`
	cfg, err := ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ImageChecksum != "abc123def456" {
		t.Errorf("ImageChecksum = %q", cfg.ImageChecksum)
	}
	if cfg.ImageChecksumType != "sha256" {
		t.Errorf("ImageChecksumType = %q", cfg.ImageChecksumType)
	}
}

func TestParseVarsNumVFs(t *testing.T) {
	input := `NUM_VFS="64"`
	cfg, err := ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NumVFs != 64 {
		t.Errorf("NumVFs = %d, want 64", cfg.NumVFs)
	}
}

func TestParseVarsDisableKexec(t *testing.T) {
	input := `DISABLE_KEXEC="true"`
	cfg, err := ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DisableKexec {
		t.Error("DisableKexec should be true")
	}
}

func TestParseVarsVLANs(t *testing.T) {
	input := `VLANS="200:eno1:10.200.0.42/24,300:eno2"`
	cfg, err := ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VLANs != "200:eno1:10.200.0.42/24,300:eno2" {
		t.Errorf("VLANs = %q, want %q", cfg.VLANs, "200:eno1:10.200.0.42/24,300:eno2")
	}
}

func TestParseVarsInventory(t *testing.T) {
	input := `INVENTORY_ENABLED="true"
INVENTORY_URL="http://caprf.example.com/inventory"`
	cfg, err := ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.InventoryEnabled {
		t.Error("InventoryEnabled should be true")
	}
	if cfg.InventoryURL != "http://caprf.example.com/inventory" {
		t.Errorf("InventoryURL = %q", cfg.InventoryURL)
	}
}

func TestParseVarsFirmwareConfig(t *testing.T) {
	input := strings.Join([]string{
		`FIRMWARE_REPORT="true"`,
		`FIRMWARE_URL="http://caprf/firmware"`,
		`FIRMWARE_MIN_BIOS="U50"`,
		`FIRMWARE_MIN_BMC="2.72"`,
	}, "\n")
	cfg, err := ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.FirmwareEnabled {
		t.Error("FirmwareEnabled should be true")
	}
	if cfg.FirmwareURL != "http://caprf/firmware" {
		t.Errorf("FirmwareURL = %q", cfg.FirmwareURL)
	}
	if cfg.FirmwareMinBIOS != "U50" {
		t.Errorf("FirmwareMinBIOS = %q", cfg.FirmwareMinBIOS)
	}
	if cfg.FirmwareMinBMC != "2.72" {
		t.Errorf("FirmwareMinBMC = %q", cfg.FirmwareMinBMC)
	}
}

func TestReportFirmware(t *testing.T) {
	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		received = body
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewFromConfig(&config.MachineConfig{
		Token:       "test-token",
		FirmwareURL: srv.URL,
	})

	data := []byte(`{"bios":{"version":"U50"}}`)
	if err := client.ReportFirmware(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	if string(received) != string(data) {
		t.Errorf("received = %q, want %q", received, data)
	}
}

func TestReportFirmwareNoURL(t *testing.T) {
	client := NewFromConfig(&config.MachineConfig{})
	err := client.ReportFirmware(context.Background(), []byte(`{}`))
	if err != nil {
		t.Errorf("expected nil error when no URL, got %v", err)
	}
}

func TestParseBoolVar(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"true", true},
		{"TRUE", true},
		{"True", true},
		{"1", true},
		{"yes", true},
		{"YES", true},
		{"Yes", true},
		{" true ", true},
		{"false", false},
		{"0", false},
		{"", false},
		{"no", false},
	}
	for _, tt := range tests {
		got := parseBoolVar(tt.input)
		if got != tt.want {
			t.Errorf("parseBoolVar(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParseVarsHealthChecks(t *testing.T) {
	input := `HEALTH_CHECKS_ENABLED="true"
HEALTH_MIN_MEMORY_GB="16"
HEALTH_MIN_CPUS="4"
HEALTH_SKIP_CHECKS="disk-ioerr,thermal-state"
HEALTH_CHECK_URL="http://caprf.example.com/health"
`
	cfg, err := ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HealthChecksEnabled {
		t.Error("HealthChecksEnabled should be true")
	}
	if cfg.HealthMinMemoryGB != 16 {
		t.Errorf("HealthMinMemoryGB = %d, want 16", cfg.HealthMinMemoryGB)
	}
	if cfg.HealthMinCPUs != 4 {
		t.Errorf("HealthMinCPUs = %d, want 4", cfg.HealthMinCPUs)
	}
	if cfg.HealthSkipChecks != "disk-ioerr,thermal-state" {
		t.Errorf("HealthSkipChecks = %q", cfg.HealthSkipChecks)
	}
	if cfg.HealthCheckURL != "http://caprf.example.com/health" {
		t.Errorf("HealthCheckURL = %q", cfg.HealthCheckURL)
	}
}

func TestParseVarsHealthChecksDisabled(t *testing.T) {
	input := `HEALTH_CHECKS_ENABLED="false"`
	cfg, err := ParseVars(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HealthChecksEnabled {
		t.Error("HealthChecksEnabled should be false")
	}
}

func TestParseVarsDryRun(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{`DRY_RUN="true"`, true},
		{`DRY_RUN="TRUE"`, true},
		{`DRY_RUN="1"`, true},
		{`DRY_RUN="false"`, false},
		{`DRY_RUN="0"`, false},
	}
	for _, tt := range tests {
		cfg, err := ParseVars(strings.NewReader(tt.input))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.DryRun != tt.want {
			t.Errorf("ParseVars(%q): DryRun = %v, want %v", tt.input, cfg.DryRun, tt.want)
		}
	}
}

func TestClientReportHealthChecks(t *testing.T) {
	var receivedBody string
	var receivedContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewFromConfig(&config.MachineConfig{HealthCheckURL: srv.URL})
	results := []health.CheckResult{
		{Name: "disk-presence", Status: "pass", Severity: "critical", Message: "ok"},
	}
	err := client.ReportHealthChecks(context.Background(), results)
	if err != nil {
		t.Fatal(err)
	}

	if receivedContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", receivedContentType)
	}

	if !strings.Contains(receivedBody, "disk-presence") {
		t.Errorf("body does not contain expected check name: %s", receivedBody)
	}
}

func TestClientReportHealthChecksNoURL(t *testing.T) {
	client := NewFromConfig(&config.MachineConfig{})
	err := client.ReportHealthChecks(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestClientAcknowledgeCommand(t *testing.T) {
	var receivedMethod, receivedPath, receivedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewFromConfig(&config.MachineConfig{CommandsURL: srv.URL + "/commands"})
	err := client.AcknowledgeCommand(context.Background(), "cmd-123", "completed", "done")
	if err != nil {
		t.Fatal(err)
	}

	if receivedMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", receivedMethod)
	}
	if receivedPath != "/commands/ack" {
		t.Errorf("path = %q, want /commands/ack", receivedPath)
	}
	if !strings.Contains(receivedBody, `"id":"cmd-123"`) {
		t.Errorf("body missing cmd ID: %s", receivedBody)
	}
	if !strings.Contains(receivedBody, `"status":"completed"`) {
		t.Errorf("body missing status: %s", receivedBody)
	}
}

func TestClientAcknowledgeCommandNoURL(t *testing.T) {
	client := NewFromConfig(&config.MachineConfig{})
	err := client.AcknowledgeCommand(context.Background(), "cmd-1", "completed", "")
	if err != nil {
		t.Fatal(err)
	}
}

func TestParseVarsTPMEnabled(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{`TPM_ENABLED="true"`, true},
		{`TPM_ENABLED="1"`, true},
		{`TPM_ENABLED="yes"`, true},
		{`TPM_ENABLED=" True "`, true},
		{`TPM_ENABLED="false"`, false},
		{`TPM_ENABLED="no"`, false},
	}
	for _, tt := range tests {
		cfg, err := ParseVars(strings.NewReader(tt.input))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.TPMEnabled != tt.want {
			t.Errorf("TPMEnabled for %q = %v, want %v", tt.input, cfg.TPMEnabled, tt.want)
		}
	}
}
