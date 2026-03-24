//go:build linux

package image

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// StreamPartitions downloads an image to a tmpfs ramdisk, optionally converts
// qcow2 to raw, then copies each partition individually from the source image
// to the corresponding partition on the target disk.
//
// This preserves the partition table on the target disk and allows the source
// image partitions to differ in size from the target.
func StreamPartitions(ctx context.Context, url, device string) error {
	slog.Info("Partition-by-partition imaging", "url", url, "device", device)

	if err := setupRamdisk(); err != nil {
		return fmt.Errorf("setting up ramdisk: %w", err)
	}
	defer cleanupRamdisk()

	rawPath, err := downloadAndPrepareRaw(ctx, url)
	if err != nil {
		return err
	}

	return copyPartitions(ctx, rawPath, device)
}

// downloadAndPrepareRaw downloads an image to the ramdisk, decompresses it
// if needed, and converts qcow2 to raw. Returns path to the raw image.
func downloadAndPrepareRaw(ctx context.Context, url string) (string, error) {
	downloadPath := ramdiskPath + "/image.download"

	if err := downloadToFile(ctx, url, downloadPath); err != nil {
		return "", fmt.Errorf("downloading image: %w", err)
	}

	// Detect format of the downloaded file.
	f, err := os.Open(downloadPath) //nolint:gosec // controlled ramdisk path
	if err != nil {
		return "", fmt.Errorf("opening downloaded image: %w", err)
	}
	format, _, err := DetectFormat(f)
	_ = f.Close()
	if err != nil {
		return "", fmt.Errorf("detecting image format: %w", err)
	}
	slog.Info("Downloaded image format", "format", format)

	rawPath := ramdiskPath + "/image.raw"

	switch format {
	case FormatQCOW2:
		if err := convertQCOW2ToRaw(ctx, downloadPath, rawPath); err != nil {
			return "", err
		}
		_ = os.Remove(downloadPath)
		return rawPath, nil

	case FormatGzip, FormatZstd, FormatLZ4, FormatXZ, FormatBzip2:
		if err := decompressFile(ctx, downloadPath, rawPath, format); err != nil {
			return "", err
		}
		_ = os.Remove(downloadPath)
		return rawPath, nil

	case FormatRaw:
		// Already raw — just rename.
		if err := os.Rename(downloadPath, rawPath); err != nil {
			return "", fmt.Errorf("renaming to raw: %w", err)
		}
		return rawPath, nil
	}

	return downloadPath, nil
}

// decompressFile decompresses a file on the ramdisk to a new output file.
func decompressFile(_ context.Context, src, dst string, format Format) error {
	slog.Info("Decompressing image on ramdisk", "format", format, "src", src)

	in, err := os.Open(src) //nolint:gosec // controlled ramdisk path
	if err != nil {
		return fmt.Errorf("opening compressed file: %w", err)
	}
	defer func() { _ = in.Close() }()

	reader, closer, err := Decompressor(in, format)
	if err != nil {
		return fmt.Errorf("creating decompressor: %w", err)
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}

	out, err := os.Create(dst) //nolint:gosec // controlled ramdisk path
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}
	defer func() { _ = out.Close() }()

	written, err := io.Copy(out, reader)
	if err != nil {
		return fmt.Errorf("decompressing: %w", err)
	}
	slog.Info("Decompression complete", "bytes", written)
	return nil
}

// copyPartitions sets up a loop device for the source raw image, reads its
// partition table, then copies each partition's data to the matching partition
// on the target disk.
func copyPartitions(ctx context.Context, rawPath, targetDisk string) error {
	// Set up loop device for the raw image.
	loopDev, err := setupLoopDevice(ctx, rawPath)
	if err != nil {
		return fmt.Errorf("setting up loop device: %w", err)
	}
	defer teardownLoopDevice(ctx, loopDev)

	// Force kernel to re-read partition table on loop device.
	if _, err := runCmd(ctx, "partprobe", loopDev); err != nil {
		return fmt.Errorf("partprobe loop device: %w", err)
	}

	// Copy partition table from source to target.
	slog.Info("Copying partition table to target disk", "target", targetDisk)
	if err := copyPartitionTable(ctx, loopDev, targetDisk); err != nil {
		return fmt.Errorf("copying partition table: %w", err)
	}

	// Re-read target partition table.
	if _, err := runCmd(ctx, "partprobe", targetDisk); err != nil {
		return fmt.Errorf("partprobe target: %w", err)
	}

	// Read partitions from source loop device.
	srcParts, err := readSfdiskPartitions(ctx, loopDev)
	if err != nil {
		return fmt.Errorf("reading source partitions: %w", err)
	}

	if len(srcParts) == 0 {
		return fmt.Errorf("source image has no partitions")
	}

	// Copy each partition.
	for i, sp := range srcParts {
		srcNode := sp.Node
		// Derive target partition node from target disk.
		tgtNode := targetPartitionNode(targetDisk, i+1)
		slog.Info("Copying partition",
			"index", i+1, "src", srcNode, "dst", tgtNode,
			"type", sp.Type, "sectors", sp.Size)

		if err := ddPartition(ctx, srcNode, tgtNode); err != nil {
			return fmt.Errorf("copying partition %d (%s -> %s): %w", i+1, srcNode, tgtNode, err)
		}
	}

	slog.Info("Partition-by-partition imaging complete", "partitions", len(srcParts))
	return nil
}

