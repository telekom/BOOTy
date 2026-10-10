//go:build linux

package disk

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Disk mirroring modes accepted from the DISK_MIRRORING_MODE variable. They
// match the CAPRF RedfishHost.Spec.DiskMirroringMode values.
const (
	MirroringModeNone  = "none"
	MirroringModeRAID1 = "raid-1"
	MirroringModeAuto  = "auto"
)

// MirrorRootArray is the md device that mirrors the root partition. The name
// matches the CAPRF legacy provisioner so both installs look the same on disk
// (array name "<homehost>:2").
const MirrorRootArray = "/dev/md2"

// DetectMirrorPair returns the largest pair of non-removable, non-virtual
// disks with an identical size in bytes. ok is false when no such pair exists.
func (m *Manager) DetectMirrorPair(_ context.Context, minSizeGB int) (first, second string, ok bool, err error) {
	blockDir := m.sysfsRoot + "/block"
	entries, err := os.ReadDir(blockDir)
	if err != nil {
		return "", "", false, fmt.Errorf("reading %s: %w", blockDir, err)
	}
	allowRemovable := os.Getenv("BOOTY_ALLOW_REMOVABLE") == "true"

	type candidate struct {
		path  string
		bytes int64
	}
	var candidates []candidate
	for _, entry := range entries {
		name := entry.Name()
		if isVirtualDisk(name) || (!allowRemovable && m.isRemovableMedia(name)) {
			continue
		}
		size, sizeErr := m.readDiskSizeBytes(name)
		if sizeErr != nil || size <= 0 {
			continue
		}
		if minSizeGB > 0 && size < int64(minSizeGB)*1024*1024*1024 {
			continue
		}
		candidates = append(candidates, candidate{path: "/dev/" + name, bytes: size})
	}

	// Largest first, then by name, so the pick is deterministic and a host with
	// several matching pairs mirrors onto the biggest one.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].bytes != candidates[j].bytes {
			return candidates[i].bytes > candidates[j].bytes
		}
		return candidates[i].path < candidates[j].path
	})
	for i := 0; i+1 < len(candidates); i++ {
		if candidates[i].bytes == candidates[i+1].bytes {
			slog.Info("selected mirror pair", "disk1", candidates[i].path, "disk2", candidates[i+1].path,
				"sizeBytes", candidates[i].bytes)
			return candidates[i].path, candidates[i+1].path, true, nil
		}
	}
	return "", "", false, nil
}

func (m *Manager) readDiskSizeBytes(name string) (int64, error) {
	data, err := os.ReadFile(m.sysfsRoot + "/block/" + name + "/size") //nolint:gocritic // sysfs path
	if err != nil {
		return 0, fmt.Errorf("read disk size %s: %w", name, err)
	}
	sectors, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse disk size %s: %w", name, err)
	}
	return sectors * 512, nil
}

// ReplicatePartitionTable copies the GPT of src onto dst and gives dst new
// disk and partition GUIDs so firmware and udev can tell the members apart.
func (m *Manager) ReplicatePartitionTable(ctx context.Context, src, dst string) error {
	if out, err := m.cmd.Run(ctx, "sgdisk", "--replicate="+dst, src); err != nil {
		return fmt.Errorf("sgdisk replicate %s to %s: %s: %w", src, dst, string(out), err)
	}
	if out, err := m.cmd.Run(ctx, "sgdisk", "--randomize-guids", dst); err != nil {
		return fmt.Errorf("sgdisk randomize guids %s: %s: %w", dst, string(out), err)
	}
	return m.PartProbe(ctx, dst)
}

// CreateDegradedMirror creates a two-member RAID1 array with only member
// present. Metadata 1.0 sits at the end of the partition, so the filesystem
// already on member stays readable at offset 0 and the array can be assembled
// around it without copying data.
func (m *Manager) CreateDegradedMirror(ctx context.Context, array, member string) error {
	out, err := m.cmd.Run(ctx, "mdadm", "--create", array,
		"--run", "--level=1", "--raid-devices=2", "--metadata=1.0", "--assume-clean",
		member, "missing")
	if err != nil {
		return fmt.Errorf("mdadm create %s with %s: %s: %w", array, member, string(out), err)
	}
	slog.Info("created degraded mirror", "array", array, "member", member)
	return nil
}

// MirrorActive reports whether array is already assembled.
func (m *Manager) MirrorActive(ctx context.Context, array string) bool {
	_, err := m.cmd.Run(ctx, "mdadm", "--detail", array)
	return err == nil
}

// AddMirrorMember adds member to array; the kernel resyncs it in the background.
func (m *Manager) AddMirrorMember(ctx context.Context, array, member string) error {
	if out, err := m.cmd.Run(ctx, "mdadm", "--add", array, member); err != nil {
		return fmt.Errorf("mdadm add %s to %s: %s: %w", member, array, string(out), err)
	}
	slog.Info("added mirror member", "array", array, "member", member)
	return nil
}

// MirrorScan returns "mdadm --detail --scan" output for array, suitable for
// mdadm.conf.
func (m *Manager) MirrorScan(ctx context.Context, array string) (string, error) {
	out, err := m.cmd.Run(ctx, "mdadm", "--detail", "--scan", array)
	if err != nil {
		return "", fmt.Errorf("mdadm detail scan %s: %s: %w", array, string(out), err)
	}
	return string(out), nil
}

// CopyBlockDevice copies src onto dst, for partitions that cannot be md
// members, such as the EFI system partition that firmware reads directly.
func CopyBlockDevice(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // block device path from partition scan
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY, 0) //nolint:gosec // block device path from partition scan
	if err != nil {
		return fmt.Errorf("open %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy %s to %s: %w", src, dst, err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return fmt.Errorf("sync %s: %w", dst, err)
	}
	return out.Close()
}
