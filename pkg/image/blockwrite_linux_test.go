//go:build linux

package image

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// TestPunchHoleZeroesRange punches a hole into a file, which reads back as
// zeros like a block device after WRITE ZEROES.
func TestPunchHoleZeroesRange(t *testing.T) {
	const b = writeBlockSize
	f := targetFile(t, bytes.Repeat([]byte{0xee}, 3*b))
	if err := punchHole(f, b, b); err != nil {
		if errors.Is(err, unix.EOPNOTSUPP) {
			t.Skipf("the file system of %s cannot punch holes: %v", f.Name(), err)
		}
		t.Fatalf("punchHole() = %v", err)
	}
	got, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	want := append(append(bytes.Repeat([]byte{0xee}, b), make([]byte, b)...), bytes.Repeat([]byte{0xee}, b)...)
	if !bytes.Equal(got, want) {
		t.Fatal("punched range does not read back as zeros, or the rest changed")
	}
}

// TestStreamWriteOptions streams a zstd image with both write options and a
// download checksum: the target holds the image and the checksum still
// covers every byte.
func TestStreamWriteOptions(t *testing.T) {
	img := testImage()
	downloaded := compressForTest(t, "zst", img)
	url := serveBytes(t, downloaded) + "/image.raw.zst"
	for _, tt := range []struct {
		name     string
		checksum string
		wantErr  error
	}{
		{name: "checksum matches", checksum: sha256Hex(downloaded)},
		{name: "checksum mismatch", checksum: sha256Hex(img), wantErr: ErrChecksumMismatch},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := fakePunch(t, nil)
			f := targetFile(t, bytes.Repeat([]byte{0xee}, len(img)))
			err := Stream(t.Context(), url, f.Name(), StreamOpts{Checksum: tt.checksum, SparseWrite: true, SkipUnchanged: true})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Stream() = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if len(*calls) == 0 {
				t.Fatal("no zero run was punched")
			}
			got, err := os.ReadFile(f.Name())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, img) {
				t.Fatal("target does not hold the image")
			}
		})
	}
}
