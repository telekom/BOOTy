//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/telekom/BOOTy/pkg/caprf"
	"github.com/telekom/BOOTy/pkg/config"
)

func TestRescueModeVarsParsing(t *testing.T) {
	vars := "export MODE=\"rescue\"\n" +
		"export HOSTNAME=\"rescue-host\"\n" +
		"export RESCUE_SSH_PUBKEY=\"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest admin@ops\"\n" +
		"export RESCUE_PASSWORD_HASH=\"$6$rounds=5000$salt$hash\"\n" +
		"export RESCUE_TIMEOUT=\"3600\"\n" +
		"export RESCUE_AUTO_MOUNT=\"true\"\n" +
		"export INIT_URL=\"http://srv/status/init\"\n" +
		"export ERROR_URL=\"http://srv/status/error\"\n" +
		"export SUCCESS_URL=\"http://srv/status/success\"\n"

	cfg, err := caprf.ParseVars(strings.NewReader(vars))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Mode != "rescue" {
		t.Errorf("Mode = %q, want %q", cfg.Mode, "rescue")
	}
	if cfg.Hostname != "rescue-host" {
		t.Errorf("Hostname = %q, want %q", cfg.Hostname, "rescue-host")
	}
	if cfg.RescueSSHPubKey != "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest admin@ops" {
		t.Errorf("RescueSSHPubKey = %q", cfg.RescueSSHPubKey)
	}
	if cfg.RescuePasswordHash != "$6$rounds=5000$salt$hash" {
		t.Errorf("RescuePasswordHash = %q", cfg.RescuePasswordHash)
	}
	if cfg.RescueTimeout != 3600 {
		t.Errorf("RescueTimeout = %d, want 3600", cfg.RescueTimeout)
	}
	if !cfg.RescueAutoMountDisks {
		t.Error("RescueAutoMountDisks = false, want true")
	}
}

func TestRescueModeStatusReporting(t *testing.T) {
	var (
		mu       sync.Mutex
		statuses []string
	)
	received := make(chan struct{}, 2)

	mux := http.NewServeMux()
	mux.HandleFunc("/status/init", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		statuses = append(statuses, "init:"+string(body))
		mu.Unlock()
		received <- struct{}{}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/heartbeat", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		statuses = append(statuses, "heartbeat")
		mu.Unlock()
		received <- struct{}{}
		w.WriteHeader(http.StatusOK)
	})
	base := startServer(t, mux)

	vars := "export MODE=\"rescue\"\n" +
		"export HOSTNAME=\"rescue-e2e\"\n" +
		"export INIT_URL=\"" + base + "/status/init\"\n" +
		"export HEARTBEAT_URL=\"" + base + "/heartbeat\"\n"

	cfg, err := caprf.ParseVars(strings.NewReader(vars))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "rescue" {
		t.Fatalf("Mode = %q, want rescue", cfg.Mode)
	}

	client := caprf.NewFromConfig(cfg)
	ctx := context.Background()

	if err := client.ReportStatus(ctx, config.StatusInit, "rescue-mode-active"); err != nil {
		t.Fatalf("ReportStatus: %v", err)
	}
	if err := client.Heartbeat(ctx); err != nil {
		t.Errorf("Heartbeat failed: %v", err)
	}

	deadline := time.After(2 * time.Second)
	count := 0
	for count < 2 {
		select {
		case <-received:
			count++
		case <-deadline:
			t.Fatal("timed out waiting for status and heartbeat calls")
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if len(statuses) == 0 {
		t.Fatal("no status calls received by mock server")
	}
	if !strings.HasPrefix(statuses[0], "init:") {
		t.Errorf("first status = %q, want init:...", statuses[0])
	}
}

func TestRescueModeCommandPolling(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/commands", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		cmds := []map[string]string{
			{"ID": "cmd-1", "Type": "reboot"},
		}
		data, _ := json.Marshal(cmds)
		_, _ = w.Write(data)
	})

	base := startServer(t, mux)

	vars := "export MODE=\"rescue\"\n" +
		"export HOSTNAME=\"rescue-cmd\"\n" +
		"export COMMANDS_URL=\"" + base + "/commands\"\n"

	cfg, err := caprf.ParseVars(strings.NewReader(vars))
	if err != nil {
		t.Fatal(err)
	}

	client := caprf.NewFromConfig(cfg)
	cmds, err := client.FetchCommands(context.Background())
	if err != nil {
		t.Fatalf("FetchCommands: %v", err)
	}

	found := false
	for _, cmd := range cmds {
		if cmd.Type == "reboot" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected reboot command from CAPRF, not found")
	}
}
