package cryptenroll

import (
	"fmt"
	"sort"
	"strings"

	tpm "github.com/telekom/BOOTy/pkg/tpm"
)

// Config holds LUKS2 TPM2 enrollment parameters.
type Config struct {
	PCRs       []int  `json:"pcrs"`
	PCRBank    string `json:"pcrBank"`
	KeySlot    int    `json:"keySlot"`
	TPMPath    string `json:"tpmPath"`
	WithPIN    bool   `json:"withPin"`
	LUKSDevice string `json:"luksDevice"`
}

// DefaultConfig returns enrollment defaults.
func DefaultConfig() *Config {
	return &Config{
		PCRs:    []int{tpm.PCRSecureBoot},
		PCRBank: "sha256",
		KeySlot: 2,
		TPMPath: "/dev/tpmrm0",
	}
}

// ApplyDefaults fills zero-valued fields with defaults.
// Note: KeySlot uses -1 as sentinel for "unset" to allow slot 0.
func (c *Config) ApplyDefaults() {
	d := DefaultConfig()
	if len(c.PCRs) == 0 {
		c.PCRs = d.PCRs
	}
	if c.PCRBank == "" {
		c.PCRBank = d.PCRBank
	}
	if c.KeySlot < 0 {
		c.KeySlot = d.KeySlot
	}
	if c.TPMPath == "" {
		c.TPMPath = d.TPMPath
	}
}

// Validate checks the enrollment configuration.
func (c *Config) Validate() error {
	if c.LUKSDevice == "" {
		return fmt.Errorf("missing LUKS device path")
	}
	for _, pcr := range c.PCRs {
		if pcr < 0 || pcr > 23 {
			return fmt.Errorf("invalid PCR index %d: must be 0-23", pcr)
		}
	}
	if c.KeySlot < 0 || c.KeySlot > 31 {
		return fmt.Errorf("invalid key slot %d: must be 0-31", c.KeySlot)
	}
	return nil
}

// PCRPolicy describes a PCR-based policy for enrollment.
type PCRPolicy struct {
	PCRs         []int          `json:"pcrs"`
	Bank         string         `json:"bank"`
	Descriptions map[int]string `json:"descriptions"`
}

// BuildPCRPolicy creates a policy description from the config.
func (c *Config) BuildPCRPolicy() *PCRPolicy {
	desc := make(map[int]string, len(c.PCRs))
	for _, pcr := range c.PCRs {
		desc[pcr] = tpm.PCRDescription(pcr)
	}
	return &PCRPolicy{
		PCRs:         c.PCRs,
		Bank:         c.PCRBank,
		Descriptions: desc,
	}
}

// FormatPCRSelection formats PCR indices for display (e.g. "7+8+9").
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

// EnrollResult holds the outcome of a TPM enrollment operation.
type EnrollResult struct {
	Success bool   `json:"success"`
	KeySlot int    `json:"keySlot"`
	PCRs    []int  `json:"pcrs"`
	TPMPath string `json:"tpmPath"`
	Message string `json:"message,omitempty"`
}
