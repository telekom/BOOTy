package tpm

import (
	"encoding/json"
	"fmt"
	"time"
)

// Quote represents a TPM2 attestation quote.
type Quote struct {
	Nonce     string         `json:"nonce"`
	PCRs      map[int]string `json:"pcrs"`
	Timestamp time.Time      `json:"timestamp"`
	TPMPath   string         `json:"tpmPath"`
	Signature string         `json:"signature,omitempty"`
}

// GoldenPCR defines expected PCR values for attestation verification.
type GoldenPCR struct {
	PCR         int    `json:"pcr"`
	Digest      string `json:"digest"`
	Description string `json:"description"`
}

// GoldenPolicy defines a set of expected PCR values.
type GoldenPolicy struct {
	Name    string      `json:"name"`
	PCRs    []GoldenPCR `json:"pcrs"`
	Version int         `json:"version"`
}

// VerifyQuote checks a quote against a golden policy.
func VerifyQuote(quote *Quote, policy *GoldenPolicy) (*VerificationResult, error) {
	if quote == nil {
		return nil, fmt.Errorf("quote is nil")
	}
	if policy == nil {
		return nil, fmt.Errorf("policy is nil")
	}

	result := &VerificationResult{
		PolicyName:    policy.Name,
		PolicyVersion: policy.Version,
		Timestamp:     quote.Timestamp,
	}

	for _, golden := range policy.PCRs {
		actual, ok := quote.PCRs[golden.PCR]
		if !ok {
			result.Mismatches = append(result.Mismatches, PCRMismatch{
				PCR:      golden.PCR,
				Expected: golden.Digest,
				Actual:   "",
				Reason:   "PCR not present in quote",
			})
			continue
		}
		if actual != golden.Digest {
			result.Mismatches = append(result.Mismatches, PCRMismatch{
				PCR:      golden.PCR,
				Expected: golden.Digest,
				Actual:   actual,
				Reason:   "digest mismatch",
			})
		}
	}

	result.Passed = len(result.Mismatches) == 0
	return result, nil
}

// VerificationResult holds the outcome of quote verification.
type VerificationResult struct {
	Passed        bool          `json:"passed"`
	PolicyName    string        `json:"policyName"`
	PolicyVersion int           `json:"policyVersion"`
	Timestamp     time.Time     `json:"timestamp"`
	Mismatches    []PCRMismatch `json:"mismatches,omitempty"`
}

// PCRMismatch describes a PCR value mismatch.
type PCRMismatch struct {
	PCR      int    `json:"pcr"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Reason   string `json:"reason"`
}

// JSON returns the verification result as JSON bytes.
func (r *VerificationResult) JSON() ([]byte, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("marshal verification result: %w", err)
	}
	return data, nil
}
