//go:build e2e

package e2e

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/telekom/BOOTy/pkg/caprf"
	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/kexec"
	"github.com/telekom/BOOTy/pkg/network"
)

// --- helpers ---

func startServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "http://" + ln.Addr().String()
}

// statusRecorder records status calls with their auth headers.
type statusRecorder struct {
	mu       sync.Mutex
	statuses []string
}

func (r *statusRecorder) record(status, auth string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statuses = append(r.statuses, status+":"+auth)
}

func (r *statusRecorder) get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.statuses))
	copy(out, r.statuses)
	return out
}

// --- Feature Gate: Vars Parsing ---

func TestVarsParsingAllFields(t *testing.T) {
	vars := `export IMAGE="http://img1.gz http://img2.gz"
export HOSTNAME="node-42"
export TOKEN="secret-token"
export MODE="provision"
export MIN_DISK_SIZE_GB="100"
export DISABLE_KEXEC="true"
export MACHINE_EXTRA_KERNEL_PARAMS="console=ttyS0 audit=0"
export FAILURE_DOMAIN="az-1"
export REGION="eu-west"
export PROVIDER_ID="redfish://192.168.1.1/Systems/1"
export LOG_URL="http://srv/log"
export INIT_URL="http://srv/status/init"
export ERROR_URL="http://srv/status/error"
export SUCCESS_URL="http://srv/status/success"
export DEBUG_URL="http://srv/debug"
underlay_subnet="10.0.0.0/24"
underlay_ip="10.0.0.5"
overlay_subnet="fd00::/64"
ipmi_subnet="172.16.0.0/24"
asn_server="65001"
provision_vni="10100"
dns_resolver="8.8.8.8,1.1.1.1"
dcgw_ips="10.99.0.1,10.99.0.2"
leaf_asn="65100"
local_asn="65200"
overlay_aggregate="fd00::/48"
vpn_rt="65001:10100"
`
	cfg, err := caprf.ParseVars(strings.NewReader(vars))
	if err != nil {
		t.Fatal(err)
	}

	// Multiple images split on whitespace.
	if len(cfg.ImageURLs) != 2 {
		t.Fatalf("expected 2 image URLs, got %d", len(cfg.ImageURLs))
	}
	if cfg.ImageURLs[0] != "http://img1.gz" || cfg.ImageURLs[1] != "http://img2.gz" {
		t.Errorf("imageURLs = %v", cfg.ImageURLs)
	}

	// String fields.
	checks := map[string]string{
		"Hostname":          cfg.Hostname,
		"Token":             cfg.Token,
		"Mode":              cfg.Mode,
		"ExtraKernelParams": cfg.ExtraKernelParams,
		"FailureDomain":     cfg.FailureDomain,
		"Region":            cfg.Region,
		"ProviderID":        cfg.ProviderID,
		"UnderlaySubnet":    cfg.UnderlaySubnet,
		"UnderlayIP":        cfg.UnderlayIP,
		"OverlaySubnet":     cfg.OverlaySubnet,
		"IPMISubnet":        cfg.IPMISubnet,
		"DNSResolvers":      cfg.DNSResolvers,
		"DCGWIPs":           cfg.DCGWIPs,
		"OverlayAggregate":  cfg.OverlayAggregate,
		"VPNRT":             cfg.VPNRT,
	}
	expected := map[string]string{
		"Hostname":          "node-42",
		"Token":             "secret-token",
		"Mode":              "provision",
		"ExtraKernelParams": "console=ttyS0 audit=0",
		"FailureDomain":     "az-1",
		"Region":            "eu-west",
		"ProviderID":        "redfish://192.168.1.1/Systems/1",
		"UnderlaySubnet":    "10.0.0.0/24",
		"UnderlayIP":        "10.0.0.5",
		"OverlaySubnet":     "fd00::/64",
		"IPMISubnet":        "172.16.0.0/24",
		"DNSResolvers":      "8.8.8.8,1.1.1.1",
		"DCGWIPs":           "10.99.0.1,10.99.0.2",
		"OverlayAggregate":  "fd00::/48",
		"VPNRT":             "65001:10100",
	}
	for k, got := range checks {
		if got != expected[k] {
			t.Errorf("%s = %q, want %q", k, got, expected[k])
		}
	}

	// Uint32 fields.
	if cfg.ASN != 65001 {
		t.Errorf("ASN = %d, want 65001", cfg.ASN)
	}
	if cfg.ProvisionVNI != 10100 {
		t.Errorf("ProvisionVNI = %d, want 10100", cfg.ProvisionVNI)
	}
	if cfg.LeafASN != 65100 {
		t.Errorf("LeafASN = %d, want 65100", cfg.LeafASN)
	}
	if cfg.LocalASN != 65200 {
		t.Errorf("LocalASN = %d, want 65200", cfg.LocalASN)
	}

	// Feature gates.
	if cfg.MinDiskSizeGB != 100 {
		t.Errorf("MinDiskSizeGB = %d, want 100", cfg.MinDiskSizeGB)
	}
	if !cfg.DisableKexec {
		t.Error("DisableKexec should be true")
	}
}

