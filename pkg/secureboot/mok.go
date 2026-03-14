package secureboot

import (
	"fmt"
	"log/slog"
)

const mokNewGUID = "605dab50-e046-4300-abb6-3dd810dd8b23"

// MOKEnroller manages Machine Owner Key enrollment.
type MOKEnroller struct {
	log    *slog.Logger
	efivar *EFIVarReader
}

// NewMOKEnroller creates a MOK enroller using the given efivarfs reader.
func NewMOKEnroller(log *slog.Logger, efivar *EFIVarReader) *MOKEnroller {
	return &MOKEnroller{log: log, efivar: efivar}
}

// EnrollMOK writes a DER-encoded certificate to the MokNew EFI variable.
// After writing, a reboot is required to complete enrollment via MokManager.
// NOTE: production use should encode the cert into an EFI_SIGNATURE_LIST
// with EFI_CERT_X509_GUID and handle MokAuth. Consider falling back to
// mokutil --import for broader compatibility.
func (e *MOKEnroller) EnrollMOK(certDER []byte) error {
	if len(certDER) == 0 {
		return fmt.Errorf("empty MOK certificate")
	}

	varName := "MokNew-" + mokNewGUID

	// EFI_VARIABLE_NON_VOLATILE | EFI_VARIABLE_BOOTSERVICE_ACCESS | EFI_VARIABLE_RUNTIME_ACCESS
	const attrs uint32 = 0x07

	if err := e.efivar.WriteVar(varName, attrs, certDER); err != nil {
		return fmt.Errorf("write MokNew variable: %w", err)
	}

	if e.log != nil {
		e.log.Info("enrolled MOK certificate", "size", len(certDER))
	}
	return nil
}

// ListMOKs returns the names of MOK-related EFI variables.
func (e *MOKEnroller) ListMOKs() ([]string, error) {
	return e.efivar.ListVars("Mok")
}
