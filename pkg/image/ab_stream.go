//go:build linux

package image

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"unicode/utf16"
)

const (
	gptSectorSize       = 512
	streamABPrefixBytes = 1 << 20
)

var errNoGPT = errors.New("source image has no GPT partition table")

type abStreamRange struct {
	name  string
	start int64
	size  int64
	dst   string
}

func readStreamPrefix(r io.Reader, limit int64) ([]byte, error) {
	var buf bytes.Buffer
	_, err := io.CopyN(&buf, r, limit)
	switch {
	case err == nil:
		return buf.Bytes(), nil
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return buf.Bytes(), nil
	default:
		return nil, fmt.Errorf("reading source image prefix: %w", err)
	}
}

func parseGPTPartitions(prefix []byte) ([]sfdiskPartition, error) {
	if len(prefix) < 2*gptSectorSize {
		return nil, errNoGPT
	}

	header := prefix[gptSectorSize : 2*gptSectorSize]
	if string(header[:8]) != "EFI PART" {
		return nil, errNoGPT
	}

	entriesLBA := binary.LittleEndian.Uint64(header[72:80])
	entryCount := binary.LittleEndian.Uint32(header[80:84])
	entrySize := binary.LittleEndian.Uint32(header[84:88])
	if entrySize < 56 {
		return nil, fmt.Errorf("invalid GPT partition entry size %d", entrySize)
	}
	if entryCount > 4096 {
		return nil, fmt.Errorf("refusing GPT with %d partition entries", entryCount)
	}

	entriesOffset := entriesLBA * gptSectorSize
	entriesBytes := uint64(entryCount) * uint64(entrySize)
	if entriesOffset > uint64(len(prefix)) || entriesBytes > uint64(len(prefix))-entriesOffset {
		return nil, fmt.Errorf("GPT partition entries exceed streaming prefix: offset=%d size=%d prefix=%d",
			entriesOffset, entriesBytes, len(prefix))
	}

	parts := make([]sfdiskPartition, 0, entryCount)
	for i := uint32(0); i < entryCount; i++ {
		offset := int(entriesOffset + uint64(i)*uint64(entrySize))
		entry := prefix[offset : offset+int(entrySize)]
		if isZeroGUID(entry[:16]) {
			continue
		}
		firstLBA := binary.LittleEndian.Uint64(entry[32:40])
		lastLBA := binary.LittleEndian.Uint64(entry[40:48])
		if firstLBA == 0 || lastLBA < firstLBA {
			return nil, fmt.Errorf("invalid GPT partition %d: first_lba=%d last_lba=%d", i+1, firstLBA, lastLBA)
		}
		parts = append(parts, sfdiskPartition{
			Node:   fmt.Sprintf("gpt-part-%d", i+1),
			Start:  int64(firstLBA),
			Size:   int64(lastLBA - firstLBA + 1),
			Type:   guidString(entry[:16]),
			Name:   decodeGPTPartitionName(entry[56:min(len(entry), 128)]),
			Number: int(i + 1),
		})
	}
	return parts, nil
}

func decodeGPTPartitionName(name []byte) string {
	if len(name)%2 == 1 {
		name = name[:len(name)-1]
	}
	runes := make([]uint16, 0, len(name)/2)
	for i := 0; i+1 < len(name); i += 2 {
		r := binary.LittleEndian.Uint16(name[i : i+2])
		if r == 0 {
			break
		}
		runes = append(runes, r)
	}
	return strings.TrimSpace(string(utf16.Decode(runes)))
}

func isZeroGUID(guid []byte) bool {
	for _, b := range guid {
		if b != 0 {
			return false
		}
	}
	return true
}

func guidString(guid []byte) string {
	return strings.ToUpper(fmt.Sprintf("%08x-%04x-%04x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		binary.LittleEndian.Uint32(guid[0:4]),
		binary.LittleEndian.Uint16(guid[4:6]),
		binary.LittleEndian.Uint16(guid[6:8]),
		guid[8], guid[9],
		guid[10], guid[11], guid[12], guid[13], guid[14], guid[15],
	))
}

