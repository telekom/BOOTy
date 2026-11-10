package config

// Values of ImageConfig.ChecksumScope.
const (
	ChecksumScopeDownload     = "download"
	ChecksumScopeDecompressed = "decompressed"
)

// MaxImageParallelRanges bounds ImageConfig.ParallelRanges: each range holds
// an 8 MiB chunk in RAM.
const MaxImageParallelRanges = 16

// Values of ImageConfig.SparseWrite.
const (
	SparseWriteOff  = "off"
	SparseWriteAuto = "auto"
)

// ImageConfig defines the OS image to stream onto the target disk.
// Supports multiple image formats (raw, OCI) and integrity verification
// via checksums and GPG signatures.
type ImageConfig struct {
	// URLs is the list of image source URLs. Multiple URLs enable fallback.
	// Supports HTTP(S) URLs and OCI registry references.
	// Default: [] (required for provisioning)
	URLs []string `yaml:"urls" json:"urls"`

	// AllowInsecureHTTP permits plain HTTP image sources. This is intended for
	// local testing/airgapped environments without TLS infrastructure.
	// Preset by legacyCompat: true.
	// Default: nil (false, requires HTTPS or OCI)
	AllowInsecureHTTP *bool `yaml:"allowInsecureHTTP,omitempty" json:"allowInsecureHTTP,omitempty"`

	// Checksum is the expected hex digest of the image. ChecksumScope
	// selects the bytes it covers. Verified after streaming completes.
	// Default: "" (no verification)
	Checksum string `yaml:"checksum" json:"checksum"`

	// ChecksumType is the hash algorithm for Checksum.
	// Valid values: "sha256", "sha512"
	// Default: "" (inferred from checksum length if possible)
	ChecksumType string `yaml:"checksumType" json:"checksumType"`

	// ChecksumScope selects the bytes that Checksum covers.
	// "download": the file as downloaded, before decompression or qcow2
	// conversion, like the checksum files published next to the images
	// (image.raw.lz4.sha256) and like the CAPRF legacy provisioner.
	// "decompressed": the decompressed image; for qcow2 the converted raw
	// image.
	// Valid values: "download", "decompressed"
	// Default: "" (download)
	ChecksumScope string `yaml:"checksumScope" json:"checksumScope"`

	// Stream forbids staging the image in RAM (tmpfs). BOOTy then writes
	// every image as one pipeline (download, decompress, write) and fails
	// for the formats that need staging: a qcow2 image fails before the
	// disk wipe, and Mode "partition" fails config validation. Raw and
	// compressed raw images stream in every mode anyway, so their
	// behavior does not change.
	// Default: false (qcow2 and Mode "partition" stage in tmpfs)
	Stream bool `yaml:"stream" json:"stream"`

	// SparseWrite selects how whole-disk streaming writes all-zero blocks
	// of the image. "off" writes them. "auto" zeroes each run of zero
	// blocks with a hole punch (WRITE ZEROES or deallocate) that the device
	// must guarantee to read back as zeros, and writes the zeros when the
	// device cannot.
	// Valid values: "off", "auto"
	// Default: "" (off)
	SparseWrite string `yaml:"sparseWrite,omitempty" json:"sparseWrite,omitempty"`

	// SkipUnchanged makes whole-disk streaming read each block of the
	// target first and skip the write when the target already holds it.
	// Reinstalling the same image then writes only the blocks that differ.
	// The download and the checksum still cover the whole image.
	// Needs Disk.WipeDepth "quick" without SecureErase and DiscardNVMe, and
	// a disk that reads deallocated blocks deterministically (a block the old
	// OS trimmed may compare equal now and read back differently later).
	// Default: false
	SkipUnchanged bool `yaml:"skipUnchanged,omitempty" json:"skipUnchanged,omitempty"`

	// Resume continues an HTTP download that breaks mid-body with a ranged
	// GET from the last byte received, bound to the strong ETag of the
	// first response (If-Range, If-Match), so the bytes come from the same
	// image. Servers without ranges, a strong ETag or a Content-Length
	// download in one GET.
	// Default: false (a break fails the attempt; the step retry downloads
	// from the first byte)
	Resume bool `yaml:"resume,omitempty" json:"resume,omitempty"`

	// ParallelRanges downloads an HTTP image as that many ranged GETs of
	// 8 MiB at a time and passes the bytes on in order. It helps when one
	// connection cannot fill the link. A failed range is fetched again.
	// Needs the same server support as Resume.
	// Valid values: 0 to 16
	// Default: 0 (one GET)
	ParallelRanges int `yaml:"parallelRanges,omitempty" json:"parallelRanges,omitempty"`

	// Mode controls how the image is written to disk.
	// Valid values: "whole-disk" (default), "partition", "ab"
	//   - whole-disk: dd-style write of the entire image to the block device,
	//     or source-root filesystem streaming into the declared root partition
	//     when provision.disk.partitionLayout is set
	//   - partition: per-partition extraction (streams each partition independently;
	//     cannot be combined with Provision.Disk.PartitionLayout)
	//   - ab: copy the image boot/root partitions into a generated dual-root
	//     A/B layout and boot the selected target slot
	// Default: "whole-disk"
	Mode string `yaml:"mode" json:"mode"`

	// SourceRootLabel selects the source-image GPT partition label to stream
	// into a declarative partition layout root. It is mutually exclusive with
	// SourceRootPartition. A/B mode uses provision.ab.sourceRootLabel instead.
	// Default: "" (auto-select a common root label or unambiguous Linux partition)
	SourceRootLabel string `yaml:"sourceRootLabel" json:"sourceRootLabel"`

	// SourceRootPartition selects the 1-based source-image partition number to
	// stream into a declarative partition layout root. It is mutually exclusive
	// with SourceRootLabel. A/B mode uses provision.ab.sourceRootPartition.
	// Default: 0 (not set)
	SourceRootPartition int `yaml:"sourceRootPartition" json:"sourceRootPartition"`

	// SignatureURL is the URL to a detached GPG signature for image verification.
	// Default: ""
	SignatureURL string `yaml:"signatureURL" json:"signatureURL"`

	// GPGPubKey is the path to the GPG public key for signature verification.
	// Default: ""
	GPGPubKey string `yaml:"gpgPubKey" json:"gpgPubKey"`

	// CosignPublicKeys lists PEM public key files. When set, an oci:// image
	// source must be pinned by digest and carry a cosign signature by one of
	// the keys; BOOTy checks it before the disk is wiped. HTTP sources keep
	// Checksum and SignatureURL.
	// Default: none (no cosign verification)
	CosignPublicKeys []string `yaml:"cosignPublicKeys,omitempty" json:"cosignPublicKeys,omitempty"`
}