func TestVarsParsingDisableKexecVariants(t *testing.T) {
	for _, val := range []string{"true", "1", "yes"} {
		cfg, err := caprf.ParseVars(strings.NewReader(`DISABLE_KEXEC="` + val + `"` + "\n"))
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.DisableKexec {
			t.Errorf("DISABLE_KEXEC=%q should enable disable-kexec", val)
		}
	}
	for _, val := range []string{"false", "0", "no", ""} {
		cfg, err := caprf.ParseVars(strings.NewReader(`DISABLE_KEXEC="` + val + `"` + "\n"))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.DisableKexec {
			t.Errorf("DISABLE_KEXEC=%q should NOT enable disable-kexec", val)
		}
	}
}

func TestVarsParsingMinDiskSizeGBDefault(t *testing.T) {
	cfg, err := caprf.ParseVars(strings.NewReader(`export HOSTNAME="test"` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MinDiskSizeGB != 0 {
		t.Errorf("MinDiskSizeGB should default to 0, got %d", cfg.MinDiskSizeGB)
	}
}

func TestVarsParsingModeVariants(t *testing.T) {
	modes := []string{"provision", "deprovision", "soft-deprovision"}
	for _, mode := range modes {
		cfg, err := caprf.ParseVars(strings.NewReader(`export MODE="` + mode + `"` + "\n"))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Mode != mode {
			t.Errorf("Mode = %q, want %q", cfg.Mode, mode)
		}
	}
}

// --- Feature Gate: Network Mode Detection ---

func TestNetworkModeDetectionFRR(t *testing.T) {
	netCfg := &network.Config{
		UnderlaySubnet: "10.0.0.0/24",
		ASN:            65001,
	}
	if !netCfg.IsFRRMode() {
		t.Error("should be FRR mode when UnderlaySubnet and ASN are set")
	}
}

func TestNetworkModeDetectionFRRWithUnderlayIP(t *testing.T) {
	netCfg := &network.Config{
		UnderlayIP: "10.0.0.5",
		ASN:        65001,
	}
	if !netCfg.IsFRRMode() {
		t.Error("should be FRR mode when UnderlayIP and ASN are set")
	}
}

func TestNetworkModeDetectionDHCPNoASN(t *testing.T) {
	netCfg := &network.Config{
		UnderlaySubnet: "10.0.0.0/24",
	}
	if netCfg.IsFRRMode() {
		t.Error("should not be FRR mode without ASN")
	}
}

func TestNetworkModeDetectionDHCPNoUnderlay(t *testing.T) {
	netCfg := &network.Config{
		ASN: 65001,
	}
	if netCfg.IsFRRMode() {
		t.Error("should not be FRR mode without underlay config")
	}
}

func TestNetworkModeDetectionDHCPEmpty(t *testing.T) {
	netCfg := &network.Config{}
	if netCfg.IsFRRMode() {
		t.Error("empty config should default to DHCP")
	}
}

// --- CAPRF Client: Status Reporting E2E ---

func TestCAPRFProvisioningStatusFlow(t *testing.T) {
	rec := &statusRecorder{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /status/init", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.record("init", r.Header.Get("Authorization")+"|"+string(body))
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /status/success", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.record("success", r.Header.Get("Authorization")+"|"+string(body))
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /status/error", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.record("error", r.Header.Get("Authorization")+"|"+string(body))
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /log", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srvURL := startServer(t, mux)
	cfg := &config.MachineConfig{
		Token:      "prov-token",
		InitURL:    srvURL + "/status/init",
		SuccessURL: srvURL + "/status/success",
		ErrorURL:   srvURL + "/status/error",
		LogURL:     srvURL + "/log",
	}
	client := caprf.NewFromConfig(cfg)
	ctx := context.Background()

	// Simulate provisioning lifecycle: init → success.
	if err := client.ReportStatus(ctx, config.StatusInit, "starting"); err != nil {
		t.Fatal(err)
	}
	if err := client.ShipLog(ctx, "provisioning step 1"); err != nil {
		t.Fatal(err)
	}
	if err := client.ReportStatus(ctx, config.StatusSuccess, "done"); err != nil {
		t.Fatal(err)
	}

	statuses := rec.get()
	if len(statuses) != 2 {
		t.Fatalf("expected 2 status calls, got %d: %v", len(statuses), statuses)
	}
	if !strings.Contains(statuses[0], "init:Bearer prov-token|starting") {
		t.Errorf("unexpected init: %s", statuses[0])
	}
	if !strings.Contains(statuses[1], "success:Bearer prov-token|done") {
		t.Errorf("unexpected success: %s", statuses[1])
	}
}

func TestCAPRFDeprovisionStatusFlow(t *testing.T) {
	rec := &statusRecorder{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /status/init", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.record("init", string(body))
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /status/success", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.record("success", string(body))
		w.WriteHeader(http.StatusOK)
	})

	srvURL := startServer(t, mux)
	cfg := &config.MachineConfig{
		Token:      "deprov-token",
		InitURL:    srvURL + "/status/init",
		SuccessURL: srvURL + "/status/success",
		Mode:       "deprovision",
	}
	client := caprf.NewFromConfig(cfg)
	ctx := context.Background()

	// Deprovision reports init then success.
	if err := client.ReportStatus(ctx, config.StatusInit, "deprovisioning"); err != nil {
		t.Fatal(err)
	}
	if err := client.ReportStatus(ctx, config.StatusSuccess, "wiped"); err != nil {
		t.Fatal(err)
	}

	statuses := rec.get()
	if len(statuses) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(statuses))
	}
	if !strings.HasPrefix(statuses[0], "init:") {
		t.Errorf("first should be init: %s", statuses[0])
	}
	if !strings.HasPrefix(statuses[1], "success:") {
		t.Errorf("second should be success: %s", statuses[1])
	}
}

