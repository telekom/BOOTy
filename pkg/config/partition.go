package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
)

// PartitionLayout defines a declarative GPT partitioning scheme for the target
// disk. It supports plain partitions and an optional LVM layer on top of one of
// the partitions.
//
// Constraints enforced by ParsePartitionLayout:
//   - At most one partition may have SizeMB=0 (fill remaining), and it must be last.
//   - Mountpoint "/" must appear in exactly one partition or LVM volume.
//   - Mountpoints must be unique across partitions and LVM volumes.
//   - Only "gpt" table type is supported.
//
// An external controller can embed this struct directly and call
// ParsePartitionLayout to validate a JSON representation, or build the struct
// programmatically and include it in DiskConfig.PartitionLayout.
type PartitionLayout struct {
	// Table is the partition table type.
	// Only "gpt" is supported. Defaults to "gpt" when empty.
	Table string `json:"table" yaml:"table"`

	// Device overrides automatic disk detection.
	// Must be an absolute, clean path (e.g. "/dev/sda"). Empty means auto-detect.
	Device string `json:"device,omitempty" yaml:"device,omitempty"`

	// Partitions is the ordered list of GPT partitions to create.
	// At least one partition is required.
	Partitions []Partition `json:"partitions" yaml:"partitions"`

	// LVM optionally adds an LVM volume group on top of one of the partitions.
	// When nil, no LVM is configured.
	LVM *LVMConfig `json:"lvm,omitempty" yaml:"lvm,omitempty"`
}

// Partition defines a single GPT partition within a PartitionLayout.
type Partition struct {
	// Label is the GPT partition name. Must be unique, at most 36 characters,
	// and contain only alphanumerics, hyphens, underscores, dots, or spaces.
	// Required.
	Label string `json:"label" yaml:"label"`

	// SizeMB is the partition size in MiB. 0 means "fill remaining space" and
	// must only appear on the last partition.
	SizeMB int `json:"sizeMB,omitempty" yaml:"sizeMB,omitempty"`

	// TypeGUID is the GPT partition type GUID (e.g. "C12A7328-F81F-11D2-BA4B-00A0C93EC93B"
	// for EFI). When empty, the type is auto-set based on Filesystem.
	TypeGUID string `json:"typeGUID,omitempty" yaml:"typeGUID,omitempty"`

	// Filesystem is the filesystem type to create with mkfs.
	// Supported values: "vfat", "ext4", "xfs", "swap", "" (no filesystem).
	Filesystem string `json:"filesystem,omitempty" yaml:"filesystem,omitempty"`

	// Mountpoint is the absolute path where the partition is mounted inside the
	// installed OS (e.g. "/", "/boot", "/boot/efi").
	// A Mountpoint requires a Filesystem. Swap partitions must not set a Mountpoint.
	Mountpoint string `json:"mountpoint,omitempty" yaml:"mountpoint,omitempty"`

	// MountOptions overrides the generated fstab mount options. Empty lets the
	// fstab generator use its fixed defaults. Swap partitions ignore this field.
	MountOptions string `json:"mountOptions,omitempty" yaml:"mountOptions,omitempty"`
}

// LVMConfig defines an LVM volume group and its logical volumes.
// The VG is created on top of a single partition (identified by PVPartition).
// That partition must not define its own Filesystem or Mountpoint.
type LVMConfig struct {
	// VolumeGroup is the LVM volume group name (e.g. "sysvg").
	// Must be a valid LVM name: alphanumerics, hyphens, underscores, dots;
	// must not start with a hyphen or dot. Required.
	VolumeGroup string `json:"volumeGroup" yaml:"volumeGroup"`

	// PVPartition is the 1-based index into PartitionLayout.Partitions that
	// becomes the physical volume for this VG. Required (>= 1).
	PVPartition int `json:"pvPartition" yaml:"pvPartition"`

	// Volumes is the ordered list of logical volumes to create in this VG.
	// At least one volume is required.
	Volumes []LVVolume `json:"volumes" yaml:"volumes"`
}

