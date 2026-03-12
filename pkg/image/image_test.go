package image

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCounter(t *testing.T) {
	wc := &WriteCounter{}
	data := []byte("hello world")
	n, err := wc.Write(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(data) {
		t.Errorf("expected %d bytes written, got %d", len(data), n)
	}
	if wc.Total.Load() != uint64(len(data)) {
		t.Errorf("expected Total=%d, got %d", len(data), wc.Total.Load())
	}

	// Write more data
	n2, err := wc.Write(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n2 != len(data) {
		t.Errorf("expected %d bytes written, got %d", len(data), n2)
	}
	if wc.Total.Load() != uint64(2*len(data)) {
		t.Errorf("expected Total=%d, got %d", 2*len(data), wc.Total.Load())
	}
}

func TestWriteUncompressed(t *testing.T) {
	content := []byte("test image content for write")

	// Create a test HTTP server that serves the content
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(content)
	}))
	defer ts.Close()

	// Create a temp file as the destination device
	tmpDir := t.TempDir()
	destFile := filepath.Join(tmpDir, "disk.img")

	err := Write(ts.URL+"/image.img", destFile, false)
	if err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	// Verify written content
	got, err := os.ReadFile(destFile)
	if err != nil {
		t.Fatalf("error reading output: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("written content mismatch: got %q, want %q", got, content)
	}
}

func TestWriteCompressed(t *testing.T) {
	content := []byte("test image content for compressed write")

	// Gzip the content
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(buf.Bytes())
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	destFile := filepath.Join(tmpDir, "disk.img")

	err := Write(ts.URL+"/image.zmg", destFile, true)
	if err != nil {
		t.Fatalf("Write() error: %v", err)
	}

	got, err := os.ReadFile(destFile)
	if err != nil {
		t.Fatalf("error reading output: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("written content mismatch: got %q, want %q", got, content)
	}
}

func TestWrite404(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	destFile := filepath.Join(tmpDir, "disk.img")

	err := Write(ts.URL+"/missing.img", destFile, false)
	if err == nil {
		t.Fatal("expected error for 404 response, got nil")
	}
	if got := err.Error(); got != ts.URL+"/missing.img not found" {
		t.Errorf("unexpected error message: %s", got)
	}
}

func TestWriteServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	destFile := filepath.Join(tmpDir, "disk.img")

	err := Write(ts.URL+"/error.img", destFile, false)
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}

func TestWriteInvalidURL(t *testing.T) {
	tmpDir := t.TempDir()
	destFile := filepath.Join(tmpDir, "disk.img")

	err := Write("http://127.0.0.1:0/invalid", destFile, false)
	if err == nil {
		t.Fatal("expected error for unreachable server, got nil")
	}
}

func TestWriteBadDestination(t *testing.T) {
	content := []byte("data")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer ts.Close()

	err := Write(ts.URL+"/image.img", "/nonexistent/path/disk.img", false)
	if err == nil {
		t.Fatal("expected error for bad destination path, got nil")
	}
}

func TestReadUncompressed(t *testing.T) {
	// Create a server that accepts multipart uploads
	var received []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		file, _, err := r.FormFile("BootyImage")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()
		received, _ = io.ReadAll(file)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	// Create a temp file as the source device
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "source.img")
	content := []byte("source disk data for read test")
	if err := os.WriteFile(srcFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	err := Read(srcFile, ts.URL+"/image", "aa-bb-cc-dd-ee-ff", false)
	if err != nil {
		t.Fatalf("Read() error: %v", err)
	}

	if !bytes.Equal(received, content) {
		t.Errorf("received content mismatch: got %d bytes, want %d bytes", len(received), len(content))
	}
}

func TestReadCompressed(t *testing.T) {
	var received []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		file, _, err := r.FormFile("BootyImage")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()
		received, _ = io.ReadAll(file)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "source.img")
	content := []byte("source disk data for compressed read test")
	if err := os.WriteFile(srcFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	err := Read(srcFile, ts.URL+"/image", "aa-bb-cc-dd-ee-ff", true)
	if err != nil {
		t.Fatalf("Read() error: %v", err)
	}

	// The received data should be gzip compressed
	gr, err := gzip.NewReader(bytes.NewReader(received))
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer gr.Close()
	decompressed, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("failed to decompress: %v", err)
	}
	if !bytes.Equal(decompressed, content) {
		t.Errorf("decompressed content mismatch: got %q, want %q", decompressed, content)
	}
}
