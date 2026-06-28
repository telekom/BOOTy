//go:build linux

package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestTargetPartitionNode(t *testing.T) {
	tests := []struct {
		name    string
		disk    string
		partNum int
		want    string
	}{
		{"sda partition 1", "/dev/sda", 1, "/dev/sda1"},
		{"sda partition 2", "/dev/sda", 2, "/dev/sda2"},
		{"nvme partition 1", "/dev/nvme0n1", 1, "/dev/nvme0n1p1"},
		{"nvme partition 3", "/dev/nvme0n1", 3, "/dev/nvme0n1p3"},
		{"loop partition 1", "/dev/loop0", 1, "/dev/loop0p1"},
		{"vda partition 2", "/dev/vda", 2, "/dev/vda2"},
		{"mmcblk partition 1", "/dev/mmcblk0", 1, "/dev/mmcblk0p1"},
		{"mmcblk partition 2", "/dev/mmcblk0", 2, "/dev/mmcblk0p2"},
		{"mmcblk1 partition 3", "/dev/mmcblk1", 3, "/dev/mmcblk1p3"},
		{"md partition 1", "/dev/md0", 1, "/dev/md0p1"},
		{"nbd partition 2", "/dev/nbd0", 2, "/dev/nbd0p2"},
		{"by-id partition 1", "/dev/disk/by-id/nvme-eui.0011223344556677", 1, "/dev/disk/by-id/nvme-eui.0011223344556677-part1"},
		{"by-path partition 2", "/dev/disk/by-path/pci-0000:00:1f.2-ata-1", 2, "/dev/disk/by-path/pci-0000:00:1f.2-ata-1-part2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := targetPartitionNode(tt.disk, tt.partNum)
			if got != tt.want {
				t.Errorf("targetPartitionNode(%q, %d) = %q, want %q", tt.disk, tt.partNum, got, tt.want)
			}
		})
	}
}

func TestConvertQCOW2HookRegistered(t *testing.T) {
	// On linux, the init() in qcow2.go should have set the hook.
	if convertQCOW2Hook == nil {
		t.Fatal("convertQCOW2Hook is nil on linux")
	}
}

func TestVerifyRawImageChecksumPasses(t *testing.T) {
	raw := []byte("partition-mode raw image")
	path := writeRawChecksumFixture(t, raw)
	sum := sha256.Sum256(raw)

	err := verifyRawImageChecksum(path, StreamOpts{
		Checksum:     hex.EncodeToString(sum[:]),
		ChecksumType: "sha256",
	})
	if err != nil {
		t.Fatalf("verifyRawImageChecksum: %v", err)
	}
}

func TestVerifyRawImageChecksumMismatch(t *testing.T) {
	path := writeRawChecksumFixture(t, []byte("partition-mode raw image"))

	err := verifyRawImageChecksum(path, StreamOpts{
		Checksum:     strings.Repeat("0", sha256.Size*2),
		ChecksumType: "sha256",
	})
	if err == nil {
		t.Fatal("expected checksum mismatch")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error = %q, want checksum mismatch", err.Error())
	}
}

func TestDownloadAndPrepareRawRejectsUnsupportedVMwareContainer(t *testing.T) {
	if err := os.RemoveAll(ramdiskPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ramdiskPath, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(ramdiskPath) })

	data := append([]byte{'K', 'D', 'M', 'V'}, []byte("vmdk payload")...)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	_, err := downloadAndPrepareRaw(context.Background(), srv.URL+"/image.vmdk")
	if err == nil {
		t.Fatal("expected unsupported VMDK error")
	}
	if !strings.Contains(err.Error(), "unsupported image format vmdk") {
		t.Fatalf("error = %q, want unsupported VMDK format", err.Error())
	}
}

func writeRawChecksumFixture(t *testing.T, data []byte) string {
	t.Helper()
	path := t.TempDir() + "/image.raw"
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write raw fixture: %v", err)
	}
	return path
}

