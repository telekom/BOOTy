package config

import (
	"fmt"
	"strings"
)

const (
	// ImageModeWholeDisk streams a raw image to the selected disk.
	ImageModeWholeDisk = "whole-disk"
	// ImageModePartition streams each partition from the source image separately.
	ImageModePartition = "partition"
	// ImageModeAB streams the image into one root slot of a dual-root layout.
	ImageModeAB = "ab"

	// ABSchemeDualRoot is the supported A/B layout with shared boot/state and two root slots.
	ABSchemeDualRoot = "dual-root"
	// ABSchemeSystemAB is an A/B layout for immutable system roots with shared data partitions.
	ABSchemeSystemAB = "system-ab"
	// ABSlotA identifies root slot A.
	ABSlotA = "a"
	// ABSlotB identifies root slot B.
	ABSlotB = "b"
	// ABTargetInactive selects the opposite slot from the active slot.
	ABTargetInactive = "inactive"

	defaultABBootSizeMB = 512
	defaultABRootSizeMB = 32768

	defaultABDataLabel      = "BOOTY-DATA"
	defaultABDataMountpoint = "/var"
	defaultABDataFilesystem = "ext4"
)

// ABConfig defines a dual-root A/B provisioning scheme.
//
// In image.mode=ab, BOOTy writes the selected OS image into the target root
// slot while preserving the other root slot for rollback. Initial provisioning
// creates the GPT layout unless PreserveExisting is true; upgrades set
// PreserveExisting and target the inactive slot.
type ABConfig struct {
	// Scheme selects the partitioning scheme. Empty defaults to "dual-root".
	Scheme string `yaml:"scheme" json:"scheme"`

	// ActiveSlot is the currently booted slot ("a" or "b"). It is used when
	// TargetSlot is "inactive" or empty. Empty defaults to no active slot, so
	// the target resolves to slot "a" for initial provisioning.
	ActiveSlot string `yaml:"activeSlot" json:"activeSlot"`

	// TargetSlot is the slot to write ("a", "b", or "inactive"). Empty
	// defaults to "inactive".
	TargetSlot string `yaml:"targetSlot" json:"targetSlot"`

	// PreserveExisting skips whole-disk wipe and partition layout creation.
	// Use this for OS upgrades on an existing A/B layout.
	PreserveExisting bool `yaml:"preserveExisting" json:"preserveExisting"`

	// BootSizeMB is the shared EFI system partition size. Zero defaults to 512.
	BootSizeMB int `yaml:"bootSizeMB" json:"bootSizeMB"`

	// RootSizeMB is the size of each root slot. Zero defaults to 32768.
	RootSizeMB int `yaml:"rootSizeMB" json:"rootSizeMB"`

	// StateSizeMB is the persistent state partition size. Zero means it fills
	// the remaining disk. In system-ab, this remains a compatibility alias for
	// the default BOOTY-DATA partition size when DataPartitions is not set.
	StateSizeMB int `yaml:"stateSizeMB" json:"stateSizeMB"`

	// DataPartitions are shared data partitions for system-ab. Empty defaults
	// to one ext4 partition labeled BOOTY-DATA mounted at /var. SizeMB=0 means
	// fill remaining space and must only be used by the final data partition.
	DataPartitions []ABDataPartition `yaml:"dataPartitions,omitempty" json:"dataPartitions,omitempty"`

	// SourceRootLabel selects the source-image GPT partition label to copy into
	// the target root slot. When empty, BOOTy accepts common root labels or a
	// single unambiguous Linux root partition and fails on ambiguous layouts.
	SourceRootLabel string `yaml:"sourceRootLabel" json:"sourceRootLabel"`

	// SourceRootPartition selects the 1-based source-image partition number to
	// copy into the target root slot. It is mutually exclusive with
	// SourceRootLabel and exists for images that do not label root partitions.
	SourceRootPartition int `yaml:"sourceRootPartition" json:"sourceRootPartition"`
}

// ABDataPartition defines a shared data partition in the system-ab layout.
type ABDataPartition struct {
	Label      string `yaml:"label" json:"label"`
	SizeMB     int    `yaml:"sizeMB,omitempty" json:"sizeMB,omitempty"`
	Filesystem string `yaml:"filesystem,omitempty" json:"filesystem,omitempty"`
	Mountpoint string `yaml:"mountpoint" json:"mountpoint"`
}

