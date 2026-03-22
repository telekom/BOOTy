//go:build linux

package tpm

import (
	"fmt"

	"github.com/google/go-tpm/tpm2"
)

// SealedBlob holds a TPM-sealed secret and metadata.
type SealedBlob struct {
	Public  []byte `json:"public"`
	Private []byte `json:"private"`
	PCRs    []int  `json:"pcrs"`
}

// SealSecret seals data under the TPM with a PCR policy.
func (d *Device) SealSecret(data []byte, pcrs []int) (*SealedBlob, error) {
	srk, err := d.createSRK()
	if err != nil {
		return nil, fmt.Errorf("creating SRK: %w", err)
	}
	defer d.flushContext(srk)

	// Derive the policy digest via a trial session so the sealed object
	// can later be unsealed only when the same PCR values are present.
	policyDigest, err := d.policyDigestForPCRs(pcrs)
	if err != nil {
		return nil, fmt.Errorf("computing pcr policy digest: %w", err)
	}

	sensitive := tpm2.TPM2BSensitiveCreate{
		Sensitive: &tpm2.TPMSSensitiveCreate{
			Data: tpm2.NewTPMUSensitiveCreate(
				&tpm2.TPM2BSensitiveData{Buffer: data},
			),
		},
	}

	createCmd := tpm2.Create{
		ParentHandle: tpm2.AuthHandle{
			Handle: srk,
			Auth:   tpm2.PasswordAuth(nil),
		},
		InSensitive: sensitive,
		InPublic: tpm2.New2B(tpm2.TPMTPublic{
			Type:    tpm2.TPMAlgKeyedHash,
			NameAlg: tpm2.TPMAlgSHA256,
			ObjectAttributes: tpm2.TPMAObject{
				FixedTPM:     true,
				FixedParent:  true,
				UserWithAuth: true,
			},
			AuthPolicy: policyDigest,
		}),
	}

	rsp, err := createCmd.Execute(d.tpm)
	if err != nil {
		return nil, fmt.Errorf("sealing: %w", err)
	}
	return &SealedBlob{
		Public:  rsp.OutPublic.Bytes(),
		Private: rsp.OutPrivate.Buffer,
		PCRs:    pcrs,
	}, nil
}

// UnsealSecret unseals a previously sealed blob.
func (d *Device) UnsealSecret(blob *SealedBlob) ([]byte, error) {
	srk, err := d.createSRK()
	if err != nil {
		return nil, fmt.Errorf("creating SRK: %w", err)
	}
	defer d.flushContext(srk)

	loadCmd := tpm2.Load{
		ParentHandle: tpm2.AuthHandle{
			Handle: srk,
			Auth:   tpm2.PasswordAuth(nil),
		},
		InPublic:  tpm2.BytesAs2B[tpm2.TPMTPublic](blob.Public),
		InPrivate: tpm2.TPM2BPrivate{Buffer: blob.Private},
	}
	loadRsp, err := loadCmd.Execute(d.tpm)
	if err != nil {
		return nil, fmt.Errorf("loading sealed object: %w", err)
	}
	defer d.flushContext(loadRsp.ObjectHandle)

	session, cleanup, err := d.createPCRPolicySession(blob.PCRs)
	if err != nil {
		return nil, fmt.Errorf("creating policy session: %w", err)
	}
	defer cleanup() //nolint:errcheck // best-effort session cleanup

	unsealCmd := tpm2.Unseal{
		ItemHandle: tpm2.AuthHandle{
			Handle: loadRsp.ObjectHandle,
			Auth:   session,
		},
	}
	unsealRsp, err := unsealCmd.Execute(d.tpm)
	if err != nil {
		return nil, fmt.Errorf("unsealing: %w", err)
	}
	return unsealRsp.OutData.Buffer, nil
}

func (d *Device) createSRK() (tpm2.TPMHandle, error) {
	primary := tpm2.CreatePrimary{
		PrimaryHandle: tpm2.AuthHandle{
			Handle: tpm2.TPMRHOwner,
			Auth:   tpm2.PasswordAuth(nil),
		},
		InPublic: tpm2.New2B(tpm2.TPMTPublic{
			Type:    tpm2.TPMAlgRSA,
			NameAlg: tpm2.TPMAlgSHA256,
			ObjectAttributes: tpm2.TPMAObject{
				FixedTPM:            true,
				FixedParent:         true,
				SensitiveDataOrigin: true,
				UserWithAuth:        true,
				Restricted:          true,
				Decrypt:             true,
			},
			Parameters: tpm2.NewTPMUPublicParms(tpm2.TPMAlgRSA,
				&tpm2.TPMSRSAParms{
					Symmetric: tpm2.TPMTSymDefObject{
						Algorithm: tpm2.TPMAlgAES,
						KeyBits:   tpm2.NewTPMUSymKeyBits(tpm2.TPMAlgAES, tpm2.TPMKeyBits(128)),
						Mode:      tpm2.NewTPMUSymMode(tpm2.TPMAlgAES, tpm2.TPMAlgCFB),
					},
					KeyBits: 2048,
				}),
		}),
	}
	rsp, err := primary.Execute(d.tpm)
	if err != nil {
		return 0, fmt.Errorf("creating SRK: %w", err)
	}
	return rsp.ObjectHandle, nil
}

func (d *Device) createPCRPolicySession(pcrs []int) (tpm2.Session, func() error, error) {
	sel, err := buildPCRSelection(pcrs)
	if err != nil {
		return nil, nil, err
	}
	sess, cleanup, err := tpm2.PolicySession(d.tpm, tpm2.TPMAlgSHA256, 16)
	if err != nil {
		return nil, nil, fmt.Errorf("starting policy session: %w", err)
	}
	policyPCR := tpm2.PolicyPCR{
		PolicySession: sess.Handle(),
		Pcrs:          sel,
	}
	if _, err := policyPCR.Execute(d.tpm); err != nil {
		cleanup() //nolint:errcheck,gosec // best-effort cleanup
		return nil, nil, fmt.Errorf("policy pcr: %w", err)
	}
	return sess, cleanup, nil
}

// policyDigestForPCRs derives the PCR policy digest using a trial session.
// The resulting digest is used as AuthPolicy when sealing objects.
func (d *Device) policyDigestForPCRs(pcrs []int) (tpm2.TPM2BDigest, error) {
	sel, err := buildPCRSelection(pcrs)
	if err != nil {
		return tpm2.TPM2BDigest{}, err
	}
	sess, cleanup, err := tpm2.PolicySession(d.tpm, tpm2.TPMAlgSHA256, 16, tpm2.Trial())
	if err != nil {
		return tpm2.TPM2BDigest{}, fmt.Errorf("starting trial session: %w", err)
	}
	defer cleanup() //nolint:errcheck // best-effort cleanup

	policyPCR := tpm2.PolicyPCR{
		PolicySession: sess.Handle(),
		Pcrs:          sel,
	}
	if _, err := policyPCR.Execute(d.tpm); err != nil {
		return tpm2.TPM2BDigest{}, fmt.Errorf("trial policy pcr: %w", err)
	}

	pgd := tpm2.PolicyGetDigest{PolicySession: sess.Handle()}
	rsp, err := pgd.Execute(d.tpm)
	if err != nil {
		return tpm2.TPM2BDigest{}, fmt.Errorf("getting policy digest: %w", err)
	}
	return rsp.PolicyDigest, nil
}
