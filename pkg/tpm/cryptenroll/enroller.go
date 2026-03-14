// Package cryptenroll binds LUKS2 key slots to TPM2 PCR policies.
package cryptenroll

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"
)

// Config specifies the TPM2 enrollment parameters.
type Config struct {
	PCRs       []int  `json:"pcrs"`              // PCR indices to bind (default: [7]).
	PCRBank    string `json:"pcrBank,omitempty"` // PCR hash algorithm (default: "sha256").
	KeySlot    int    `json:"keySlot,omitempty"` // LUKS key slot (default: 1).
	TPMPath    string `json:"tpmPath,omitempty"` // TPM device path (default: "/dev/tpmrm0").
	WithPIN    bool   `json:"withPin,omitempty"` // Require PIN in addition to TPM2.
	LUKSDevice string `json:"luksDevice"`        // LUKS device path.
}

// ApplyDefaults fills in default values.
func (c *Config) ApplyDefaults() {
	if len(c.PCRs) == 0 {
		c.PCRs = []int{7}
	}
	if c.PCRBank == "" {
		c.PCRBank = "sha256"
	}
	if c.KeySlot == 0 {
		c.KeySlot = 1
	}
	if c.TPMPath == "" {
		c.TPMPath = "/dev/tpmrm0"
	}
}

// Validate checks Config for correctness.
func (c *Config) Validate() error {
	if c.LUKSDevice == "" {
		return fmt.Errorf("LUKS device path is required")
	}
	for _, pcr := range c.PCRs {
		if pcr < 0 || pcr > 23 {
			return fmt.Errorf("invalid PCR index %d, must be 0-23", pcr)
		}
	}
	switch c.PCRBank {
	case "sha256", "sha1", "sha384", "sha512":
		// Valid.
	default:
		return fmt.Errorf("unsupported PCR bank %q", c.PCRBank)
	}
	if c.KeySlot < 0 || c.KeySlot > 31 {
		return fmt.Errorf("invalid key slot %d, must be 0-31", c.KeySlot)
	}
	return nil
}

// PCRPolicy represents a PCR policy for TPM2 sealing.
type PCRPolicy struct {
	PCRs       []int          `json:"pcrs"`
	Bank       string         `json:"bank"`
	Digests    map[int]string `json:"digests"`
	PolicyHash string         `json:"policyHash"`
}

// EnrollResult holds the result of a TPM2 enrollment operation.
type EnrollResult struct {
	KeySlot     int       `json:"keySlot"`
	PCRPolicy   PCRPolicy `json:"pcrPolicy"`
	TPMPath     string    `json:"tpmPath"`
	Recoverable bool      `json:"recoverable"`
}

// Enroller seals LUKS keys to TPM2 PCR policies.
type Enroller struct {
	log *slog.Logger
	cfg Config
}

// New creates a new TPM2 enroller.
func New(log *slog.Logger, cfg *Config) *Enroller {
	cfg.ApplyDefaults()
	return &Enroller{log: log, cfg: *cfg}
}

// BuildPCRPolicy creates a PCR policy from provided digest values.
func BuildPCRPolicy(pcrs []int, bank string, digests map[int]string) (*PCRPolicy, error) {
	if len(pcrs) == 0 {
		return nil, fmt.Errorf("at least one PCR index required")
	}

	sorted := make([]int, len(pcrs))
	copy(sorted, pcrs)
	sort.Ints(sorted)

	for _, pcr := range sorted {
		if _, ok := digests[pcr]; !ok {
			return nil, fmt.Errorf("missing digest for PCR %d", pcr)
		}
	}

	// Compute composite policy hash.
	h := sha256.New()
	for _, pcr := range sorted {
		d, err := hex.DecodeString(digests[pcr])
		if err != nil {
			return nil, fmt.Errorf("decode PCR %d digest: %w", pcr, err)
		}
		h.Write(d)
	}

	return &PCRPolicy{
		PCRs:       sorted,
		Bank:       bank,
		Digests:    digests,
		PolicyHash: hex.EncodeToString(h.Sum(nil)),
	}, nil
}

// FormatPCRSelection formats PCR indices for display.
func FormatPCRSelection(pcrs []int) string {
	sorted := make([]int, len(pcrs))
	copy(sorted, pcrs)
	sort.Ints(sorted)
	parts := make([]string, len(sorted))
	for i, p := range sorted {
		parts[i] = fmt.Sprintf("%d", p)
	}
	return strings.Join(parts, "+")
}

// PCRDescription returns a human-readable description of a PCR index.
func PCRDescription(pcr int) string {
	descriptions := map[int]string{
		0:  "SRTM (platform firmware)",
		1:  "platform configuration",
		2:  "option ROM code",
		3:  "option ROM data",
		4:  "MBR/boot manager",
		5:  "MBR/boot partition table",
		6:  "platform state transitions",
		7:  "SecureBoot state",
		8:  "kernel command line",
		9:  "kernel image and initramfs",
		10: "reserved for IMA",
		11: "unified kernel image",
		14: "MOK/provisioner identity",
	}
	if d, ok := descriptions[pcr]; ok {
		return d
	}
	return "reserved"
}
