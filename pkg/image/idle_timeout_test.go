package image

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPImageDownloadAbortsWhenServerStalls(t *testing.T) {
	orig := imageIdleReadTimeout
	t.Cleanup(func() { imageIdleReadTimeout = orig })
	imageIdleReadTimeout = 100 * time.Millisecond

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 4096))
		w.(http.Flusher).Flush()
		select { // stall without closing the connection
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)

	body, err := httpGetWithRetry(context.Background(), srv.URL+"/image.raw")
	if err != nil {
		t.Fatalf("httpGetWithRetry() = %v", err)
	}
	defer func() { _ = body.Close() }()

	done := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(body)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "stalled") {
			t.Fatalf("ReadAll() error = %v, want a stall error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("download of a stalled image did not abort")
	}
}

func TestHTTPImageDownloadKeepsSlowButSteadyStream(t *testing.T) {
	orig := imageIdleReadTimeout
	t.Cleanup(func() { imageIdleReadTimeout = orig })
	imageIdleReadTimeout = 200 * time.Millisecond

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for range 5 {
			_, _ = w.Write(make([]byte, 1024))
			w.(http.Flusher).Flush()
			time.Sleep(80 * time.Millisecond)
		}
	}))
	defer srv.Close()

	body, err := httpGetWithRetry(context.Background(), srv.URL+"/image.raw")
	if err != nil {
		t.Fatalf("httpGetWithRetry() = %v", err)
	}
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(body)
	if err != nil || len(data) != 5*1024 {
		t.Fatalf("ReadAll() = %d bytes, %v; want 5120 bytes", len(data), err)
	}
}
