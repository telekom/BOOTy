//go:build e2e

package e2e

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/stream"
	"github.com/google/go-containerregistry/pkg/v1/types"

	"github.com/telekom/BOOTy/pkg/image"
)

// startOCIRegistry creates an in-memory OCI registry and returns the server
// and the host:port address for image references.
func startOCIRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	handler := registry.New()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// pushTestImage pushes a single-layer OCI image containing the given data
// to the in-memory registry.
func pushTestImage(t *testing.T, _ string, ref string, data []byte) {
	t.Helper()

	layer := stream.NewLayer(
		nopRC{bytes.NewReader(data)},
		stream.WithMediaType(types.OCILayer),
	)

	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		t.Fatalf("mutate.AppendLayers: %v", err)
	}

	tag, err := name.NewTag(ref, name.Insecure)
	if err != nil {
		t.Fatalf("name.NewTag(%q): %v", ref, err)
	}

	if err := remote.Write(tag, img); err != nil {
		t.Fatalf("remote.Write: %v", err)
	}
}

// pushGzipTestImage pushes a single-layer OCI image where the layer content
// is gzip-compressed data.
func pushGzipTestImage(t *testing.T, ref string, rawData []byte) {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(rawData); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	layer := stream.NewLayer(
		nopRC{bytes.NewReader(buf.Bytes())},
		stream.WithMediaType(types.OCILayer),
	)

	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		t.Fatal(err)
	}

	tag, err := name.NewTag(ref, name.Insecure)
	if err != nil {
		t.Fatal(err)
	}

	if err := remote.Write(tag, img); err != nil {
		t.Fatal(err)
	}
}

type nopRC struct{ *bytes.Reader }

func (nopRC) Close() error { return nil }

// ---------------------------------------------------------------------------
// Test 1: OCI Stream Raw Image
// ---------------------------------------------------------------------------

func TestOCIStreamRawImageE2E(t *testing.T) {
	srv := startOCIRegistry(t)
	host := srv.Listener.Addr().String()

	testData := bytes.Repeat([]byte("OCIRAWIMAGE"), 512)
	ref := fmt.Sprintf("%s/test/raw-image:latest", host)
	pushTestImage(t, srv.URL, ref, testData)

	tmpDir := t.TempDir()
	tmpPath := filepath.Join(tmpDir, "disk.img")
	if err := os.WriteFile(tmpPath, []byte{}, 0o666); err != nil {
		t.Fatal(err)
	}

	err := image.Stream(context.Background(), "oci://"+ref, tmpPath)
	if err != nil {
		t.Fatalf("image.Stream OCI raw failed: %v", err)
	}

	written, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(written, testData) {
		t.Errorf("written data (%d bytes) != original (%d bytes)", len(written), len(testData))
	}
}

// ---------------------------------------------------------------------------
// Test 2: OCI Stream Gzip-Compressed Image
// ---------------------------------------------------------------------------

func TestOCIStreamGzipImageE2E(t *testing.T) {
	srv := startOCIRegistry(t)
	host := srv.Listener.Addr().String()

	testData := bytes.Repeat([]byte("OCIGZIMAGE"), 1024)
	ref := fmt.Sprintf("%s/test/gzip-image:v1", host)
	pushGzipTestImage(t, ref, testData)

	tmpDir := t.TempDir()
	tmpPath := filepath.Join(tmpDir, "disk.img")
	if err := os.WriteFile(tmpPath, []byte{}, 0o666); err != nil {
		t.Fatal(err)
	}

	err := image.Stream(context.Background(), "oci://"+ref, tmpPath)
	if err != nil {
		t.Fatalf("image.Stream OCI gzip failed: %v", err)
	}

	written, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(written, testData) {
		t.Errorf("written data (%d bytes) != original (%d bytes)", len(written), len(testData))
	}
}

// ---------------------------------------------------------------------------
// Test 3: OCI FetchOCILayer Directly
// ---------------------------------------------------------------------------

