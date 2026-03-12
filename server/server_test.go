package main

import (
	"encoding/json"
	"fmt"
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

func TestConfigHandlerLargePayload(t *testing.T) {
	// Test config handler with a larger config.
	cfg := types.BootyConfig{
		Action:            types.WriteImage,
		SourceImage:       "http://example.com/big-image.img",
		DryRun:            false,
		DropToShell:       true,
		DestinationDevice: "/dev/nvme0n1",
		GrowPartition:     3,
		WipeDevice:        true,
		LVMRootName:       "/dev/vg/root",
		Address:           "10.0.0.1/24",
		Gateway:           "10.0.0.254",
	}
	configData, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{configData: configData}

	req := httptest.NewRequest(http.MethodGet, "/booty/xx-yy-zz.bty", nil)
	w := httptest.NewRecorder()
	srv.configHandler(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var got types.BootyConfig
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.DestinationDevice != "/dev/nvme0n1" {
		t.Errorf("expected /dev/nvme0n1, got %s", got.DestinationDevice)
	}
	if !got.WipeDevice {
		t.Error("expected WipeDevice=true")
	}
}

func TestConfigHandlerEmptyConfig(t *testing.T) {
	srv := &Server{configData: []byte("{}")}
	req := httptest.NewRequest(http.MethodGet, "/booty/test.bty", nil)
	w := httptest.NewRecorder()
	srv.configHandler(w, req)

	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestSetupServerWriteImage(t *testing.T) {
	srv := &Server{}
	cfg := types.BootyConfig{
		Action:            types.WriteImage,
		SourceImage:       "http://example.com/image.img",
		DestinationDevice: "/dev/sda",
	}
	mux, err := srv.setupServer("00:11:22:33:44:55", &cfg)
	if err != nil {
		t.Fatalf("setupServer error: %v", err)
	}
	if mux == nil {
		t.Fatal("expected non-nil mux")
	}
	if len(srv.configData) == 0 {
		t.Fatal("expected configData to be set")
	}
}

func TestSetupServerReadImage(t *testing.T) {
	srv := &Server{}
	cfg := types.BootyConfig{Action: types.ReadImage}
	mux, err := srv.setupServer("aa:bb:cc:dd:ee:ff", &cfg)
	if err != nil {
		t.Fatalf("setupServer error: %v", err)
	}
	if mux == nil {
		t.Fatal("expected non-nil mux")
	}
}

func TestSetupServerNoMac(t *testing.T) {
	srv := &Server{}
	cfg := types.BootyConfig{Action: types.WriteImage}
	mux, err := srv.setupServer("", &cfg)
	if err != nil {
		t.Fatalf("setupServer error: %v", err)
	}
	if mux == nil {
		t.Fatal("expected non-nil mux")
	}
}

func TestSetupServerUnknownAction(t *testing.T) {
	srv := &Server{}
	cfg := types.BootyConfig{Action: "invalidAction"}
	_, err := srv.setupServer("00:11:22:33:44:55", &cfg)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestSetupServerMuxRouting(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)
	os.MkdirAll("images", 0o755)

	srv := &Server{}
	cfg := types.BootyConfig{
		Action:      types.WriteImage,
		SourceImage: "http://example.com/image.img",
	}
	mux, err := srv.setupServer("00:11:22:33:44:55", &cfg)
	if err != nil {
		t.Fatalf("setupServer error: %v", err)
	}

	// Test config handler via mux.
	req := httptest.NewRequest(http.MethodGet, "/booty/00-11-22-33-44-55.bty", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var got types.BootyConfig
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if got.Action != types.WriteImage {
		t.Errorf("expected writeImage action, got %s", got.Action)
	}
}

func TestParseFlagsWriteImage(t *testing.T) {
	mac, cfg, err := parseFlags([]string{
		"-mac", "00:11:22:33:44:55",
		"-action", "writeImage",
		"-sourceImage", "http://example.com/image.img",
		"-destinationDevice", "/dev/sda",
		"-growPartition", "3",
		"-wipe",
	})
	if err != nil {
		t.Fatalf("parseFlags error: %v", err)
	}
	if mac != "00:11:22:33:44:55" {
		t.Errorf("mac = %q, want %q", mac, "00:11:22:33:44:55")
	}
	if cfg.Action != "writeImage" {
		t.Errorf("Action = %q, want %q", cfg.Action, "writeImage")
	}
	if cfg.SourceImage != "http://example.com/image.img" {
		t.Errorf("SourceImage = %q", cfg.SourceImage)
	}
	if cfg.DestinationDevice != "/dev/sda" {
		t.Errorf("DestinationDevice = %q", cfg.DestinationDevice)
	}
	if cfg.GrowPartition != 3 {
		t.Errorf("GrowPartition = %d, want 3", cfg.GrowPartition)
	}
	if !cfg.WipeDevice {
		t.Error("expected WipeDevice=true")
	}
}

func TestParseFlagsReadImage(t *testing.T) {
	mac, cfg, err := parseFlags([]string{
		"-mac", "aa:bb:cc:dd:ee:ff",
		"-action", "readImage",
		"-sourceDevice", "/dev/sda",
		"-destinationAddress", "http://server:3000",
	})
	if err != nil {
		t.Fatalf("parseFlags error: %v", err)
	}
	if mac != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("mac = %q", mac)
	}
	if cfg.Action != "readImage" {
		t.Errorf("Action = %q", cfg.Action)
	}
	if cfg.SourceDevice != "/dev/sda" {
		t.Errorf("SourceDevice = %q", cfg.SourceDevice)
	}
}

func TestParseFlagsDefaults(t *testing.T) {
	_, cfg, err := parseFlags([]string{})
	if err != nil {
		t.Fatalf("parseFlags error: %v", err)
	}
	if cfg.LVMRootName != "/dev/ubuntu-vg/root" {
		t.Errorf("LVMRootName default = %q", cfg.LVMRootName)
	}
	if cfg.GrowPartition != 1 {
		t.Errorf("GrowPartition default = %d", cfg.GrowPartition)
	}
}

func TestParseFlagsInvalid(t *testing.T) {
	_, _, err := parseFlags([]string{"-unknown-flag"})
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestParseFlagsAllOptions(t *testing.T) {
	mac, cfg, err := parseFlags([]string{
		"-mac", "00:11:22:33:44:55",
		"-action", "writeImage",
		"-dryRun",
		"-shell",
		"-wipe",
		"-lvmRoot", "/dev/vg/root",
		"-growPartition", "2",
		"-sourceImage", "http://example.com/img",
		"-sourceDevice", "/dev/sdb",
		"-destinationAddress", "http://dest",
		"-destinationDevice", "/dev/nvme0n1",
		"-address", "10.0.0.1/24",
		"-gateway", "10.0.0.254",
	})
	if err != nil {
		t.Fatalf("parseFlags error: %v", err)
	}
	if mac != "00:11:22:33:44:55" {
		t.Errorf("mac = %q", mac)
	}
	if !cfg.DryRun {
		t.Error("expected DryRun=true")
	}
	if !cfg.DropToShell {
		t.Error("expected DropToShell=true")
	}
	if cfg.LVMRootName != "/dev/vg/root" {
		t.Errorf("LVMRootName = %q", cfg.LVMRootName)
	}
	if cfg.Address != "10.0.0.1/24" {
		t.Errorf("Address = %q", cfg.Address)
	}
	if cfg.Gateway != "10.0.0.254" {
		t.Errorf("Gateway = %q", cfg.Gateway)
	}
}

func TestImageHandlerOpenFileError(t *testing.T) {
	// Use a directory where we can't open the file (read-only).
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)
	// Make directory read-only so file creation fails.
	os.Chmod(tmpDir, 0o555)
	defer os.Chmod(tmpDir, 0o755)

	body := new(strings.Builder)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("BootyImage", "test.img")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte("image data"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/image", strings.NewReader(body.String()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.RemoteAddr = "127.0.0.1:1234"

	w := httptest.NewRecorder()
	srv := &Server{}
	srv.imageHandler(w, req)

	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", resp.StatusCode)
	}
}

// failWriter is a ResponseWriter that always fails on Write.
type failWriter struct {
	header http.Header
}

func (f *failWriter) Header() http.Header         { return f.header }
func (f *failWriter) Write(_ []byte) (int, error) { return 0, fmt.Errorf("write failed") }
func (f *failWriter) WriteHeader(_ int)           {}

func TestConfigHandlerWriteError(t *testing.T) {
	srv := &Server{configData: []byte(`{"action":"writeImage"}`)}
	req := httptest.NewRequest(http.MethodGet, "/booty/test.bty", nil)
	w := &failWriter{header: make(http.Header)}
	srv.configHandler(w, req)
	// configHandler logs the error but doesn't return an error.
	// Just checking it doesn't panic.
}
