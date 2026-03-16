package attest

import (
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"sort"
	"time"
)

const maxPCRIndex PCRIndex = 23

// PCRIndex represents a TPM Platform Configuration Register index.
type PCRIndex int

// Standard PCR indices used during provisioning.
const (
	PCRFirmware    PCRIndex = 0  // UEFI firmware hash (set by firmware)
	PCRFirmwareCfg PCRIndex = 1  // UEFI configuration
	PCRBootLoader  PCRIndex = 4  // Boot loader hash
	PCRSecureBoot  PCRIndex = 7  // SecureBoot state
	PCRImage       PCRIndex = 9  // OS image checksum
	PCRConfig      PCRIndex = 10 // Provisioning config hash
	PCRProvisioner PCRIndex = 14 // Provisioner step measurements
)

// HashAlgorithm identifies a TPM hash algorithm.
type HashAlgorithm string

// Supported hash algorithms for TPM operations.
const (
	AlgSHA256 HashAlgorithm = "sha256"
	AlgSHA384 HashAlgorithm = "sha384"
)

// Measurement represents a single PCR extend operation.
type Measurement struct {
	PCR       PCRIndex      `json:"pcr"`
	Algorithm HashAlgorithm `json:"algorithm"`
	Digest    string        `json:"digest"`
	Label     string        `json:"label"`
	Timestamp time.Time     `json:"timestamp"`
}

// MeasurementLog records all PCR extensions during a provisioning run.
type MeasurementLog struct {
	Entries []Measurement `json:"entries"`
}

// Add records a measurement in the log.
func (l *MeasurementLog) Add(pcr PCRIndex, label string, data []byte) Measurement {
	return l.AddWithAlgorithm(pcr, label, data, AlgSHA256)
}

// AddWithAlgorithm records a measurement using a specific hash algorithm.
func (l *MeasurementLog) AddWithAlgorithm(pcr PCRIndex, label string, data []byte, algo HashAlgorithm) Measurement {
	var digest string
	switch algo {
	case AlgSHA384:
		h := sha512.Sum384(data)
		digest = hex.EncodeToString(h[:])
	default:
		h := sha256.Sum256(data)
		digest = hex.EncodeToString(h[:])
		algo = AlgSHA256
	}

	m := Measurement{
		PCR:       pcr,
		Algorithm: algo,
		Digest:    digest,
		Label:     label,
		Timestamp: time.Now(),
	}
	l.Entries = append(l.Entries, m)
	return m
}

// DigestsForPCR returns all digest values extended into a specific PCR.
func (l *MeasurementLog) DigestsForPCR(pcr PCRIndex) []string {
	digests := make([]string, 0)
	for _, e := range l.Entries {
		if e.PCR == pcr {
			digests = append(digests, e.Digest)
		}
	}
	return digests
}

// Quote represents a TPM attestation quote.
type Quote struct {
	Nonce     []byte              `json:"nonce"`
	PCRs      map[PCRIndex][]byte `json:"pcrs"`
	QuoteRaw  []byte              `json:"quote_raw"`
	Signature []byte              `json:"signature"`
	Algorithm HashAlgorithm       `json:"algorithm"`
}

// AttestationResult holds the outcome of an attestation verification.
type AttestationResult struct {
	Verified   bool              `json:"verified"`
	PCRMatches map[PCRIndex]bool `json:"pcr_matches"`
	Errors     []string          `json:"errors,omitempty"`
	VerifiedAt time.Time         `json:"verified_at"`
}

// Config holds TPM attestation configuration.
type Config struct {
	Enabled      bool       `json:"enabled"`
	DevicePath   string     `json:"device_path"`
	PCRImageIdx  PCRIndex   `json:"pcr_image_index"`
	PCRConfigIdx PCRIndex   `json:"pcr_config_index"`
	PCRProvIdx   PCRIndex   `json:"pcr_provisioner_index"`
	AttestPCRs   []PCRIndex `json:"attest_pcrs"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:      false,
		DevicePath:   "",
		PCRImageIdx:  PCRImage,
		PCRConfigIdx: PCRConfig,
		PCRProvIdx:   PCRProvisioner,
		AttestPCRs:   []PCRIndex{PCRFirmware, PCRFirmwareCfg, PCRBootLoader, PCRSecureBoot, PCRImage, PCRConfig, PCRProvisioner},
	}
}

// Validate checks the attestation configuration.
func (c *Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.DevicePath == "" {
		return fmt.Errorf("tpm device path must not be empty when enabled")
	}
	for _, idx := range []PCRIndex{c.PCRImageIdx, c.PCRConfigIdx, c.PCRProvIdx} {
		if idx < 0 || idx > maxPCRIndex {
			return fmt.Errorf("pcr index %d out of range 0-23", idx)
		}
	}
	for _, pcr := range c.AttestPCRs {
		if pcr < 0 || pcr > maxPCRIndex {
			return fmt.Errorf("attest pcr index %d out of range 0-23", pcr)
		}
	}
	return nil
}

// VerifyPCRs compares expected PCR values against actual values.
func VerifyPCRs(expected, actual map[PCRIndex][]byte) *AttestationResult {
	result := &AttestationResult{
		Verified:   true,
		PCRMatches: make(map[PCRIndex]bool),
		VerifiedAt: time.Now(),
	}

	indices := make([]int, 0, len(expected))
	for idx := range expected {
		indices = append(indices, int(idx))
	}
	sort.Ints(indices)

	for _, idxInt := range indices {
		idx := PCRIndex(idxInt)
		exp := expected[idx]
		act, ok := actual[idx]
		if !ok {
			result.PCRMatches[idx] = false
			result.Verified = false
			result.Errors = append(result.Errors, fmt.Sprintf("missing PCR[%d] in actual values", idx))
			continue
		}

		match := len(exp) == len(act) && subtle.ConstantTimeCompare(exp, act) == 1
		result.PCRMatches[idx] = match
		if !match {
			result.Verified = false
			result.Errors = append(result.Errors,
				fmt.Sprintf("PCR[%d] mismatch: expected=%s got=%s", idx, shortHex(exp), shortHex(act)))
		}
	}
	return result
}

func shortHex(v []byte) string {
	const maxBytes = 8
	if len(v) <= maxBytes {
		return hex.EncodeToString(v)
	}
	return fmt.Sprintf("%s...(%d bytes)", hex.EncodeToString(v[:maxBytes]), len(v))
}