func TestCAPRFErrorStatusFlow(t *testing.T) {
	rec := &statusRecorder{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /status/init", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.record("init", string(body))
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /status/error", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.record("error", string(body))
		w.WriteHeader(http.StatusOK)
	})

	srvURL := startServer(t, mux)
	cfg := &config.MachineConfig{
		Token:    "err-token",
		InitURL:  srvURL + "/status/init",
		ErrorURL: srvURL + "/status/error",
	}
	client := caprf.NewFromConfig(cfg)
	ctx := context.Background()

	if err := client.ReportStatus(ctx, config.StatusInit, "starting"); err != nil {
		t.Fatal(err)
	}
	if err := client.ReportStatus(ctx, config.StatusError, "disk not found"); err != nil {
		t.Fatal(err)
	}

	statuses := rec.get()
	if len(statuses) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(statuses))
	}
	if !strings.Contains(statuses[1], "error:disk not found") {
		t.Errorf("error status should contain message: %s", statuses[1])
	}
}

func TestCAPRFNoURLSkipsStatus(t *testing.T) {
	cfg := &config.MachineConfig{} // no URLs set
	client := caprf.NewFromConfig(cfg)
	ctx := context.Background()

	// Should not error — just skips.
	if err := client.ReportStatus(ctx, config.StatusInit, "starting"); err != nil {
		t.Fatal(err)
	}
	if err := client.ShipLog(ctx, "log line"); err != nil {
		t.Fatal(err)
	}
}

func TestCAPRFServerRejectsAuth(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /status/init", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	srvURL := startServer(t, mux)
	cfg := &config.MachineConfig{
		Token:   "bad-token",
		InitURL: srvURL + "/status/init",
	}
	client := caprf.NewFromConfig(cfg)
	ctx := context.Background()

	err := client.ReportStatus(ctx, config.StatusInit, "start")
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected 403 in error, got: %v", err)
	}
}

// --- Feature Gate: Kexec Grub Parsing ---

func TestKexecGrubParsingMenuentry(t *testing.T) {
	grubCfg := `
set default="0"
menuentry 'Ubuntu' {
	linux /boot/vmlinuz root=/dev/sda1 ro
	initrd /boot/initrd.img
}
menuentry 'Recovery' {
	linux /boot/vmlinuz root=/dev/sda1 ro single
	initrd /boot/initrd.img
}
`
	entries, err := kexec.ParseGrubCfg(strings.NewReader(grubCfg))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Name != "Ubuntu" {
		t.Errorf("first entry name = %q", entries[0].Name)
	}
	if entries[0].Kernel != "/boot/vmlinuz" {
		t.Errorf("kernel = %q", entries[0].Kernel)
	}
	if entries[0].KernelArgs != "root=/dev/sda1 ro" {
		t.Errorf("args = %q", entries[0].KernelArgs)
	}
	if entries[0].Initramfs != "/boot/initrd.img" {
		t.Errorf("initrd = %q", entries[0].Initramfs)
	}
}

