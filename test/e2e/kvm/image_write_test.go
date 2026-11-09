//go:build e2e

package kvm

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

var skippedBlocksRe = regexp.MustCompile(`image write skipped blocks.*punched_zero_bytes=(\d+) unchanged_bytes=(\d+) written_bytes=(\d+)`)

// TestProvisionImageWriteOptionsQEMU provisions a zstd image with
// IMAGE_SPARSE_WRITE=auto onto a fresh virtio disk, then the same image again
// onto the same disk with IMAGE_SKIP_UNCHANGED=true. The first run must punch
// the zero blocks (virtio-blk offers WRITE ZEROES), the second must find most
// blocks in place, and both must boot to a root with the right hostname.
func TestProvisionImageWriteOptionsQEMU(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	requireDiskInspectTools(t)

	rawDisk := createTestDiskImage(t, 512)
	addRandomRootPayload(t, rawDisk, 64)
	zstdImage := compressFile(t, rawDisk, filepath.Join(t.TempDir(), "image.raw.zst"), func(w io.Writer) io.WriteCloser {
		zw, _ := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedFastest))
		return zw
	})
	baseURL := startImageServer(t, zstdImage)
	targetDisk := filepath.Join(t.TempDir(), "target.qcow2")
	run(t, "create target disk", "qemu-img", "create", "-f", "qcow2", targetDisk, "2G")

	for _, tt := range []struct {
		name string
		vars map[string]string
		// check gets punched, unchanged and written bytes.
		check func(t *testing.T, punched, unchanged, written int64)
	}{
		{
			name: "sparse",
			vars: map[string]string{"IMAGE_SPARSE_WRITE": "auto"},
			check: func(t *testing.T, punched, _, _ int64) {
				t.Helper()
				if punched == 0 {
					t.Error("no zero block was punched")
				}
			},
		},
		{
			name: "skip-unchanged",
			// IMAGE_SKIP_UNCHANGED needs the quick wipe, which keeps the data.
			vars: map[string]string{"IMAGE_SKIP_UNCHANGED": "true", "WIPE_DEPTH": "quick"},
			check: func(t *testing.T, _, unchanged, written int64) {
				t.Helper()
				if unchanged < 9*written {
					t.Errorf("unchanged %d bytes, written %d: want at least 90 %% in place", unchanged, written)
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			hostname := "image-write-" + tt.name
			vars := map[string]string{
				"HOSTNAME":                  hostname,
				"IMAGE_ALLOW_INSECURE_HTTP": "true",
				"IMAGE":                     baseURL + "/image.gz", // BOOTy detects the format by magic
				"MODE":                      "provision",
				"DISK_DEVICE":               "/dev/vda",
			}
			for k, v := range tt.vars {
				vars[k] = v
			}
			output := runQEMUProvision(t, findKernel(t), buildProvisionInitramfs(t, vars), targetDisk, 6*time.Minute)
			m := skippedBlocksRe.FindStringSubmatch(string(output))
			if m == nil {
				t.Fatalf("no image write skipped blocks line; tail:\n%s", tail(output, 3000))
			}
			n := make([]int64, 3)
			for i := range n {
				n[i], _ = strconv.ParseInt(m[i+1], 10, 64)
			}
			t.Logf("punched %d, unchanged %d, written %d bytes", n[0], n[1], n[2])
			tt.check(t, n[0], n[1], n[2])

			rootMount, cleanup := mountQcow2(t, targetDisk)
			defer cleanup()
			if got := strings.TrimSpace(readProvisionedFile(t, rootMount, "etc/hostname")); got != hostname {
				t.Errorf("hostname: got %q, want %q", got, hostname)
			}
		})
	}
}

// TestProvisionImageDownloadOptionsQEMU provisions a zstd image from a server
// that breaks every full GET at half of the image and serves ranges with a
// strong ETag. IMAGE_RESUME must continue the broken download and
// IMAGE_PARALLEL_RANGES must fetch the image in ranges; both runs must boot
// to a root with the right hostname.
func TestProvisionImageDownloadOptionsQEMU(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	requireDiskInspectTools(t)

	rawDisk := createTestDiskImage(t, 512)
	addRandomRootPayload(t, rawDisk, 64) // over 8 MiB compressed: parallel ranges apply
	zstdImage := compressFile(t, rawDisk, filepath.Join(t.TempDir(), "image.raw.zst"), func(w io.Writer) io.WriteCloser {
		zw, _ := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedFastest))
		return zw
	})
	baseURL := startBreakingImageServer(t, zstdImage)

	for _, tt := range []struct {
		name, key, value, wantLog string
	}{
		{"resume", "IMAGE_RESUME", "true", "image download broke, resuming"},
		{"parallel", "IMAGE_PARALLEL_RANGES", "4", "downloading image in parallel ranges"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			hostname := "image-download-" + tt.name
			targetDisk := filepath.Join(t.TempDir(), "target.qcow2")
			run(t, "create target disk", "qemu-img", "create", "-f", "qcow2", targetDisk, "2G")
			output := runQEMUProvision(t, findKernel(t), buildProvisionInitramfs(t, map[string]string{
				"HOSTNAME":                  hostname,
				"IMAGE_ALLOW_INSECURE_HTTP": "true",
				"IMAGE":                     baseURL + "/image.gz",
				"MODE":                      "provision",
				"DISK_DEVICE":               "/dev/vda",
				tt.key:                      tt.value,
			}), targetDisk, 6*time.Minute)
			if !strings.Contains(string(output), tt.wantLog) {
				t.Fatalf("no %q line; tail:\n%s", tt.wantLog, tail(output, 3000))
			}
			rootMount, cleanup := mountQcow2(t, targetDisk)
			defer cleanup()
			if got := strings.TrimSpace(readProvisionedFile(t, rootMount, "etc/hostname")); got != hostname {
				t.Errorf("hostname: got %q, want %q", got, hostname)
			}
		})
	}
}

// startBreakingImageServer serves imagePath at /image.gz with a strong ETag
// and Range support, and breaks every GET without Range after half of the
// file, like a connection reset.
func startBreakingImageServer(t *testing.T, imagePath string) string {
	t.Helper()
	data, err := os.ReadFile(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"kvm-image"`)
		if r.Header.Get("Range") == "" {
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			_, _ = w.Write(data[:len(data)/2])
			panic(http.ErrAbortHandler)
		}
		http.ServeContent(w, r, "image.gz", time.Time{}, bytes.NewReader(data))
	})
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close() })
	return fmt.Sprintf("http://10.0.2.2:%d", listener.Addr().(*net.TCPAddr).Port)
}
