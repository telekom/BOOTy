package image

import (
	"bufio"
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/ulikunitz/xz"
)

// Format represents a detected compression format.
type Format string

const (
	FormatRaw   Format = "raw"
	FormatGzip  Format = "gzip"
	FormatZstd  Format = "zstd"
	FormatLZ4   Format = "lz4"
	FormatXZ    Format = "xz"
	FormatBzip2 Format = "bzip2"
)

// Magic byte signatures for auto-detection.
var magicBytes = []struct {
	format Format
	magic  []byte
}{
	{FormatGzip, []byte{0x1f, 0x8b}},
	{FormatZstd, []byte{0x28, 0xb5, 0x2f, 0xfd}},
	{FormatLZ4, []byte{0x04, 0x22, 0x4d, 0x18}},
	{FormatXZ, []byte{0xfd, 0x37, 0x7a, 0x58, 0x5a, 0x00}},
	{FormatBzip2, []byte{0x42, 0x5a, 0x68}},
}

// DetectFormat peeks at the first bytes of the reader to determine
// compression format via magic bytes. Returns the detected format and
// a new reader that replays the peeked bytes.
func DetectFormat(r io.Reader) (Format, io.Reader, error) {
	br := bufio.NewReaderSize(r, 6)
	header, err := br.Peek(6)
	if err != nil && len(header) == 0 {
		return FormatRaw, br, fmt.Errorf("peek header: %w", err)
	}

	for _, m := range magicBytes {
		if len(header) >= len(m.magic) && matchBytes(header, m.magic) {
			return m.format, br, nil
		}
	}
	return FormatRaw, br, nil
}

// matchBytes checks if data starts with the given prefix.
func matchBytes(data, prefix []byte) bool {
	for i, b := range prefix {
		if data[i] != b {
			return false
		}
	}
	return true
}

// Decompressor wraps a reader with the appropriate decompression based on format.
// The returned io.Reader streams decompressed data. The closer (if non-nil)
// must be closed when done.
func Decompressor(r io.Reader, f Format) (io.Reader, io.Closer, error) {
	switch f {
	case FormatGzip:
		gz, err := gzip.NewReader(r)
		if err != nil {
			return nil, nil, fmt.Errorf("gzip reader: %w", err)
		}
		return gz, gz, nil

	case FormatZstd:
		zr, err := zstd.NewReader(r)
		if err != nil {
			return nil, nil, fmt.Errorf("zstd reader: %w", err)
		}
		return zr, zr.IOReadCloser(), nil

	case FormatLZ4:
		return lz4.NewReader(r), nil, nil

	case FormatXZ:
		xzr, err := xz.NewReader(r)
		if err != nil {
			return nil, nil, fmt.Errorf("xz reader: %w", err)
		}
		return xzr, nil, nil

	case FormatBzip2:
		return bzip2.NewReader(r), nil, nil

	case FormatRaw:
		return r, nil, nil

	default:
		return r, nil, nil
	}
}
