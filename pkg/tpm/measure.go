// Package tpm provides TPM measurement and attestation operations.
package tpm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// MeasurementPCR constants for BOOTy-specific measurements.
const (
	// PCRBootyIdentity is used for BOOTy self-measurement and config.
	PCRBootyIdentity = 14
	// PCROSImage is used for OS image and cloud-init measurement.
	PCROSImage = 15
)

// Measurement represents a single PCR extend operation.
type Measurement struct {
	PCR         int    `json:"pcr"`
	Description string `json:"description"`
	Digest      string `json:"digest"` // hex-encoded SHA-256.
}

// EventLog records all measurements for attestation verification.
type EventLog struct {
	Entries []EventEntry `json:"entries"`
}

// EventEntry is a single event log entry.
type EventEntry struct {
	PCR         int    `json:"pcr"`
	EventType   string `json:"eventType"`
	Description string `json:"description"`
	Digest      string `json:"digest"`
}

// NewEventLog creates an empty event log.
func NewEventLog() *EventLog {
	return &EventLog{}
}

// AddEntry appends a measurement event.
func (e *EventLog) AddEntry(pcr int, eventType, description, digest string) {
	e.Entries = append(e.Entries, EventEntry{
		PCR:         pcr,
		EventType:   eventType,
		Description: description,
		Digest:      digest,
	})
}

// HashFile computes the SHA-256 hash of a file.
func HashFile(path string) (string, error) {
	f, err := os.Open(path) //#nosec G304 -- path is provisioning-controlled.
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// HashBytes computes the SHA-256 hash of a byte slice.
func HashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// ExtendPCR simulates a PCR extend operation (hash chaining).
func ExtendPCR(currentDigest, newDigest string) (string, error) {
	current, err := hex.DecodeString(currentDigest)
	if err != nil {
		return "", fmt.Errorf("decode current digest: %w", err)
	}
	incoming, err := hex.DecodeString(newDigest)
	if err != nil {
		return "", fmt.Errorf("decode new digest: %w", err)
	}
	combined := make([]byte, len(current)+len(incoming))
	copy(combined, current)
	copy(combined[len(current):], incoming)
	result := sha256.Sum256(combined)
	return hex.EncodeToString(result[:]), nil
}

// ReplayEventLog replays an event log to compute expected PCR values.
func ReplayEventLog(log *EventLog) (map[int]string, error) {
	zeroDigest := hex.EncodeToString(make([]byte, 32))
	pcrs := make(map[int]string)

	for _, entry := range log.Entries {
		current, ok := pcrs[entry.PCR]
		if !ok {
			current = zeroDigest
		}
		extended, err := ExtendPCR(current, entry.Digest)
		if err != nil {
			return nil, fmt.Errorf("replay PCR %d event %q: %w", entry.PCR, entry.Description, err)
		}
		pcrs[entry.PCR] = extended
	}
	return pcrs, nil
}