func TestReadSfdiskPartitionsDerivesNumbersFromDeviceNodes(t *testing.T) {
	previous := runCmd
	runCmd = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "sfdisk" || len(args) != 2 || args[0] != "--json" || args[1] != "/dev/loop0" {
			t.Fatalf("unexpected command %s %v", name, args)
		}
		return []byte(`sfdisk noise
{"partitiontable":{"partitions":[
  {"node":"/dev/loop0p5","start":2048,"size":4096,"type":"83"},
  {"node":"/dev/loop0p7","start":6144,"size":4096,"type":"83"},
  {"node":"/dev/mapper/root","start":10240,"size":4096,"type":"83"}
]}}`), nil
	}
	t.Cleanup(func() { runCmd = previous })

	parts, err := readSfdiskPartitions(context.Background(), "/dev/loop0")
	if err != nil {
		t.Fatalf("readSfdiskPartitions: %v", err)
	}
	got := []int{parts[0].Number, parts[1].Number, parts[2].Number}
	want := []int{5, 7, 3}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("partition numbers = %v, want %v", got, want)
		}
	}
}

func TestTargetPartitionNodeForSourceUsesParsedSourceNumber(t *testing.T) {
	tests := []struct {
		name     string
		disk     string
		part     sfdiskPartition
		fallback int
		wantNode string
		wantNum  int
	}{
		{
			name:     "sparse source partition",
			disk:     "/dev/sda",
			part:     sfdiskPartition{Node: "/dev/loop0p7", Number: 7},
			fallback: 2,
			wantNode: "/dev/sda7",
			wantNum:  7,
		},
		{
			name:     "nvme sparse source partition",
			disk:     "/dev/nvme0n1",
			part:     sfdiskPartition{Node: "/dev/loop0p5", Number: 5},
			fallback: 1,
			wantNode: "/dev/nvme0n1p5",
			wantNum:  5,
		},
		{
			name:     "fallback for missing parsed number",
			disk:     "/dev/sda",
			part:     sfdiskPartition{Node: "/dev/mapper/root"},
			fallback: 3,
			wantNode: "/dev/sda3",
			wantNum:  3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotNode, gotNum := targetPartitionNodeForSource(tt.disk, tt.part, tt.fallback)
			if gotNode != tt.wantNode || gotNum != tt.wantNum {
				t.Fatalf("targetPartitionNodeForSource() = (%q, %d), want (%q, %d)",
					gotNode, gotNum, tt.wantNode, tt.wantNum)
			}
		})
	}
}

func TestSelectSourcePartitionsForAB(t *testing.T) {
	parts := []sfdiskPartition{
		{Node: "/dev/loop0p1", Type: efiSystemPartitionGUID, Size: 1024, Number: 1},
		{Node: "/dev/loop0p2", Type: linuxFilesystemGUID, Size: 2048, Name: "rootfs", Number: 2},
		{Node: "/dev/loop0p3", Type: linuxFilesystemGUID, Size: 4096, Name: "data", Number: 3},
	}
	boot, ok := selectSourceBootPartition(parts)
	if !ok || boot.Node != "/dev/loop0p1" {
		t.Fatalf("boot = %#v, ok=%v", boot, ok)
	}
	root, err := selectSourceRootPartition(parts, "", 0)
	if err != nil || root.Node != "/dev/loop0p2" {
		t.Fatalf("root = %#v, err=%v", root, err)
	}
}

func TestSelectSourceRootPartitionRejectsAmbiguousLinuxPartitions(t *testing.T) {
	parts := []sfdiskPartition{
		{Node: "/dev/loop0p1", Type: efiSystemPartitionGUID, Size: 1024},
		{Node: "/dev/loop0p2", Type: linuxFilesystemGUID, Size: 2048},
		{Node: "/dev/loop0p3", Type: linuxFilesystemGUID, Size: 8192},
	}
	if _, err := selectSourceRootPartition(parts, "", 0); err == nil {
		t.Fatal("expected ambiguous Linux root selection to fail")
	}
}

func TestSelectSourceRootPartitionSupportsExplicitSelectors(t *testing.T) {
	parts := []sfdiskPartition{
		{Node: "/dev/loop0p1", Type: efiSystemPartitionGUID, Size: 1024, Number: 1},
		{Node: "/dev/loop0p2", Type: linuxFilesystemGUID, Size: 2048, Name: "ROOT-A", Number: 2},
		{Node: "/dev/loop0p3", Type: linuxFilesystemGUID, Size: 8192, Name: "STATE", Number: 3},
	}

	root, err := selectSourceRootPartition(parts, "root-a", 0)
	if err != nil || root.Node != "/dev/loop0p2" {
		t.Fatalf("label root = %#v, err=%v", root, err)
	}

	root, err = selectSourceRootPartition(parts, "", 3)
	if err != nil || root.Node != "/dev/loop0p3" {
		t.Fatalf("partition root = %#v, err=%v", root, err)
	}
}