// LVVolume defines a single logical volume within an LVM volume group.
type LVVolume struct {
	// Name is the logical volume name within the VG (e.g. "root", "var", "data").
	// Must be a valid LVM name. Required.
	Name string `json:"name" yaml:"name"`

	// SizeMB is the volume size in MiB. 0 combined with an empty Extents means
	// "fill remaining space" (equivalent to Extents="100%FREE"). Must be last.
	SizeMB int `json:"sizeMB,omitempty" yaml:"sizeMB,omitempty"`

	// Extents is the size expressed as LVM extent syntax (e.g. "100%FREE", "50%VG").
	// Mutually exclusive with SizeMB.
	Extents string `json:"extents,omitempty" yaml:"extents,omitempty"`

	// Filesystem is the filesystem type to create with mkfs.
	// Supported values: "vfat", "ext4", "xfs", "swap", "" (no filesystem).
	Filesystem string `json:"filesystem,omitempty" yaml:"filesystem,omitempty"`

	// Mountpoint is the absolute path where the volume is mounted inside the
	// installed OS. A Mountpoint requires a Filesystem.
	Mountpoint string `json:"mountpoint,omitempty" yaml:"mountpoint,omitempty"`

	// MountOptions overrides the generated fstab mount options. Empty lets the
	// fstab generator use its fixed defaults. Swap volumes ignore this field.
	MountOptions string `json:"mountOptions,omitempty" yaml:"mountOptions,omitempty"`
}

// ParsePartitionLayout parses and validates a JSON partition layout string.
//
// Validation rules applied:
//   - Table must be "gpt" or empty (defaults to "gpt")
//   - Device, if set, must be an absolute, clean, whitespace-free path
//   - At least one and at most 128 partitions required
//   - Each partition label must be unique, ≤36 chars, alphanumeric/hyphen/underscore/dot/space
//   - At most one partition may have SizeMB=0 (fill remaining), and it must be last
//   - Mountpoints must be absolute, whitespace-free, path-traversal-free, and unique
//   - A mountpoint requires a filesystem; swap partitions must not define a mountpoint
//   - Mount options must be fstab-token safe and whitespace-free
//   - Supported filesystems: "vfat", "ext4", "xfs", "swap", "" (raw)
//   - TypeGUID, when set, must be a valid UUID
//   - LVM validation: volumeGroup and pvPartition required, pvPartition must not define
//     filesystem or mountpoint, LV names must be unique valid LVM names, fill-remaining
//     LV must be last, sizeMB and extents are mutually exclusive
//   - Mountpoint "/" must appear in exactly one partition or LVM volume
//
// Returns a validated *PartitionLayout with Table defaulted to "gpt" and
// Device trimmed. Returns an error describing the first validation failure.
func ParsePartitionLayout(data string) (*PartitionLayout, error) {
	var layout PartitionLayout
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&layout); err != nil {
		return nil, fmt.Errorf("parsing partition layout: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parsing partition layout: unexpected trailing content")
	}

	if len(layout.Partitions) == 0 {
		return nil, fmt.Errorf("partition layout has no partitions")
	}
	if len(layout.Partitions) > maxPartitions {
		return nil, fmt.Errorf("partition layout has %d partitions, maximum is %d", len(layout.Partitions), maxPartitions)
	}
	if layout.Table == "" {
		layout.Table = "gpt"
	}
	if layout.Table != "gpt" {
		return nil, fmt.Errorf("unsupported partition table %q, only \"gpt\" is supported", layout.Table)
	}
	device, err := normalizePartitionLayoutDevice(layout.Device)
	if err != nil {
		return nil, err
	}
	layout.Device = device
	if err := validatePartitions(layout.Partitions); err != nil {
		return nil, err
	}
	if err := validateLVMConfig(layout.LVM, layout.Partitions); err != nil {
		return nil, err
	}
	if err := validateUniqueMountpoints(layout.Partitions, layout.LVM); err != nil {
		return nil, err
	}
	if err := validateRootPresence(layout.Partitions, layout.LVM); err != nil {
		return nil, err
	}
	return &layout, nil
}

func normalizePartitionLayoutDevice(device string) (string, error) {
	trimmed := strings.TrimSpace(device)
	if trimmed == "" {
		return "", nil
	}
	if strings.ContainsAny(trimmed, " \t\n\r") {
		return "", fmt.Errorf("partition layout device %q must not contain whitespace", device)
	}
	if !filepath.IsAbs(trimmed) {
		return "", fmt.Errorf("partition layout device %q must be an absolute path", device)
	}
	cleaned := filepath.Clean(trimmed)
	if cleaned != trimmed {
		return "", fmt.Errorf("partition layout device %q must be a clean path (got %q after normalization)", device, cleaned)
	}
	return cleaned, nil
}

