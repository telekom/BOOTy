//go:build linux

package tpm

import (
	"fmt"

	"github.com/google/go-tpm/tpm2"
)

// SealedBlob holds a TPM-sealed secret and its associated policy.
type SealedBlob struct {
	Public  []byte `json:"public"`  // TPM2B_PUBLIC marshaled
	Private []byte `json:"private"` // TPM2B_PRIVATE marshaled
}

// SealSecret seals data to the current values of the specified PCR registers.
// The secret can only be unsealed when the PCRs match the values at seal time.
func (d *Device) SealSecret(secret []byte, pcrSelection []int) (*SealedBlob, error) {
	if len(secret) == 0 {
		return nil, fmt.Errorf("secret must not be empty")
	}
	if len(pcrSelection) == 0 {
		return nil, fmt.Errorf("pcrSelection must not be empty: sealing without PCR binding provides no security")
	}
	for _, idx := range pcrSelection {
		if idx < 0 || idx > 23 {
			return nil, fmt.Errorf("invalid PCR index %d: must be 0-23", idx)
		}
	}

	// Create a primary storage key in the owner hierarchy.
	srkResp, err := d.createSRK()
	if err != nil {
		return nil, fmt.Errorf("creating storage root key: %w", err)
	}
	defer func() {
		flushCtx := tpm2.FlushContext{FlushHandle: srkResp.ObjectHandle}
		_, _ = flushCtx.Execute(d.transport) //nolint:errcheck // best-effort TPM cleanup
	}()

	// Build PCR policy session.
	sess, err := d.createPCRPolicySession(pcrSelection)
	if err != nil {
		return nil, fmt.Errorf("creating PCR policy: %w", err)
	}

	// Create a sealed object under the SRK.
	createResp, err := tpm2.Create{
		ParentHandle: tpm2.AuthHandle{
			Handle: srkResp.ObjectHandle,
			Auth:   tpm2.PasswordAuth(nil),
		},
		InSensitive: tpm2.TPM2BSensitiveCreate{
			Sensitive: &tpm2.TPMSSensitiveCreate{
				Data: tpm2.NewTPMUSensitiveCreate(
					&tpm2.TPM2BSensitiveData{Buffer: secret},
				),
			},
		},
		InPublic: tpm2.New2B(tpm2.TPMTPublic{
			Type:    tpm2.TPMAlgKeyedHash,
			NameAlg: tpm2.TPMAlgSHA256,
			ObjectAttributes: tpm2.TPMAObject{
				FixedTPM:        true,
				FixedParent:     true,
				NoDA:            true,
				AdminWithPolicy: true,
			},
			AuthPolicy: sess.policyDigest,
			Parameters: tpm2.NewTPMUPublicParms(
				tpm2.TPMAlgKeyedHash,
				&tpm2.TPMSKeyedHashParms{
					Scheme: tpm2.TPMTKeyedHashScheme{
						Scheme: tpm2.TPMAlgNull,
					},
				},
			),
		}),
	}.Execute(d.transport)
	if err != nil {
		return nil, fmt.Errorf("sealing secret: %w", err)
	}

	d.log.Info("Sealed secret to TPM PCR policy",
		"pcrs", pcrSelection,
		"secret_len", len(secret),
	)

	return &SealedBlob{
		Public:  createResp.OutPublic.Bytes(),
		Private: createResp.OutPrivate.Buffer,
	}, nil
}

