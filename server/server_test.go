package main

import (
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/plunderclient/types"
)

func TestConfigHandler(t *testing.T) {
	cfg := types.BootyConfig{
		Action:      types.WriteImage,
		SourceImage: "http://example.com/image.img",
		DryRun:      true,
	}
	configData, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{configData: configData}

	req := httptest.NewRequest(http.MethodGet, "/booty/aa-bb-cc.bty", nil)
	w := httptest.NewRecorder()

	srv.configHandler(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var got types.BootyConfig
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("error unmarshalling response: %v", err)
	}
	if got.Action != types.WriteImage {
		t.Errorf("expected action %q, got %q", types.WriteImage, got.Action)
	}
	if !got.DryRun {
		t.Error("expected DryRun=true")
	}
}

func TestImageHandler(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	// Build a multipart request
	content := []byte("test image data")
	body := new(strings.Builder)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("BootyImage", "test.img")
	if err != nil {
		t.Fatal(err)
	}
	part.Write(content)
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/image", strings.NewReader(body.String()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	// Set RemoteAddr to a safe filename
	req.RemoteAddr = "127.0.0.1:1234"

	w := httptest.NewRecorder()
	srv := &Server{}
	srv.imageHandler(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	// Verify the file was written
	imgFile := filepath.Join(tmpDir, "127.0.0.1:1234.img")
	got, err := os.ReadFile(imgFile)
	if err != nil {
		t.Fatalf("image file not written: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("image content mismatch: got %q, want %q", got, content)
	}
}

func TestImageHandlerBadRequest(t *testing.T) {
	// Send a non-multipart request
	req := httptest.NewRequest(http.MethodPost, "/image", strings.NewReader("not multipart"))
	req.Header.Set("Content-Type", "text/plain")

	w := httptest.NewRecorder()
	srv := &Server{}
	srv.imageHandler(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		t.Error("expected non-200 status for bad request")
	}
}

func TestWriteCounterServer(t *testing.T) {
	wc := &WriteCounter{}
	d := []byte("hello world")
	n, err := wc.Write(d)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(d) {
		t.Errorf("expected %d, got %d", len(d), n)
	}
	if wc.Total != uint64(len(d)) {
		t.Errorf("expected Total=%d, got %d", len(d), wc.Total)
	}
}

func TestWriteCounterMultipleWrites(t *testing.T) {
	wc := &WriteCounter{}
	wc.Write([]byte("hello"))
	wc.Write([]byte(" world"))
	if wc.Total != 11 {
		t.Errorf("expected Total=11, got %d", wc.Total)
	}
}

func TestPrintProgress(t *testing.T) {
	wc := WriteCounter{Total: 1024}
	// Smoke test: should not panic
	wc.PrintProgress()
}

func TestImageHandlerMissingFormField(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	body := new(strings.Builder)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("WrongField", "test.img")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte("data"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/image", strings.NewReader(body.String()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.RemoteAddr = "127.0.0.1:9999"

	w := httptest.NewRecorder()
	srv := &Server{}
	srv.imageHandler(w, req)

	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("expected non-200 status for missing BootyImage field")
	}
}
