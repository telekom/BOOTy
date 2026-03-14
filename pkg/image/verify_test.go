// verify_test.go tests image checksum verification.
package image

import (
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestParseChecksum(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		algo    string
		hex     string
		wantErr bool
	}{
		{"sha256", "sha256:abcdef", "sha256", "abcdef", false},
		{"sha512", "SHA512:ABCDEF", "sha512", "abcdef", false},
		{"no colon", "abcdef", "", "", true},
		{"bad algo", "md5:abcdef", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			algo, hex, err := ParseChecksum(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseChecksum(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if algo != tt.algo || hex != tt.hex {
				t.Errorf("ParseChecksum(%q) = (%q, %q), want (%q, %q)", tt.input, algo, hex, tt.algo, tt.hex)
			}
		})
	}
}

func TestChecksumReaderVerify(t *testing.T) {
	data := "hello world"
	h := sha256.Sum256([]byte(data))
	checksum := fmt.Sprintf("sha256:%x", h)

	cr, err := NewChecksumReader(strings.NewReader(data), checksum)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(cr); err != nil {
		t.Fatal(err)
	}
	if err := cr.Verify(); err != nil {
		t.Fatalf("Verify() should pass: %v", err)
	}
}

func TestChecksumReaderMismatch(t *testing.T) {
	cr, err := NewChecksumReader(strings.NewReader("hello"), "sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(cr); err != nil {
		t.Fatal(err)
	}
	if err := cr.Verify(); err == nil {
		t.Fatal("Verify() should fail on mismatch")
	}
}
