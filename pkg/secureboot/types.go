package secureboot

// ChainResult holds the verification outcome for each boot component.
type ChainResult struct {
	Shim   ComponentStatus `json:"shim"`
	GRUB   ComponentStatus `json:"grub"`
	Kernel ComponentStatus `json:"kernel"`
	Valid  bool            `json:"valid"`
}

// ComponentStatus describes the signing status of a boot component.
type ComponentStatus struct {
	Path     string `json:"path"`
	Signed   bool   `json:"signed"`
	SignedBy string `json:"signedBy,omitempty"`
	Valid    bool   `json:"valid"`
	Error    string `json:"error,omitempty"`
}
