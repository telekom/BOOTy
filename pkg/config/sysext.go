package config

const (
	// DefaultSysextCatalogDir is the inactive target directory for preloaded sysext images and catalog.json.
	DefaultSysextCatalogDir = "/usr/lib/tcaas-sysext/preloaded"

	// DefaultSysextActiveDir is the target directory for active sysext images.
	DefaultSysextActiveDir = "/var/lib/extensions"
)

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

	// CatalogDir is the inactive target directory inside the provisioned root
	// for preloaded sysext images and catalog.json. It must not be an active
	// systemd-sysext search path.
	// Default: DefaultSysextCatalogDir
	CatalogDir string `yaml:"catalogDir" json:"catalogDir"`

	// ActiveDir is the target directory inside the provisioned root for active
	// sysext images.
	// Default: DefaultSysextActiveDir
	ActiveDir string `yaml:"activeDir" json:"activeDir"`

	// AllowInsecureHTTP permits plain HTTP sysext sources. This is intended for
	// controlled provisioning networks and tests; production sources should use
	// HTTPS, OCI, or local provisioner files.
	// Default: false
	AllowInsecureHTTP bool `yaml:"allowInsecureHTTP" json:"allowInsecureHTTP"`

	// Layers lists sysext images to copy into the provisioned OS.
	Layers []SysextLayerConfig `yaml:"layers" json:"layers"`

	// CosignPublicKeys lists PEM public key files. When set, every oci://
	// layer source must be pinned by digest and carry a cosign signature by
	// one of the keys; BOOTy checks them before the disk is wiped. Other
	// sources keep SHA256.
	// Default: none (no cosign verification)
	CosignPublicKeys []string `yaml:"cosignPublicKeys,omitempty" json:"cosignPublicKeys,omitempty"`

	// CosignTrustedRoot is the path of a Sigstore trusted_root.json in the
	// ramdisk (never fetched). With CosignIdentityRegexp and
	// CosignOIDCIssuer it enables keyless verification: every oci:// layer source, pinned by digest, must carry a
	// keyless cosign signature whose Fulcio certificate, SCT and Rekor entry
	// verify against it. With CosignPublicKeys too, either kind of
	// signature is accepted.
	// Default: "" (no keyless verification)
	CosignTrustedRoot string `yaml:"cosignTrustedRoot,omitempty" json:"cosignTrustedRoot,omitempty"`

	// CosignIdentityRegexp must match the whole SAN of the signing
	// certificate (Go regexp, implicitly anchored).
	CosignIdentityRegexp string `yaml:"cosignIdentityRegexp,omitempty" json:"cosignIdentityRegexp,omitempty"`

	// CosignOIDCIssuer must equal the OIDC issuer extension of the signing
	// certificate.
	CosignOIDCIssuer string `yaml:"cosignOIDCIssuer,omitempty" json:"cosignOIDCIssuer,omitempty"`
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
	// sysext .raw image. Plain HTTP sources require allowInsecureHTTP=true.
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
