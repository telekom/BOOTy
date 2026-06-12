//go:build linux

package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/network"
)

type connectivityTestMode struct {
	gotTarget string
}

func (m *connectivityTestMode) Setup(context.Context, *network.Config) error { return nil }

func (m *connectivityTestMode) WaitForConnectivity(_ context.Context, target string, _ time.Duration) error {
	m.gotTarget = target
	return nil
}

func (m *connectivityTestMode) Teardown(context.Context) error { return nil }

func TestEnsureNetworkConnectivityRedactsTargetInLogs(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
	})

	target := "http://leaky-user:super-secret@example.com/init?token=abc#frag"
	mode := &connectivityTestMode{}
	_, err := ensureNetworkConnectivity(context.Background(), &config.MachineConfig{}, mode, target)
	if err != nil {
		t.Fatalf("ensureNetworkConnectivity() error = %v", err)
	}
	if mode.gotTarget != target {
		t.Fatalf("WaitForConnectivity target = %q, want raw target passed through", mode.gotTarget)
	}

	got := logs.String()
	for _, leaked := range []string{"leaky-user", "super-secret", "token=abc", "frag"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("logs leaked %q: %s", leaked, got)
		}
	}
	if !strings.Contains(got, "http://example.com/init") {
		t.Fatalf("logs = %q, want redacted target context", got)
	}
}
