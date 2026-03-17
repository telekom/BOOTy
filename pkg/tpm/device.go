//go:build linux

package tpm

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/google/go-tpm/tpm2"
	"github.com/google/go-tpm/tpm2/transport"
)

// Device wraps a TPM2 device handle for hardware operations.
type Device struct {
	tpm transport.TPMCloser
}

// Open opens the TPM device, preferring the resource-managed path.
func Open() (*Device, error) {
	for _, path := range []string{devTPMRM, devTPM} {
		tpm, err := transport.OpenTPM(path)
		if err == nil {
			return &Device{tpm: tpm}, nil
		}
	}
	return nil, errors.New("no TPM device available")
}

// Close releases the TPM device.
func (d *Device) Close() error {
	if d.tpm != nil {
		return d.tpm.Close()
	}
	return nil
}

// ExtendPCR extends a hardware PCR with SHA-256 data.
func (d *Device) ExtendPCR(pcrIndex int, data []byte) error {
	hash := sha256.Sum256(data)
	pcrHandle := tpm2.TPMHandle(pcrIndex)
	digest := tpm2.TPM2BDigest{Buffer: hash[:]}

	cmd := tpm2.PCRExtend{
		PCRHandle: tpm2.AuthHandle{
			Handle: pcrHandle,
			Auth:   tpm2.PasswordAuth(nil),
		},
		Digests: tpm2.TPMLDigestValues{
			Digests: []tpm2.TPMTHA{
				{HashAlg: tpm2.TPMAlgSHA256, Digest: digest.Buffer},
			},
		},
	}
	_, err := cmd.Execute(d.tpm)
	return err
}

// ReadPCR reads the current value of a hardware PCR (SHA-256).
func (d *Device) ReadPCR(pcrIndex int) ([]byte, error) {
	sel := tpm2.TPMLPCRSelection{
		PCRSelections: []tpm2.TPMSPCRSelection{
			{
				Hash:      tpm2.TPMAlgSHA256,
				PCRSelect: pcrSelectSingle(pcrIndex),
			},
		},
	}
	cmd := tpm2.PCRRead{PCRSelectionIn: sel}
	rsp, err := cmd.Execute(d.tpm)
	if err != nil {
		return nil, fmt.Errorf("PCR read failed: %w", err)
	}
	if len(rsp.PCRValues.Digests) == 0 {
		return nil, fmt.Errorf("no PCR digest returned for index %d", pcrIndex)
	}
	return rsp.PCRValues.Digests[0].Buffer, nil
}

// MeasureReader extends a PCR with the SHA-256 hash of an io.Reader.
func (d *Device) MeasureReader(pcrIndex int, r io.Reader) ([]byte, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return nil, fmt.Errorf("hashing reader: %w", err)
	}
	digest := h.Sum(nil)
	if err := d.ExtendPCR(pcrIndex, digest); err != nil {
		return nil, fmt.Errorf("extending PCR %d: %w", pcrIndex, err)
	}
	return digest, nil
}

// MeasureFile extends a PCR with the SHA-256 hash of a file.
func (d *Device) MeasureFile(pcrIndex int, path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // intentional file read
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()
	return d.MeasureReader(pcrIndex, f)
}

func pcrSelectSingle(index int) []byte {
	mask := make([]byte, (index/8)+1)
	mask[index/8] |= 1 << (index % 8)
	return mask
}
