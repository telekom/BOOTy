//go:build linux

package image

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
)

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func compressForTest(t *testing.T, format string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	switch format {
	case "raw":
		return data
	case "gz":
		w := gzip.NewWriter(&buf)
		_, _ = w.Write(data)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	case "zst":
		w, err := zstd.NewWriter(&buf)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(data)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	case "lz4":
		w := lz4.NewWriter(&buf)
		_, _ = w.Write(data)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown format %s", format)
	}
	return buf.Bytes()
}

func serveBytes(t *testing.T, body []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestStreamChecksumScope checks that IMAGE_CHECKSUM covers the file as
// downloaded by default, like the .sha256 files published next to .lz4
// images, and the decompressed image with ChecksumScopeDecompressed.
func TestStreamChecksumScope(t *testing.T) {
	data := bytes.Repeat([]byte("BOOTy checksum scope payload "), 4096)
	for _, format := range []string{"raw", "gz", "zst", "lz4"} {
		downloaded := compressForTest(t, format, data)
		url := serveBytes(t, downloaded) + "/image." + format
		type scopeCase struct {
			name     string
			opt      StreamOpts
			wantFail bool
		}
		tests := []scopeCase{
			{name: "default scope, digest of download", opt: StreamOpts{Checksum: sha256Hex(downloaded)}},
			{name: "download scope, digest of download", opt: StreamOpts{Checksum: sha256Hex(downloaded), ChecksumScope: "download"}},
			{name: "decompressed scope, digest of image", opt: StreamOpts{Checksum: sha256Hex(data), ChecksumScope: "Decompressed"}},
			{name: "default scope, wrong digest", opt: StreamOpts{Checksum: strings.Repeat("0", 64)}, wantFail: true},
			{name: "decompressed scope, wrong digest", opt: StreamOpts{Checksum: strings.Repeat("0", 64), ChecksumScope: "decompressed"}, wantFail: true},
		}
		if format != "raw" {
			tests = append(tests,
				scopeCase{name: "default scope, digest of image", opt: StreamOpts{Checksum: sha256Hex(data)}, wantFail: true},
				scopeCase{name: "decompressed scope, digest of download", opt: StreamOpts{Checksum: sha256Hex(downloaded), ChecksumScope: "decompressed"}, wantFail: true},
			)
		}
		for _, tt := range tests {
			t.Run(format+"/"+tt.name, func(t *testing.T) {
				disk := filepath.Join(t.TempDir(), "disk.img")
				if err := os.WriteFile(disk, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				err := Stream(context.Background(), url, disk, tt.opt)
				if tt.wantFail {
					if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
						t.Fatalf("Stream() = %v, want checksum mismatch", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("Stream() = %v", err)
				}
				if got := readFile(t, disk); !bytes.Equal(got, data) {
					t.Fatalf("disk has %d bytes, want the %d image bytes", len(got), len(data))
				}
			})
		}
	}
}

func TestStreamChecksumScopeRejectsUnknownScope(t *testing.T) {
	url := serveBytes(t, []byte("x")) + "/image.raw"
	err := Stream(context.Background(), url, filepath.Join(t.TempDir(), "disk"),
		StreamOpts{Checksum: sha256Hex([]byte("x")), ChecksumScope: "payload"})
	if err == nil || !strings.Contains(err.Error(), "unsupported checksum scope") {
		t.Fatalf("Stream() = %v, want unsupported checksum scope", err)
	}
}

// TestDownloadToFileVerifiesDownloadChecksum covers the ramdisk download of
// IMAGE_MODE=partition: a mismatch fails the download before the image is
// decompressed and copied.
func TestDownloadToFileVerifiesDownloadChecksum(t *testing.T) {
	downloaded := compressForTest(t, "lz4", []byte("partition mode payload"))
	url := serveBytes(t, downloaded) + "/image.raw.lz4"
	for _, tt := range []struct {
		checksum string
		wantErr  bool
	}{
		{checksum: sha256Hex(downloaded)},
		{checksum: sha256Hex([]byte("partition mode payload")), wantErr: true},
	} {
		opt, err := withDownloadChecksum(StreamOpts{Checksum: tt.checksum})
		if err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(t.TempDir(), "image.download")
		err = downloadToFile(context.Background(), url, dest, opt.download)
		if (err != nil) != tt.wantErr {
			t.Fatalf("downloadToFile(%s) = %v, want error %v", tt.checksum[:8], err, tt.wantErr)
		}
		if err == nil {
			if verr := verifyRawImageChecksum(dest, opt); verr != nil {
				t.Fatalf("verifyRawImageChecksum() = %v after a verified download", verr)
			}
		}
	}
}

// TestStreamRootVerifiesDownloadChecksum covers the root and A/B stream
// path, which stops reading the payload after the root partition. The image
// carries data past the root partition, so a download-scope checksum must
// wait for the decompressor before it hashes the rest (zstd read-ahead).
func TestStreamRootVerifiesDownloadChecksum(t *testing.T) {
	// Incompressible data, so the compressed stream is still being read
	// when the root partition is done.
	tailData := make([]byte, 8<<20)
	if _, err := rand.Read(tailData); err != nil {
		t.Fatal(err)
	}
	raw := append(testGPTImage(t), tailData...)
	for _, format := range []string{"gz", "zst"} {
		downloaded := compressForTest(t, format, raw)
		url := serveBytes(t, downloaded) + "/image.raw." + format
		for _, tt := range []struct {
			checksum string
			wantErr  bool
		}{
			{checksum: sha256Hex(downloaded)},
			{checksum: sha256Hex(raw), wantErr: true},
		} {
			for range 3 {
				root := filepath.Join(t.TempDir(), "root.img")
				createTargetFile(t, root)
				err := StreamRoot(context.Background(), url, RootTarget{RootPartition: root}, StreamOpts{Checksum: tt.checksum})
				if (err != nil) != tt.wantErr {
					t.Fatalf("%s: StreamRoot(%s) = %v, want error %v", format, tt.checksum[:8], err, tt.wantErr)
				}
			}
		}
	}
}

// TestStreamChecksumScopeTrailingData: data after the last compressed frame
// (a zstd skippable frame, as padding) belongs to the download, so the
// download-scope digest covers it.
func TestStreamChecksumScopeTrailingData(t *testing.T) {
	data := bytes.Repeat([]byte("payload "), 8192)
	skippable := append([]byte{0x50, 0x2a, 0x4d, 0x18, 8, 0, 0, 0}, []byte("PADDING!")...)
	downloaded := append(compressForTest(t, "zst", data), skippable...)
	url := serveBytes(t, downloaded) + "/image.raw.zst"
	for _, tt := range []struct {
		checksum string
		wantErr  bool
	}{
		{checksum: sha256Hex(downloaded)},
		{checksum: sha256Hex(downloaded[:len(downloaded)-len(skippable)]), wantErr: true},
	} {
		disk := filepath.Join(t.TempDir(), "disk.img")
		if err := os.WriteFile(disk, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		err := Stream(context.Background(), url, disk, StreamOpts{Checksum: tt.checksum})
		if (err != nil) != tt.wantErr {
			t.Fatalf("Stream(%s) = %v, want error %v", tt.checksum[:8], err, tt.wantErr)
		}
		if tt.wantErr && (!errors.Is(err, ErrChecksumMismatch) || !strings.Contains(err.Error(), "IMAGE_CHECKSUM_SCOPE=decompressed")) {
			t.Fatalf("Stream() = %v, want a checksum mismatch that names IMAGE_CHECKSUM_SCOPE", err)
		}
	}
}
