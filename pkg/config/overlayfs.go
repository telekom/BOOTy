package config

const (
	// OverlayFSModeTmpfs stores overlay writes in RAM through Ubuntu overlayroot.
	OverlayFSModeTmpfs = "tmpfs"

	// OverlayFSModeDevice stores overlay writes on an operator-provided backing
	// filesystem through Ubuntu overlayroot.
	OverlayFSModeDevice = "device"
)

// OverlayFSConfig controls target-root overlayFS/overlayroot configuration for
// immutable Ubuntu images.
type OverlayFSConfig struct {
	// Enabled writes overlayroot configuration into the provisioned Ubuntu root.
	// Default: false
	Enabled bool `yaml:"enabled" json:"enabled"`

	// Mode selects the overlayroot backing mode.
	// Valid values: "tmpfs" (default), "device".
	Mode string `yaml:"mode" json:"mode"`

	// Device is the overlayroot backing filesystem for mode "device".
	// Examples: "/dev/disk/by-label/BOOTY-OVERLAY", "LABEL=BOOTY-OVERLAY".
	// Default: ""
	Device string `yaml:"device" json:"device"`

	// Directory is the overlayroot dir= value under the backing filesystem.
	// Leave empty to use overlayroot's package default.
	// Default: ""
	Directory string `yaml:"directory" json:"directory"`

	// UpperDir is an optional target-root directory BOOTy pre-creates for
	// custom overlayFS initramfs hooks. It is not used by Ubuntu overlayroot.
	// Default: ""
	UpperDir string `yaml:"upperDir" json:"upperDir"`

	// WorkDir is an optional target-root directory BOOTy pre-creates for
	// custom overlayFS initramfs hooks. It must be set together with UpperDir.
	// Default: ""
	WorkDir string `yaml:"workDir" json:"workDir"`

	// Recurse maps to overlayroot recurse=1. BOOTy defaults to recurse=0 so
	// separately mounted data partitions remain writable unless explicitly set.
	// Default: false
	Recurse bool `yaml:"recurse" json:"recurse"`

	// Swap maps to overlayroot swap=1.
	// Default: false
	Swap bool `yaml:"swap" json:"swap"`

	// Debug maps to overlayroot debug=1.
	// Default: false
	Debug bool `yaml:"debug" json:"debug"`

	// TimeoutSec is used with mode "device" while waiting for Device to appear.
	// Default: 0
	TimeoutSec int `yaml:"timeoutSec" json:"timeoutSec"`
}
