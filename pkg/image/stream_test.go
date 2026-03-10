package image

import (
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
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

	err = Stream(context.Background(), srv.URL+"/image.img.gz", tmpFile.Name())
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
