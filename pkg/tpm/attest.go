//go:build linux

package tpm

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/google/go-tpm/tpm2"
)

// AttestationQuote holds a TPM2 quote and associated metadata.
type AttestationQuote struct {
	QuoteData []byte         `json:"quoteData"`
	Signature []byte         `json:"signature"`
	PCRDigest []byte         `json:"pcrDigest"`
	PCRValues map[int][]byte `json:"pcrValues"`
	Nonce     []byte         `json:"nonce"`
	PubKeyX   []byte         `json:"pubKeyX"`
	PubKeyY   []byte         `json:"pubKeyY"`
}

// Quote generates a TPM2 attestation quote over the specified PCRs.
func (d *Device) Quote(pcrIndices []int, nonce []byte) (*AttestationQuote, error) {
	if len(pcrIndices) == 0 {
		return nil, fmt.Errorf("no PCR indices specified")
	}

	ak, akX, akY, err := d.createAK()
	if err != nil {
		return nil, fmt.Errorf("creating attestation key: %w", err)
	}
	defer d.flushContext(ak)

	sel, err := buildPCRSelection(pcrIndices)
	if err != nil {
		return nil, err
	}
	qualifying := tpm2.TPM2BData{Buffer: nonce}

	quoteCmd := tpm2.Quote{
		SignHandle: tpm2.AuthHandle{
			Handle: ak,
			Auth:   tpm2.PasswordAuth(nil),
		},
		QualifyingData: qualifying,
		InScheme: tpm2.TPMTSigScheme{
			Scheme: tpm2.TPMAlgECDSA,
			Details: tpm2.NewTPMUSigScheme(tpm2.TPMAlgECDSA,
				&tpm2.TPMSSchemeHash{HashAlg: tpm2.TPMAlgSHA256}),
		},
		PCRSelect: sel,
	}
	quoteRsp, err := quoteCmd.Execute(d.tpm)
	if err != nil {
		return nil, fmt.Errorf("TPM2_Quote: %w", err)
	}

	// Read PCR values for the quoted selection.
	pcrValues := make(map[int][]byte, len(pcrIndices))
	for _, idx := range pcrIndices {
		val, err := d.ReadPCR(idx)
		if err == nil {
			pcrValues[idx] = val
		}
	}

	return &AttestationQuote{
		QuoteData: quoteRsp.Quoted.Bytes(),
		Signature: marshalECDSASig(quoteRsp.Signature),
		PCRDigest: compositePCRDigest(pcrValues, pcrIndices),
		PCRValues: pcrValues,
		Nonce:     nonce,
		PubKeyX:   akX,
		PubKeyY:   akY,
	}, nil
}

// VerifyQuoteSignature verifies an attestation quote ECDSA signature.
func VerifyQuoteSignature(quote *AttestationQuote) (bool, error) {
	if quote == nil {
		return false, fmt.Errorf("nil quote")
	}
	pubKey := ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     new(big.Int).SetBytes(quote.PubKeyX),
		Y:     new(big.Int).SetBytes(quote.PubKeyY),
	}
	hash := sha256.Sum256(quote.QuoteData)
	valid := ecdsa.VerifyASN1(&pubKey, hash[:], quote.Signature)
	return valid, nil
}

// MarshalQuote serializes a quote to JSON.
func MarshalQuote(quote *AttestationQuote) ([]byte, error) {
	data, err := json.Marshal(quote)
	if err != nil {
		return nil, fmt.Errorf("marshaling quote: %w", err)
	}
	return data, nil
}

func (d *Device) createAK() (tpm2.TPMHandle, []byte, []byte, error) {
	primary := tpm2.CreatePrimary{
		PrimaryHandle: tpm2.AuthHandle{
			Handle: tpm2.TPMRHEndorsement,
			Auth:   tpm2.PasswordAuth(nil),
		},
		InPublic: tpm2.New2B(tpm2.TPMTPublic{
			Type:    tpm2.TPMAlgECC,
			NameAlg: tpm2.TPMAlgSHA256,
			ObjectAttributes: tpm2.TPMAObject{
				FixedTPM:            true,
				FixedParent:         true,
				SensitiveDataOrigin: true,
				UserWithAuth:        true,
				SignEncrypt:         true,
			},
			Parameters: tpm2.NewTPMUPublicParms(tpm2.TPMAlgECC,
				&tpm2.TPMSECCParms{
					Scheme: tpm2.TPMTECCScheme{
						Scheme: tpm2.TPMAlgECDSA,
						Details: tpm2.NewTPMUAsymScheme(tpm2.TPMAlgECDSA,
							&tpm2.TPMSSigSchemeECDSA{HashAlg: tpm2.TPMAlgSHA256}),
					},
					CurveID: tpm2.TPMECCNistP256,
				}),
		}),
	}
	rsp, err := primary.Execute(d.tpm)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("creating AK primary: %w", err)
	}

	pub, err := rsp.OutPublic.Contents()
	if err != nil {
		d.flushContext(rsp.ObjectHandle)
		return 0, nil, nil, fmt.Errorf("reading AK public: %w", err)
	}
	ecc, err := pub.Unique.ECC()
	if err != nil {
		d.flushContext(rsp.ObjectHandle)
		return 0, nil, nil, fmt.Errorf("reading ECC params: %w", err)
	}

	return rsp.ObjectHandle, ecc.X.Buffer, ecc.Y.Buffer, nil
}

func (d *Device) flushContext(handle tpm2.TPMHandle) {
	cmd := tpm2.FlushContext{FlushHandle: handle}
	_, _ = cmd.Execute(d.tpm)
}

func buildPCRSelection(indices []int) (tpm2.TPMLPCRSelection, error) {
	sel := make([]byte, 3)
	for _, idx := range indices {
		if idx < 0 || idx > 23 {
			return tpm2.TPMLPCRSelection{}, fmt.Errorf("invalid PCR index %d: must be 0-23", idx)
		}
		sel[idx/8] |= 1 << (idx % 8)
	}
	return tpm2.TPMLPCRSelection{
		PCRSelections: []tpm2.TPMSPCRSelection{
			{Hash: tpm2.TPMAlgSHA256, PCRSelect: sel},
		},
	}, nil
}

func marshalECDSASig(sig tpm2.TPMTSignature) []byte {
	ecdsaSig, err := sig.Signature.ECDSA()
	if err != nil {
		return nil
	}
	r := new(big.Int).SetBytes(ecdsaSig.SignatureR.Buffer)
	s := new(big.Int).SetBytes(ecdsaSig.SignatureS.Buffer)
	// Encode as ASN.1 DER to match ecdsa.VerifyASN1 expectation.
	der, err := asn1.Marshal(struct{ R, S *big.Int }{r, s})
	if err != nil {
		return nil
	}
	return der
}

func compositePCRDigest(vals map[int][]byte, indices []int) []byte {
	h := sha256.New()
	for _, idx := range indices {
		if v, ok := vals[idx]; ok {
			h.Write(v)
		}
	}
	return h.Sum(nil)
}
