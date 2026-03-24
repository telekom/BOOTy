//go:build linux

package image

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

const (
	// ramdiskPath is the tmpfs mount point for qcow2 conversion scratch space.
	ramdiskPath = "/tmp/booty-ramdisk"
	// ramdiskSizeOpt is the tmpfs size option — "0" means use all available RAM.
	ramdiskSizeOpt = "size=0"
)

func init() {
	convertQCOW2Hook = ConvertQCOW2
}

// ConvertQCOW2 downloads a qcow2 image to a tmpfs ramdisk, converts it to raw
// using qemu-img, and streams the result to the target device.
//
// The flow is: download → tmpfs → qemu-img convert -f qcow2 -O raw → dd to device.
// The ramdisk is cleaned up after conversion.
func ConvertQCOW2(ctx context.Context, url, device string) error {
	slog.Info("qcow2 image detected, downloading to ramdisk for conversion",
		"url", filepath.Base(url), "device", device)

	if err := setupRamdisk(); err != nil {
		return fmt.Errorf("setting up ramdisk: %w", err)
	}
	defer cleanupRamdisk()

	qcow2Path := filepath.Join(ramdiskPath, "image.qcow2")
	rawPath := filepath.Join(ramdiskPath, "image.raw")

	if err := downloadToFile(ctx, url, qcow2Path); err != nil {
		return fmt.Errorf("downloading qcow2 image: %w", err)
	}

	if err := convertQCOW2ToRaw(ctx, qcow2Path, rawPath); err != nil {
		return fmt.Errorf("converting qcow2 to raw: %w", err)
	}

	// Remove qcow2 to free ramdisk space before streaming.
	_ = os.Remove(qcow2Path)

	if err := streamRawToDevice(rawPath, device); err != nil {
		return fmt.Errorf("streaming raw image to device: %w", err)
	}

	return nil
}

// setupRamdisk creates and mounts a tmpfs at ramdiskPath.
func setupRamdisk() error {
	if err := os.MkdirAll(ramdiskPath, 0o700); err != nil {
		return fmt.Errorf("creating ramdisk dir: %w", err)
	}
	if err := syscall.Mount("tmpfs", ramdiskPath, "tmpfs", 0, ramdiskSizeOpt); err != nil {
		return fmt.Errorf("mounting tmpfs at %s: %w", ramdiskPath, err)
	}
	slog.Info("Ramdisk mounted", "path", ramdiskPath)
	return nil
}

// cleanupRamdisk unmounts and removes the tmpfs ramdisk.
func cleanupRamdisk() {
	if err := syscall.Unmount(ramdiskPath, 0); err != nil {
		slog.Warn("Failed to unmount ramdisk", "error", err)
	}
	_ = os.RemoveAll(ramdiskPath)
}

// downloadToFile downloads a URL to a local file path.
func downloadToFile(ctx context.Context, url, dest string) error {
	body, err := openSource(ctx, url)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()

	f, err := os.Create(dest) //nolint:gosec // dest is a controlled ramdisk path
	if err != nil {
		return fmt.Errorf("creating file %s: %w", dest, err)
	}
	defer func() { _ = f.Close() }()

	counter := &WriteCounter{}
	stopProgress := startProgressTicker(counter)

	written, err := io.Copy(f, io.TeeReader(body, counter))
	stopProgress()
	if err != nil {
		return fmt.Errorf("writing to file: %w", err)
	}
	fmt.Println()
	slog.Info("qcow2 downloaded to ramdisk", "bytes", written)
	return nil
}

// convertQCOW2ToRaw runs qemu-img convert to transform qcow2 to raw.
func convertQCOW2ToRaw(ctx context.Context, src, dst string) error {
	qemuImg, err := exec.LookPath("qemu-img")
	if err != nil {
		return fmt.Errorf("qemu-img not found (install qemu-utils): %w", err)
	}

	slog.Info("Converting qcow2 to raw", "src", src, "dst", dst)
	cmd := exec.CommandContext(ctx, qemuImg, "convert", "-f", "qcow2", "-O", "raw", src, dst) //nolint:gosec // controlled paths
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("qemu-img convert: %w", err)
	}

	slog.Info("qcow2 conversion complete")
	return nil
}

// streamRawToDevice streams a raw image file to a block device.
func streamRawToDevice(rawPath, device string) error {
	slog.Info("Streaming raw image to device", "src", rawPath, "device", device)

	src, err := os.Open(rawPath) //nolint:gosec // controlled ramdisk path
	if err != nil {
		return fmt.Errorf("opening raw image: %w", err)
	}
	defer func() { _ = src.Close() }()

	dst, err := os.OpenFile(device, os.O_WRONLY, 0) //nolint:gosec // device from config
	if err != nil {
		return fmt.Errorf("opening device %s: %w", device, err)
	}
	defer func() { _ = dst.Close() }()

	counter := &WriteCounter{}
	stopProgress := startProgressTicker(counter)

	written, err := io.Copy(dst, io.TeeReader(src, counter))
	stopProgress()
	if err != nil {
		return fmt.Errorf("writing raw to device: %w", err)
	}
	fmt.Println()
	slog.Info("Raw image written to device", "bytes", written)
	return nil
}