// WithDefaults returns a copy with production-safe defaults filled in.
func (a *ABConfig) WithDefaults() ABConfig {
	cfg := ABConfig{}
	if a != nil {
		cfg = *a
	}
	cfg.Scheme = normalizeABScheme(cfg.Scheme)
	if cfg.Scheme == "" {
		cfg.Scheme = ABSchemeDualRoot
	}
	cfg.ActiveSlot = normalizeABSlot(cfg.ActiveSlot)
	cfg.TargetSlot = strings.ToLower(strings.TrimSpace(cfg.TargetSlot))
	if cfg.TargetSlot == "" {
		cfg.TargetSlot = ABTargetInactive
	}
	if cfg.BootSizeMB == 0 {
		cfg.BootSizeMB = defaultABBootSizeMB
	}
	if cfg.RootSizeMB == 0 {
		cfg.RootSizeMB = defaultABRootSizeMB
	}
	cfg.DataPartitions = defaultABDataPartitions(cfg.Scheme, cfg.StateSizeMB, cfg.DataPartitions)
	return cfg
}

// ResolvedTargetSlot returns the concrete slot BOOTy should write.
func (a *ABConfig) ResolvedTargetSlot() (string, error) {
	cfg := a.WithDefaults()
	switch cfg.TargetSlot {
	case ABSlotA, ABSlotB:
		return cfg.TargetSlot, nil
	case ABTargetInactive:
		switch cfg.ActiveSlot {
		case ABSlotA:
			return ABSlotB, nil
		case ABSlotB:
			return ABSlotA, nil
		case "":
			return ABSlotA, nil
		default:
			return "", fmt.Errorf("invalid active slot %q", cfg.ActiveSlot)
		}
	default:
		return "", fmt.Errorf("invalid target slot %q", cfg.TargetSlot)
	}
}

// PartitionLayout returns the GPT layout for the configured A/B scheme.
func (a *ABConfig) PartitionLayout(device string) (*PartitionLayout, error) {
	cfg := a.WithDefaults()
	if cfg.Scheme != ABSchemeDualRoot && cfg.Scheme != ABSchemeSystemAB {
		return nil, fmt.Errorf("unsupported A/B scheme %q", cfg.Scheme)
	}
	target, err := cfg.ResolvedTargetSlot()
	if err != nil {
		return nil, err
	}

	rootAMount := ""
	rootBMount := ""
	if target == ABSlotA {
		rootAMount = "/"
	} else {
		rootBMount = "/"
	}

	partitions := []Partition{
		{
			Label:      "BOOTY-EFI",
			SizeMB:     cfg.BootSizeMB,
			Filesystem: "vfat",
			Mountpoint: "/boot/efi",
		},
		{
			Label:      "BOOTY-ROOT-A",
			SizeMB:     cfg.RootSizeMB,
			Filesystem: "ext4",
			Mountpoint: rootAMount,
		},
		{
			Label:      "BOOTY-ROOT-B",
			SizeMB:     cfg.RootSizeMB,
			Filesystem: "ext4",
			Mountpoint: rootBMount,
		},
	}
	if cfg.Scheme == ABSchemeDualRoot {
		partitions = append(partitions, Partition{
			Label:      "BOOTY-STATE",
			SizeMB:     cfg.StateSizeMB,
			Filesystem: "ext4",
			Mountpoint: "/var/lib/booty",
		})
	} else {
		for _, data := range cfg.DataPartitions {
			partitions = append(partitions, Partition{
				Label:      data.Label,
				SizeMB:     data.SizeMB,
				Filesystem: data.Filesystem,
				Mountpoint: data.Mountpoint,
			})
		}
	}

	return &PartitionLayout{
		Table:      "gpt",
		Device:     device,
		Partitions: partitions,
	}, nil
}

func defaultABDataPartitions(scheme string, stateSizeMB int, parts []ABDataPartition) []ABDataPartition {
	if scheme != ABSchemeSystemAB {
		return normalizeABDataPartitions(parts)
	}
	if len(parts) == 0 {
		return []ABDataPartition{{
			Label:      defaultABDataLabel,
			SizeMB:     stateSizeMB,
			Filesystem: defaultABDataFilesystem,
			Mountpoint: defaultABDataMountpoint,
		}}
	}
	return normalizeABDataPartitions(parts)
}

func normalizeABDataPartitions(parts []ABDataPartition) []ABDataPartition {
	if len(parts) == 0 {
		return nil
	}
	out := make([]ABDataPartition, 0, len(parts))
	for _, part := range parts {
		part.Label = strings.TrimSpace(part.Label)
		part.Filesystem = strings.ToLower(strings.TrimSpace(part.Filesystem))
		part.Mountpoint = strings.TrimSpace(part.Mountpoint)
		if part.Filesystem == "" {
			part.Filesystem = defaultABDataFilesystem
		}
		out = append(out, part)
	}
	return out
}

func normalizeABScheme(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeABSlot(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
