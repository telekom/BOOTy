//go:build linux

package tpm

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"

	"github.com/google/go-tpm/tpm2"
)

// PCR indices used for provisioning measurements.
const (
	PCRBOOTyBinary   = 8  // Hash of the BOOTy binary itself.
	PCRImageChecksum = 9  // Hash of the provisioned OS image.
	PCRProvisionCfg  = 10 // Hash of the provisioning configuration.
	PCRCustomSteps   = 14 // Hash of custom provisioner step results.
)

// AttestationQuote holds a signed TPM quote and associated metadata.
type AttestationQuote struct {
	QuoteData []byte            `json:"quoteData"` // TPMS_ATTEST serialized
	Signature []byte            `json:"signature"` // Quote signature
	PCRDigest []byte            `json:"pcrDigest"` // Composite PCR digest
	PCRValues map[int][]byte    `json:"pcrValues"` // Individual PCR values
	Nonce     []byte            `json:"nonce"`     // Server-provided nonce
	ExtraData map[string]string `json:"extraData"` // Optional metadata
}

// Quote generates a TPM 2.0 attestation quote over the selected PCRs
// using an Attestation Key created in the TPM.
func (d *Device) Quote(pcrSelection []int, nonce []byte) (*AttestationQuote, error) {
	if len(pcrSelection) == 0 {
		return nil, fmt.Errorf("pcrSelection must not be empty for attestation quote")
	}
	for _, idx := range pcrSelection {
		if err := validatePCRIndex(idx); err != nil {
			return nil, err
		}
	}
	if len(nonce) == 0 {
		return nil, fmt.Errorf("nonce must not be empty")
	}

	createResp, err := d.createAttestationKey()
	if err != nil {
		return nil, fmt.Errorf("creating attestation key: %w", err)
	}
	defer func() {
		flushCtx := tpm2.FlushContext{FlushHandle: createResp.ObjectHandle}
		_, _ = flushCtx.Execute(d.transport) //nolint:errcheck // best-effort TPM cleanup
	}()

	sel := buildPCRSelection(pcrSelection)
	quoteResp, err := d.generateQuote(createResp.ObjectHandle, nonce, sel)
	if err != nil {
		return nil, fmt.Errorf("generating quote: %w", err)
	}

	pcrValues, pcrDigest, err := d.readPCRDigest(pcrSelection, sel)
	if err != nil {
		return nil, fmt.Errorf("reading PCR digest: %w", err)
	}

	result := &AttestationQuote{
		QuoteData: tpm2.Marshal(quoteResp.Quoted),
		Signature: tpm2.Marshal(quoteResp.Signature),
		PCRDigest: pcrDigest,
		PCRValues: pcrValues,
		Nonce:     nonce,
	}

	d.log.Info("Generated TPM attestation quote",
		"pcrs", pcrSelection,
		"nonce_len", len(nonce),
	)

	return result, nil
}

// createAttestationKey creates a primary ECC key in the endorsement hierarchy.
func (d *Device) createAttestationKey() (*tpm2.CreatePrimaryResponse, error) {
	primaryTemplate := tpm2.TPMTPublic{
		Type:    tpm2.TPMAlgECC,
		NameAlg: tpm2.TPMAlgSHA256,
		ObjectAttributes: tpm2.TPMAObject{
			FixedTPM:            true,
			FixedParent:         true,
			SensitiveDataOrigin: true,
			UserWithAuth:        true,
			SignEncrypt:         true,
			Restricted:          true,
		},
		Parameters: tpm2.NewTPMUPublicParms(
			tpm2.TPMAlgECC,
			&tpm2.TPMSECCParms{
				Scheme: tpm2.TPMTECCScheme{
					Scheme: tpm2.TPMAlgECDSA,
					Details: tpm2.NewTPMUAsymScheme(
						tpm2.TPMAlgECDSA,
						&tpm2.TPMSSigSchemeECDSA{
							HashAlg: tpm2.TPMAlgSHA256,
						},
					),
				},
				CurveID: tpm2.TPMECCNistP256,
			},
		),
	}

	resp, err := tpm2.CreatePrimary{
		PrimaryHandle: tpm2.AuthHandle{
			Handle: tpm2.TPMRHEndorsement,
			Auth:   tpm2.PasswordAuth(nil),
		},
		InPublic: tpm2.New2B(primaryTemplate),
	}.Execute(d.transport)
	if err != nil {
		return nil, fmt.Errorf("creating attestation key: %w", err)
	}
	return resp, nil
}

// buildPCRSelection creates a TPM PCR selection structure.
func buildPCRSelection(pcrSelection []int) tpm2.TPMLPCRSelection {
	return tpm2.TPMLPCRSelection{
		PCRSelections: []tpm2.TPMSPCRSelection{
			{
				Hash:      tpm2.TPMAlgSHA256,
				PCRSelect: pcrSelectMultiple(pcrSelection),
			},
		},
	}
}

