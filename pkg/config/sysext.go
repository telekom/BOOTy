package config

// SysextConfig controls optional systemd-sysext handling during provisioning.
//
// The default mode is "preload": selected sysext images are copied into the
// provisioned OS under CatalogDir and recorded in catalog.json, but they are not
// placed in an active systemd-sysext search path.
type SysextConfig struct {
	// Enabled turns on sysext handling during provisioning.
	// Default: false
	Enabled bool `yaml:"enabled" json:"enabled"`

	// DefaultMode is used for layers whose mode is empty.
	// Valid values: "preload" (default), "active"
	DefaultMode string `yaml:"defaultMode" json:"defaultMode"`

	// CatalogDir is the target directory inside the provisioned root for
	// preloaded sysext images and catalog.json.
	// Default: /usr/lib/tcaas-sysext/preloaded
	CatalogDir string `yaml:"catalogDir" json:"catalogDir"`

	// ActiveDir is the target directory inside the provisioned root for active
	// sysext images.
	// Default: /var/lib/extensions
	ActiveDir string `yaml:"activeDir" json:"activeDir"`

	// Layers lists sysext images to copy into the provisioned OS.
	Layers []SysextLayerConfig `yaml:"layers" json:"layers"`
}

// SysextLayerConfig identifies one sysext artifact to load into the provisioned
// OS image.
type SysextLayerConfig struct {
	// Name is the logical sysext layer name stored in catalog.json.
	Name string `yaml:"name" json:"name"`

	// Version is the optional layer version stored in catalog.json.
	// Default: "unknown"
	Version string `yaml:"version" json:"version"`

	// Source is a local path, HTTPS URL, or oci:// registry reference for a
	// sysext .raw image. Plain HTTP sources are rejected.
	Source string `yaml:"source" json:"source"`

	// FileName overrides the target file name. Defaults to basename(Source), or
	// name + ".raw" when Source is an OCI reference or a URL without a usable
	// basename.
	FileName string `yaml:"fileName" json:"fileName"`

	// SHA256 is the expected SHA256 digest for the source content. Both bare hex
	// and sha256:<hex> forms are accepted. It is required unless Source is an
	// OCI digest reference.
	SHA256 string `yaml:"sha256" json:"sha256"`

	// Mode overrides SysextConfig.DefaultMode for this layer.
	// Valid values: "preload", "active".
	Mode string `yaml:"mode" json:"mode"`
}
