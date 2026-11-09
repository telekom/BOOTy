//go:build e2e

package kvm

import (
	"io"
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
		zw, err := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedFastest))
		if err != nil {
			t.Fatal(err)
		}
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
			// Zero blocks are never compared, so punch them.
			vars: map[string]string{"IMAGE_SKIP_UNCHANGED": "true", "IMAGE_SPARSE_WRITE": "auto", "WIPE_DEPTH": "quick"},
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