func validatePartitions(partitions []Partition) error {
	fillCount := 0
	seen := make(map[string]bool)
	for i := range partitions {
		part := &partitions[i]
		if err := validatePartitionEntry(i, part, len(partitions), seen); err != nil {
			return err
		}
		if part.SizeMB == 0 {
			fillCount++
		}
	}
	if fillCount > 1 {
		return fmt.Errorf("only one partition may use sizeMB=0 (fill remaining), got %d", fillCount)
	}
	return nil
}

func validatePartitionEntry(index int, part *Partition, partitionCount int, seen map[string]bool) error {
	if err := validatePartitionLabel(index, part.Label, seen); err != nil {
		return err
	}
	if err := validatePartitionMountpoint(index, part); err != nil {
		return err
	}
	if err := validateMountOptions(fmt.Sprintf("partition %d (%s)", index+1, part.Label), part.MountOptions); err != nil {
		return err
	}
	if !isSupportedFilesystem(part.Filesystem) {
		return fmt.Errorf("partition %d (%s): unsupported filesystem %q", index+1, part.Label, part.Filesystem)
	}
	if err := validatePartitionSize(index, part, partitionCount); err != nil {
		return err
	}
	if part.TypeGUID != "" && !typeGUIDRE.MatchString(part.TypeGUID) {
		return fmt.Errorf("partition %d (%s): invalid typeGUID %q, must be a UUID", index+1, part.Label, part.TypeGUID)
	}
	return nil
}

func validatePartitionLabel(index int, label string, seen map[string]bool) error {
	if label == "" {
		return fmt.Errorf("partition %d: label is required", index+1)
	}
	if !isValidPartitionLabel(label) {
		return fmt.Errorf("partition %d: label %q contains invalid characters or exceeds 36 characters", index+1, label)
	}
	if seen[label] {
		return fmt.Errorf("partition %d: duplicate label %q", index+1, label)
	}
	seen[label] = true
	return nil
}

func validatePartitionMountpoint(index int, part *Partition) error {
	if part.Mountpoint != "" && !strings.HasPrefix(part.Mountpoint, "/") {
		return fmt.Errorf("partition %d (%s): mountpoint %q must be an absolute path", index+1, part.Label, part.Mountpoint)
	}
	if strings.ContainsAny(part.Mountpoint, " \t\n\r") {
		return fmt.Errorf("partition %d (%s): mountpoint %q must not contain whitespace", index+1, part.Label, part.Mountpoint)
	}
	if part.Mountpoint != "" && containsPathTraversal(part.Mountpoint) {
		return fmt.Errorf("partition %d (%s): mountpoint %q must not contain path traversal", index+1, part.Label, part.Mountpoint)
	}
	if part.Mountpoint != "" && part.Filesystem == "" {
		return fmt.Errorf("partition %d (%s): mountpoint %q requires a filesystem", index+1, part.Label, part.Mountpoint)
	}
	if part.Filesystem == "swap" && part.Mountpoint != "" {
		return fmt.Errorf("partition %d (%s): swap partition must not define mountpoint %q", index+1, part.Label, part.Mountpoint)
	}
	return nil
}

func validatePartitionSize(index int, part *Partition, partitionCount int) error {
	if part.SizeMB < 0 {
		return fmt.Errorf("partition %d (%s): sizeMB must be non-negative", index+1, part.Label)
	}
	if part.SizeMB == 0 && index != partitionCount-1 {
		return fmt.Errorf("partition %d (%s): sizeMB=0 (fill remaining) must be the last partition", index+1, part.Label)
	}
	return nil
}