func TestFetchOCILayerDirectE2E(t *testing.T) {
	srv := startOCIRegistry(t)
	host := srv.Listener.Addr().String()

	testData := []byte("OCI-LAYER-CONTENT-FOR-DIRECT-FETCH")
	ref := fmt.Sprintf("%s/test/direct:latest", host)
	pushTestImage(t, srv.URL, ref, testData)

	ctx := context.Background()
	rc, err := image.FetchOCILayer(ctx, ref)
	if err != nil {
		t.Fatalf("FetchOCILayer failed: %v", err)
	}
	defer func() { _ = rc.Close() }()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(buf.Bytes(), testData) {
		t.Errorf("layer data (%d bytes) != original (%d bytes)", buf.Len(), len(testData))
	}
}

// ---------------------------------------------------------------------------
// Test 4: OCI Stream with Checksum Verification
// ---------------------------------------------------------------------------

func TestOCIStreamWithChecksumE2E(t *testing.T) {
	srv := startOCIRegistry(t)
	host := srv.Listener.Addr().String()

	testData := bytes.Repeat([]byte("CHECKSUMDATA"), 256)
	h := sha256.Sum256(testData)
	checksum := hex.EncodeToString(h[:])

	ref := fmt.Sprintf("%s/test/checksum:v1", host)
	pushTestImage(t, srv.URL, ref, testData)

	tmpDir := t.TempDir()
	tmpPath := filepath.Join(tmpDir, "disk.img")
	if err := os.WriteFile(tmpPath, []byte{}, 0o666); err != nil {
		t.Fatal(err)
	}

	err := image.Stream(context.Background(), "oci://"+ref, tmpPath, image.StreamOpts{
		Checksum:     checksum,
		ChecksumType: "sha256",
	})
	if err != nil {
		t.Fatalf("image.Stream OCI with checksum failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test 5: OCI Stream Checksum Mismatch
// ---------------------------------------------------------------------------

func TestOCIStreamChecksumMismatchE2E(t *testing.T) {
	srv := startOCIRegistry(t)
	host := srv.Listener.Addr().String()

	testData := []byte("mismatch content")
	ref := fmt.Sprintf("%s/test/mismatch:v1", host)
	pushTestImage(t, srv.URL, ref, testData)

	tmpDir := t.TempDir()
	tmpPath := filepath.Join(tmpDir, "disk.img")
	if err := os.WriteFile(tmpPath, []byte{}, 0o666); err != nil {
		t.Fatal(err)
	}

	err := image.Stream(context.Background(), "oci://"+ref, tmpPath, image.StreamOpts{
		Checksum:     "0000000000000000000000000000000000000000000000000000000000000000",
		ChecksumType: "sha256",
	})
	if err == nil {
		t.Fatal("expected checksum mismatch error for OCI stream")
	}
}

// ---------------------------------------------------------------------------
// Test 6: OCI Reference Not Found
// ---------------------------------------------------------------------------

func TestOCIStreamNotFoundE2E(t *testing.T) {
	srv := startOCIRegistry(t)
	host := srv.Listener.Addr().String()

	tmpDir := t.TempDir()
	tmpPath := filepath.Join(tmpDir, "disk.img")
	if err := os.WriteFile(tmpPath, []byte{}, 0o666); err != nil {
		t.Fatal(err)
	}

	err := image.Stream(context.Background(), "oci://"+host+"/test/nonexistent:v1", tmpPath)
	if err == nil {
		t.Fatal("expected error for non-existent OCI image")
	}
}

// ---------------------------------------------------------------------------
// Test 7: OCI Multi-Layer Image (last layer used)
// ---------------------------------------------------------------------------

func TestOCIMultiLayerLastUsedE2E(t *testing.T) {
	srv := startOCIRegistry(t)
	host := srv.Listener.Addr().String()

	layer1Data := []byte("FIRST-LAYER-CONTENT")
	layer2Data := []byte("SECOND-LAYER-THIS-IS-THE-ONE")

	l1 := stream.NewLayer(
		nopRC{bytes.NewReader(layer1Data)},
		stream.WithMediaType(types.OCILayer),
	)
	l2 := stream.NewLayer(
		nopRC{bytes.NewReader(layer2Data)},
		stream.WithMediaType(types.OCILayer),
	)

	img, err := mutate.AppendLayers(empty.Image, l1, l2)
	if err != nil {
		t.Fatal(err)
	}

	ref := fmt.Sprintf("%s/test/multi:latest", host)
	tag, err := name.NewTag(ref, name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(tag, img); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	rc, err := image.FetchOCILayer(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(rc); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(buf.Bytes(), layer2Data) {
		t.Errorf("expected last layer data, got %d bytes", buf.Len())
	}
}

// ---------------------------------------------------------------------------
// Test 8: OCI Stream Context Cancellation
// ---------------------------------------------------------------------------

func TestOCIStreamContextCancellationE2E(t *testing.T) {
	srv := startOCIRegistry(t)
	host := srv.Listener.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tmpDir := t.TempDir()
	tmpPath := filepath.Join(tmpDir, "disk.img")
	if err := os.WriteFile(tmpPath, []byte{}, 0o666); err != nil {
		t.Fatal(err)
	}

	err := image.Stream(ctx, "oci://"+host+"/test/cancel:v1", tmpPath)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

// ---------------------------------------------------------------------------
// Test 9: OCI IsOCIReference and TrimOCIScheme
// ---------------------------------------------------------------------------

func TestOCIReferenceHelpersE2E(t *testing.T) {
	tests := []struct {
		url    string
		isOCI  bool
		trim   string
	}{
		{"oci://ghcr.io/org/img:latest", true, "ghcr.io/org/img:latest"},
		{"oci://localhost:5000/test:v1", true, "localhost:5000/test:v1"},
		{"http://example.com/image.gz", false, "http://example.com/image.gz"},
		{"https://example.com/image.gz", false, "https://example.com/image.gz"},
	}
	for _, tt := range tests {
		if got := image.IsOCIReference(tt.url); got != tt.isOCI {
			t.Errorf("IsOCIReference(%q) = %v, want %v", tt.url, got, tt.isOCI)
		}
		if got := image.TrimOCIScheme(tt.url); got != tt.trim {
			t.Errorf("TrimOCIScheme(%q) = %q, want %q", tt.url, got, tt.trim)
		}
	}
}

// ---------------------------------------------------------------------------
// Test 10: Full OCI Provision Flow (OCI stream → file)
// ---------------------------------------------------------------------------

func TestOCIFullProvisionFlowE2E(t *testing.T) {
	srv := startOCIRegistry(t)
	host := srv.Listener.Addr().String()

	// Simulate a real disk image (4KB)
	imageData := bytes.Repeat([]byte{0xAA, 0xBB, 0xCC, 0xDD}, 1024)
	h := sha256.Sum256(imageData)
	checksum := hex.EncodeToString(h[:])

	ref := fmt.Sprintf("%s/prod/ubuntu:22.04", host)
	pushTestImage(t, srv.URL, ref, imageData)

	tmpDir := t.TempDir()
	tmpPath := filepath.Join(tmpDir, "sda")
	if err := os.WriteFile(tmpPath, []byte{}, 0o666); err != nil {
		t.Fatal(err)
	}

	// Stream with checksum verification
	err := image.Stream(context.Background(), "oci://"+ref, tmpPath, image.StreamOpts{
		Checksum:     checksum,
		ChecksumType: "sha256",
	})
	if err != nil {
		t.Fatalf("full OCI provision flow failed: %v", err)
	}

	written, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(written, imageData) {
		t.Errorf("provision flow data mismatch: got %d bytes, want %d", len(written), len(imageData))
	}
}
