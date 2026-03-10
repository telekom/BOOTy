package plunderclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/plunderclient/types"
)

func TestGetConfigForAddress(t *testing.T) {
	cfg := types.BootyConfig{
		Action:            types.WriteImage,
		SourceImage:       "http://example.com/image.img",
		DestinationDevice: "/dev/sda",
		Compressed:        true,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/00-11-22-33-44-55.bty") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cfg)
	}))
	defer server.Close()

	os.Setenv("BOOTYURL", server.URL)
	defer os.Unsetenv("BOOTYURL")

	result, err := GetConfigForAddress("00-11-22-33-44-55")
	if err != nil {
		t.Fatalf("GetConfigForAddress() error: %v", err)
	}
	if result.Action != types.WriteImage {
		t.Errorf("Action = %q, want %q", result.Action, types.WriteImage)
	}
	if result.SourceImage != cfg.SourceImage {
		t.Errorf("SourceImage = %q, want %q", result.SourceImage, cfg.SourceImage)
	}
	if result.DestinationDevice != cfg.DestinationDevice {
		t.Errorf("DestinationDevice = %q, want %q", result.DestinationDevice, cfg.DestinationDevice)
	}
	if result.Compressed != cfg.Compressed {
		t.Errorf("Compressed = %v, want %v", result.Compressed, cfg.Compressed)
	}
}

func TestGetConfigForAddressNoURL(t *testing.T) {
	os.Unsetenv("BOOTYURL")
	_, err := GetConfigForAddress("00-11-22-33-44-55")
	if err == nil {
		t.Error("GetConfigForAddress() with no BOOTYURL should return error")
	}
}

func TestGetConfigForAddress404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	os.Setenv("BOOTYURL", server.URL)
	defer os.Unsetenv("BOOTYURL")

	_, err := GetConfigForAddress("nonexistent-mac")
	if err == nil {
		t.Error("GetConfigForAddress() with 404 response should return error")
	}
}