func validateLVMConfig(lvm *LVMConfig, partitions []Partition) error {
	if lvm == nil {
		return nil
	}
	if len(lvm.Volumes) == 0 {
		return fmt.Errorf("lvm: at least one volume is required")
	}
	if len(lvm.Volumes) > maxLVMVolumes {
		return fmt.Errorf("lvm: %d volumes exceeds maximum of %d", len(lvm.Volumes), maxLVMVolumes)
	}
	if err := validateLVMPVPartition(lvm, partitions); err != nil {
		return err
	}
	seenNames := make(map[string]bool)
	for i := range lvm.Volumes {
		vol := &lvm.Volumes[i]
		if err := validateLVMVolume(i, vol); err != nil {
			return err
		}
		if seenNames[vol.Name] {
			return fmt.Errorf("lvm volume %d: duplicate name %q", i+1, vol.Name)
		}
		seenNames[vol.Name] = true
		if usesAllRemainingLVMExtents(vol) && i != len(lvm.Volumes)-1 {
			return fmt.Errorf("lvm volume %d (%s): fill-remaining volume must be the last lvm volume", i+1, vol.Name)
		}
	}
	return nil
}

func usesAllRemainingLVMExtents(vol *LVVolume) bool {
	if vol.SizeMB > 0 {
		return false
	}
	extents := strings.TrimSpace(vol.Extents)
	return extents == "" || strings.EqualFold(extents, "100%FREE")
}

func validateUniqueMountpoints(partitions []Partition, lvm *LVMConfig) error {
	seen := make(map[string]string)

	addMountpoint := func(mountpoint, location string) error {
		if mountpoint == "" {
			return nil
		}
		if prev, ok := seen[mountpoint]; ok {
			return fmt.Errorf("mountpoint %q is defined multiple times (%s, %s)", mountpoint, prev, location)
		}
		seen[mountpoint] = location
		return nil
	}

	for i, part := range partitions {
		location := fmt.Sprintf("partition %d (%s)", i+1, part.Label)
		if err := addMountpoint(part.Mountpoint, location); err != nil {
			return err
		}
	}

	if lvm == nil {
		return nil
	}

	for i, vol := range lvm.Volumes {
		location := fmt.Sprintf("lvm volume %d (%s)", i+1, vol.Name)
		if err := addMountpoint(vol.Mountpoint, location); err != nil {
			return err
		}
	}

	return nil
}

func validateLVMPVPartition(lvm *LVMConfig, partitions []Partition) error {
	if lvm.VolumeGroup == "" {
		return fmt.Errorf("lvm: volumeGroup is required")
	}
	if !isValidLVMName(lvm.VolumeGroup) {
		return fmt.Errorf("lvm: invalid volumeGroup name %q", lvm.VolumeGroup)
	}
	if lvm.PVPartition < 1 {
		return fmt.Errorf("lvm: pvPartition must be >= 1, got %d", lvm.PVPartition)
	}
	if lvm.PVPartition > len(partitions) {
		return fmt.Errorf("lvm: pvPartition %d exceeds partition count %d", lvm.PVPartition, len(partitions))
	}

	pvPart := partitions[lvm.PVPartition-1]
	if pvPart.Mountpoint != "" {
		return fmt.Errorf("lvm: pvPartition %d (%s) must not define mountpoint %q", lvm.PVPartition, pvPart.Label, pvPart.Mountpoint)
	}
	if pvPart.Filesystem != "" {
		return fmt.Errorf("lvm: pvPartition %d (%s) must not define filesystem %q", lvm.PVPartition, pvPart.Label, pvPart.Filesystem)
	}
	return nil
}

func validateLVMVolume(index int, vol *LVVolume) error {
	if vol.Name == "" {
		return fmt.Errorf("lvm volume %d: name is required", index+1)
	}
	if !isValidLVMName(vol.Name) {
		return fmt.Errorf("lvm volume %d: invalid name %q", index+1, vol.Name)
	}
	if err := validateLVMVolumeMountpoint(index, vol); err != nil {
		return err
	}
	if err := validateMountOptions(fmt.Sprintf("lvm volume %d (%s)", index+1, vol.Name), vol.MountOptions); err != nil {
		return err
	}
	if !isSupportedFilesystem(vol.Filesystem) {
		return fmt.Errorf("lvm volume %d (%s): unsupported filesystem %q", index+1, vol.Name, vol.Filesystem)
	}
	if err := validateLVMVolumeSize(index, vol); err != nil {
		return err
	}
	return nil
}