func TestKexecGrubDefaultEntry(t *testing.T) {
	grubCfg := `
menuentry 'Ubuntu 22.04 LTS' {
	linux /vmlinuz root=UUID=abc-123 ro quiet splash
	initrd /initrd.img
}
`
	entries, err := kexec.ParseGrubCfg(strings.NewReader(grubCfg))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := kexec.GetDefaultEntry(entries)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != "Ubuntu 22.04 LTS" {
		t.Errorf("default entry = %q", entry.Name)
	}
}

func TestKexecGrubEmptyConfig(t *testing.T) {
	entries, err := kexec.ParseGrubCfg(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	_, err = kexec.GetDefaultEntry(entries)
	if err == nil {
		t.Error("expected error for empty grub config")
	}
}

// --- Feature Gate: Network Config Defaults ---

func TestNetworkConfigDefaults(t *testing.T) {
	cfg := &network.Config{}
	cfg.ApplyDefaults()
	if cfg.MTU != 9000 {
		t.Errorf("default MTU = %d, want 9000", cfg.MTU)
	}
}

// --- CAPRF Integration: Vars File → Client → Status Reporting ---

func TestVarsToClientEndToEnd(t *testing.T) {
	// Simulate the real flow: parse vars → create client → report status.
	rec := &statusRecorder{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /init", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.record("init", r.Header.Get("Authorization")+"|"+string(body))
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /success", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.record("success", r.Header.Get("Authorization")+"|"+string(body))
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /log", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srvURL := startServer(t, mux)

	vars := `export IMAGE="http://images.example.com/ubuntu-22.04.gz"
export HOSTNAME="e2e-node-01"
export TOKEN="e2e-bearer-token"
export MODE="provision"
export MIN_DISK_SIZE_GB="50"
export INIT_URL="` + srvURL + `/init"
export SUCCESS_URL="` + srvURL + `/success"
export LOG_URL="` + srvURL + `/log"
underlay_subnet="10.0.0.0/24"
asn_server="65001"
dns_resolver="8.8.8.8"
`

	cfg, err := caprf.ParseVars(strings.NewReader(vars))
	if err != nil {
		t.Fatal(err)
	}

	// Verify parsed config.
	if cfg.Hostname != "e2e-node-01" {
		t.Errorf("hostname = %q", cfg.Hostname)
	}
	if cfg.MinDiskSizeGB != 50 {
		t.Errorf("MinDiskSizeGB = %d", cfg.MinDiskSizeGB)
	}

	// Network mode should be FRR.
	netCfg := &network.Config{
		UnderlaySubnet: cfg.UnderlaySubnet,
		ASN:            cfg.ASN,
	}
	if !netCfg.IsFRRMode() {
		t.Error("expected FRR mode for this config")
	}

	// Create client and use it for status reporting.
	client := caprf.NewFromConfig(cfg)
	ctx := context.Background()

	if err := client.ReportStatus(ctx, config.StatusInit, "provisioning started"); err != nil {
		t.Fatal(err)
	}
	if err := client.ShipLog(ctx, "streaming image..."); err != nil {
		t.Fatal(err)
	}
	if err := client.ReportStatus(ctx, config.StatusSuccess, "provisioning complete"); err != nil {
		t.Fatal(err)
	}

	statuses := rec.get()
	if len(statuses) != 2 {
		t.Fatalf("expected 2 status reports, got %d: %v", len(statuses), statuses)
	}
	if !strings.Contains(statuses[0], "Bearer e2e-bearer-token") {
		t.Errorf("init should carry bearer token: %s", statuses[0])
	}
	if !strings.Contains(statuses[0], "provisioning started") {
		t.Errorf("init should carry message: %s", statuses[0])
	}
	if !strings.Contains(statuses[1], "provisioning complete") {
		t.Errorf("success should carry message: %s", statuses[1])
	}
}

// --- CAPRF Client: Heartbeat and FetchCommands are no-ops ---

func TestCAPRFHeartbeatAndFetchCommandsNoop(t *testing.T) {
	client := caprf.NewFromConfig(&config.MachineConfig{})
	ctx := context.Background()

	if err := client.Heartbeat(ctx); err != nil {
		t.Errorf("Heartbeat should be no-op: %v", err)
	}
	cmds, err := client.FetchCommands(ctx)
	if err != nil {
		t.Errorf("FetchCommands should be no-op: %v", err)
	}
	if cmds != nil {
		t.Errorf("FetchCommands should return nil, got %v", cmds)
	}
}

// --- Feature Gate: DHCP Mode is Default ---

func TestDHCPModeSetupTeardownNoop(t *testing.T) {
	mode := &network.DHCPMode{}
	ctx := context.Background()

	if err := mode.Setup(ctx, nil); err != nil {
		t.Errorf("DHCP Setup should be no-op: %v", err)
	}
	if err := mode.Teardown(ctx); err != nil {
		t.Errorf("DHCP Teardown should be no-op: %v", err)
	}
}

func TestDHCPModeWaitForConnectivity(t *testing.T) {
	// Start a test server that responds to GET.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srvURL := startServer(t, mux)

	mode := &network.DHCPMode{}
	ctx := context.Background()

	if err := mode.WaitForConnectivity(ctx, srvURL, 5*time.Second); err != nil {
		t.Errorf("DHCP connectivity should succeed: %v", err)
	}
}

// --- Feature Gate: Provider Interface Contract ---

func TestCAPRFClientImplementsProviderInterface(t *testing.T) {
	// Compile-time verification that *caprf.Client satisfies config.Provider.
	var _ config.Provider = caprf.NewFromConfig(&config.MachineConfig{})
}

// --- Feature Gate: Kexec with Extra Kernel Params ---

func TestGrubParsingWithExtraKernelParams(t *testing.T) {
	grubCfg := `
menuentry 'Ubuntu' {
	linux /boot/vmlinuz-5.15.0-91 root=UUID=abc-123 ro console=ttyS0,115200 quiet
	initrd /boot/initrd.img-5.15.0-91
}
`
	entries, err := kexec.ParseGrubCfg(strings.NewReader(grubCfg))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if !strings.Contains(e.KernelArgs, "console=ttyS0,115200") {
		t.Errorf("kernel args should contain console param: %q", e.KernelArgs)
	}
	if !strings.Contains(e.KernelArgs, "root=UUID=abc-123") {
		t.Errorf("kernel args should contain root param: %q", e.KernelArgs)
	}
	if e.Kernel != "/boot/vmlinuz-5.15.0-91" {
		t.Errorf("kernel path = %q", e.Kernel)
	}
	if e.Initramfs != "/boot/initrd.img-5.15.0-91" {
		t.Errorf("initrd path = %q", e.Initramfs)
	}
}

// --- Feature Gate: Multiple Image URLs ---

func TestMultipleImageURLsParsing(t *testing.T) {
	vars := `export IMAGE="http://img1.gz http://img2.gz http://img3.gz"` + "\n"
	cfg, err := caprf.ParseVars(strings.NewReader(vars))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ImageURLs) != 3 {
		t.Fatalf("expected 3 image URLs, got %d: %v", len(cfg.ImageURLs), cfg.ImageURLs)
	}
}

func TestSingleImageURLParsing(t *testing.T) {
	vars := `export IMAGE="http://single.gz"` + "\n"
	cfg, err := caprf.ParseVars(strings.NewReader(vars))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ImageURLs) != 1 || cfg.ImageURLs[0] != "http://single.gz" {
		t.Fatalf("expected 1 image URL, got %v", cfg.ImageURLs)
	}
}

