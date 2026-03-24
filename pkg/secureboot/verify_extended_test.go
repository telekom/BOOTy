package secureboot

import (
	"testing"

	"github.com/telekom/BOOTy/pkg/efi"
)

func TestChainVerifier_ComponentPresence_AllMissing(t *testing.T) {
	vars := efi.NewEFIVarReader(t.TempDir())
	cv := NewChainVerifier(vars)
	components := cv.checkComponentPresence()

	if len(components) != 3 {
		t.Fatalf("expected 3 components, got %d", len(components))
	}
	names := map[string]bool{"shim": false, "grub": false, "kernel": false}
	for _, c := range components {
		if _, ok := names[c.Name]; !ok {
			t.Errorf("unexpected component %q", c.Name)
		}
		if c.Error == "" {
			t.Errorf("component %q should have error when files missing", c.Name)
		}
		names[c.Name] = true
	}
	for name, found := range names {
		if !found {
			t.Errorf("missing component %q in results", name)
		}
	}
}

func TestAllComponentsPresent_Empty(t *testing.T) {
	vars := efi.NewEFIVarReader(t.TempDir())
	cv := NewChainVerifier(vars)
	if cv.allComponentsPresent(nil) {
		t.Error("nil components should return false")
	}
	if cv.allComponentsPresent([]ComponentStatus{}) {
		t.Error("empty components should return false")
	}
}

func TestAllComponentsPresent_WithError(t *testing.T) {
	vars := efi.NewEFIVarReader(t.TempDir())
	cv := NewChainVerifier(vars)
	components := []ComponentStatus{
		{Name: "shim"},
		{Name: "grub", Error: "not found"},
	}
	if cv.allComponentsPresent(components) {
		t.Error("should return false when any component has error")
	}
}

func TestAllComponentsPresent_AllGood(t *testing.T) {
	vars := efi.NewEFIVarReader(t.TempDir())
	cv := NewChainVerifier(vars)
	components := []ComponentStatus{
		{Name: "shim"},
		{Name: "grub"},
		{Name: "kernel"},
	}
	if !cv.allComponentsPresent(components) {
		t.Error("should return true when all components have no errors")
	}
}

func TestChainVerifier_VerifyResult(t *testing.T) {
	vars := efi.NewEFIVarReader(t.TempDir())
	cv := NewChainVerifier(vars)
	result, err := cv.Verify()
	if err != nil {
		t.Fatal(err)
	}
	// In temp dir, SecureBoot should not be enabled.
	if result.SecureBootEnabled {
		t.Error("SecureBoot should not be enabled in temp dir")
	}
	// Valid should be false (no SecureBoot + missing components).
	if result.Valid {
		t.Error("chain should not be valid without SecureBoot")
	}
	// Components should be populated.
	if len(result.Components) == 0 {
		t.Error("expected components in result")
	}
}