func validateLVMVolumeMountpoint(index int, vol *LVVolume) error {
	if vol.Mountpoint != "" && !strings.HasPrefix(vol.Mountpoint, "/") {
		return fmt.Errorf("lvm volume %d (%s): mountpoint %q must be an absolute path", index+1, vol.Name, vol.Mountpoint)
	}
	if strings.ContainsAny(vol.Mountpoint, " \t\n\r") {
		return fmt.Errorf("lvm volume %d (%s): mountpoint %q must not contain whitespace", index+1, vol.Name, vol.Mountpoint)
	}
	if vol.Mountpoint != "" && containsPathTraversal(vol.Mountpoint) {
		return fmt.Errorf("lvm volume %d (%s): mountpoint %q must not contain path traversal", index+1, vol.Name, vol.Mountpoint)
	}
	if vol.Mountpoint != "" && vol.Filesystem == "" {
		return fmt.Errorf("lvm volume %d (%s): mountpoint %q requires a filesystem", index+1, vol.Name, vol.Mountpoint)
	}
	if vol.Filesystem == "swap" && vol.Mountpoint != "" {
		return fmt.Errorf("lvm volume %d (%s): swap volume must not define mountpoint %q", index+1, vol.Name, vol.Mountpoint)
	}
	return nil
}

func validateMountOptions(location, options string) error {
	if options == "" {
		return nil
	}
	if strings.TrimSpace(options) != options || strings.ContainsAny(options, " \t\n\r") {
		return fmt.Errorf("%s: mountOptions %q must not contain whitespace", location, options)
	}
	if !mountOptionsRE.MatchString(options) {
		return fmt.Errorf("%s: mountOptions %q contains unsupported characters", location, options)
	}
	return nil
}

// containsPathTraversal detects ".." path components in a mountpoint string.
// filepath.Clean collapses traversal sequences, so we check the raw components.
func containsPathTraversal(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func validateLVMVolumeSize(index int, vol *LVVolume) error {
	if vol.SizeMB < 0 {
		return fmt.Errorf("lvm volume %d (%s): sizeMB must be non-negative", index+1, vol.Name)
	}
	if vol.Extents != "" && vol.SizeMB > 0 {
		return fmt.Errorf("lvm volume %d (%s): specify either sizeMB or extents, not both", index+1, vol.Name)
	}
	if vol.Extents != "" && !isValidLVMExtents(vol.Extents) {
		return fmt.Errorf("lvm volume %d (%s): invalid extents format %q", index+1, vol.Name, vol.Extents)
	}
	return nil
}

func validateRootPresence(partitions []Partition, lvm *LVMConfig) error {
	for _, part := range partitions {
		if part.Mountpoint == "/" {
			return nil
		}
	}
	if lvm != nil {
		for _, vol := range lvm.Volumes {
			if vol.Mountpoint == "/" {
				return nil
			}
		}
	}
	return fmt.Errorf("partition layout must include mountpoint \"/\" in either a partition or an lvm volume")
}

func isSupportedFilesystem(fs string) bool {
	switch fs {
	case "", "vfat", "ext4", "xfs", "swap":
		return true
	default:
		return false
	}
}

// typeGUIDRE validates GPT type GUID format.
var typeGUIDRE = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)

// mountOptionsRE validates a single fstab options token without whitespace.
var mountOptionsRE = regexp.MustCompile(`^[A-Za-z0-9_.,=:/+-]+$`)

const maxPartitions = 128
const maxLVMVolumes = 256

func isValidPartitionLabel(label string) bool {
	if len(label) > 36 {
		return false
	}
	for _, c := range label {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') &&
			c != '_' && c != '-' && c != '.' && c != ' ' {
			return false
		}
	}
	return true
}

func isValidLVMName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.HasPrefix(name, "-") || strings.HasPrefix(name, ".") {
		return false
	}
	for _, c := range name {
		if !isValidLVMNameChar(c) {
			return false
		}
	}
	return true
}

func isValidLVMNameChar(c rune) bool {
	if c >= 'a' && c <= 'z' {
		return true
	}
	if c >= 'A' && c <= 'Z' {
		return true
	}
	if c >= '0' && c <= '9' {
		return true
	}
	return c == '_' || c == '-' || c == '.'
}

func isValidLVMExtents(extents string) bool {
	if extents == "" {
		return false
	}
	for _, c := range extents {
		if (c < '0' || c > '9') && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && c != '%' && c != '+' {
			return false
		}
	}
	return true
}
