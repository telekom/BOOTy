package secureboot

import (
	"testing"

	"github.com/telekom/BOOTy/pkg/efi"
)

func TestChainVerifier_Verify(t *testing.T) {
	// Use a temp dir that won't have real EFI variables
	vars := efi.NewEFIVarReader(t.TempDir())
	cv := NewChainVerifier(vars)
	result, err := cv.Verify()
	if err != nil {
		t.Fatal(err)
	}
	// Without efivarfs, SecureBoot should not be detected as enabled
	if result.SecureBootEnabled {
		t.Error("expected SecureBoot disabled in temp dir")
	}
	if result.Valid {
		t.Error("expected chain not valid without real EFI vars")
	}
}
