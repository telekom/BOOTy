//go:build linux

package image

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

const testMiB = 1 << 20

// newWipeTarget returns a 4 MiB target filled with 0xff.
func newWipeTarget(t *testing.T) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "disk")
	if err := os.WriteFile(target, bytes.Repeat([]byte{0xff}, 4*testMiB), 0o600); err != nil {
		t.Fatal(err)
	}
	return target
}

// requireWiped fails unless the first and the last MiB of target are zero.
func requireWiped(t *testing.T, target string) {
	t.Helper()
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	zero := make([]byte, testMiB)
	if !bytes.Equal(got[:testMiB], zero) {
		t.Fatal("first MiB of the target not wiped")
	}
	if !bytes.Equal(got[len(got)-testMiB:], zero) {
		t.Fatal("last MiB of the target not wiped")
	}
}

// TestStreamChecksumMismatchWipesTarget covers both mismatch paths of
// Stream: a download-scope digest fails while the image is copied, a
// decompressed-scope digest after the copy.
func TestStreamChecksumMismatchWipesTarget(t *testing.T) {
	url := serveBytes(t, bytes.Repeat([]byte{0xaa}, 2*testMiB))
	for _, scope := range []string{ChecksumScopeDownload, ChecksumScopeDecompressed} {
		t.Run(scope, func(t *testing.T) {
			target := newWipeTarget(t)
			err := Stream(context.Background(), url+"/image", target, StreamOpts{
				Checksum:      strings.Repeat("0", 64),
				ChecksumScope: scope,
			})
			if !errors.Is(err, ErrChecksumMismatch) {
				t.Fatalf("Stream() = %v, want ErrChecksumMismatch", err)
			}
			requireWiped(t, target)
		})
	}
}

// TestStreamWriteErrorWipesTarget breaks the download after 2 MiB.
func TestStreamWriteErrorWipesTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(8*testMiB))
		_, _ = w.Write(bytes.Repeat([]byte{0xaa}, 2*testMiB))
	}))
	defer srv.Close()
	target := newWipeTarget(t)
	if err := Stream(context.Background(), srv.URL+"/image", target); err == nil {
		t.Fatal("Stream() of a truncated download succeeded")
	}
	requireWiped(t, target)
}

// TestStreamRawToDeviceErrorWipesTarget reads a directory as the converted
// raw image, which fails the copy.
func TestStreamRawToDeviceErrorWipesTarget(t *testing.T) {
	target := newWipeTarget(t)
	if err := streamRawToDevice(t.TempDir(), target, StreamOpts{}); err == nil {
		t.Fatal("streamRawToDevice() of a directory succeeded")
	}
	requireWiped(t, target)
}

func TestSelectDefaultOCILayerRejectsTwoRawImageLayers(t *testing.T) {
	_, err := selectDefaultOCILayer([]v1.Layer{
		static.NewLayer([]byte("lz4"), "application/vnd.tcaas.node-image+raw.lz4"),
		static.NewLayer([]byte("zst"), "application/vnd.tcaas.node-image+raw.zst"),
	})
	if err == nil {
		t.Fatal("two +raw* layers were accepted")
	}
}

// pushOCIImageBlob pushes blob unchanged as the only layer of an image.
func pushOCIImageBlob(t *testing.T, blob []byte, mediaType string) string {
	t.Helper()
	srv := startTestRegistry(t)
	t.Cleanup(srv.Close)
	img, err := mutate.AppendLayers(empty.Image, static.NewLayer(blob, types.MediaType(mediaType)))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := name.ParseReference(strings.TrimPrefix(srv.URL, "http://") + "/test/node-image:v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(ref, img); err != nil {
		t.Fatal(err)
	}
	return "oci://" + ref.String()
}

