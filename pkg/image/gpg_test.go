package image

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestVerifyGPGSignature_NoSignatureURL(t *testing.T) {
	// Should never be called with empty URL, but verify it doesn't panic.
	err := VerifyGPGSignature(context.Background(), "http://example.com/img", "", "/tmp/key.gpg")
	if err == nil {
		t.Error("expected error for empty signature URL")
	}
}

func TestVerifyGPGSignature_MissingPubKey(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	err := VerifyGPGSignature(context.Background(), ts.URL+"/img", ts.URL+"/sig", "/nonexistent/key.gpg")
	if err == nil {
		t.Error("expected error for missing public key file")
	}
}

func TestDownloadToTemp(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("test-content"))
	}))
	defer ts.Close()

	path, err := downloadToTemp(context.Background(), ts.URL+"/file", "booty-test-*")
	if err != nil {
		t.Fatalf("downloadToTemp() = %v", err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading temp file: %v", err)
	}
	if string(data) != "test-content" {
		t.Errorf("content = %q, want %q", string(data), "test-content")
	}
}

func TestDownloadToTemp_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	_, err := downloadToTemp(context.Background(), ts.URL+"/file", "booty-test-*")
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestRunGPGVerifyStream_NoBinaryAvailable(t *testing.T) {
	// Save and clear PATH so neither gpgv nor gpg is found.
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", "/nonexistent")
	defer func() { _ = os.Setenv("PATH", origPath) }()

	err := runGPGVerifyStream(context.Background(), "/tmp/key", "/tmp/sig", strings.NewReader("dummy"))
	if err != nil {
		t.Errorf("should succeed with warning when no GPG binary, got %v", err)
	}
}
