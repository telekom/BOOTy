//go:build e2e

package kvm

import (
	"crypto/rand"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
)

// imageStreamPayloadMiB is the incompressible file in the root filesystem of
// the test image: it makes the tmpfs staging of a qcow2 image visible in the
// guest memory (1024 MiB).
const imageStreamPayloadMiB = 200

var peakUsedRe = regexp.MustCompile(`image step memory.*peak_used_mib=(\d+)`)

// TestProvisionImageStreamQEMU provisions the same OS image as a staged
// qcow2 and, with IMAGE_STREAM=true, as lz4 and zstd raw images, and compares
// the RAM high-water mark of the image step that BOOTy logs. A qcow2 image
// with IMAGE_STREAM=true must fail before the disk wipe.
func TestProvisionImageStreamQEMU(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	requireDiskInspectTools(t)

	rawDisk := createTestDiskImage(t, 512)
	addRandomRootPayload(t, rawDisk, imageStreamPayloadMiB)
	dir := t.TempDir()
	qcow2Image := filepath.Join(dir, "image.qcow2")
	run(t, "convert image to qcow2", "qemu-img", "convert", "-f", "raw", "-O", "qcow2", rawDisk, qcow2Image)
	lz4Image := compressFile(t, rawDisk, filepath.Join(dir, "image.raw.lz4"), func(w io.Writer) io.WriteCloser {
		lw := lz4.NewWriter(w)
		_ = lw.Apply(lz4.ChecksumOption(false)) // like --no-frame-crc of the image builder
		return lw
	})
	zstdImage := compressFile(t, rawDisk, filepath.Join(dir, "image.raw.zst"), func(w io.Writer) io.WriteCloser {
		zw, _ := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedFastest))
		return zw
	})

	peaks := map[string]int{}
	for _, tt := range []struct {
		name   string
		image  string
		stream bool
	}{
		{"qcow2 staged", qcow2Image, false},
		{"lz4 IMAGE_STREAM", lz4Image, true},
		{"zstd IMAGE_STREAM", zstdImage, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			hostname := "stream-" + strings.ReplaceAll(strings.ToLower(tt.name), " ", "-")
			output, targetDisk := provisionImageQEMU(t, tt.image, hostname, tt.stream)
			rootMount, cleanup := mountQcow2(t, targetDisk)
			defer cleanup()
			if got := strings.TrimSpace(readProvisionedFile(t, rootMount, "etc/hostname")); got != hostname {
				t.Errorf("hostname: got %q, want %q", got, hostname)
			}
			m := peakUsedRe.FindStringSubmatch(string(output))
			if m == nil {
				t.Fatalf("no image step memory line; tail:\n%s", tail(output, 3000))
			}
			peaks[tt.name], _ = strconv.Atoi(m[1])
			t.Logf("image step peak used RAM: %s MiB", m[1])
		})
	}

	staged, streamed := peaks["qcow2 staged"], max(peaks["lz4 IMAGE_STREAM"], peaks["zstd IMAGE_STREAM"])
	t.Logf("peak used RAM of the image step: qcow2 staged %d MiB, streamed at most %d MiB", staged, streamed)
	if staged > 0 && streamed > 0 && staged-streamed < imageStreamPayloadMiB {
		t.Errorf("streaming saved %d MiB, want at least the %d MiB payload", staged-streamed, imageStreamPayloadMiB)
	}

	t.Run("qcow2 IMAGE_STREAM fails before the wipe", func(t *testing.T) {
		output, _ := provisionImageQEMU(t, qcow2Image, "stream-qcow2-rejected", true)
		if !strings.Contains(string(output), "IMAGE_STREAM=true forbids staging") {
			t.Fatalf("no IMAGE_STREAM rejection; tail:\n%s", tail(output, 3000))
		}
		if strings.Contains(string(output), "image step memory") {
			t.Fatalf("the image step ran; tail:\n%s", tail(output, 3000))
		}
	})
}