func TestSelectSourceRootPartitionSupportsFlatcarUsrSlots(t *testing.T) {
	parts := []sfdiskPartition{
		{Node: "/dev/loop0p1", Type: efiSystemPartitionGUID, Size: 1024, Name: "EFI-SYSTEM", Number: 1},
		{Node: "/dev/loop0p3", Type: linuxFilesystemGUID, Size: 2048, Name: "USR-A", Number: 3},
		{Node: "/dev/loop0p4", Type: linuxFilesystemGUID, Size: 2048, Name: "USR-B", Number: 4},
		{Node: "/dev/loop0p9", Type: linuxFilesystemGUID, Size: 8192, Name: "ROOT", Number: 9},
	}

	root, err := selectSourceRootPartition(parts, "USR-A", 0)
	if err != nil || root.Node != "/dev/loop0p3" {
		t.Fatalf("flatcar explicit USR-A root = %#v, err=%v", root, err)
	}

	_, err = selectSourceRootPartition(parts, "", 0)
	if err == nil {
		t.Fatal("expected Flatcar USR-A/USR-B source image to require an explicit selector")
	}
	if !strings.Contains(err.Error(), "Flatcar-like USR-A/USR-B") {
		t.Fatalf("error = %q, want Flatcar-like USR slot rejection", err.Error())
	}
}

func TestSelectSourceRootPartitionRejectsImplicitSingleFlatcarUsrSlot(t *testing.T) {
	parts := []sfdiskPartition{
		{Node: "/dev/loop0p1", Type: efiSystemPartitionGUID, Size: 1024, Name: "EFI-SYSTEM", Number: 1},
		{Node: "/dev/loop0p3", Type: linuxFilesystemGUID, Size: 2048, Name: "USR-A", Number: 3},
	}

	_, err := selectSourceRootPartition(parts, "", 0)
	if err == nil {
		t.Fatal("expected single Flatcar USR-A source image to require an explicit selector")
	}
	if !strings.Contains(err.Error(), "Flatcar-like USR-A/USR-B") {
		t.Fatalf("error = %q, want Flatcar-like USR slot rejection", err.Error())
	}
}

func TestSelectSourceRootPartitionRejectsExplicitEFI(t *testing.T) {
	parts := []sfdiskPartition{
		{Node: "/dev/loop0p1", Type: efiSystemPartitionGUID, Size: 1024, Number: 1},
		{Node: "/dev/loop0p2", Type: linuxFilesystemGUID, Size: 2048, Name: "rootfs", Number: 2},
	}

	_, err := selectSourceRootPartition(parts, "", 1)
	if err == nil {
		t.Fatal("expected explicit EFI partition selector to fail")
	}
	if !strings.Contains(err.Error(), "is EFI, not a root partition") {
		t.Fatalf("error = %q, want EFI rejection", err.Error())
	}
}

func TestSelectSourceRootPartitionRejectsImplicitUnknownNonEFI(t *testing.T) {
	parts := []sfdiskPartition{
		{Node: "/dev/loop0p1", Type: efiSystemPartitionGUID, Size: 1024},
		{Node: "/dev/loop0p2", Type: "unknown", Size: 8192},
	}
	_, err := selectSourceRootPartition(parts, "", 0)
	if err == nil {
		t.Fatal("expected unknown non-EFI partition to require an explicit selector")
	}
	if !strings.Contains(err.Error(), "no Linux root partition candidate") {
		t.Fatalf("error = %q, want Linux root candidate rejection", err.Error())
	}
}

func TestSelectSourceRootPartitionRejectsImplicitMicrosoftBasicData(t *testing.T) {
	parts := []sfdiskPartition{
		{Node: "/dev/loop0p1", Type: efiSystemPartitionGUID, Size: 1024},
		{Node: "/dev/loop0p2", Type: "EBD0A0A2-B9E5-4433-87C0-68B6B72699C7", Size: 8192},
	}
	_, err := selectSourceRootPartition(parts, "", 0)
	if err == nil {
		t.Fatal("expected Microsoft Basic Data partition to require an explicit selector")
	}
	if !strings.Contains(err.Error(), "no Linux root partition candidate") {
		t.Fatalf("error = %q, want Linux root candidate rejection", err.Error())
	}
}
