//go:build linux

package disk

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
)

// edgeSectors is 1 MiB in 512 byte sectors: a thorough wipe zeroes this
// much at the start and the end of each partition and of the disk.
const edgeSectors = 2048

// SetQuickWipe makes WipeDisk and WipeAllDisks clear only the signatures of
// the disk itself (sgdisk --zap-all and wipefs), like the CAPRF legacy
// wipe_all_disks. The default is the thorough wipe, see wipeDevice.
func (m *Manager) SetQuickWipe(enabled bool) {
	m.quickWipe = enabled
}

// blockNode is one device of `lsblk --json --bytes --paths`.
type blockNode struct {
	Name     string      `json:"name"`
	Type     string      `json:"type"`
	FSType   string      `json:"fstype"`
	Size     json.Number `json:"size"`
	Children []blockNode `json:"children"`
}

// wipeDevice clears a disk so that nothing of the old content is found
// again, also when the old partition table is restored. The thorough wipe
// first stops the LVM, dm-crypt and MD devices on top of the disk, then
// removes the LVM PV and MD superblocks, the signatures (wipefs) and the
// first and last MiB of every partition, then the partition table (GPT with
// its backup and MBR), the disk signatures and the first and last MiB of the
// disk, and discards the disk where the device supports it. Only the final
// disk wipefs fails the wipe; the other steps log failures.
func (m *Manager) wipeDevice(ctx context.Context, device string) error {
	var root *blockNode
	if !m.quickWipe {
		root = m.blockTree(ctx, device)
	}
	if root != nil {
		m.releaseHolders(ctx, root.Children)
		for i := range root.Children {
			if root.Children[i].Type == "part" {
				m.wipeMember(ctx, &root.Children[i])
			}
		}
		m.removeMembership(ctx, root)
	}
	if out, err := m.cmd.Run(ctx, "sgdisk", "--zap-all", device); err != nil {
		slog.Debug("sgdisk zap failed (may not be GPT)", "device", device, "output", string(out), "err", err)
	}
	if _, err := m.cmd.Run(ctx, "wipefs", "-af", device); err != nil {
		return err
	}
	if m.quickWipe {
		return nil
	}
	if root != nil {
		m.zeroEdges(ctx, device, root.Size)
	}
	if out, err := m.cmd.Run(ctx, "blkdiscard", device); err != nil {
		slog.Debug("blkdiscard not supported or failed", "device", device, "output", strings.TrimSpace(string(out)), "error", err)
	}
	return nil
}

// blockTree returns the device and its partitions and holders, or nil.
func (m *Manager) blockTree(ctx context.Context, device string) *blockNode {
	out, err := m.cmd.Run(ctx, "lsblk", "--json", "--bytes", "--paths", "--output", "NAME,TYPE,FSTYPE,SIZE", device)
	if err != nil {
		slog.Warn("cannot list partitions and holders, wiping the disk only", "device", device, "error", err)
		return nil
	}
	var tree struct {
		Devices []blockNode `json:"blockdevices"`
	}
	if err := json.Unmarshal(out, &tree); err != nil || len(tree.Devices) == 0 {
		slog.Warn("cannot parse lsblk output, wiping the disk only", "device", device, "error", err)
		return nil
	}
	return &tree.Devices[0]
}

// releaseHolders stops the LVM, dm-crypt and MD devices in the tree, the
// top ones first, so that their members can be wiped.
func (m *Manager) releaseHolders(ctx context.Context, nodes []blockNode) {
	for i := range nodes {
		n := &nodes[i]
		m.releaseHolders(ctx, n.Children)
		var name string
		var args []string
		switch {
		case n.Type == "lvm":
			name, args = "lvm", []string{"lvchange", "-an", n.Name}
		case n.Type == "crypt":
			name, args = "cryptsetup", []string{"close", n.Name}
		case strings.HasPrefix(n.Type, "raid"):
			name, args = "mdadm", []string{"--stop", n.Name}
		default:
			continue
		}
		if out, err := m.cmd.Run(ctx, name, args...); err != nil {
			slog.Warn("cannot stop device before the wipe", "device", n.Name, "type", n.Type,
				"output", strings.TrimSpace(string(out)), "error", err)
		}
	}
}

// wipeMember clears one partition: membership, signatures, first and last MiB.
func (m *Manager) wipeMember(ctx context.Context, n *blockNode) {
	m.removeMembership(ctx, n)
	if out, err := m.cmd.Run(ctx, "wipefs", "-af", n.Name); err != nil {
		slog.Warn("wipefs failed", "device", n.Name, "output", strings.TrimSpace(string(out)), "error", err)
	}
	m.zeroEdges(ctx, n.Name, n.Size)
}

// removeMembership removes an LVM PV label or an MD superblock.
func (m *Manager) removeMembership(ctx context.Context, n *blockNode) {
	var name string
	var args []string
	switch n.FSType {
	case "LVM2_member":
		name, args = "lvm", []string{"pvremove", "-ff", "-y", n.Name}
	case "linux_raid_member":
		name, args = "mdadm", []string{"--zero-superblock", n.Name}
	default:
		return
	}
	if out, err := m.cmd.Run(ctx, name, args...); err != nil {
		slog.Warn("cannot remove the member label", "device", n.Name, "fstype", n.FSType,
			"output", strings.TrimSpace(string(out)), "error", err)
	}
}

// zeroEdges writes zeros to the first and the last MiB of a device.
func (m *Manager) zeroEdges(ctx context.Context, device string, size json.Number) {
	sectors := int64(0)
	if bytes, err := size.Int64(); err == nil {
		sectors = bytes / 512
	}
	offsets := []int64{0}
	if sectors > 2*edgeSectors {
		offsets = append(offsets, sectors-edgeSectors)
	}
	for _, seek := range offsets {
		args := []string{"if=/dev/zero", "of=" + device, "bs=512", "count=" + strconv.Itoa(edgeSectors),
			"seek=" + strconv.FormatInt(seek, 10), "conv=notrunc,fsync"}
		if out, err := m.cmd.Run(ctx, "dd", args...); err != nil {
			slog.Warn("cannot zero the device edge", "device", device, "seek", seek,
				"output", strings.TrimSpace(string(out)), "error", err)
		}
	}
}
