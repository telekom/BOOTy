//go:build linux

package image

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
)

// ErrEncryptedRootUnsupported marks an image that StreamEncryptedRoot cannot
// write: qcow2, no GPT, or a root that is not the last partition.
var ErrEncryptedRootUnsupported = errors.New("image layout not supported for format-first disk encryption")

// EncryptedRoot selects the source partition that goes into the LUKS2
// mapping: Number > 0 selects by number, else the root selection rules of
// StreamRoot apply. An image without a root partition candidate and with one
// Linux LVM partition has its root on LVM: that partition is selected.
type EncryptedRoot struct {
	Number int
	// Open is called when the stream reaches the selected partition. The
	// partition table and every partition before it are on the disk then.
	// Open grows the partition to the disk end, formats and opens it, and
	// returns the device that receives the partition content. lvm is true
	// when the partition has the Linux LVM type (an LVM physical volume).
	Open func(ctx context.Context, number int, size int64, lvm bool) (string, error)
	// Check, when set, is called with the selected partition before the
	// first disk write; an error stops the stream with nothing written.
	Check func(lvm bool) error
}

// StreamEncryptedRoot writes a raw GPT image to disk like Stream, except
// the selected partition, whose content goes into the device that
// target.Open returns. Nothing of that partition reaches the disk in plain
// text. The bytes after it (the backup GPT of the image) are read for the
// checksum but not written: the partition grows to the disk end, and growpart
// writes a new backup GPT. The partition must therefore be the last one.
func StreamEncryptedRoot(ctx context.Context, url, disk string, target EncryptedRoot, opts ...StreamOpts) error {
	var opt StreamOpts
	if len(opts) > 0 {
		opt = opts[0]
	}
	opt, err := withDownloadChecksum(opt, url)
	if err != nil {
		return err
	}
	src, cleanup, format, err := openAndDecompress(ctx, url, opt.download)
	if err != nil {
		return err
	}
	defer cleanup()
	if format == FormatQCOW2 {
		return fmt.Errorf("%w: qcow2", ErrEncryptedRootUnsupported)
	}
	prefix, err := readStreamPrefix(src, streamABPrefixBytes)
	if err != nil {
		return err
	}
	parts, err := parseGPTPartitions(prefix)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrEncryptedRootUnsupported, err)
	}
	root, err := selectEncryptedRoot(parts, target.Number)
	if err != nil {
		return err
	}
	if target.Check != nil {
		if err := target.Check(isLinuxLVMPartition(root)); err != nil {
			return err
		}
	}
	stream, checksum, err := wrapChecksum(io.MultiReader(bytes.NewReader(prefix), src), opt)
	if err != nil {
		return err
	}
	// Byte progress for the console and the watchdog, like Stream.
	counter := &WriteCounter{}
	defer startProgressTicker(counter)()
	stream = io.TeeReader(stream, counter)
	start, size := int64(gptSectorSize)*root.Start, int64(gptSectorSize)*root.Size
	slog.Info("streaming image with an encrypted partition", "url", RedactURL(url), "disk", disk, "partition", root.Number, "bytes", size)
	if err := copyReaderRangeToDevice(ctx, stream, disk, start); err != nil {
		wipeImageTarget(disk)
		return fmt.Errorf("writing the image up to partition %d: %w", root.Number, err)
	}
	dst, err := target.Open(ctx, root.Number, size, isLinuxLVMPartition(root))
	if err != nil {
		wipeImageTarget(disk)
		return err
	}
	if err := copyReaderRangeToDevice(ctx, stream, dst, size); err != nil {
		wipeImageTarget(disk)
		return fmt.Errorf("writing partition %d into %s: %w", root.Number, dst, err)
	}
	if _, err := io.Copy(io.Discard, stream); err != nil {
		wipeImageTarget(disk)
		return fmt.Errorf("reading the image after partition %d: %w", root.Number, err)
	}
	if err := verifyChecksum(checksum, opt); err != nil {
		wipeImageTarget(disk)
		return err
	}
	return nil
}

// selectEncryptedRoot picks the partition and checks that it is the last.
func selectEncryptedRoot(parts []sfdiskPartition, number int) (sfdiskPartition, error) {
	candidates := parts
	if number == 0 {
		// A physical volume is never a root partition, also with a root label;
		// only the guarded fallback below picks one.
		candidates = slices.DeleteFunc(slices.Clone(parts), isLinuxLVMPartition)
	}
	root, err := selectSourceRootPartition(candidates, "", number)
	if err != nil && number == 0 && !slices.ContainsFunc(parts, isLinuxFilesystemPartition) {
		// No root partition: the root is on LVM, in the single physical volume.
		if pv, pvErr := selectSingleSourceRootCandidate(parts, isLinuxLVMPartition, "Linux LVM partition"); pvErr == nil {
			root, err = pv, nil
		}
	}
	if err != nil {
		return sfdiskPartition{}, err
	}
	for _, p := range parts {
		if p.Number != root.Number && p.Start > root.Start {
			return sfdiskPartition{}, fmt.Errorf("%w: partition %d is not the last partition (partition %d follows it)",
				ErrEncryptedRootUnsupported, root.Number, p.Number)
		}
	}
	return root, nil
}

// linuxLVMGUID is the GPT type of an LVM physical volume.
const linuxLVMGUID = "E6D6D379-F507-44C2-A23C-238F2A3DF928"

func isLinuxLVMPartition(part sfdiskPartition) bool {
	return strings.EqualFold(part.Type, linuxLVMGUID)
}