// TestProvisionQCOW2LargerThanStagingQEMU stages a qcow2 image that is larger
// than the staging tmpfs (3/4 of MemAvailable of the 1024 MiB guest): the run
// must fail once with a clear error, without an out-of-memory kill and
// without stream-image retries.
func TestProvisionQCOW2LargerThanStagingQEMU(t *testing.T) {
	requireRoot(t)
	qemuAvailable(t)
	requireProvisionTools(t)
	requireDiskInspectTools(t)

	rawDisk := createTestDiskImage(t, 1024)
	addRandomRootPayload(t, rawDisk, 750)
	qcow2Image := filepath.Join(t.TempDir(), "image.qcow2")
	run(t, "convert image to qcow2", "qemu-img", "convert", "-f", "raw", "-O", "qcow2", rawDisk, qcow2Image)

	output, _ := provisionImageQEMU(t, qcow2Image, "stream-qcow2-too-large", false)
	if !strings.Contains(string(output), "staged image does not fit (tmpfs") {
		t.Fatalf("no staging-full error; tail:\n%s", tail(output, 3000))
	}
	if n := strings.Count(string(output), "qcow2 image detected, writing to ramdisk"); n != 1 {
		t.Fatalf("staged %d times, want 1 (no retry); tail:\n%s", n, tail(output, 3000))
	}
	if strings.Contains(string(output), "Out of memory") {
		t.Fatalf("the guest ran out of memory; tail:\n%s", tail(output, 3000))
	}
}

// provisionImageQEMU provisions a fresh 2 GiB target from image and returns
// the QEMU output and the target disk.
func provisionImageQEMU(t *testing.T, image, hostname string, stream bool) (output []byte, targetDisk string) {
	t.Helper()
	baseURL := startImageServer(t, image)
	targetDisk = filepath.Join(t.TempDir(), "target.qcow2")
	run(t, "create target disk", "qemu-img", "create", "-f", "qcow2", targetDisk, "2G")
	initramfs := buildProvisionInitramfs(t, map[string]string{
		"HOSTNAME":                  hostname,
		"IMAGE_ALLOW_INSECURE_HTTP": "true",
		// The server path is /image.gz; BOOTy detects the format by magic.
		"IMAGE":        baseURL + "/image.gz",
		"IMAGE_STREAM": strconv.FormatBool(stream),
		"MODE":         "provision",
		"DISK_DEVICE":  "/dev/vda",
	})
	output = runQEMUProvision(t, findKernel(t), initramfs, targetDisk, 6*time.Minute)
	t.Logf("QEMU output tail:\n%s", tail(output, 2000))
	return output, targetDisk
}

// addRandomRootPayload writes an incompressible file of sizeMiB into the root
// partition of a test disk image.
func addRandomRootPayload(t *testing.T, rawPath string, sizeMiB int) {
	t.Helper()
	loopDev := strings.TrimSpace(string(runOutput(t, "attach payload loop", "losetup", "--find", "--show", "--partscan", rawPath)))
	defer func() { _ = exec.CommandContext(t.Context(), "losetup", "-d", loopDev).Run() }()
	rootDev := loopDev + "p2"
	waitForDevice(t, rootDev, 5*time.Second)
	mountDir := filepath.Join(t.TempDir(), "rootmnt")
	if err := os.MkdirAll(mountDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mountWithRetry(t, "mount payload root", rootDev, mountDir)
	defer func() { _ = exec.CommandContext(t.Context(), "umount", mountDir).Run() }()

	f, err := os.Create(filepath.Join(mountDir, "var", "payload.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(f, rand.Reader, int64(sizeMiB)<<20); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	run(t, "unmount payload root", "umount", mountDir)
}

func compressFile(t *testing.T, src, dst string, enc func(io.Writer) io.WriteCloser) string {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	w := enc(out)
	if _, err := io.Copy(w, in); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return dst
}
