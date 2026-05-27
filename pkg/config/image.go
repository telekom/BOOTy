package config

// ImageConfig defines the OS image to stream onto the target disk.
// Supports multiple image formats (raw, OCI) and integrity verification
// via checksums and GPG signatures.
type ImageConfig struct {
	// URLs is the list of image source URLs. Multiple URLs enable fallback.
	// Supports HTTP(S) URLs and OCI registry references.
	// Default: [] (required for provisioning)
	URLs []string `yaml:"urls" json:"urls"`

	// Checksum is the expected hex digest of the raw (decompressed) image.
	// Verified after streaming completes.
	// Default: "" (no verification)
	Checksum string `yaml:"checksum" json:"checksum"`

	// ChecksumType is the hash algorithm for Checksum.
	// Valid values: "sha256", "sha512"
	// Default: "" (inferred from checksum length if possible)
	ChecksumType string `yaml:"checksumType" json:"checksumType"`

	// Mode controls how the image is written to disk.
	// Valid values: "whole-disk" (default), "partition"
	//   - whole-disk: dd-style write of the entire image to the block device
	//   - partition: per-partition extraction (streams each partition independently;
	//     does NOT use Provision.Disk.PartitionLayout, which is not yet supported)
	// Default: "whole-disk"
	Mode string `yaml:"mode" json:"mode"`

	// SignatureURL is the URL to a detached GPG signature for image verification.
	// Default: ""
	SignatureURL string `yaml:"signatureURL" json:"signatureURL"`

	// GPGPubKey is the path to the GPG public key for signature verification.
	// Default: ""
	GPGPubKey string `yaml:"gpgPubKey" json:"gpgPubKey"`
}
