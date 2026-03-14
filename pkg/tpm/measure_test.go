package tpm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHashFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bin")
	os.WriteFile(path, []byte("hello"), 0o644)

	hash, err := HashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// SHA-256 of "hello".
	if hash != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Errorf("hash = %q", hash)
	}
}

func TestHashFile_NotFound(t *testing.T) {
	_, err := HashFile("/nonexistent")
	if err == nil {
		t.Error("expected error")
	}
}

func TestHashBytes(t *testing.T) {
	hash := HashBytes([]byte("hello"))
	if hash != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Errorf("hash = %q", hash)
	}
}

func TestExtendPCR(t *testing.T) {
	zero := "0000000000000000000000000000000000000000000000000000000000000000"
	digest := HashBytes([]byte("test"))

	result, err := ExtendPCR(zero, digest)
	if err != nil {
		t.Fatal(err)
	}
	if result == "" {
		t.Error("empty result")
	}
	if result == zero {
		t.Error("result should differ from zero")
	}
}

func TestExtendPCR_InvalidHex(t *testing.T) {
	_, err := ExtendPCR("invalid", "0000")
	if err == nil {
		t.Error("expected error for invalid hex")
	}
}

func TestEventLog(t *testing.T) {
	log := NewEventLog()
	log.AddEntry(14, "BOOTY_SELF", "BOOTy binary", HashBytes([]byte("init")))
	log.AddEntry(15, "OS_IMAGE", "disk image", HashBytes([]byte("image")))

	if len(log.Entries) != 2 {
		t.Fatalf("entries = %d", len(log.Entries))
	}
	if log.Entries[0].PCR != 14 {
		t.Errorf("PCR = %d", log.Entries[0].PCR)
	}
}

func TestReplayEventLog(t *testing.T) {
	log := NewEventLog()
	log.AddEntry(14, "BOOTY_SELF", "binary", HashBytes([]byte("init")))
	log.AddEntry(14, "CONFIG", "config", HashBytes([]byte("config")))
	log.AddEntry(15, "OS_IMAGE", "image", HashBytes([]byte("image")))

	pcrs, err := ReplayEventLog(log)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pcrs[14]; !ok {
		t.Error("missing PCR 14")
	}
	if _, ok := pcrs[15]; !ok {
		t.Error("missing PCR 15")
	}
}

func TestReplayEventLog_Empty(t *testing.T) {
	log := NewEventLog()
	pcrs, err := ReplayEventLog(log)
	if err != nil {
		t.Fatal(err)
	}
	if len(pcrs) != 0 {
		t.Errorf("pcrs = %d", len(pcrs))
	}
}

func TestVerifyQuote_Pass(t *testing.T) {
	digest := HashBytes([]byte("test"))
	quote := &Quote{
		Nonce:     "abc123",
		PCRs:      map[int]string{7: digest},
		Timestamp: time.Now(),
	}
	policy := &GoldenPolicy{
		Name:    "prod",
		Version: 1,
		PCRs:    []GoldenPCR{{PCR: 7, Digest: digest}},
	}

	result, err := VerifyQuote(quote, policy)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed {
		t.Errorf("expected pass, got mismatches: %v", result.Mismatches)
	}
}

func TestVerifyQuote_Mismatch(t *testing.T) {
	quote := &Quote{
		PCRs:      map[int]string{7: "aaaa"},
		Timestamp: time.Now(),
	}
	policy := &GoldenPolicy{
		Name: "prod",
		PCRs: []GoldenPCR{{PCR: 7, Digest: "bbbb"}},
	}

	result, err := VerifyQuote(quote, policy)
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed {
		t.Error("should fail with mismatch")
	}
	if len(result.Mismatches) != 1 {
		t.Errorf("mismatches = %d", len(result.Mismatches))
	}
}

func TestVerifyQuote_MissingPCR(t *testing.T) {
	quote := &Quote{
		PCRs:      map[int]string{},
		Timestamp: time.Now(),
	}
	policy := &GoldenPolicy{
		Name: "prod",
		PCRs: []GoldenPCR{{PCR: 7, Digest: "abc"}},
	}

	result, err := VerifyQuote(quote, policy)
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed {
		t.Error("should fail with missing PCR")
	}
}

func TestVerifyQuote_NilInputs(t *testing.T) {
	_, err := VerifyQuote(nil, &GoldenPolicy{})
	if err == nil {
		t.Error("expected error for nil quote")
	}

	_, err = VerifyQuote(&Quote{}, nil)
	if err == nil {
		t.Error("expected error for nil policy")
	}
}

func TestVerificationResult_JSON(t *testing.T) {
	result := &VerificationResult{
		Passed:     true,
		PolicyName: "test",
	}
	data, err := result.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded VerificationResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Passed {
		t.Error("decoded not passed")
	}
}

func TestPCRConstants(t *testing.T) {
	if PCRBootyIdentity != 14 {
		t.Error("PCRBootyIdentity")
	}
	if PCROSImage != 15 {
		t.Error("PCROSImage")
	}
}

func TestGoldenPolicy_Types(t *testing.T) {
	p := GoldenPolicy{
		Name:    "production",
		Version: 2,
		PCRs: []GoldenPCR{
			{PCR: 7, Digest: "aaa", Description: "secure boot"},
		},
	}
	if p.Name != "production" {
		t.Error("name")
	}
	if p.Version != 2 {
		t.Error("version")
	}
}