// generateQuote executes a TPM Quote command.
func (d *Device) generateQuote(handle tpm2.TPMHandle, nonce []byte, sel tpm2.TPMLPCRSelection) (*tpm2.QuoteResponse, error) {
	nonceTPM := tpm2.TPM2BData{Buffer: nonce}
	resp, err := tpm2.Quote{
		SignHandle: tpm2.AuthHandle{
			Handle: handle,
			Auth:   tpm2.PasswordAuth(nil),
		},
		QualifyingData: nonceTPM,
		InScheme: tpm2.TPMTSigScheme{
			Scheme: tpm2.TPMAlgECDSA,
			Details: tpm2.NewTPMUSigScheme(
				tpm2.TPMAlgECDSA,
				&tpm2.TPMSSchemeHash{
					HashAlg: tpm2.TPMAlgSHA256,
				},
			),
		},
		PCRSelect: sel,
	}.Execute(d.transport)
	if err != nil {
		return nil, fmt.Errorf("generating TPM quote: %w", err)
	}
	return resp, nil
}

// readPCRDigest reads PCR values and computes a composite digest.
func (d *Device) readPCRDigest(pcrSelection []int, sel tpm2.TPMLPCRSelection) (pcrValues map[int][]byte, pcrDigest []byte, err error) {
	pcrReadResp, err := tpm2.PCRRead{
		PCRSelectionIn: sel,
	}.Execute(d.transport)
	if err != nil {
		return nil, nil, fmt.Errorf("reading PCR values for quote: %w", err)
	}

	// TPM PCRRead returns digests in ascending PCR order matching the sorted
	// selection. Validate that we received exactly the expected number of digests.
	sortedPCRs := sortedInts(pcrSelection)
	expectedDigests := len(sortedPCRs)
	actualDigests := len(pcrReadResp.PCRValues.Digests)
	if actualDigests != expectedDigests {
		return nil, nil, fmt.Errorf("PCR read returned %d digests, expected %d for PCR selection %v", actualDigests, expectedDigests, pcrSelection)
	}
	pcrValues = make(map[int][]byte, len(sortedPCRs))
	for i, idx := range sortedPCRs {
		pcrValues[idx] = pcrReadResp.PCRValues.Digests[i].Buffer
	}

	// Compute PCR composite digest over deterministic sorted order to ensure
	// consistent results regardless of the pcrSelection argument ordering.
	h := sha256.New()
	for _, idx := range sortedPCRs {
		if v, ok := pcrValues[idx]; ok {
			h.Write(v)
		}
	}
	pcrDigest = h.Sum(nil)

	return pcrValues, pcrDigest, nil
}

// VerifyQuoteSignature verifies an attestation quote signature using
// the provided ECDSA public key. The signature is a TPM2-marshaled
// TPMT_SIGNATURE structure that must be decoded to extract the raw
// ECDSA (r, s) components.
func VerifyQuoteSignature(quote *AttestationQuote, pubkey *ecdsa.PublicKey) error {
	if quote == nil {
		return fmt.Errorf("quote must not be nil")
	}
	if pubkey == nil {
		return fmt.Errorf("public key must not be nil")
	}
	digest := sha256.Sum256(quote.QuoteData)

	// Decode TPM-marshaled TPMT_SIGNATURE to extract ECDSA r, s values.
	sig, err := tpm2.Unmarshal[tpm2.TPMTSignature](quote.Signature)
	if err != nil {
		return fmt.Errorf("unmarshaling TPM signature: %w", err)
	}
	ecdsaSig, err := sig.Signature.ECDSA()
	if err != nil {
		return fmt.Errorf("extracting ECDSA signature: %w", err)
	}

	var r, s big.Int
	r.SetBytes(ecdsaSig.SignatureR.Buffer)
	s.SetBytes(ecdsaSig.SignatureS.Buffer)

	if !ecdsa.Verify(pubkey, digest[:], &r, &s) {
		return fmt.Errorf("tpm quote signature verification failed")
	}
	return nil
}

// MarshalQuote serializes an attestation quote to JSON.
func MarshalQuote(quote *AttestationQuote) ([]byte, error) {
	data, err := json.Marshal(quote)
	if err != nil {
		return nil, fmt.Errorf("marshaling attestation quote: %w", err)
	}
	return data, nil
}

// pcrSelectMultiple builds a PCR selection bitmap for multiple indices.
func pcrSelectMultiple(indices []int) []byte {
	if len(indices) == 0 {
		return []byte{0}
	}
	maxIdx := 0
	for _, idx := range indices {
		if idx > maxIdx {
			maxIdx = idx
		}
	}
	mask := make([]byte, (maxIdx/8)+1)
	for _, idx := range indices {
		mask[idx/8] |= 1 << (idx % 8)
	}
	return mask
}

// sortedInts returns a sorted copy of the input slice.
func sortedInts(a []int) []int {
	c := make([]int, len(a))
	copy(c, a)
	sort.Ints(c)
	return c
}
