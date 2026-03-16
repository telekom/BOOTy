// Package verify provides image checksum verification for streamed OS images.
package verify

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"
)

// Algorithm represents a supported hash algorithm.
type Algorithm string

const (
	// SHA256 is SHA-256 hash algorithm.
	SHA256 Algorithm = "sha256"
	// SHA512 is SHA-512 hash algorithm.
	SHA512 Algorithm = "sha512"
)

// ParseChecksum parses a checksum string in the format "algo:hexhash".
// It validates that the hash is valid hex and the correct length for the algorithm.
func ParseChecksum(s string) (Algorithm, string, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid checksum format %q, expected algo:hash", s)
	}
	algo := Algorithm(parts[0])
	if err := algo.Validate(); err != nil {
		return "", "", err
	}
	hexHash := strings.ToLower(parts[1])
	if hexHash == "" {
		return "", "", fmt.Errorf("empty hash value")
	}
	decoded, err := hex.DecodeString(hexHash)
	if err != nil {
		return "", "", fmt.Errorf("invalid hex in checksum: %w", err)
	}
	expectedLen := algo.DigestSize()
	if len(decoded) != expectedLen {
		return "", "", fmt.Errorf("checksum length %d bytes, want %d for %s", len(decoded), expectedLen, algo)
	}
	return algo, hexHash, nil
}

// Validate checks if the algorithm is supported.
func (a Algorithm) Validate() error {
	switch a {
	case SHA256, SHA512:
		return nil
	default:
		return fmt.Errorf("unsupported checksum algorithm %q", a)
	}
}

// DigestSize returns the expected digest size in bytes for the algorithm.
func (a Algorithm) DigestSize() int {
	switch a {
	case SHA256:
		return sha256.Size
	case SHA512:
		return sha512.Size
	default:
		return 0
	}
}

// NewHash creates a new hash.Hash for the given algorithm.
func (a Algorithm) NewHash() (hash.Hash, error) {
	switch a {
	case SHA256:
		return sha256.New(), nil
	case SHA512:
		return sha512.New(), nil
	default:
		return nil, fmt.Errorf("unsupported algorithm %q", a)
	}
}

// ChecksumReader wraps an io.Reader and computes a running checksum.
type ChecksumReader struct {
	reader   io.Reader
	hash     hash.Hash
	expected string
	algo     Algorithm
}

// NewChecksumReader creates a reader that computes checksum while reading.
func NewChecksumReader(r io.Reader, checksum string) (*ChecksumReader, error) {
	algo, expected, err := ParseChecksum(checksum)
	if err != nil {
		return nil, fmt.Errorf("parsing checksum: %w", err)
	}
	h, err := algo.NewHash()
	if err != nil {
		return nil, fmt.Errorf("creating hash: %w", err)
	}
	return &ChecksumReader{
		reader:   io.TeeReader(r, h),
		hash:     h,
		expected: expected,
		algo:     algo,
	}, nil
}

// Read implements io.Reader.
func (c *ChecksumReader) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	if err == nil {
		return n, nil
	}
	if errors.Is(err, io.EOF) {
		return n, io.EOF
	}
	return n, fmt.Errorf("checksum read: %w", err)
}

// Verify checks the computed checksum against expected.
// Must be called after all data has been read.
func (c *ChecksumReader) Verify() error {
	actual := hex.EncodeToString(c.hash.Sum(nil))
	if actual != c.expected {
		return fmt.Errorf("checksum mismatch: expected %s:%s, got %s:%s",
			c.algo, c.expected, c.algo, actual)
	}
	return nil
}

// Actual returns the computed hex hash after reading.
func (c *ChecksumReader) Actual() string {
	return hex.EncodeToString(c.hash.Sum(nil))
}

// HashBytes computes the checksum of a byte slice.
func HashBytes(algo Algorithm, data []byte) (string, error) {
	h, err := algo.NewHash()
	if err != nil {
		return "", fmt.Errorf("creating hash: %w", err)
	}
	if _, err := h.Write(data); err != nil {
		return "", fmt.Errorf("hashing data: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyConfig holds verification configuration.
type VerifyConfig struct {
	Checksum      string `json:"checksum,omitempty"`
	SignatureURL  string `json:"signatureUrl,omitempty"`
	PublicKeyPath string `json:"publicKeyPath,omitempty"`
}

// Validate checks the verification config.
func (c *VerifyConfig) Validate() error {
	if c.Checksum != "" {
		if _, _, err := ParseChecksum(c.Checksum); err != nil {
			return fmt.Errorf("invalid checksum: %w", err)
		}
	}
	if c.SignatureURL != "" && c.PublicKeyPath == "" {
		return fmt.Errorf("public key path required when signature URL is set")
	}
	return nil
}

// HasChecksum returns true if checksum verification is configured.
func (c *VerifyConfig) HasChecksum() bool {
	return c.Checksum != ""
}

// HasSignature returns true if signature verification is configured.
func (c *VerifyConfig) HasSignature() bool {
	return c.SignatureURL != "" && c.PublicKeyPath != ""
}
