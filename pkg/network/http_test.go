package network

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestWaitForHTTPRedactsErrorURLInDebugLogs(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() {
		slog.SetDefault(previous)
	})

	target := "http://leaky-user:super-secret@127.0.0.1:1/image.raw?token=abc#frag"
	err := WaitForHTTP(context.Background(), target, 20*time.Millisecond)
	if err == nil {
		t.Fatal("expected connectivity timeout")
	}

	got := logs.String()
	for _, leaked := range []string{"leaky-user", "super-secret", "token=abc", "frag"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("logs leaked %q: %s", leaked, got)
		}
	}
	if !strings.Contains(got, "http://127.0.0.1:1/image.raw") {
		t.Fatalf("logs = %q, want redacted URL context", got)
	}
}

func TestWaitForHTTPRedactsCreateRequestErrors(t *testing.T) {
	target := "http://leaky-user:super-secret@%zz/image.raw?token=abc#frag"
	err := WaitForHTTP(context.Background(), target, time.Second)
	if err == nil {
		t.Fatal("expected create request error")
	}
	for _, leaked := range []string{"leaky-user", "super-secret", "token=abc", "frag"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("error leaked %q: %s", leaked, err.Error())
		}
	}
	if !strings.Contains(err.Error(), "[redacted invalid URL]") {
		t.Fatalf("error = %q, want redacted invalid URL context", err.Error())
	}
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		t.Fatalf("error does not preserve wrapped URL error: %v", err)
	}
}
