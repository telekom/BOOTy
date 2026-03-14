package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"
)

func TestParseChecksum(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		algo    Algorithm
		hash    string
		wantErr bool
	}{
		{"sha256", "sha256:abc123", SHA256, "abc123", false},
		{"sha512", "sha512:def456", SHA512, "def456", false},
		{"no colon", "sha256abc", "", "", true},
		{"bad algo", "md5:abc", "", "", true},
		{"empty hash", "sha256:", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			algo, hash, err := ParseChecksum(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseChecksum(%q) err = %v", tc.input, err)
			}
			if !tc.wantErr {
				if algo != tc.algo {
					t.Errorf("algo = %q, want %q", algo, tc.algo)
				}
				if hash != tc.hash {
					t.Errorf("hash = %q, want %q", hash, tc.hash)
				}
			}
		})
	}
}

func TestAlgorithm_Validate(t *testing.T) {
	tests := []struct {
		algo    Algorithm
		wantErr bool
	}{
		{SHA256, false},
		{SHA512, false},
		{Algorithm("md5"), true},
		{Algorithm(""), true},
	}
	for _, tc := range tests {
		err := tc.algo.Validate()
		if (err != nil) != tc.wantErr {
			t.Errorf("Algorithm(%q).Validate() err = %v", tc.algo, err)
		}
	}
}

func TestAlgorithm_NewHash(t *testing.T) {
	h, err := SHA256.NewHash()
	if err != nil {
		t.Fatal(err)
	}
	if h == nil {
		t.Error("hash is nil")
	}

	h, err = SHA512.NewHash()
	if err != nil {
		t.Fatal(err)
	}
	if h == nil {
		t.Error("hash is nil")
	}

	_, err = Algorithm("bad").NewHash()
	if err == nil {
		t.Error("expected error")
	}
}

func TestChecksumReader(t *testing.T) {
	data := "hello world"
	sum := sha256.Sum256([]byte(data))
	expected := hex.EncodeToString(sum[:])

	r, err := NewChecksumReader(strings.NewReader(data), "sha256:"+expected)
	if err != nil {
		t.Fatal(err)
	}

	// Read all data.
	buf, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf) != data {
		t.Errorf("data = %q", string(buf))
	}

	if err := r.Verify(); err != nil {
		t.Errorf("Verify() = %v", err)
	}
}

func TestChecksumReader_Mismatch(t *testing.T) {
	r, err := NewChecksumReader(strings.NewReader("hello"), "sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}

	_, _ = io.ReadAll(r)
	if err := r.Verify(); err == nil {
		t.Error("expected mismatch error")
	}
}

func TestChecksumReader_Actual(t *testing.T) {
	data := "test"
	sum := sha256.Sum256([]byte(data))
	expected := hex.EncodeToString(sum[:])

	r, err := NewChecksumReader(strings.NewReader(data), "sha256:"+expected)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(r)

	if r.Actual() != expected {
		t.Errorf("Actual() = %q, want %q", r.Actual(), expected)
	}
}

func TestChecksumReader_InvalidChecksum(t *testing.T) {
	_, err := NewChecksumReader(strings.NewReader("x"), "bad")
	if err == nil {
		t.Error("expected error")
	}
}

func TestHashBytes(t *testing.T) {
	data := []byte("test data")
	sum := sha256.Sum256(data)
	expected := hex.EncodeToString(sum[:])

	result, err := HashBytes(SHA256, data)
	if err != nil {
		t.Fatal(err)
	}
	if result != expected {
		t.Errorf("HashBytes() = %q, want %q", result, expected)
	}
}

func TestHashBytes_BadAlgo(t *testing.T) {
	_, err := HashBytes(Algorithm("bad"), []byte("x"))
	if err == nil {
		t.Error("expected error")
	}
}

func TestVerifyConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     VerifyConfig
		wantErr bool
	}{
		{"empty", VerifyConfig{}, false},
		{"valid checksum", VerifyConfig{Checksum: "sha256:abc"}, false},
		{"bad checksum", VerifyConfig{Checksum: "bad"}, true},
		{"sig without key", VerifyConfig{SignatureURL: "http://x"}, true},
		{"sig with key", VerifyConfig{SignatureURL: "http://x", PublicKeyPath: "/k"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestVerifyConfig_Predicates(t *testing.T) {
	cfg := VerifyConfig{Checksum: "sha256:abc"}
	if !cfg.HasChecksum() {
		t.Error("HasChecksum should be true")
	}
	if cfg.HasSignature() {
		t.Error("HasSignature should be false")
	}

	cfg2 := VerifyConfig{SignatureURL: "http://x", PublicKeyPath: "/k"}
	if cfg2.HasChecksum() {
		t.Error("HasChecksum should be false")
	}
	if !cfg2.HasSignature() {
		t.Error("HasSignature should be true")
	}
}

func TestAlgorithmConstants(t *testing.T) {
	if string(SHA256) != "sha256" {
		t.Error("SHA256")
	}
	if string(SHA512) != "sha512" {
		t.Error("SHA512")
	}
}
