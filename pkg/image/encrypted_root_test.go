//go:build linux

package image

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func serveEncryptedImage(t *testing.T, raw []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(raw) }))
	t.Cleanup(srv.Close)
	return srv.URL + "/image.raw"
}

func TestStreamEncryptedRoot(t *testing.T) {
	raw := testGPTImage(t)
	fillPartition(raw, 8160, 8191, 'Z') // stands in for the backup GPT
	sum := sha256.Sum256(raw)
	dir := t.TempDir()
	disk, mapped := filepath.Join(dir, "disk"), filepath.Join(dir, "mapped")
	if err := os.WriteFile(disk, make([]byte, 8192*gptSectorSize), 0o600); err != nil {
		t.Fatal(err)
	}
	createTargetFile(t, mapped)
	var gotNum int
	var gotSize int64
	err := StreamEncryptedRoot(context.Background(), serveEncryptedImage(t, raw), disk, EncryptedRoot{
		Open: func(_ context.Context, number int, size int64) (string, error) {
			gotNum, gotSize = number, size
			// The partition table and the ESP are on the disk already.
			if d := readFile(t, disk); !bytes.Equal(d[:4096*gptSectorSize], raw[:4096*gptSectorSize]) {
				t.Error("the image before the root is not on the disk when Open runs")
			}
			return mapped, nil
		},
	}, StreamOpts{Checksum: hex.EncodeToString(sum[:]), ChecksumType: "sha256"})
	if err != nil {
		t.Fatal(err)
	}
	if gotNum != 2 || gotSize != 16*gptSectorSize {
		t.Fatalf("Open(%d, %d), want partition 2 with 16 sectors", gotNum, gotSize)
	}
	d := readFile(t, disk)
	if tail := d[4096*gptSectorSize:]; bytes.ContainsRune(tail, 'R') || bytes.ContainsRune(tail, 'Z') {
		t.Fatal("the root partition or the image tail reached the disk")
	}
	if !bytes.Equal(readFile(t, mapped)[:16*gptSectorSize], bytes.Repeat([]byte("R"), 16*gptSectorSize)) {
		t.Fatal("the root partition did not go into the mapping")
	}
}

func TestStreamEncryptedRootChecksumMismatchWipesDisk(t *testing.T) {
	raw := testGPTImage(t)
	dir := t.TempDir()
	disk, mapped := filepath.Join(dir, "disk"), filepath.Join(dir, "mapped")
	createTargetFile(t, disk)
	createTargetFile(t, mapped)
	err := StreamEncryptedRoot(context.Background(), serveEncryptedImage(t, raw), disk, EncryptedRoot{
		Open: func(context.Context, int, int64) (string, error) { return mapped, nil },
	}, StreamOpts{Checksum: string(bytes.Repeat([]byte("0"), 64)), ChecksumType: "sha256"})
	if err == nil {
		t.Fatal("checksum mismatch accepted")
	}
	if bytes.Contains(readFile(t, disk)[:2*gptSectorSize], []byte("EFI PART")) {
		t.Fatal("partition table left after a checksum mismatch")
	}
}

func TestStreamEncryptedRootNotLast(t *testing.T) {
	raw := testGPTImage(t)
	// A third partition behind the root.
	writeGPTEntry(raw, 2, []byte{0x28, 0x73, 0x2a, 0xc1, 0x1f, 0xf8, 0xd2, 0x11, 0xba, 0x4b, 0x00, 0xa0, 0xc9, 0x3e, 0xc9, 0x3b}, 6000, 6007, "late")
	disk := filepath.Join(t.TempDir(), "disk")
	createTargetFile(t, disk)
	opened := false
	err := StreamEncryptedRoot(context.Background(), serveEncryptedImage(t, raw), disk, EncryptedRoot{
		Open: func(context.Context, int, int64) (string, error) { opened = true; return "", nil },
	})
	if !errors.Is(err, ErrEncryptedRootUnsupported) || opened {
		t.Fatalf("err = %v opened = %v, want ErrEncryptedRootUnsupported before Open", err, opened)
	}
	if bytes.Contains(readFile(t, disk), []byte("EFI PART")) {
		t.Fatal("disk written for an unsupported layout")
	}
}
