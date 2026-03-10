//go:build e2e_integration

package integration

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/telekom/BOOTy/pkg/caprf"
	"github.com/telekom/BOOTy/pkg/config"
	redfishmock "github.com/telekom/BOOTy/test/e2e/redfish"
)

func TestRedfishMockPowerCycle(t *testing.T) {
	mock := redfishmock.NewMockServer(t)

	if got := mock.GetPowerState(); got != redfishmock.PowerOff {
		t.Fatalf("expected PowerOff, got %s", got)
	}

	postJSON(t, mock.URL()+"/redfish/v1/Systems/1/Actions/ComputerSystem.Reset",
		`{"ResetType":"On"}`)

	if got := mock.GetPowerState(); got != redfishmock.PowerOn {
		t.Fatalf("expected PowerOn, got %s", got)
	}
}

func TestRedfishMockVirtualMediaInsertEject(t *testing.T) {
	mock := redfishmock.NewMockServer(t)

	postJSON(t, mock.URL()+"/redfish/v1/Managers/1/VirtualMedia/CD1/Actions/VirtualMedia.InsertMedia",
		`{"Image":"http://boot.iso"}`)

	vm := mock.GetVirtualMedia()
	if !vm.Inserted || vm.Image != "http://boot.iso" {
		t.Fatalf("unexpected virtual media: %+v", vm)
	}

	postJSON(t, mock.URL()+"/redfish/v1/Managers/1/VirtualMedia/CD1/Actions/VirtualMedia.EjectMedia",
		`{}`)

	vm = mock.GetVirtualMedia()
	if vm.Inserted {
		t.Fatal("expected ejected")
	}
}

func TestCAPRFClientStatusReporting(t *testing.T) {
	statuses := make(chan string, 10)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /status/init", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		statuses <- "init:" + auth
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /status/success", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		statuses <- "success:" + auth
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /log", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srvURL := startTestServer(t, mux)

	cfg := &config.MachineConfig{
		Token:      "e2e-token",
		InitURL:    srvURL + "/status/init",
		SuccessURL: srvURL + "/status/success",
		LogURL:     srvURL + "/log",
	}
	client := caprf.NewFromConfig(cfg)
	ctx := context.Background()

	if err := client.ReportStatus(ctx, config.StatusInit, "starting"); err != nil {
		t.Fatal(err)
	}
	if err := client.ReportStatus(ctx, config.StatusSuccess, "done"); err != nil {
		t.Fatal(err)
	}

	select {
	case s := <-statuses:
		if s != "init:Bearer e2e-token" {
			t.Fatalf("unexpected init status: %s", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for init status")
	}

	select {
	case s := <-statuses:
		if s != "success:Bearer e2e-token" {
			t.Fatalf("unexpected success status: %s", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for success status")
	}
}

func TestCAPRFVarsParsingRoundTrip(t *testing.T) {
	vars := `export IMAGE="http://example.com/image.gz"
export HOSTNAME="e2e-test-host"
export TOKEN="round-trip-token"
export MODE="provision"
export MIN_DISK_SIZE_GB="50"
export LOG_URL="http://localhost:9999/log"
export INIT_URL="http://localhost:9999/status/init"
export ERROR_URL="http://localhost:9999/status/error"
export SUCCESS_URL="http://localhost:9999/status/success"
export DEBUG_URL="http://localhost:9999/debug"
`
	cfg, err := caprf.ParseVars(strings.NewReader(vars))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hostname != "e2e-test-host" {
		t.Fatalf("hostname = %q, want e2e-test-host", cfg.Hostname)
	}
	if cfg.Mode != "provision" {
		t.Fatalf("mode = %q, want provision", cfg.Mode)
	}
	if cfg.MinDiskSizeGB != 50 {
		t.Fatalf("min disk = %d, want 50", cfg.MinDiskSizeGB)
	}
	if len(cfg.ImageURLs) != 1 || cfg.ImageURLs[0] != "http://example.com/image.gz" {
		t.Fatalf("image URLs = %v", cfg.ImageURLs)
	}
}

func postJSON(t *testing.T, url, body string) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body)) //nolint:gosec // test URL
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
}

func startTestServer(t *testing.T, handler http.Handler) string {
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
