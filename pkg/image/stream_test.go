package image

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/ulikunitz/xz"
)

func TestRedactURLRemovesSensitiveParts(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "credentials query fragment",
			in:   "https://user:secret@example.com/image.raw?token=abc#frag",
			want: "https://example.com/image.raw",
		},
		{
			name: "query fragment without credentials",
			in:   "https://example.com/image.raw?token=abc#frag",
			want: "https://example.com/image.raw",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RedactURL(tt.in); got != tt.want {
				t.Fatalf("RedactURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

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

	// Magic bytes detect gzip regardless of URL suffix now.
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

func TestStreamZstd(t *testing.T) {
	data := []byte("zstd image content for testing")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		zw, _ := zstd.NewWriter(w)
		_, _ = zw.Write(data)
		_ = zw.Close()
	}))
	defer srv.Close()

	tmpFile, err := os.CreateTemp(t.TempDir(), "disk-*")
	if err != nil {
		t.Fatal(err)
	}
	_ = tmpFile.Close()

	err = Stream(context.Background(), srv.URL+"/image.zst", tmpFile.Name())
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

func TestStreamLZ4(t *testing.T) {
	data := []byte("lz4 image content for testing")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		lw := lz4.NewWriter(w)
		_, _ = lw.Write(data)
		_ = lw.Close()
	}))
	defer srv.Close()

	tmpFile, err := os.CreateTemp(t.TempDir(), "disk-*")
	if err != nil {
		t.Fatal(err)
	}
	_ = tmpFile.Close()

	err = Stream(context.Background(), srv.URL+"/image.lz4", tmpFile.Name())
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

func TestStreamXZ(t *testing.T) {
	data := []byte("xz image content for testing")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		xw, _ := xz.NewWriter(w)
		_, _ = xw.Write(data)
		_ = xw.Close()
	}))
	defer srv.Close()

	tmpFile, err := os.CreateTemp(t.TempDir(), "disk-*")
	if err != nil {
		t.Fatal(err)
	}
	_ = tmpFile.Close()

	err = Stream(context.Background(), srv.URL+"/image.xz", tmpFile.Name())
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