// sfdiskPartition mirrors the sfdisk JSON partition entry.
type sfdiskPartition struct {
	Node  string `json:"node"`
	Start int64  `json:"start"`
	Size  int64  `json:"size"`
	Type  string `json:"type"`
}

// readSfdiskPartitions reads partition entries from a disk/loop device.
func readSfdiskPartitions(ctx context.Context, dev string) ([]sfdiskPartition, error) {
	out, err := runCmd(ctx, "sfdisk", "--json", dev)
	if err != nil {
		return nil, fmt.Errorf("sfdisk --json %s: %w", dev, err)
	}

	jsonBytes := out
	if idx := bytes.IndexByte(out, '{'); idx > 0 {
		jsonBytes = out[idx:]
	}

	var result struct {
		PartitionTable struct {
			Partitions []sfdiskPartition `json:"partitions"`
		} `json:"partitiontable"`
	}
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		return nil, fmt.Errorf("parsing sfdisk output: %w", err)
	}
	return result.PartitionTable.Partitions, nil
}

// copyPartitionTable copies the GPT/MBR partition table from src to dst using sfdisk.
func copyPartitionTable(ctx context.Context, src, dst string) error {
	dump, err := runCmd(ctx, "sfdisk", "--dump", src)
	if err != nil {
		return fmt.Errorf("dumping partition table: %w", err)
	}

	cmd := exec.CommandContext(ctx, "sfdisk", "--force", dst) //nolint:gosec // controlled device path
	cmd.Stdin = bytes.NewReader(dump)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("applying partition table: %w\n%s", err, string(out))
	}
	return nil
}

// setupLoopDevice attaches a raw image to a loop device and returns the device path.
func setupLoopDevice(ctx context.Context, imagePath string) (string, error) {
	out, err := runCmd(ctx, "losetup", "--find", "--show", "--partscan", imagePath)
	if err != nil {
		return "", fmt.Errorf("losetup: %w", err)
	}
	dev := strings.TrimSpace(string(out))
	slog.Info("Loop device attached", "device", dev, "image", imagePath)
	return dev, nil
}

// teardownLoopDevice detaches a loop device.
func teardownLoopDevice(ctx context.Context, dev string) {
	if _, err := runCmd(ctx, "losetup", "--detach", dev); err != nil {
		slog.Warn("Failed to detach loop device", "device", dev, "error", err)
	}
}

// ddPartition copies data from src partition device to dst partition device.
func ddPartition(ctx context.Context, src, dst string) error {
	cmd := exec.CommandContext(ctx, "dd",
		"if="+src, "of="+dst, "bs=4M", "conv=fsync", "status=progress",
	) //nolint:gosec // controlled device paths from partition table
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// targetPartitionNode derives the partition device node for a given disk and
// partition number. Handles both /dev/sdX and /dev/nvme0n1 naming.
func targetPartitionNode(disk string, partNum int) string {
	// NVMe devices use "p" separator: /dev/nvme0n1p1.
	if strings.Contains(disk, "nvme") || strings.Contains(disk, "loop") {
		return fmt.Sprintf("%sp%d", disk, partNum)
	}
	// SCSI/SATA: /dev/sda1.
	return fmt.Sprintf("%s%d", disk, partNum)
}

// runCmd executes a command and returns its combined output.
func runCmd(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // controlled arguments
	return cmd.CombinedOutput()
}
