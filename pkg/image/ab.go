//go:build linux

package image

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

const (
	efiSystemPartitionGUID = "C12A7328-F81F-11D2-BA4B-00A0C93EC93B"
	linuxFilesystemGUID    = "0FC63DAF-8483-4772-8E79-3D69D8477DE4"
)

// ABTargets names the already-created target partitions for an A/B image copy.
type ABTargets struct {
	Disk          string
	BootPartition string
	RootPartition string
}

// StreamAB downloads an OS image and copies its boot/root payload into an
// existing dual-root A/B layout. It does not replace the target partition table.
func StreamAB(ctx context.Context, url string, target ABTargets, opts ...StreamOpts) error {
	if strings.TrimSpace(target.RootPartition) == "" {
		return fmt.Errorf("A/B root partition is required")
	}
	slog.Info("A/B image streaming", "url", url, "disk", target.Disk, "root", target.RootPartition, "boot", target.BootPartition)

	var opt StreamOpts
	if len(opts) > 0 {
		opt = opts[0]
	}

	decompressed, cleanup, format, err := openAndDecompress(ctx, url)
	if err != nil {
		return err
	}
	if format != FormatQCOW2 {
		defer cleanup()
		if err := streamABRaw(ctx, decompressed, target, opt); err != nil {
			wipeLeadingSectors(target.RootPartition)
			return err
		}
		return nil
	}
	cleanup()
	return streamABViaRamdisk(ctx, url, target, opt)
}

func streamABViaRamdisk(ctx context.Context, url string, target ABTargets, opt StreamOpts) error {
	if err := setupRamdisk(); err != nil {
		return fmt.Errorf("setting up ramdisk: %w", err)
	}
	defer cleanupRamdisk()

	rawPath, err := downloadAndPrepareRaw(ctx, url)
	if err != nil {
		return err
	}

	if err := verifyFileChecksum(rawPath, opt); err != nil {
		return err
	}

	if err := copyABPayload(ctx, rawPath, target); err != nil {
		wipeLeadingSectors(target.RootPartition)
		return err
	}
	return nil
}

func streamABRaw(ctx context.Context, src io.Reader, target ABTargets, opt StreamOpts) error {
	prefix, err := readStreamPrefix(src, streamABPrefixBytes)
	if err != nil {
		return err
	}

	stream, checksum, err := wrapChecksum(io.MultiReader(bytes.NewReader(prefix), src), opt)
	if err != nil {
		return err
	}

	parts, err := parseGPTPartitions(prefix)
	if err != nil {
		if errors.Is(err, errNoGPT) {
			slog.Info("source image has no GPT partition table; copying as root filesystem")
			if err := streamReaderToDevice(ctx, stream, target.RootPartition); err != nil {
				return err
			}
			return verifyChecksum(checksum, opt)
		}
		return err
	}

	var ranges []abStreamRange
	if target.BootPartition != "" {
		if boot, ok := selectSourceBootPartition(parts); ok {
			ranges = append(ranges, abStreamRange{
				name:  "boot",
				start: int64(gptSectorSize) * boot.Start,
				size:  int64(gptSectorSize) * boot.Size,
				dst:   target.BootPartition,
			})
		} else {
			slog.Warn("source image has no EFI partition; leaving shared boot partition unchanged")
		}
	}

	root, ok := selectSourceRootPartition(parts)
	if !ok {
		return fmt.Errorf("source image has no root partition candidate")
	}
	ranges = append(ranges, abStreamRange{
		name:  "root",
		start: int64(gptSectorSize) * root.Start,
		size:  int64(gptSectorSize) * root.Size,
		dst:   target.RootPartition,
	})

	if err := copyABStreamRanges(ctx, stream, ranges, checksum != nil); err != nil {
		return err
	}
	return verifyChecksum(checksum, opt)
}

func verifyFileChecksum(path string, opt StreamOpts) error {
	if opt.Checksum == "" {
		return nil
	}
	opt, err := normalizeChecksumOpt(opt)
	if err != nil {
		return err
	}
	h, err := newHash(opt.ChecksumType)
	if err != nil {
		return err
	}
	f, err := os.Open(path) //nolint:gosec // controlled ramdisk path
	if err != nil {
		return fmt.Errorf("opening image for checksum: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only best-effort close
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hashing image: %w", err)
	}
	return verifyChecksum(h, opt)
}

func copyABPayload(ctx context.Context, rawPath string, target ABTargets) error {
	loopDev, err := setupLoopDevice(ctx, rawPath)
	if err != nil {
		slog.Info("source image is not loop-mountable as a disk image; copying as root filesystem", "error", err)
		return ddFileToDevice(ctx, rawPath, target.RootPartition)
	}
	defer teardownLoopDevice(ctx, loopDev)

	if _, err := runCmd(ctx, "partprobe", loopDev); err != nil {
		return fmt.Errorf("partprobe source image: %w", err)
	}

	srcParts, err := readSfdiskPartitions(ctx, loopDev)
	if err != nil || len(srcParts) == 0 {
		if err != nil {
			slog.Info("source image has no partition table; copying as root filesystem", "error", err)
		}
		return ddFileToDevice(ctx, rawPath, target.RootPartition)
	}

	if target.BootPartition != "" {
		if boot, ok := selectSourceBootPartition(srcParts); ok {
			slog.Info("copying source boot partition to shared A/B boot partition", "src", boot.Node, "dst", target.BootPartition)
			if err := ddPartition(ctx, boot.Node, target.BootPartition); err != nil {
				return fmt.Errorf("copying boot partition: %w", err)
			}
		} else {
			slog.Warn("source image has no EFI partition; leaving shared boot partition unchanged")
		}
	}

	root, ok := selectSourceRootPartition(srcParts)
	if !ok {
		return fmt.Errorf("source image has no root partition candidate")
	}
	slog.Info("copying source root partition to A/B target slot", "src", root.Node, "dst", target.RootPartition)
	return ddPartition(ctx, root.Node, target.RootPartition)
}

func selectSourceBootPartition(parts []sfdiskPartition) (sfdiskPartition, bool) {
	for _, part := range parts {
		if strings.EqualFold(part.Type, efiSystemPartitionGUID) {
			return part, true
		}
	}
	return sfdiskPartition{}, false
}

func selectSourceRootPartition(parts []sfdiskPartition) (sfdiskPartition, bool) {
	var fallback sfdiskPartition
	for _, part := range parts {
		if strings.EqualFold(part.Type, linuxFilesystemGUID) {
			if part.Size > fallback.Size {
				fallback = part
			}
			continue
		}
		if !strings.EqualFold(part.Type, efiSystemPartitionGUID) && part.Size > fallback.Size {
			fallback = part
		}
	}
	return fallback, fallback.Node != ""
}

func ddFileToDevice(ctx context.Context, src, dst string) error {
	cmd := exec.CommandContext(ctx, "dd", //nolint:gosec // controlled ramdisk path and target partition
		"if="+src, "of="+dst, "bs=4M", "conv=fsync", "status=progress",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("dd %s to %s: %w", src, dst, err)
	}
	return nil
}
