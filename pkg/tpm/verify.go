package tpm

import (
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"time"
)

// GoldenPCR defines an expected PCR value.
type GoldenPCR struct {
	PCR       int    `json:"pcr"`
	Algorithm string `json:"algorithm"`
	Digest    string `json:"digest"` // hex-encoded
}

// GoldenPolicy is a set of expected PCR values for verification.
type GoldenPolicy struct {
	Name    string      `json:"name"`
	Version string      `json:"version"`
	PCRs    []GoldenPCR `json:"pcrs"`
}

// VerificationResult holds the outcome of a quote verification.
type VerificationResult struct {
	Valid      bool          `json:"valid"`
	Timestamp  time.Time     `json:"timestamp"`
	Mismatches []PCRMismatch `json:"mismatches,omitempty"`
	Error      string        `json:"error,omitempty"`
}

// VerifyQuoteAgainstPolicy verifies PCR values from a quote against a golden policy.
func VerifyQuoteAgainstPolicy(pcrValues map[int][]byte, policy *GoldenPolicy) *VerificationResult {
	result := &VerificationResult{
		Timestamp: time.Now(),
		Valid:     true,
	}
	if policy == nil {
		result.Error = "nil policy"
		result.Valid = false
		return result
	}
	if len(policy.PCRs) == 0 {
		result.Error = "empty policy: no PCRs to verify"
		result.Valid = false
		return result
	}
	for _, golden := range policy.PCRs {
		expected, err := hex.DecodeString(golden.Digest)
		if err != nil {
			result.Mismatches = append(result.Mismatches, PCRMismatch{
				PCR:      golden.PCR,
				Expected: golden.Digest,
				Actual:   fmt.Sprintf("decode error: %v", err),
			})
			result.Valid = false
			continue
		}
		actual, ok := pcrValues[golden.PCR]
		if !ok || subtle.ConstantTimeCompare(expected, actual) != 1 {
			result.Mismatches = append(result.Mismatches, PCRMismatch{
				PCR:      golden.PCR,
				Expected: golden.Digest,
				Actual:   hex.EncodeToString(actual),
			})
			result.Valid = false
		}
	}
	return result
}
