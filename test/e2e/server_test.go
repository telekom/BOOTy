//go:build e2e

package e2e

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/telekom/BOOTy/pkg/plunderclient/types"
)

// newTestServer returns an httptest.Server wired up with image upload,
// config serving, and static file serving endpoints.
func newTestServer(t *testing.T, dir string, config *types.BootyConfig) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	// Image upload handler — writes multipart form file to dir.
	mux.HandleFunc("/image", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		file, header, err := r.FormFile("BootyImage")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer func() { _ = file.Close() }()

		dst := filepath.Join(dir, header.Filename)
		out, err := os.Create(dst)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() { _ = out.Close() }()

		if _, err := io.Copy(out, file); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	// Config endpoint.
	if config != nil {
		data, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		mux.HandleFunc("/booty/test.bty", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(data)
		})
	}

	// Static file server for images directory.
	mux.Handle("/images/", http.StripPrefix("/images/", http.FileServer(http.Dir(dir))))

	return httptest.NewServer(mux)
}

func TestImageUploadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t, dir, nil)
	defer srv.Close()

	// Create random test data (1 KB).
	payload := make([]byte, 1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}

	// Upload via multipart form.
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("BootyImage", "test-upload.img")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(srv.URL+"/image", writer.FormDataContentType(), body) //nolint:gosec // test URL
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload failed: status %d", resp.StatusCode)
	}

	// Verify uploaded file matches.
	got, err := os.ReadFile(filepath.Join(dir, "test-upload.img"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Error("uploaded file content does not match original payload")
	}
}

func TestImageDownloadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t, dir, nil)
	defer srv.Close()

	// Write a file directly to the images directory.
	payload := []byte("this is a test disk image")
	if err := os.WriteFile(filepath.Join(dir, "download.img"), payload, 0o600); err != nil {
		t.Fatal(err)
	}

	// Download via static file server.
	resp, err := http.Get(srv.URL + "/images/download.img") //nolint:gosec // test URL
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download failed: status %d", resp.StatusCode)
	}

	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Error("downloaded content does not match original file")
	}
}

func TestCompressedUploadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t, dir, nil)
	defer srv.Close()

	// Create test data and gzip it.
	original := []byte("repeated data for compression test - repeated data for compression test")
	var compressed bytes.Buffer
	gzw := gzip.NewWriter(&compressed)
	if _, err := gzw.Write(original); err != nil {
		t.Fatal(err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatal(err)
	}

	// Upload compressed data.
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("BootyImage", "test.zmg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(compressed.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(srv.URL+"/image", writer.FormDataContentType(), body) //nolint:gosec // test URL
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload failed: status %d", resp.StatusCode)
	}

	// Download and decompress.
	dlResp, err := http.Get(srv.URL + "/images/test.zmg") //nolint:gosec // test URL
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dlResp.Body.Close() }()

	gzr, err := gzip.NewReader(dlResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gzr.Close() }()

	decompressed, err := io.ReadAll(gzr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decompressed, original) {
		t.Error("decompressed content does not match original")
	}
}

func TestConfigEndpoint(t *testing.T) {
	dir := t.TempDir()
	cfg := &types.BootyConfig{
		Action:            types.WriteImage,
		SourceImage:       "http://example.com/test.img",
		DestinationDevice: "/dev/sda",
		DryRun:            true,
		DropToShell:       false,
		GrowPartition:     1,
	}
	srv := newTestServer(t, dir, cfg)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/booty/test.bty") //nolint:gosec // test URL
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("config fetch failed: status %d", resp.StatusCode)
	}

	var got types.BootyConfig
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	if got.Action != types.WriteImage {
		t.Errorf("expected action %q, got %q", types.WriteImage, got.Action)
	}
	if got.SourceImage != cfg.SourceImage {
		t.Errorf("expected sourceImage %q, got %q", cfg.SourceImage, got.SourceImage)
	}
	if got.DestinationDevice != cfg.DestinationDevice {
		t.Errorf("expected destinationDevice %q, got %q", cfg.DestinationDevice, got.DestinationDevice)
	}
	if !got.DryRun {
		t.Error("expected DryRun=true")
	}
}

func TestConfigNotFound(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t, dir, nil) // no config registered
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/booty/nonexistent.bty") //nolint:gosec // test URL
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK {
		t.Error("expected non-200 for missing config")
	}
}

func TestLargeImageUpload(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t, dir, nil)
	defer srv.Close()

	// Create 1 MB of random data.
	payload := make([]byte, 1<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("BootyImage", "large.img")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(srv.URL+"/image", writer.FormDataContentType(), body) //nolint:gosec // test URL
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload failed: status %d", resp.StatusCode)
	}

	got, err := os.ReadFile(filepath.Join(dir, "large.img"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(payload) {
		t.Errorf("size mismatch: got %d, want %d", len(got), len(payload))
	}
	if !bytes.Equal(got, payload) {
		t.Error("large file content does not match")
	}
}

func TestUploadDownloadIntegrity(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t, dir, nil)
	defer srv.Close()

	// Upload.
	payload := make([]byte, 4096)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("BootyImage", "integrity.img")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	uploadResp, err := http.Post(srv.URL+"/image", writer.FormDataContentType(), body) //nolint:gosec // test URL
	if err != nil {
		t.Fatal(err)
	}
	_ = uploadResp.Body.Close()

	// Download the same file.
	dlResp, err := http.Get(fmt.Sprintf("%s/images/integrity.img", srv.URL)) //nolint:gosec // test URL
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dlResp.Body.Close() }()

	downloaded, err := io.ReadAll(dlResp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(downloaded, payload) {
		t.Error("downloaded file does not match uploaded payload — data integrity failure")
	}
}