// TestStreamOCIChecksumScopes checks that the download scope of an oci://
// source covers the pushed file (what a .raw.zst.sha256 or .raw.lz4.sha256
// covers) and the decompressed scope the image, for both compressions.
func TestStreamOCIChecksumScopes(t *testing.T) {
	data := testImageData()
	for _, tt := range []struct {
		mediaType string
		blob      []byte
	}{
		{"application/vnd.tcaas.node-image+raw.zst", encodeZstdFrames(t, data)},
		{"application/vnd.tcaas.node-image+raw.lz4", encodeLZ4(t, data, 1)},
	} {
		source := pushOCIImageBlob(t, tt.blob, tt.mediaType)
		for scope, digested := range map[string][]byte{ChecksumScopeDownload: tt.blob, ChecksumScopeDecompressed: data} {
			t.Run(tt.mediaType+" "+scope, func(t *testing.T) {
				target := filepath.Join(t.TempDir(), "disk")
				if err := os.WriteFile(target, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256(digested)
				err := Stream(context.Background(), source, target, StreamOpts{
					Checksum:      hex.EncodeToString(sum[:]),
					ChecksumScope: scope,
				})
				if err != nil {
					t.Fatalf("Stream() = %v", err)
				}
				if got, _ := os.ReadFile(target); !bytes.Equal(got, data) {
					t.Fatalf("target has %d bytes, want the %d decompressed bytes", len(got), len(data))
				}
			})
		}
	}
}

// TestStreamOCIContainerLayer pushes a gzip image as a container layer,
// which gzips it again for transport: BOOTy must remove both.
func TestStreamOCIContainerLayer(t *testing.T) {
	data := testImageData()
	gz := encodeGzipMembers(t, data)
	var transport bytes.Buffer
	w := gzip.NewWriter(&transport)
	if _, err := w.Write(gz); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	source := pushOCIImageBlob(t, transport.Bytes(), string(types.OCILayer))
	target := filepath.Join(t.TempDir(), "disk")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(gz)
	if err := Stream(context.Background(), source, target, StreamOpts{Checksum: hex.EncodeToString(sum[:])}); err != nil {
		t.Fatalf("Stream() = %v", err)
	}
	if got, _ := os.ReadFile(target); !bytes.Equal(got, data) {
		t.Fatalf("target has %d bytes, want the %d image bytes", len(got), len(data))
	}
}

// TestValidateStreamingPrerequisitesFindsOCIQCOW2 reads the magic of an
// oci:// layer, as the pre-wipe probe does with IMAGE_STREAM.
func TestValidateStreamingPrerequisitesFindsOCIQCOW2(t *testing.T) {
	qcow2 := append([]byte{0x51, 0x46, 0x49, 0xfb}, make([]byte, 1024)...)
	source := pushOCIImageBlob(t, qcow2, "application/vnd.tcaas.node-image+qcow2")
	format, err := ValidateStreamingPrerequisites(context.Background(), source)
	if err != nil || format != FormatQCOW2 {
		t.Fatalf("ValidateStreamingPrerequisites() = %s, %v; want qcow2", format, err)
	}
}

func TestNewStagingLimit(t *testing.T) {
	orig := ramdiskMemInfo
	t.Cleanup(func() { ramdiskMemInfo = orig })

	for _, tt := range []struct {
		name    string
		meminfo string
		want    stagingLimit
	}{
		{"3/4 of MemAvailable", "MemTotal: 4096000 kB\nMemAvailable: 2048000 kB\n", stagingLimit{tmpfsMiB: 1500, availableMiB: 2000}},
		{"at least 1 MiB", "MemTotal: 4096000 kB\nMemAvailable: 1024 kB\n", stagingLimit{tmpfsMiB: 1, availableMiB: 1}},
		{"no meminfo", "", stagingLimit{tmpfsMiB: ramdiskFallbackMiB}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ramdiskMemInfo = filepath.Join(t.TempDir(), "meminfo")
			if tt.meminfo != "" {
				if err := os.WriteFile(ramdiskMemInfo, []byte(tt.meminfo), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got := newStagingLimit(); got != tt.want {
				t.Fatalf("newStagingLimit() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// fakeFullStagingTmpfs points the staging tmpfs at a directory whose staged
// files are /dev/full, so every staging write fails with ENOSPC, and sizes
// it from a 2000 MiB MemAvailable.
func fakeFullStagingTmpfs(t *testing.T) {
	t.Helper()
	origPath, origMount, origInfo := ramdiskPath, mountTmpfs, ramdiskMemInfo
	t.Cleanup(func() { ramdiskPath, mountTmpfs, ramdiskMemInfo = origPath, origMount, origInfo })
	ramdiskPath = t.TempDir()
	mountTmpfs = func(string, string, string, uintptr, string) error { return nil }
	ramdiskMemInfo = filepath.Join(t.TempDir(), "meminfo")
	if err := os.WriteFile(ramdiskMemInfo, []byte("MemAvailable: 2048000 kB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"image.qcow2", "image.download"} {
		if err := os.Symlink("/dev/full", filepath.Join(ramdiskPath, name)); err != nil {
			t.Fatal(err)
		}
	}
}

// TestStagingFullEveryCaller checks that every staging caller reports a full
// staging tmpfs as ErrStagingFull.
func TestStagingFullEveryCaller(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("no /dev/full")
	}
	qcow2 := append([]byte{0x51, 0x46, 0x49, 0xfb}, make([]byte, 1024)...)
	target := filepath.Join(t.TempDir(), "disk")
	for name, stage := range map[string]func() error{
		"qcow2 whole-disk": func() error {
			return ConvertQCOW2FromReader(context.Background(), bytes.NewReader(qcow2), "image.qcow2", target)
		},
		"qcow2 A/B": func() error {
			return streamABViaRamdisk(context.Background(), bytes.NewReader(qcow2), ABTargets{RootPartition: target}, StreamOpts{})
		},
		"partition mode": func() error {
			return StreamPartitions(context.Background(), serveBytes(t, qcow2)+"/image", target)
		},
	} {
		t.Run(name, func(t *testing.T) {
			fakeFullStagingTmpfs(t)
			err := stage()
			if !errors.Is(err, ErrStagingFull) || !strings.Contains(err.Error(), "tmpfs 1500 MiB, MemAvailable 2000 MiB") {
				t.Fatalf("err = %v, want ErrStagingFull for a 1500 MiB tmpfs", err)
			}
		})
	}
}

func TestStagingLimitCheck(t *testing.T) {
	limit := stagingLimit{tmpfsMiB: 1500, availableMiB: 2000}
	full := fmt.Errorf("writing qcow2 image: %w", &fs.PathError{Op: "write", Path: "image.qcow2", Err: syscall.ENOSPC})
	err := limit.check(full)
	if !errors.Is(err, ErrStagingFull) || !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("check(ENOSPC) = %v, want ErrStagingFull wrapping ENOSPC", err)
	}
	want := "staged image does not fit (tmpfs 1500 MiB, MemAvailable 2000 MiB); use a .raw.lz4 image or IMAGE_STREAM"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("check(ENOSPC) = %q, want %q", err, want)
	}
	other := errors.New("read error")
	if err := limit.check(other); err != other { //nolint:errorlint // the same error must come back
		t.Fatalf("check(other) = %v, want it unchanged", err)
	}
	if err := limit.check(nil); err != nil {
		t.Fatalf("check(nil) = %v", err)
	}
}

// TestOCIImageBlobAbortsWhenRegistryStalls stalls the layer blob after 4 KiB:
// the idle read timeout must end the read, like for an HTTP source.
func TestOCIImageBlobAbortsWhenRegistryStalls(t *testing.T) {
	orig := imageIdleReadTimeout
	t.Cleanup(func() { imageIdleReadTimeout = orig })
	imageIdleReadTimeout = 100 * time.Millisecond

	blob := bytes.Repeat([]byte{0xaa}, 64<<10)
	layer := static.NewLayer(blob, "application/vnd.tcaas.node-image+raw.lz4")
	digest, err := layer.Digest()
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	var stall atomic.Bool
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !stall.Load() || r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, digest.String()) {
			reg.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(blob)))
		_, _ = w.Write(blob[:4096])
		w.(http.Flusher).Flush()
		select { // stall without closing the connection
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)

	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := name.ParseReference(strings.TrimPrefix(srv.URL, "http://") + "/test/stall:v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(ref, img); err != nil {
		t.Fatal(err)
	}
	stall.Store(true)

	rc, err := FetchOCIImageBlob(context.Background(), ref.String())
	if err != nil {
		t.Fatalf("FetchOCIImageBlob() = %v", err)
	}
	defer func() { _ = rc.Close() }()
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(rc)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "stalled") {
			t.Fatalf("read error = %v, want a stall error", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the stalled OCI blob read did not end")
	}
}
