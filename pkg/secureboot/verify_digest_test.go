package secureboot

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateDigest(t *testing.T) {
	path := writeTempFile(t, []byte("trusted artifact"), "vmlinuz")
	sum := sha256.Sum256([]byte("trusted artifact"))
	want := fmt.Sprintf("%x", sum[:])

	if err := validateDigest(path, want); err != nil {
		t.Fatalf("validateDigest() bare hex = %v", err)
	}
	if err := validateDigest(path, "sha256:"+want); err != nil {
		t.Fatalf("validateDigest() sha256 prefix = %v", err)
	}
	if err := validateDigest(path, strings.Repeat("0", 64)); err == nil {
		t.Fatal("validateDigest() mismatch error = nil, want error")
	}
	if err := validateDigest(path, "not-a-digest"); err == nil {
		t.Fatal("validateDigest() invalid expected error = nil, want error")
	}
}

func TestWithPinnedDigestsCopiesInputMap(t *testing.T) {
	path := writeTempFile(t, []byte("kernel"), "vmlinuz")
	sum := sha256.Sum256([]byte("kernel"))
	pins := map[string]string{"kernel": fmt.Sprintf("%x", sum[:])}
	cv := NewChainVerifier(nil).WithPinnedDigests(pins)
	pins["kernel"] = strings.Repeat("0", 64)

	if err := validateDigest(path, cv.pinnedDigests["kernel"]); err != nil {
		t.Fatalf("pinned digest changed after caller map mutation: %v", err)
	}
}

func TestFindValidCandidateEnforcesPinnedDigest(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad-vmlinuz")
	if err := os.WriteFile(bad, []byte("tampered"), 0o600); err != nil {
		t.Fatalf("write bad kernel: %v", err)
	}
	good := filepath.Join(dir, "vmlinuz")
	if err := os.WriteFile(good, []byte("trusted"), 0o600); err != nil {
		t.Fatalf("write good kernel: %v", err)
	}
	sum := sha256.Sum256([]byte("trusted"))

	cv := NewChainVerifier(nil).WithPinnedDigests(map[string]string{
		"kernel": fmt.Sprintf("%x", sum[:]),
	})
	status := cv.findValidCandidate("kernel", []string{bad, good})
	if status.Error != "" {
		t.Fatalf("findValidCandidate() error = %q", status.Error)
	}

	status = cv.findValidCandidate("kernel", []string{bad})
	if status.Error == "" {
		t.Fatal("findValidCandidate() error = empty, want digest mismatch")
	}
	if !strings.Contains(status.Error, "digest mismatch") {
		t.Fatalf("findValidCandidate() error = %q, want digest mismatch", status.Error)
	}
}
