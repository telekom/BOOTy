package tpm

import (
	"encoding/hex"
	"testing"
)

func TestVerifyQuoteAgainstPolicy_NilPolicy(t *testing.T) {
	result := VerifyQuoteAgainstPolicy(nil, nil)
	if result.Valid {
		t.Error("expected invalid for nil policy")
	}
}

func TestVerifyQuoteAgainstPolicy_Match(t *testing.T) {
	digest := HashBytes([]byte("test"))
	policy := &GoldenPolicy{
		Name:    "test-policy",
		Version: "1.0",
		PCRs: []GoldenPCR{
			{PCR: 0, Algorithm: "sha256", Digest: hex.EncodeToString(digest)},
		},
	}
	actual := map[int][]byte{0: digest}
	result := VerifyQuoteAgainstPolicy(actual, policy)
	if !result.Valid {
		t.Errorf("expected valid, got mismatches: %v", result.Mismatches)
	}
}

func TestVerifyQuoteAgainstPolicy_Mismatch(t *testing.T) {
	policy := &GoldenPolicy{
		PCRs: []GoldenPCR{
			{PCR: 0, Algorithm: "sha256", Digest: hex.EncodeToString(HashBytes([]byte("expected")))},
		},
	}
	actual := map[int][]byte{0: HashBytes([]byte("actual"))}
	result := VerifyQuoteAgainstPolicy(actual, policy)
	if result.Valid {
		t.Error("expected invalid")
	}
	if len(result.Mismatches) != 1 {
		t.Errorf("expected 1 mismatch, got %d", len(result.Mismatches))
	}
}

func TestVerifyQuoteAgainstPolicy_MissingPCR(t *testing.T) {
	policy := &GoldenPolicy{
		PCRs: []GoldenPCR{
			{PCR: 7, Digest: hex.EncodeToString(HashBytes([]byte("data")))},
		},
	}
	result := VerifyQuoteAgainstPolicy(map[int][]byte{}, policy)
	if result.Valid {
		t.Error("expected invalid for missing PCR")
	}
}