func TestStreamChecksumPass(t *testing.T) {
	data := []byte("data for checksum test")
	h := sha256.Sum256(data)
	checksum := hex.EncodeToString(h[:])

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

	err = Stream(context.Background(), srv.URL+"/image.img", tmpFile.Name(), StreamOpts{
		Checksum:     checksum,
		ChecksumType: "sha256",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStreamChecksumInfersTypeAndStripsPrefix(t *testing.T) {
	data := []byte("data for checksum inference")
	h := sha256.Sum256(data)
	checksum := "sha256:" + strings.ToUpper(hex.EncodeToString(h[:]))

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

	err = Stream(context.Background(), srv.URL+"/image.img", tmpFile.Name(), StreamOpts{
		Checksum: checksum,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStreamChecksumMismatch(t *testing.T) {
	data := []byte("data for checksum mismatch test")
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

	err = Stream(context.Background(), srv.URL+"/image.img", tmpFile.Name(), StreamOpts{
		Checksum:     "0000000000000000000000000000000000000000000000000000000000000000",
		ChecksumType: "sha256",
	})
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
}

func TestStreamChecksumSHA512(t *testing.T) {
	data := []byte("sha512 checksum test data")
	h := sha512.Sum512(data)
	checksum := hex.EncodeToString(h[:])

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

	err = Stream(context.Background(), srv.URL+"/image.img", tmpFile.Name(), StreamOpts{
		Checksum:     checksum,
		ChecksumType: "sha512",
	})
	if err != nil {
		t.Fatalf("sha512 stream failed: %v", err)
	}
}

func TestStreamUnsupportedChecksumType(t *testing.T) {
	data := []byte("unsupported hash")
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

	err = Stream(context.Background(), srv.URL+"/image.img", tmpFile.Name(), StreamOpts{
		Checksum:     "deadbeef",
		ChecksumType: "md5",
	})
	if err == nil {
		t.Fatal("expected error for unsupported checksum type")
	}
	if !strings.Contains(err.Error(), "unsupported checksum type") {
		t.Errorf("error = %q, want to contain 'unsupported checksum type'", err.Error())
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

func TestIsOCIReference(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"oci://ghcr.io/org/image:latest", true},
		{"http://example.com/image.gz", false},
		{"https://example.com/image.gz", false},
		{"oci://registry.example.com/repo@sha256:abc", true},
	}
	for _, tt := range tests {
		got := IsOCIReference(tt.url)
		if got != tt.want {
			t.Errorf("IsOCIReference(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestTrimOCIScheme(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"oci://ghcr.io/org/image:latest", "ghcr.io/org/image:latest"},
		{"ghcr.io/org/image:latest", "ghcr.io/org/image:latest"},
	}
	for _, tt := range tests {
		got := TrimOCIScheme(tt.url)
		if got != tt.want {
			t.Errorf("TrimOCIScheme(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestHTTPGetWithRetry_TransientErrorsThenSuccess(t *testing.T) {
	retryBackoffBase = 0
	t.Cleanup(func() { retryBackoffBase = time.Second })

	const wantAttempts = 2
	attempts := 0
	data := []byte("image content")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts < wantAttempts {
			w.WriteHeader(http.StatusServiceUnavailable) // 503 → triggers retry
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	rc, err := httpGetWithRetry(context.Background(), srv.URL+"/image.img")
	if err != nil {
		t.Fatalf("httpGetWithRetry() returned error: %v", err)
	}
	defer rc.Close()

	buf := new(strings.Builder)
	if _, err := io.Copy(buf, rc); err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if buf.String() != string(data) {
		t.Errorf("body = %q, want %q", buf.String(), data)
	}
	if attempts != wantAttempts {
		t.Errorf("server received %d requests, want %d", attempts, wantAttempts)
	}
}

func TestHTTPGetWithRetry_AllFail(t *testing.T) {
	retryBackoffBase = 0
	t.Cleanup(func() { retryBackoffBase = time.Second })

	const maxRetries = 3
	attempts := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadGateway) // 502 → always retried, exhausts retries
	}))
	defer srv.Close()

	_, err := httpGetWithRetry(context.Background(), srv.URL+"/image.img")
	if err == nil {
		t.Fatal("expected error after exhausting all retries")
	}
	if attempts != maxRetries {
		t.Errorf("server received %d requests, want %d (maxRetries)", attempts, maxRetries)
	}
}

func TestHTTPGetWithRetryErrorRedactsSensitiveURLParts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	source := strings.Replace(srv.URL, "http://", "http://user:secret@", 1) + "/image.img?token=abc#frag"
	_, err := httpGetWithRetry(context.Background(), source)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "token=abc") || strings.Contains(err.Error(), "#frag") {
		t.Fatalf("error leaked sensitive URL parts: %q", err.Error())
	}
	if !strings.Contains(err.Error(), srv.URL+"/image.img") {
		t.Fatalf("error = %q, want redacted URL context", err.Error())
	}
}

func TestStreamQCOW2Detection(t *testing.T) {
	// Serve qcow2 magic bytes — Stream should detect and redirect to qcow2 hook.
	data := append([]byte{0x51, 0x46, 0x49, 0xfb}, make([]byte, 100)...)
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	tmpFile, err := os.CreateTemp(t.TempDir(), "disk-*")
	if err != nil {
		t.Fatal(err)
	}
	_ = tmpFile.Close()

	// Override hook to verify it's called.
	called := false
	orig := convertQCOW2Hook
	convertQCOW2Hook = func(_ context.Context, src io.Reader, sourceName, device string, _ StreamOpts) error {
		called = true
		if !strings.Contains(sourceName, srv.URL) {
			t.Errorf("expected hook source to contain %s, got %s", srv.URL, sourceName)
		}
		got, err := io.ReadAll(src)
		if err != nil {
			t.Fatalf("reading hook source: %v", err)
		}
		if string(got) != string(data) {
			t.Fatalf("hook source = %q, want %q", got, data)
		}
		return nil
	}
	defer func() { convertQCOW2Hook = orig }()

	err = Stream(context.Background(), srv.URL+"/image.qcow2", tmpFile.Name())
	if err != nil {
		t.Fatalf("Stream() = %v", err)
	}
	if !called {
		t.Error("convertQCOW2Hook was not invoked for qcow2 image")
	}
	if requests != 1 {
		t.Fatalf("server received %d requests, want 1", requests)
	}
}

func TestStreamQCOW2ChecksumMismatch(t *testing.T) {
	data := append([]byte{0x51, 0x46, 0x49, 0xfb}, []byte("qcow2 payload for checksum")...)
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

	orig := convertQCOW2Hook
	convertQCOW2Hook = func(_ context.Context, src io.Reader, _ string, _ string, opt StreamOpts) error {
		checksummed, h, err := wrapChecksum(src, opt)
		if err != nil {
			return err
		}
		if _, err := io.Copy(io.Discard, checksummed); err != nil {
			return err
		}
		return verifyChecksum(h, opt)
	}
	defer func() { convertQCOW2Hook = orig }()

	err = Stream(context.Background(), srv.URL+"/image.qcow2", tmpFile.Name(), StreamOpts{
		Checksum:     "0000000000000000000000000000000000000000000000000000000000000000",
		ChecksumType: "sha256",
	})
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error = %q, want checksum mismatch", err.Error())
	}
}

func TestOpenAndDecompressQCOW2KeepsDetectedSource(t *testing.T) {
	data := append([]byte{0x51, 0x46, 0x49, 0xfb}, []byte("payload after qcow2 header")...)
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	src, cleanup, format, err := openAndDecompress(context.Background(), srv.URL+"/image.qcow2")
	if err != nil {
		t.Fatalf("openAndDecompress() = %v", err)
	}
	defer cleanup()
	if format != FormatQCOW2 {
		t.Fatalf("format = %s, want %s", format, FormatQCOW2)
	}

	got, err := io.ReadAll(src)
	if err != nil {
		t.Fatalf("reading returned source: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("returned source = %q, want %q", got, data)
	}
	if requests != 1 {
		t.Fatalf("server received %d requests, want 1", requests)
	}
}