func copyABStreamRanges(ctx context.Context, src io.Reader, ranges []abStreamRange, drain bool) error {
	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i].start < ranges[j].start
	})

	pos := int64(0)
	var dirtyTargets []string
	for _, r := range ranges {
		if err := ctx.Err(); err != nil {
			return dirtyABTargetsError(fmt.Errorf("copying A/B ranges canceled: %w", err), dirtyTargets...)
		}
		if r.start < pos {
			return dirtyABTargetsError(fmt.Errorf("overlapping A/B copy ranges at %s", r.name), dirtyTargets...)
		}
		if r.size <= 0 {
			return dirtyABTargetsError(fmt.Errorf("invalid A/B copy range size for %s: %d", r.name, r.size), dirtyTargets...)
		}
		if _, err := io.CopyN(io.Discard, src, r.start-pos); err != nil {
			return dirtyABTargetsError(fmt.Errorf("skipping to %s partition at byte %d: %w", r.name, r.start, err), dirtyTargets...)
		}
		pos = r.start

		slog.Info("streaming source partition to A/B target", "partition", r.name, "dst", r.dst, "bytes", r.size)
		if err := copyReaderRangeToDevice(ctx, src, r.dst, r.size); err != nil {
			return dirtyABTargetsError(fmt.Errorf("copying %s partition to %s: %w", r.name, r.dst, err), dirtyTargets...)
		}
		dirtyTargets = append(dirtyTargets, r.dst)
		pos += r.size
	}

	if drain {
		if _, err := io.Copy(io.Discard, src); err != nil {
			return dirtyABTargetsError(fmt.Errorf("draining source image for checksum: %w", err), dirtyTargets...)
		}
	}
	return nil
}

func streamReaderToDevice(ctx context.Context, src io.Reader, dst string) error {
	slog.Info("streaming source image to A/B root target", "dst", dst)
	out, err := os.OpenFile(dst, os.O_WRONLY, 0) //nolint:gosec // target path from trusted config
	if err != nil {
		return fmt.Errorf("opening target %s: %w", dst, err)
	}
	defer func() { _ = out.Close() }()

	written, err := copyReader(ctx, out, src)
	if err != nil {
		err = fmt.Errorf("writing root target %s: %w", dst, err)
		if written > 0 {
			return dirtyABTargetsError(err, dst)
		}
		return err
	}
	if err := out.Sync(); err != nil {
		return dirtyABTargetsError(fmt.Errorf("syncing root target %s: %w", dst, err), dst)
	}
	return nil
}

func copyReaderRangeToDevice(ctx context.Context, src io.Reader, dst string, size int64) error {
	out, err := os.OpenFile(dst, os.O_WRONLY, 0) //nolint:gosec // target path from trusted config
	if err != nil {
		return fmt.Errorf("opening target %s: %w", dst, err)
	}
	defer func() { _ = out.Close() }()

	limited := &io.LimitedReader{R: src, N: size}
	written, err := copyReader(ctx, out, limited)
	if err != nil {
		err = fmt.Errorf("copying stream to target: %w", err)
		if written > 0 {
			return dirtyABTargetsError(err, dst)
		}
		return err
	}
	if limited.N != 0 {
		err := fmt.Errorf("source image ended %d bytes before partition range completed", limited.N)
		if written > 0 {
			return dirtyABTargetsError(err, dst)
		}
		return err
	}
	if err := out.Sync(); err != nil {
		return dirtyABTargetsError(fmt.Errorf("syncing target %s: %w", dst, err), dst)
	}
	return nil
}

func copyReader(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, imageCopyBufferSize)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, fmt.Errorf("copy canceled: %w", err)
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			written, err := dst.Write(buf[:n])
			total += int64(written)
			if err != nil {
				return total, fmt.Errorf("writing stream chunk: %w", err)
			}
			if written != n {
				return total, fmt.Errorf("writing stream chunk: %w", io.ErrShortWrite)
			}
		}
		if errors.Is(readErr, io.EOF) {
			return total, nil
		}
		if readErr != nil {
			return total, fmt.Errorf("reading stream chunk: %w", readErr)
		}
	}
}
