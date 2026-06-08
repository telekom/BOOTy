//go:build linux

package image

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestStreamABRawCopiesGPTBootAndRootRanges(t *testing.T) {
	raw := testGPTImage(t)
	sum := sha256.Sum256(raw)

	bootTarget := filepath.Join(t.TempDir(), "boot.img")
	rootTarget := filepath.Join(t.TempDir(), "root.img")
	createTargetFile(t, bootTarget)
	createTargetFile(t, rootTarget)

	err := streamABRaw(context.Background(), bytes.NewReader(raw), ABTargets{
		BootPartition: bootTarget,
		RootPartition: rootTarget,
	}, StreamOpts{Checksum: hex.EncodeToString(sum[:]), ChecksumType: "sha256"})
	if err != nil {
		t.Fatalf("streamABRaw: %v", err)
	}

	boot := readFile(t, bootTarget)
	if !bytes.HasPrefix(boot, bytes.Repeat([]byte("B"), 32)) {
		t.Fatalf("boot target was not copied from EFI partition: %q", string(boot[:min(32, len(boot))]))
	}
	root := readFile(t, rootTarget)
	if !bytes.HasPrefix(root, bytes.Repeat([]byte("R"), 32)) {
		t.Fatalf("root target was not copied from root partition: %q", string(root[:min(32, len(root))]))
	}
}

func TestStreamABRawFallsBackToRootFilesystemWhenNoGPT(t *testing.T) {
	raw := []byte("rootfs without partition table")
	rootTarget := filepath.Join(t.TempDir(), "root.img")
	createTargetFile(t, rootTarget)

	err := streamABRaw(context.Background(), bytes.NewReader(raw), ABTargets{RootPartition: rootTarget}, StreamOpts{})
	if err != nil {
		t.Fatalf("streamABRaw fallback: %v", err)
	}

	got := strings.TrimRight(string(readFile(t, rootTarget)), "\x00")
	if got != string(raw) {
		t.Fatalf("root fallback = %q, want %q", got, string(raw))
	}
}

func TestParseGPTPartitionsSelectsExpectedTypes(t *testing.T) {
	parts, err := parseGPTPartitions(testGPTImage(t)[:streamABPrefixBytes])
	if err != nil {
		t.Fatalf("parseGPTPartitions: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("partitions = %d, want 2", len(parts))
	}

	boot, ok := selectSourceBootPartition(parts)
	if !ok || boot.Type != efiSystemPartitionGUID || boot.Start != 2048 {
		t.Fatalf("boot partition = %#v, ok=%v", boot, ok)
	}
	root, err := selectSourceRootPartition(parts, "", 0)
	if err != nil || root.Type != linuxFilesystemGUID || root.Start != 4096 || root.Name != "rootfs" || root.Number != 2 {
		t.Fatalf("root partition = %#v, err=%v", root, err)
	}
}

func TestCopyReaderFailsOnShortWrite(t *testing.T) {
	err := copyReader(context.Background(), shortWriter{}, strings.NewReader("payload"))
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("copyReader() error = %v, want io.ErrShortWrite", err)
	}
	if err != nil && !strings.Contains(err.Error(), "writing stream chunk") {
		t.Fatalf("copyReader() error = %q, want stream write context", err.Error())
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return len(p) - 1, nil
}

func testGPTImage(t *testing.T) []byte {
	t.Helper()

	const sectors = 8192
	raw := make([]byte, sectors*gptSectorSize)
	header := raw[gptSectorSize : 2*gptSectorSize]
	copy(header[:8], "EFI PART")
	binary.LittleEndian.PutUint32(header[8:12], 0x00010000)
	binary.LittleEndian.PutUint32(header[12:16], 92)
	binary.LittleEndian.PutUint64(header[24:32], 1)
	binary.LittleEndian.PutUint64(header[32:40], sectors-1)
	binary.LittleEndian.PutUint64(header[40:48], 2048)
	binary.LittleEndian.PutUint64(header[48:56], sectors-34)
	binary.LittleEndian.PutUint64(header[72:80], 2)
	binary.LittleEndian.PutUint32(header[80:84], 128)
	binary.LittleEndian.PutUint32(header[84:88], 128)

	writeGPTEntry(raw, 0, []byte{
		0x28, 0x73, 0x2a, 0xc1, 0x1f, 0xf8, 0xd2, 0x11,
		0xba, 0x4b, 0x00, 0xa0, 0xc9, 0x3e, 0xc9, 0x3b,
	}, 2048, 2055, "ESP")
	writeGPTEntry(raw, 1, []byte{
		0xaf, 0x3d, 0xc6, 0x0f, 0x83, 0x84, 0x72, 0x47,
		0x8e, 0x79, 0x3d, 0x69, 0xd8, 0x47, 0x7d, 0xe4,
	}, 4096, 4111, "rootfs")

	fillPartition(raw, 2048, 2055, 'B')
	fillPartition(raw, 4096, 4111, 'R')
	return raw
}

func writeGPTEntry(raw []byte, index int, typeGUID []byte, firstLBA, lastLBA uint64, name string) {
	entry := raw[2*gptSectorSize+index*128 : 2*gptSectorSize+(index+1)*128]
	copy(entry[:16], typeGUID)
	for i := 16; i < 32; i++ {
		entry[i] = byte(index + 1)
	}
	binary.LittleEndian.PutUint64(entry[32:40], firstLBA)
	binary.LittleEndian.PutUint64(entry[40:48], lastLBA)
	encodedName := utf16.Encode([]rune(name))
	for i, r := range encodedName {
		if 56+i*2+1 >= len(entry) {
			break
		}
		binary.LittleEndian.PutUint16(entry[56+i*2:58+i*2], r)
	}
}

func fillPartition(raw []byte, firstLBA, lastLBA uint64, value byte) {
	start := int(firstLBA) * gptSectorSize
	end := int(lastLBA+1) * gptSectorSize
	for i := start; i < end; i++ {
		raw[i] = value
	}
}

func createTargetFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, make([]byte, 128*1024), 0o600); err != nil {
		t.Fatalf("create target file: %v", err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test path
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
