package luks

// UnlockMethod specifies how LUKS volumes auto-unlock on boot.
type UnlockMethod string

const (
	// UnlockPassphrase requires manual passphrase entry at boot.
	UnlockPassphrase UnlockMethod = "passphrase"
	// UnlockTPM2 binds the key to TPM2 PCR values.
	UnlockTPM2 UnlockMethod = "tpm2"
	// UnlockClevis uses network-bound decryption via tang server.
	UnlockClevis UnlockMethod = "clevis"
	// UnlockKeyFile uses a key file embedded in the initramfs.
	UnlockKeyFile UnlockMethod = "keyfile"
)

// Config holds LUKS encryption configuration.
type Config struct {
	Enabled      bool         `json:"enabled"`
	Partitions   []Target     `json:"partitions"`
	UnlockMethod UnlockMethod `json:"unlockMethod"`
	Passphrase   string       `json:"passphrase,omitempty"`
	TangURL      string       `json:"tangUrl,omitempty"` // Phase 2: tang server URL for clevis enrollment
	TPMPCRs      []int        `json:"tpmPcrs,omitempty"` // Phase 2: PCR values for TPM2 enrollment
	Cipher       string       `json:"cipher,omitempty"`
	KeySize      int          `json:"keySize,omitempty"`
	Hash         string       `json:"hash,omitempty"`
}

// Target identifies a partition to encrypt.
type Target struct {
	Device     string `json:"device"`
	MappedName string `json:"mappedName"`
}

// MappedPath returns the /dev/mapper path for a mapped LUKS volume.
func MappedPath(mappedName string) string {
	return "/dev/mapper/" + mappedName
}
