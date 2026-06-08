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
	Disk                string
	BootPartition       string
	RootPartition       string
	SourceRootLabel     string
	SourceRootPartition int
}

// StreamAB downloads an OS image and copies its boot/root payload into an
// existing dual-root A/B layout. It does not replace the target partition table.
func StreamAB(ctx context.Context, url string, target ABTargets, opts ...StreamOpts) error {
	if strings.TrimSpace(target.RootPartition) == "" {
		return fmt.Errorf("a/b root partition is required")
	}
	slog.Info("A/B image streaming", "url", url, "disk", target.Disk, "root", target.RootPartition, "boot", target.BootPartition)

	var opt StreamOpts
	if len(opts) > 0 {
		opt = opts[0]
	}

	src, cleanup, format, err := openAndDecompress(ctx, url)
	if err != nil {
		return err
	}
	if format != FormatQCOW2 {
		defer cleanup()
		if err := streamABRaw(ctx, src, target, opt); err != nil {
			wipeLeadingSectors(target.RootPartition)
			return err
		}
		return nil
	}
	defer cleanup()
	return streamABViaRamdisk(ctx, src, target, opt)
}

func streamABViaRamdisk(ctx context.Context, src io.Reader, target ABTargets, opt StreamOpts) error {
	if err := setupRamdisk(); err != nil {
		return fmt.Errorf("setting up ramdisk: %w", err)
	}
	defer cleanupRamdisk()

	qcow2Path := ramdiskPath + "/image.qcow2"
	rawPath := ramdiskPath + "/image.raw"

	if err := writeImageToFile(src, qcow2Path); err != nil {
		return fmt.Errorf("writing qcow2 image to ramdisk: %w", err)
	}
	if err := convertQCOW2ToRaw(ctx, qcow2Path, rawPath); err != nil {
		return err
	}
	_ = os.Remove(qcow2Path)

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

	root, err := selectSourceRootPartition(parts, target.SourceRootLabel, target.SourceRootPartition)
	if err != nil {
		return err
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

	root, err := selectSourceRootPartition(srcParts, target.SourceRootLabel, target.SourceRootPartition)
	if err != nil {
		return err
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

var commonSourceRootLabels = map[string]struct{}{
	"root":              {},
	"root-a":            {},
	"root-b":            {},
	"rootfs":            {},
	"cloudimg-rootfs":   {},
	"booty-root-a":      {},
	"booty-root-b":      {},
	"ubuntu-root":       {},
	"debian-root":       {},
	"fedora-root":       {},
	"opensuse-root":     {},
	"opensuse-rootfs":   {},
	"flatcar-root":      {},
	"bottlerocket-root": {},
}

func selectSourceRootPartition(parts []sfdiskPartition, label string, number int) (sfdiskPartition, error) {
	if number > 0 {
		for _, part := range parts {
			if part.Number == number {
				return part, nil
			}
		}
		return sfdiskPartition{}, fmt.Errorf("source image has no partition number %d", number)
	}

	if strings.TrimSpace(label) != "" {
		return selectSingleSourceRootCandidate(parts, func(part sfdiskPartition) bool {
			return strings.EqualFold(strings.TrimSpace(part.Name), strings.TrimSpace(label))
		}, fmt.Sprintf("label %q", strings.TrimSpace(label)))
	}

	if part, err := selectSingleSourceRootCandidate(parts, hasCommonSourceRootLabel, "common root partition label"); err == nil {
		return part, nil
	} else if !errors.Is(err, errNoSourceRootCandidate) {
		return sfdiskPartition{}, err
	}

	if part, err := selectSingleSourceRootCandidate(parts, isLinuxFilesystemPartition, "Linux filesystem partition"); err == nil {
		return part, nil
	} else if !errors.Is(err, errNoSourceRootCandidate) {
		return sfdiskPartition{}, err
	}

	return selectSingleSourceRootCandidate(parts, func(part sfdiskPartition) bool {
		return !strings.EqualFold(part.Type, efiSystemPartitionGUID)
	}, "non-EFI partition")
}

var errNoSourceRootCandidate = errors.New("source image has no root partition candidate")

func selectSingleSourceRootCandidate(parts []sfdiskPartition, match func(sfdiskPartition) bool, reason string) (sfdiskPartition, error) {
	var candidate sfdiskPartition
	count := 0
	for _, part := range parts {
		if !match(part) {
			continue
		}
		candidate = part
		count++
	}
	switch count {
	case 0:
		return sfdiskPartition{}, errNoSourceRootCandidate
	case 1:
		return candidate, nil
	default:
		return sfdiskPartition{}, fmt.Errorf("source image has %d %s candidates; set provision.ab.sourceRootLabel or provision.ab.sourceRootPartition", count, reason)
	}
}

func hasCommonSourceRootLabel(part sfdiskPartition) bool {
	if strings.EqualFold(part.Type, efiSystemPartitionGUID) {
		return false
	}
	_, ok := commonSourceRootLabels[normalizeSourceRootLabel(part.Name)]
	return ok
}

func normalizeSourceRootLabel(label string) string {
	label = strings.ToLower(strings.TrimSpace(label))
	label = strings.ReplaceAll(label, "_", "-")
	return label
}

func isLinuxFilesystemPartition(part sfdiskPartition) bool {
	return strings.EqualFold(part.Type, linuxFilesystemGUID)
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
