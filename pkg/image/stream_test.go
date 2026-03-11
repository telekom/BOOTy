package image

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/ulikunitz/xz"
)

func TestStreamRaw(t *testing.T) {
	data := []byte("raw image content for testing")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	tmpFile, err := os.CreateTemp(t.TempDir(), "disk-*")
	if err != nil {
		t.Fatal(err)
	}
	_ = tmpFile.Close()

	err = Stream(context.Background(), srv.URL+"/image.img", tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Errorf("got %q, want %q", got, data)
	}
}

func TestStreamGzip(t *testing.T) {
	data := []byte("gzipped image content for testing")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		gz := gzip.NewWriter(w)
		_, _ = gz.Write(data)
		_ = gz.Close()
	}))
	defer srv.Close()

	tmpFile, err := os.CreateTemp(t.TempDir(), "disk-*")
	if err != nil {
		t.Fatal(err)
	}
	_ = tmpFile.Close()

	// Magic bytes detect gzip regardless of URL suffix now.
	err = Stream(context.Background(), srv.URL+"/image.img", tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Errorf("got %q, want %q", got, data)
	}
}

func TestStreamZstd(t *testing.T) {
	data := []byte("zstd image content for testing")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		zw, _ := zstd.NewWriter(w)
		_, _ = zw.Write(data)
		_ = zw.Close()
	}))
	defer srv.Close()

	tmpFile, err := os.CreateTemp(t.TempDir(), "disk-*")
	if err != nil {
		t.Fatal(err)
	}
	_ = tmpFile.Close()

	err = Stream(context.Background(), srv.URL+"/image.zst", tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Errorf("got %q, want %q", got, data)
	}
}

func TestStreamLZ4(t *testing.T) {
	data := []byte("lz4 image content for testing")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		lw := lz4.NewWriter(w)
		_, _ = lw.Write(data)
		_ = lw.Close()
	}))
	defer srv.Close()

	tmpFile, err := os.CreateTemp(t.TempDir(), "disk-*")
	if err != nil {
		t.Fatal(err)
	}
	_ = tmpFile.Close()

	err = Stream(context.Background(), srv.URL+"/image.lz4", tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Errorf("got %q, want %q", got, data)
	}
}

func TestStreamXZ(t *testing.T) {
	data := []byte("xz image content for testing")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		xw, _ := xz.NewWriter(w)
		_, _ = xw.Write(data)
		_ = xw.Close()
	}))
	defer srv.Close()

	tmpFile, err := os.CreateTemp(t.TempDir(), "disk-*")
	if err != nil {
		t.Fatal(err)
	}
	_ = tmpFile.Close()

	err = Stream(context.Background(), srv.URL+"/image.xz", tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Errorf("got %q, want %q", got, data)
	}
}

func TestStreamChecksumPass(t *testing.T) {
	data := []byte("data for checksum test")
	h := sha256.Sum256(data)
	checksum := hex.EncodeToString(h[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	tmpFile, err := os.CreateTemp(t.TempDir(), "disk-*")
	if err != nil {
		t.Fatal(err)
	}
	_ = tmpFile.Close()

	err = Stream(context.Background(), srv.URL+"/image.img", tmpFile.Name(), StreamOpts{
		Checksum:     checksum,
		ChecksumType: "sha256",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStreamChecksumMismatch(t *testing.T) {
	data := []byte("data for checksum mismatch test")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	tmpFile, err := os.CreateTemp(t.TempDir(), "disk-*")
	if err != nil {
		t.Fatal(err)
	}
	_ = tmpFile.Close()

	err = Stream(context.Background(), srv.URL+"/image.img", tmpFile.Name(), StreamOpts{
		Checksum:     "0000000000000000000000000000000000000000000000000000000000000000",
		ChecksumType: "sha256",
	})
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
}

func TestStreamNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	tmpFile, err := os.CreateTemp(t.TempDir(), "disk-*")
	if err != nil {
		t.Fatal(err)
	}
	_ = tmpFile.Close()

	err = Stream(context.Background(), srv.URL+"/missing.img", tmpFile.Name())
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

func TestStreamServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	tmpFile, err := os.CreateTemp(t.TempDir(), "disk-*")
	if err != nil {
		t.Fatal(err)
	}
	_ = tmpFile.Close()

	err = Stream(context.Background(), srv.URL+"/image.img", tmpFile.Name())
	if err == nil {
		t.Fatal("expected error for 500")
	}
}

func TestStreamCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Stream(ctx, "http://127.0.0.1:1/image.img", "/dev/null")
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestIsOCIReference(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"oci://ghcr.io/org/image:latest", true},
		{"http://example.com/image.gz", false},
		{"https://example.com/image.gz", false},
		{"oci://registry.example.com/repo@sha256:abc", true},
	}
	for _, tt := range tests {
		got := IsOCIReference(tt.url)
		if got != tt.want {
			t.Errorf("IsOCIReference(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestTrimOCIScheme(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"oci://ghcr.io/org/image:latest", "ghcr.io/org/image:latest"},
		{"ghcr.io/org/image:latest", "ghcr.io/org/image:latest"},
	}
	for _, tt := range tests {
		got := TrimOCIScheme(tt.url)
		if got != tt.want {
			t.Errorf("TrimOCIScheme(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}
