package caprf

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/telekom/BOOTy/pkg/config"
)

func TestRemoteHandlerShipsLogs(t *testing.T) {
	ts := newTestServer(t)

	cfg := &config.MachineConfig{
		Token:  "handler-token",
		LogURL: ts.server.URL + "/log",
	}
	client := NewFromConfig(cfg)

	inner := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewRemoteHandler(client, inner, slog.LevelInfo, 100)

	logger := slog.New(handler)
	logger.Info("test message", "key", "value")

	// Close flushes the buffer.
	handler.Close()

	ts.mu.Lock()
	defer ts.mu.Unlock()

	if len(ts.logs) != 1 {
		t.Fatalf("expected 1 log shipped, got %d", len(ts.logs))
	}
}

func TestRemoteHandlerLevelFilter(t *testing.T) {
	ts := newTestServer(t)

	cfg := &config.MachineConfig{
		Token:  "filter-token",
		LogURL: ts.server.URL + "/log",
	}
	client := NewFromConfig(cfg)

	inner := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	// Only ship WARN and above.
	handler := NewRemoteHandler(client, inner, slog.LevelWarn, 100)

	logger := slog.New(handler)
	logger.Info("should be filtered")
	logger.Warn("should pass")

	handler.Close()

	ts.mu.Lock()
	defer ts.mu.Unlock()

	if len(ts.logs) != 1 {
		t.Fatalf("expected 1 log (warn only), got %d", len(ts.logs))
	}
}

func TestRemoteHandlerDropsWhenFull(t *testing.T) {
	cfg := &config.MachineConfig{
		Token: "drop-token",
		// Use a non-existent URL so we can test buffer behavior without draining.
	}
	client := NewFromConfig(cfg)

	// Very small buffer to trigger overflow.
	var mu sync.Mutex
	var shipped int

	// We'll use a real handler but with a logger that counts.
	inner := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := &RemoteHandler{
		client: client,
		inner:  inner,
		level:  slog.LevelInfo,
		buf:    make(chan string, 2), // Only 2 slots.
		done:   make(chan struct{}),
	}

	// Count messages that make it through.
	go func() {
		defer close(handler.done)
		for range handler.buf {
			mu.Lock()
			shipped++
			mu.Unlock()
		}
	}()

	logger := slog.New(handler)
	// Fire more logs than buffer can hold rapidly.
	for i := range 10 {
		logger.Info("message", "i", i)
	}

	handler.Close()

	mu.Lock()
	defer mu.Unlock()

	// At least some should have been delivered, but not necessarily all 10.
	if shipped == 0 {
		t.Fatal("expected at least some messages to be shipped")
	}
	t.Logf("shipped %d of 10 messages", shipped)
}

func TestRemoteHandlerWithAttrsAndGroups(t *testing.T) {
	ts := newTestServer(t)

	cfg := &config.MachineConfig{
		Token:  "attrs-token",
		LogURL: ts.server.URL + "/log",
	}
	client := NewFromConfig(cfg)

	inner := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := NewRemoteHandler(client, inner, slog.LevelInfo, 100)

	logger := slog.New(handler).With("component", "test").WithGroup("sub")
	logger.Info("grouped message")

	handler.Close()

	ts.mu.Lock()
	defer ts.mu.Unlock()

	if len(ts.logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(ts.logs))
	}
}

func TestRemoteHandlerEnabled(t *testing.T) {
	handler := &RemoteHandler{level: slog.LevelWarn}

	if handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("expected Info to be disabled at Warn level")
	}
	if !handler.Enabled(context.Background(), slog.LevelWarn) {
		t.Fatal("expected Warn to be enabled at Warn level")
	}
	if !handler.Enabled(context.Background(), slog.LevelError) {
		t.Fatal("expected Error to be enabled at Warn level")
	}
}

func TestRemoteHandlerCloseIdempotent(t *testing.T) {
	cfg := &config.MachineConfig{}
	client := NewFromConfig(cfg)
	inner := slog.NewTextHandler(os.Stderr, nil)
	handler := NewRemoteHandler(client, inner, slog.LevelInfo, 10)

	// Close multiple times should not panic.
	done := make(chan struct{})
	go func() {
		handler.Close()
		handler.Close()
		handler.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close() deadlocked")
	}
}
