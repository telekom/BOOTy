package caprf

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
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
	cfg := &config.MachineConfig{Token: "drop-token"}
	client := NewFromConfig(cfg)

	var mu sync.Mutex
	var shipped int

	inner := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	handler := &RemoteHandler{
		client:   client,
		inner:    inner,
		level:    slog.LevelInfo,
		buf:      make(chan string, 2),
		done:     make(chan struct{}),
		once:     &sync.Once{},
		dropped:  &atomic.Int64{},
		reported: &atomic.Int64{},
	}

	go func() {
		defer close(handler.done)
		for range handler.buf {
			mu.Lock()
			shipped++
			mu.Unlock()
		}
	}()

	logger := slog.New(handler)
	for i := range 10 {
		logger.Info("message", "i", i)
	}

	handler.Close()

	mu.Lock()
	defer mu.Unlock()

	if shipped == 0 {
		t.Fatal("expected at least some messages to be shipped")
	}
	t.Logf("shipped %d of 10 messages", shipped)
}

func TestDropCounterIncrementsOnFullBuffer(t *testing.T) {
	cfg := &config.MachineConfig{Token: "counter-token"}
	client := NewFromConfig(cfg)
	inner := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})

	buf := make(chan string, 2)
	done := make(chan struct{})
	blocker := make(chan struct{})

	handler := &RemoteHandler{
		client:   client,
		inner:    inner,
		level:    slog.LevelInfo,
		buf:      buf,
		done:     done,
		once:     &sync.Once{},
		dropped:  &atomic.Int64{},
		reported: &atomic.Int64{},
	}

	go func() {
		defer close(done)
		<-blocker
		for range buf {
			// drain the buffered channel so Close() can complete
		}
	}()

	logger := slog.New(handler)
	for i := range 10 {
		logger.Info("msg", "i", i)
	}

	n := handler.DroppedCount()
	if n == 0 {
		t.Fatal("expected drop counter > 0 when buffer is full and drain is blocked")
	}
	t.Logf("drop counter = %d after sending 10 messages to buffer-of-2", n)

	close(blocker)
	handler.Close()
}

func TestDropCounterAccessibleFromDerivedHandler(t *testing.T) {
	cfg := &config.MachineConfig{Token: "derived-counter-token"}
	client := NewFromConfig(cfg)
	inner := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})

	root := NewRemoteHandler(client, inner, slog.LevelInfo, 1)
	derived := root.WithAttrs([]slog.Attr{slog.String("k", "v")}).(*RemoteHandler)

	if root.dropped != derived.dropped {
		t.Fatal("expected root and derived handler to share the same drop counter")
	}
	if root.reported != derived.reported {
		t.Fatal("expected root and derived handler to share the same reported counter")
	}

	root.Close()
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

func TestRemoteHandlerCloseFromDerivedHandlers(t *testing.T) {
	ts := newTestServer(t)

	cfg := &config.MachineConfig{
		Token:  "derived-close-token",
		LogURL: ts.server.URL + "/log",
	}
	client := NewFromConfig(cfg)

	inner := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	root := NewRemoteHandler(client, inner, slog.LevelInfo, 10)
	derived := root.WithAttrs([]slog.Attr{slog.String("component", "test")}).(*RemoteHandler)

	logger := slog.New(derived)
	logger.Info("log from derived handler")

	root.Close()
	derived.Close()
}
