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
		Open: func(_ context.Context, number int, size int64, lvm bool) (string, error) {
			if lvm {
				t.Error("the root partition is not LVM")
			}
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
		Open: func(context.Context, int, int64, bool) (string, error) { return mapped, nil },
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
		Open: func(context.Context, int, int64, bool) (string, error) { opened = true; return "", nil },
	})
	if !errors.Is(err, ErrEncryptedRootUnsupported) || opened {
		t.Fatalf("err = %v opened = %v, want ErrEncryptedRootUnsupported before Open", err, opened)
	}
	if bytes.Contains(readFile(t, disk), []byte("EFI PART")) {
		t.Fatal("disk written for an unsupported layout")
	}
}

// lvmTypeGUID is E6D6D379-F507-44C2-A23C-238F2A3DF928 in GPT byte order.
var lvmTypeGUID = []byte{0x79, 0xd3, 0xd6, 0xe6, 0x07, 0xf5, 0xc2, 0x44, 0xa2, 0x3c, 0x23, 0x8f, 0x2a, 0x3d, 0xf9, 0x28}

// An image with the root on LVM: no root partition, one LVM physical
// volume. The volume goes into the mapping, Open learns that it is LVM.
func TestStreamEncryptedRootLVM(t *testing.T) {
	raw := testGPTImage(t)
	writeGPTEntry(raw, 1, lvmTypeGUID, 4096, 4111, "tcaas-pv")
	dir := t.TempDir()
	disk, mapped := filepath.Join(dir, "disk"), filepath.Join(dir, "mapped")
	createTargetFile(t, disk)
	createTargetFile(t, mapped)
	var gotNum int
	var gotLVM bool
	err := StreamEncryptedRoot(context.Background(), serveEncryptedImage(t, raw), disk, EncryptedRoot{
		Open: func(_ context.Context, number int, _ int64, lvm bool) (string, error) {
			gotNum, gotLVM = number, lvm
			return mapped, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotNum != 2 || !gotLVM {
		t.Fatalf("Open(%d, lvm=%v), want the LVM partition 2", gotNum, gotLVM)
	}
	if bytes.ContainsRune(readFile(t, disk)[4096*gptSectorSize:], 'R') {
		t.Fatal("the physical volume reached the disk")
	}
	if !bytes.Equal(readFile(t, mapped)[:16*gptSectorSize], bytes.Repeat([]byte("R"), 16*gptSectorSize)) {
		t.Fatal("the physical volume did not go into the mapping")
	}
}

func TestSelectEncryptedRootLVM(t *testing.T) {
	esp := sfdiskPartition{Number: 1, Start: 2048, Type: efiSystemPartitionGUID}
	pv := sfdiskPartition{Number: 2, Start: 4096, Type: linuxLVMGUID}
	root := sfdiskPartition{Number: 3, Start: 8192, Type: linuxFilesystemGUID}
	for _, tc := range []struct {
		name   string
		parts  []sfdiskPartition
		number int
		want   int
	}{
		{"root on LVM", []sfdiskPartition{esp, pv}, 0, 2},
		{"a root partition wins over a physical volume", []sfdiskPartition{esp, pv, root}, 0, 3},
		{"physical volume by number", []sfdiskPartition{esp, pv}, 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectEncryptedRoot(tc.parts, tc.number)
			if err != nil || got.Number != tc.want {
				t.Fatalf("selectEncryptedRoot = %d, %v; want partition %d", got.Number, err, tc.want)
			}
		})
	}
	two := []sfdiskPartition{esp, pv, {Number: 3, Start: 8192, Type: linuxLVMGUID}}
	if _, err := selectEncryptedRoot(two, 0); err == nil {
		t.Fatal("two physical volumes accepted without a partition number")
	}
	// A root label does not make a physical volume the root.
	named := sfdiskPartition{Number: 3, Start: 8192, Type: linuxLVMGUID, Name: "root"}
	if _, err := selectEncryptedRoot([]sfdiskPartition{esp, pv, named}, 0); err == nil {
		t.Fatal("a second physical volume named root accepted")
	}
	plain := sfdiskPartition{Number: 2, Start: 4096, Type: linuxFilesystemGUID}
	if got, err := selectEncryptedRoot([]sfdiskPartition{esp, plain, named}, 0); err == nil && got.Number == 3 {
		t.Fatal("a physical volume named root won over the root partition")
	}
}
