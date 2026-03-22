package tpm

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// HashAlgorithm identifies the digest algorithm.
type HashAlgorithm string

// Hash algorithm constants.
const (
	HashSHA256 HashAlgorithm = "sha256"
)

// Measurement records a single PCR extension event.
type Measurement struct {
	PCR       int           `json:"pcr"`
	Algorithm HashAlgorithm `json:"algorithm"`
	Digest    []byte        `json:"digest"`
	Label     string        `json:"label"`
	Timestamp time.Time     `json:"timestamp"`
}

// MeasurementLog records an ordered sequence of measurements.
type MeasurementLog struct {
	mu      sync.Mutex
	entries []Measurement
}

// NewMeasurementLog creates an empty measurement log.
func NewMeasurementLog() *MeasurementLog {
	return &MeasurementLog{}
}

// Add appends a SHA-256 measurement.
func (l *MeasurementLog) Add(pcr int, label string, digest []byte) {
	l.AddWithAlgorithm(pcr, label, digest, HashSHA256)
}

// AddWithAlgorithm appends a measurement with a specific algorithm.
func (l *MeasurementLog) AddWithAlgorithm(pcr int, label string, digest []byte, algo HashAlgorithm) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, Measurement{
		PCR:       pcr,
		Algorithm: algo,
		Digest:    append([]byte(nil), digest...),
		Label:     label,
		Timestamp: time.Now(),
	})
}

// Entries returns a copy of all measurements.
func (l *MeasurementLog) Entries() []Measurement {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Measurement, len(l.entries))
	for i, m := range l.entries {
		out[i] = Measurement{
			PCR:       m.PCR,
			Algorithm: m.Algorithm,
			Digest:    append([]byte(nil), m.Digest...),
			Label:     m.Label,
			Timestamp: m.Timestamp,
		}
	}
	return out
}

// DigestsForPCR returns all digests recorded for a given PCR.
func (l *MeasurementLog) DigestsForPCR(pcr int) [][]byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	var digests [][]byte
	for _, m := range l.entries {
		if m.PCR == pcr {
			d := make([]byte, len(m.Digest))
			copy(d, m.Digest)
			digests = append(digests, d)
		}
	}
	return digests
}

// HashFile computes the SHA-256 digest of a file.
func HashFile(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // intended file read
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // best-effort close on read-only file
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, fmt.Errorf("hashing %s: %w", path, err)
	}
	return h.Sum(nil), nil
}

// HashBytes computes the SHA-256 digest of data.
func HashBytes(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}

// ExtendPCRSoft simulates a PCR extend operation in software.
// The result is SHA-256(currentValue || newDigest).
func ExtendPCRSoft(current, newDigest []byte) []byte {
	if current == nil {
		current = make([]byte, sha256.Size)
	}
	h := sha256.New()
	h.Write(current)
	h.Write(newDigest)
	return h.Sum(nil)
}

// ReplayMeasurementLog replays a log to compute expected PCR values.
func ReplayMeasurementLog(log *MeasurementLog) map[int][]byte {
	pcrs := make(map[int][]byte)
	for _, m := range log.Entries() {
		pcrs[m.PCR] = ExtendPCRSoft(pcrs[m.PCR], m.Digest)
	}
	return pcrs
}

// VerifyPCRs compares expected PCR values against actual values
// using constant-time comparison to prevent timing attacks.
func VerifyPCRs(expected, actual map[int][]byte) (bool, []PCRMismatch) {
	var mismatches []PCRMismatch
	for pcr, exp := range expected {
		act, ok := actual[pcr]
		if !ok || subtle.ConstantTimeCompare(exp, act) != 1 {
			mismatches = append(mismatches, PCRMismatch{
				PCR:      pcr,
				Expected: hex.EncodeToString(exp),
				Actual:   hex.EncodeToString(act),
			})
		}
	}
	return len(mismatches) == 0, mismatches
}

// PCRMismatch describes a PCR value that does not match expectations.
type PCRMismatch struct {
	PCR      int    `json:"pcr"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}
