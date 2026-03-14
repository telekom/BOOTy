//go:build e2e && linux

package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/caprf"
	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
	"github.com/telekom/BOOTy/pkg/provision"
)

func TestDryRunFullPipelinePassE2E(t *testing.T) {
	// Set up a working image server that responds to HEAD requests.
	imgServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", "1024")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer imgServer.Close()

	cmd := newMockCommander()
	cfg := &config.MachineConfig{
		Mode:      "dry-run",
		DryRun:    true,
		Hostname:  "dry-node-01",
		ImageURLs: []string{imgServer.URL + "/test.img.gz"},
	}
	provider := newMockProvider(cfg)
	diskMgr := disk.NewManager(cmd)
	orch := provision.NewOrchestrator(cfg, provider, diskMgr)

	err := orch.DryRun(context.Background())
	if err != nil {
		t.Fatalf("expected dry-run to pass, got: %v", err)
	}

	statuses := provider.getStatuses()
	if len(statuses) == 0 {
		t.Fatal("expected status reports from dry-run")
	}
	last := statuses[len(statuses)-1]
	if last.Status != config.StatusSuccess {
		t.Errorf("expected success status, got %q: %s", last.Status, last.Message)
	}
	if !strings.Contains(last.Message, "dry-run passed") {
		t.Errorf("expected pass message, got %q", last.Message)
	}
}

func TestDryRunNoImagesFailE2E(t *testing.T) {
	cmd := newMockCommander()
	cfg := &config.MachineConfig{
		Mode:     "dry-run",
		DryRun:   true,
		Hostname: "dry-node-02",
		// No ImageURLs — should fail config validation.
	}
	provider := newMockProvider(cfg)
	diskMgr := disk.NewManager(cmd)
	orch := provision.NewOrchestrator(cfg, provider, diskMgr)

	err := orch.DryRun(context.Background())
	if err == nil {
		t.Fatal("expected dry-run to fail with no images configured")
	}

	statuses := provider.getStatuses()
	if len(statuses) == 0 {
		t.Fatal("expected error status report")
	}
	last := statuses[len(statuses)-1]
	if last.Status != config.StatusError {
		t.Errorf("expected error status, got %q", last.Status)
	}
}

func TestDryRunVarsParsingE2E(t *testing.T) {
	vars := `export MODE="provision"
export IMAGE="http://example.com/image.gz"
export HOSTNAME="dry-test"
export DRY_RUN="true"
export TOKEN="test-token"
`
	cfg, err := caprf.ParseVars(strings.NewReader(vars))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DryRun {
		t.Error("expected DryRun=true")
	}
}

func TestDryRunOCIImageSkipE2E(t *testing.T) {
	cmd := newMockCommander()
	cfg := &config.MachineConfig{
		Mode:     "dry-run",
		DryRun:   true,
		Hostname: "oci-node",
		ImageURLs: []string{
			"oci://registry.example.com/image:latest",
		},
	}
	provider := newMockProvider(cfg)
	diskMgr := disk.NewManager(cmd)
	orch := provision.NewOrchestrator(cfg, provider, diskMgr)

	err := orch.DryRun(context.Background())
	if err != nil {
		t.Fatalf("dry-run should pass with OCI URLs (skip reachability): %v", err)
	}
}

func TestDryRunImageUnreachableE2E(t *testing.T) {
	cmd := newMockCommander()
	cfg := &config.MachineConfig{
		Mode:     "dry-run",
		DryRun:   true,
		Hostname: "unreachable-node",
		ImageURLs: []string{
			"http://192.0.2.1:1/unreachable.img.gz",
		},
	}
	provider := newMockProvider(cfg)
	diskMgr := disk.NewManager(cmd)
	orch := provision.NewOrchestrator(cfg, provider, diskMgr)

	err := orch.DryRun(context.Background())
	if err == nil {
		t.Fatal("expected dry-run to fail with unreachable image")
	}
}
