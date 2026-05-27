package config

// DiskConfig defines target disk selection, preparation, and layout settings.
// Used during provisioning for RAID assembly, NVMe namespace management,
// disk detection, wiping, and partitioning.
//
// Processing order: NVMe namespaces → RAID creation → disk detection → wipe → partition.
type DiskConfig struct {
	// Device overrides automatic disk detection with an explicit block device path.
	// When RAID is configured, this typically points to the resulting /dev/mdX device.
	// Example: "/dev/sda", "/dev/nvme0n1", "/dev/md0", "/dev/loop0"
	// Default: "" (auto-detect largest non-removable disk, or RAID device if configured)
	Device string `yaml:"device" json:"device"`

	// MinSizeGB sets the minimum acceptable disk size in GiB.
	// Disks smaller than this are rejected during auto-detection.
	// Default: 0 (no minimum)
	MinSizeGB int `yaml:"minSizeGB" json:"minSizeGB"`

	// SecureErase uses ATA Secure Erase or NVMe Format instead of wipefs
	// for disk sanitization before provisioning.
	// Default: false (use wipefs)
	SecureErase bool `yaml:"secureErase" json:"secureErase"`

	// NumVFs is the number of SR-IOV Virtual Functions to create on Mellanox NICs.
	// Applied during the Mellanox firmware configuration step.
	// Default: 32
	NumVFs int `yaml:"numVFs" json:"numVFs"`

	// NVMeNamespaces is a JSON configuration string for NVMe namespace creation.
	// Parsed and applied before disk detection. Allows creating namespaces on
	// NVMe drives that ship with no default namespace.
	// Default: "" (no namespace management)
	NVMeNamespaces string `yaml:"nvmeNamespaces" json:"nvmeNamespaces"`

	// RAID defines software RAID arrays to create before partitioning.
	// Existing arrays are stopped first, then new arrays are assembled.
	// The resulting /dev/mdX device becomes available for Device or auto-detection.
	// Default: [] (no RAID)
	RAID []RAIDConfig `yaml:"raid" json:"raid"`

	// PartitionLayout defines declarative GPT partitioning.
	// Note: provisioning via PartitionLayout is not yet supported; this field is
	// reserved for a future release.
	// Default: nil
	PartitionLayout *PartitionLayout `yaml:"partitionLayout" json:"partitionLayout"`
}

// RAIDConfig defines a single software RAID array to create via mdadm.
// Arrays are created in order after existing arrays are stopped.
type RAIDConfig struct {
	// Name is the mdadm array name WITHOUT the /dev/ prefix.
	// The disk manager prepends /dev/ automatically.
	// Example: "md0", "md/boot"
	// Required.
	Name string `yaml:"name" json:"name"`

	// Level is the RAID level.
	// Valid values: 0, 1, 5, 6, 10
	// Required.
	Level int `yaml:"level" json:"level"`

	// Devices is the list of member block devices for the array.
	// Minimum 2 devices required.
	// Example: ["/dev/sda", "/dev/sdb"]
	// Required.
	Devices []string `yaml:"devices" json:"devices"`
}
