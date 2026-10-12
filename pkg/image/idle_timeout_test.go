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

func TestHTTPImageDownloadIgnoresSlowConsumer(t *testing.T) {
	orig := imageIdleReadTimeout
	t.Cleanup(func() { imageIdleReadTimeout = orig })
	imageIdleReadTimeout = 50 * time.Millisecond

	payload := make([]byte, 64<<10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	body, err := httpGetWithRetry(context.Background(), srv.URL+"/image.raw")
	if err != nil {
		t.Fatalf("httpGetWithRetry() = %v", err)
	}
	defer func() { _ = body.Close() }()

	buf := make([]byte, 1024)
	if _, err := body.Read(buf); err != nil {
		t.Fatalf("first Read() = %v", err)
	}
	// A consumer that takes longer than the idle timeout between reads,
	// like a slow device write, must not abort the download.
	<-time.After(4 * imageIdleReadTimeout)
	rest, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("ReadAll() after a slow consumer = %v", err)
	}
	if len(rest) == 0 {
		t.Fatal("no data after the slow consumer pause")
	}
}
