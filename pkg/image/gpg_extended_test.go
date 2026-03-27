package image

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestVerifyGPGSignature_OCI_URL(t *testing.T) {
	// Create a real pubkey file so the function reaches the OCI check.
	tmpKey := t.TempDir() + "/key.gpg"
	if err := os.WriteFile(tmpKey, []byte("fake-key"), 0o644); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("sig-data"))
	}))
	defer ts.Close()

	err := VerifyGPGSignature(context.Background(), "oci://registry.example.com/image:tag", ts.URL+"/sig", tmpKey)
	if err == nil {
		t.Error("expected error for OCI URL")
	}
	if !strings.Contains(err.Error(), "oci://") {
		t.Errorf("error should mention oci://, got: %v", err)
	}
}

func TestDownloadToTemp_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("signature-data"))
	}))
	defer ts.Close()

	path, err := downloadToTemp(context.Background(), ts.URL+"/sig", "gpg-test-*")
	if err != nil {
		t.Fatalf("downloadToTemp: %v", err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "signature-data" {
		t.Errorf("content = %q", string(data))
	}
}

func TestVerifyGPGSignature_DownloadFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "sig") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("image-data"))
	}))
	defer ts.Close()

	tmpKey := t.TempDir() + "/key.gpg"
	if err := os.WriteFile(tmpKey, []byte("fake-key"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := VerifyGPGSignature(context.Background(), ts.URL+"/img", ts.URL+"/sig", tmpKey)
	if err == nil {
		t.Error("expected error when sig download fails")
	}
}

func TestRunGPGVerifyStream_WithInvalidBinary(t *testing.T) {
	// Create a temp dir with a fake gpgv that always fails.
	tmpDir := t.TempDir()
	fakeBin := tmpDir + "/gpgv"
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tmpDir)

	err := runGPGVerifyStream(context.Background(), "/nonexistent/key", "/nonexistent/sig", strings.NewReader("data"))
	if err == nil {
		t.Error("expected error from failing gpgv")
	}
}
