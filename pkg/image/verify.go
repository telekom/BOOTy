// verify.go implements image integrity verification via checksums.
// TODO: integrate ChecksumReader into Stream() to replace inline checksum verification in stream.go.

package image

import (
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"hash"
	"io"
	"strings"
)

// ChecksumReader wraps an io.Reader to compute a checksum while streaming.
type ChecksumReader struct {
	reader   io.Reader
	hasher   hash.Hash
	expected string
	algo     string
}

// ParseChecksum parses a checksum string in "algo:hex" format (e.g. "sha256:abcdef...").
func ParseChecksum(s string) (algo, hex string, err error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid checksum format %q, expected algo:hex", s)
	}
	algo = strings.ToLower(parts[0])
	hex = strings.ToLower(parts[1])
	if algo != "sha256" && algo != "sha512" {
		return "", "", fmt.Errorf("unsupported checksum algorithm %q, supported: sha256, sha512", algo)
	}
	return algo, hex, nil
}

// NewChecksumReader wraps a reader to compute and verify a checksum.
// The checksum string must be in "algo:hex" format.
func NewChecksumReader(r io.Reader, checksum string) (*ChecksumReader, error) {
	algo, expected, err := ParseChecksum(checksum)
	if err != nil {
		return nil, err
	}

	var h hash.Hash
	switch algo {
	case "sha256":
		h = sha256.New()
	case "sha512":
		h = sha512.New()
	}

	return &ChecksumReader{
		reader:   io.TeeReader(r, h),
		hasher:   h,
		expected: expected,
		algo:     algo,
	}, nil
}

// Read implements io.Reader, computing the hash as data flows through.
func (cr *ChecksumReader) Read(p []byte) (int, error) {
	n, err := cr.reader.Read(p)

	return n, err //nolint:wrapcheck // must preserve io.EOF sentinel for callers
}

// Verify checks that the computed checksum matches the expected value.
// Must be called after all data has been read.
func (cr *ChecksumReader) Verify() error {
	actual := fmt.Sprintf("%x", cr.hasher.Sum(nil))
	if actual != cr.expected {
		return fmt.Errorf("checksum mismatch: expected %s:%s, got %s:%s", cr.algo, cr.expected, cr.algo, actual)
	}
	return nil
}