// UnsealSecret recovers a previously sealed secret. The operation will
// fail if the current PCR values do not match those at seal time.
func (d *Device) UnsealSecret(blob *SealedBlob, pcrSelection []int) ([]byte, error) {
	if blob == nil {
		return nil, fmt.Errorf("sealed blob must not be nil")
	}
	if len(pcrSelection) == 0 {
		return nil, fmt.Errorf("pcrSelection must not be empty: unsealing without PCR binding provides no security")
	}
	for _, idx := range pcrSelection {
		if idx < 0 || idx > 23 {
			return nil, fmt.Errorf("invalid PCR index %d: must be 0-23", idx)
		}
	}

	// Recreate the SRK.
	srkResp, err := d.createSRK()
	if err != nil {
		return nil, fmt.Errorf("creating storage root key: %w", err)
	}
	defer func() {
		flushCtx := tpm2.FlushContext{FlushHandle: srkResp.ObjectHandle}
		_, _ = flushCtx.Execute(d.transport) //nolint:errcheck // best-effort TPM cleanup
	}()

	// Load the sealed object.
	pub, err := tpm2.Unmarshal[tpm2.TPMTPublic](blob.Public)
	if err != nil {
		return nil, fmt.Errorf("unmarshaling sealed public: %w", err)
	}

	loadResp, err := tpm2.Load{
		ParentHandle: tpm2.AuthHandle{
			Handle: srkResp.ObjectHandle,
			Auth:   tpm2.PasswordAuth(nil),
		},
		InPublic:  tpm2.New2B(*pub),
		InPrivate: tpm2.TPM2BPrivate{Buffer: blob.Private},
	}.Execute(d.transport)
	if err != nil {
		return nil, fmt.Errorf("loading sealed object: %w", err)
	}
	defer func() {
		flushCtx := tpm2.FlushContext{FlushHandle: loadResp.ObjectHandle}
		_, _ = flushCtx.Execute(d.transport) //nolint:errcheck // best-effort TPM cleanup
	}()

	// Create a policy session and satisfy the PCR policy.
	policySession, closer, err := tpm2.PolicySession(d.transport, tpm2.TPMAlgSHA256, 16)
	if err != nil {
		return nil, fmt.Errorf("creating policy session: %w", err)
	}
	defer closer() //nolint:errcheck // best-effort policy session cleanup

	// Apply the PCR policy.
	sel := tpm2.TPMLPCRSelection{
		PCRSelections: []tpm2.TPMSPCRSelection{
			{
				Hash:      tpm2.TPMAlgSHA256,
				PCRSelect: pcrSelectMultiple(pcrSelection),
			},
		},
	}
	_, err = tpm2.PolicyPCR{
		PolicySession: policySession.Handle(),
		Pcrs:          sel,
	}.Execute(d.transport)
	if err != nil {
		return nil, fmt.Errorf("applying PCR policy for unseal: %w", err)
	}

	// Unseal.
	unsealResp, err := tpm2.Unseal{
		ItemHandle: tpm2.AuthHandle{
			Handle: loadResp.ObjectHandle,
			Auth:   policySession,
		},
	}.Execute(d.transport)
	if err != nil {
		return nil, fmt.Errorf("unsealing secret (PCR mismatch?): %w", err)
	}

	d.log.Info("Unsealed secret from TPM", "pcrs", pcrSelection)
	return unsealResp.OutData.Buffer, nil
}

// pcrPolicySession holds session info for a PCR-bound policy.
type pcrPolicySession struct {
	policyDigest tpm2.TPM2BDigest
}

// createPCRPolicySession creates a trial policy session to compute
// the policy digest for the given PCR selection.
func (d *Device) createPCRPolicySession(pcrSelection []int) (*pcrPolicySession, error) {
	// Use a trial session to compute the expected policy digest.
	trialSession, closer, err := tpm2.PolicySession(d.transport, tpm2.TPMAlgSHA256, 16, tpm2.Trial())
	if err != nil {
		return nil, fmt.Errorf("creating trial policy session: %w", err)
	}
	defer closer() //nolint:errcheck // best-effort trial session cleanup

	sel := tpm2.TPMLPCRSelection{
		PCRSelections: []tpm2.TPMSPCRSelection{
			{
				Hash:      tpm2.TPMAlgSHA256,
				PCRSelect: pcrSelectMultiple(pcrSelection),
			},
		},
	}

	_, err = tpm2.PolicyPCR{
		PolicySession: trialSession.Handle(),
		Pcrs:          sel,
	}.Execute(d.transport)
	if err != nil {
		return nil, fmt.Errorf("applying PCR policy in trial: %w", err)
	}

	gdResp, err := tpm2.PolicyGetDigest{
		PolicySession: trialSession.Handle(),
	}.Execute(d.transport)
	if err != nil {
		return nil, fmt.Errorf("getting trial policy digest: %w", err)
	}

	return &pcrPolicySession{
		policyDigest: gdResp.PolicyDigest,
	}, nil
}

// createSRK creates a Storage Root Key in the owner hierarchy.
func (d *Device) createSRK() (*tpm2.CreatePrimaryResponse, error) {
	srkTemplate := tpm2.TPMTPublic{
		Type:    tpm2.TPMAlgECC,
		NameAlg: tpm2.TPMAlgSHA256,
		ObjectAttributes: tpm2.TPMAObject{
			FixedTPM:            true,
			FixedParent:         true,
			SensitiveDataOrigin: true,
			UserWithAuth:        true,
			Restricted:          true,
			Decrypt:             true,
		},
		Parameters: tpm2.NewTPMUPublicParms(
			tpm2.TPMAlgECC,
			&tpm2.TPMSECCParms{
				Symmetric: tpm2.TPMTSymDefObject{
					Algorithm: tpm2.TPMAlgAES,
					KeyBits: tpm2.NewTPMUSymKeyBits(
						tpm2.TPMAlgAES,
						tpm2.TPMKeyBits(128),
					),
					Mode: tpm2.NewTPMUSymMode(
						tpm2.TPMAlgAES,
						tpm2.TPMAlgCFB,
					),
				},
				CurveID: tpm2.TPMECCNistP256,
			},
		),
	}

	resp, err := tpm2.CreatePrimary{
		PrimaryHandle: tpm2.AuthHandle{
			Handle: tpm2.TPMRHOwner,
			Auth:   tpm2.PasswordAuth(nil),
		},
		InPublic: tpm2.New2B(srkTemplate),
	}.Execute(d.transport)
	if err != nil {
		return nil, fmt.Errorf("creating storage root key: %w", err)
	}
	return resp, nil
}
