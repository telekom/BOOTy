//go:build linux

package tpm

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"

	"github.com/google/go-tpm/tpm2"
	"github.com/google/go-tpm/tpm2/transport"
	"github.com/google/go-tpm/tpm2/transport/linuxtpm"
)

// Device wraps a TPM 2.0 resource manager connection.
type Device struct {
	transport transport.TPMCloser
	log       *slog.Logger
}

// Open connects to the TPM 2.0 resource manager device.
func Open() (*Device, error) {
	t, err := linuxtpm.Open(tpmrmDevicePath)
	if err != nil {
		return nil, fmt.Errorf("opening TPM device %s: %w", tpmrmDevicePath, err)
	}
	return &Device{
		transport: t,
		log:       slog.Default().With("component", "tpm"),
	}, nil
}

// Close releases the TPM device handle.
func (d *Device) Close() error {
	if d.transport != nil {
		if err := d.transport.Close(); err != nil {
			return fmt.Errorf("closing TPM transport: %w", err)
		}
	}
	return nil
}

// ExtendPCR extends a PCR register with the SHA-256 hash of the given data.
func (d *Device) ExtendPCR(pcrIndex int, data []byte) error {
	digest := sha256.Sum256(data)

	pcrHandle := tpm2.TPMHandle(uint32(pcrIndex))
	_, err := tpm2.PCRExtend{
		PCRHandle: tpm2.AuthHandle{
			Handle: pcrHandle,
			Auth:   tpm2.PasswordAuth(nil),
		},
		Digests: tpm2.TPMLDigestValues{
			Digests: []tpm2.TPMTHA{
				{
					HashAlg: tpm2.TPMAlgSHA256,
					Digest:  digest[:],
				},
			},
		},
	}.Execute(d.transport)
	if err != nil {
		return fmt.Errorf("extending PCR %d: %w", pcrIndex, err)
	}

	d.log.Info("Extended PCR", "index", pcrIndex, "digest_len", len(digest))
	return nil
}

// ReadPCRDevice reads a single PCR value using the TPM command interface.
func (d *Device) ReadPCRDevice(pcrIndex int) ([]byte, error) {
	sel := tpm2.TPMLPCRSelection{
		PCRSelections: []tpm2.TPMSPCRSelection{
			{
				Hash:      tpm2.TPMAlgSHA256,
				PCRSelect: pcrSelect(pcrIndex),
			},
		},
	}
	resp, err := tpm2.PCRRead{
		PCRSelectionIn: sel,
	}.Execute(d.transport)
	if err != nil {
		return nil, fmt.Errorf("reading PCR %d: %w", pcrIndex, err)
	}

	if len(resp.PCRValues.Digests) == 0 {
		return nil, fmt.Errorf("no digest returned for PCR %d", pcrIndex)
	}
	return resp.PCRValues.Digests[0].Buffer, nil
}

// MeasureReader extends the given PCR with the SHA-256 hash of all data
// read from r. Useful for measuring files or image streams inline.
func (d *Device) MeasureReader(pcrIndex int, r io.Reader) error {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return fmt.Errorf("hashing data for PCR %d: %w", pcrIndex, err)
	}
	digest := h.Sum(nil)

	pcrHandle := tpm2.TPMHandle(uint32(pcrIndex))
	_, err := tpm2.PCRExtend{
		PCRHandle: tpm2.AuthHandle{
			Handle: pcrHandle,
			Auth:   tpm2.PasswordAuth(nil),
		},
		Digests: tpm2.TPMLDigestValues{
			Digests: []tpm2.TPMTHA{
				{
					HashAlg: tpm2.TPMAlgSHA256,
					Digest:  digest,
				},
			},
		},
	}.Execute(d.transport)
	if err != nil {
		return fmt.Errorf("extending PCR %d with stream digest: %w", pcrIndex, err)
	}
	return nil
}

// pcrSelect builds a PCR selection bitmap for a single PCR index.
func pcrSelect(index int) []byte {
	mask := make([]byte, (index/8)+1)
	mask[index/8] |= 1 << (index % 8)
	return mask
}