// --- Feature Gate: Debug URL ---

func TestCAPRFDebugEndpoint(t *testing.T) {
	var gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /debug", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	})

	srvURL := startServer(t, mux)
	cfg := &config.MachineConfig{
		Token:    "dbg-token",
		DebugURL: srvURL + "/debug",
	}
	client := caprf.NewFromConfig(cfg)
	ctx := context.Background()

	if err := client.ShipDebug(ctx, "debug payload"); err != nil {
		t.Fatal(err)
	}
	if gotBody != "debug payload" {
		t.Errorf("debug body = %q, want %q", gotBody, "debug payload")
	}
}

// --- Feature Gate: Unknown Status is Rejected ---

func TestCAPRFUnknownStatusRejected(t *testing.T) {
	cfg := &config.MachineConfig{
		InitURL: "http://localhost:1/init",
	}
	client := caprf.NewFromConfig(cfg)
	ctx := context.Background()

	err := client.ReportStatus(ctx, config.Status("unknown"), "msg")
	if err == nil {
		t.Fatal("expected error for unknown status")
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Errorf("error should mention unknown status: %v", err)
	}
}

// --- Config/Provider: Status Constants ---

func TestStatusConstants(t *testing.T) {
	if config.StatusInit != "init" {
		t.Errorf("StatusInit = %q", config.StatusInit)
	}
	if config.StatusSuccess != "success" {
		t.Errorf("StatusSuccess = %q", config.StatusSuccess)
	}
	if config.StatusError != "error" {
		t.Errorf("StatusError = %q", config.StatusError)
	}
}
