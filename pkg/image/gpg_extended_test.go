package image

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestVerifyGPGSignatureRedactsURLsInErrors(t *testing.T) {
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

	err := VerifyGPGSignature(
		context.Background(),
		ts.URL+"/img?image-token=secret#image-fragment",
		ts.URL+"/sig?signature-token=secret#signature-fragment",
		tmpKey,
	)
	if err == nil {
		t.Fatal("expected error when signature download fails")
	}
	msg := err.Error()
	for _, sensitive := range []string{"signature-token", "image-token", "secret", "signature-fragment", "image-fragment"} {
		if strings.Contains(msg, sensitive) {
			t.Fatalf("error leaked %q: %s", sensitive, msg)
		}
	}
	if !strings.Contains(msg, ts.URL+"/sig") {
		t.Fatalf("error did not preserve redacted signature URL context: %s", msg)
	}
}

func TestDownloadToTempTransportErrorRedactsURL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("sig-data"))
	}))
	rawURL := sensitiveTestURL(t, ts.URL, "/sig")
	ts.Close()

	_, err := downloadToTemp(context.Background(), rawURL, "gpg-test-*")
	if err == nil {
		t.Fatal("expected signature download transport error")
	}
	assertNoSensitiveURLParts(t, err.Error())
	assertPreservesURLError(t, err)
	if !strings.Contains(err.Error(), "/sig") {
		t.Fatalf("error did not preserve redacted signature path context: %s", err)
	}
}

func TestVerifyGPGSignatureImageTransportErrorRedactsURL(t *testing.T) {
	sigServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("sig-data"))
	}))
	defer sigServer.Close()

	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("image-data"))
	}))
	imageURL := sensitiveTestURL(t, imageServer.URL, "/image.raw")
	imageServer.Close()

	tmpKey := t.TempDir() + "/key.gpg"
	if err := os.WriteFile(tmpKey, []byte("fake-key"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := VerifyGPGSignature(context.Background(), imageURL, sigServer.URL+"/sig", tmpKey)
	if err == nil {
		t.Fatal("expected image stream transport error")
	}
	assertNoSensitiveURLParts(t, err.Error())
	assertPreservesURLError(t, err)
	if !strings.Contains(err.Error(), "/image.raw") {
		t.Fatalf("error did not preserve redacted image path context: %s", err)
	}
}

func sensitiveTestURL(t *testing.T, baseURL, path string) string {
	t.Helper()

	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse test URL: %v", err)
	}
	u.Path = path
	u.User = url.UserPassword("robot", "secret")
	u.RawQuery = "token=abc"
	u.Fragment = "frag"
	return u.String()
}

func assertNoSensitiveURLParts(t *testing.T, msg string) {
	t.Helper()

	for _, sensitive := range []string{"robot", "secret", "token=abc", "frag"} {
		if strings.Contains(msg, sensitive) {
			t.Fatalf("error leaked %q: %s", sensitive, msg)
		}
	}
}

func assertPreservesURLError(t *testing.T, err error) {
	t.Helper()

	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		t.Fatalf("error chain did not preserve *url.Error: %v", err)
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
