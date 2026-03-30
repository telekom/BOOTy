// Package secureboot verifies and manages Secure Boot chains.
package secureboot

// ComponentStatus represents the verification status of a Secure Boot component.
type ComponentStatus struct {
	Name     string `json:"name"`
	Signed   bool   `json:"signed"`
	Trusted  bool   `json:"trusted"`
	SignerCN string `json:"signerCN,omitempty"`
	Error    string `json:"error,omitempty"`
}

// ChainResult holds the verification result for a Secure Boot chain.
type ChainResult struct {
	SecureBootEnabled bool              `json:"secureBootEnabled"`
	SetupMode         bool              `json:"setupMode"`
	Components        []ComponentStatus `json:"components"`
	// PreconditionsMet is true when Secure Boot is enabled, setup mode is off,
	// and all expected boot-chain files exist on disk. It does NOT verify
	// cryptographic signatures.
	PreconditionsMet bool `json:"preconditionsMet"`
}
