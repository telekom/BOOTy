# Proposal: Custom Disk Partitioning

## Status: Proposal

Phase 1 implements: `ParsePartitionLayout`, `ApplyPartitionLayout` (GPT only via sgdisk),
`GenerateFstab`, partition type GUID resolution, device naming.
Wired into the provisioning orchestrator as the apply-partition-layout step — `MachineConfig.PartitionLayout` is parsed but
`ApplyPartitionLayout` is not called from the orchestrator.

## Priority: P3

## Summary

Support declarative disk partitioning layouts beyond the current "whole disk"
image streaming approach. This enables multi-partition setups (separate `/boot`,
`/`, `/var`), LVM-based layouts, and custom filesystem formatting — required
for compliance, performance tuning, and multi-disk configurations.

## Motivation

Currently, BOOTy streams an OS image directly to the root disk as a single
partition image. This works for standard Kubernetes worker nodes but doesn't
support:

- **Separate `/var/lib/containerd`**: Isolate container storage on a dedicated
  partition or disk for performance and wear leveling
- **Separate `/var/log`**: Prevent log explosion from filling the root FS
- **Multiple disks**: Use NVMe for OS, spinning disk for data
- **LVM Layout**: Logical volumes for flexible resizing
- **Compliance**: Some security standards require specific partition layouts
  (e.g., CIS benchmarks require separate mounts for `/tmp`, `/var`, `/home`)

### Industry Context

| Tool | Partitioning |
|------|-------------|
| **Ironic** | `root_device` hints + configdrive partitioning; IPA supports `partition` and `whole-disk` images |
| **MAAS** | Full custom partition layouts via curtin preseed: partitions, LVM, bcache, ZFS |
| **Tinkerbell** | Actions can run arbitrary partition commands; no declarative format |

## Design

### Partition Schema

```go
// pkg/disk/partition.go
type PartitionLayout struct {
    Device     string       `json:"device"`     // e.g., "/dev/sda"
    Table      string       `json:"table"`      // "gpt" or "msdos"
    Partitions []Partition  `json:"partitions"`
    LVM        []LVMConfig  `json:"lvm,omitempty"`
}

type Partition struct {
    Number     int    `json:"number"`
    Label      string `json:"label"`
    SizeMB     int    `json:"sizeMB"`     // 0 = remainder
    FSType     string `json:"fsType"`     // ext4, xfs, vfat, swap
    MountPoint string `json:"mountPoint"` // e.g., "/", "/boot/efi"
    Flags      string `json:"flags"`      // e.g., "boot,esp"
}

type LVMConfig struct {
    VGName     string      `json:"vgName"`
    PVDevices  []string    `json:"pvDevices"`  // e.g., ["/dev/sda3"]
    Volumes    []LVVolume  `json:"volumes"`
}

type LVVolume struct {
    Name       string `json:"name"`
    SizeMB     int    `json:"sizeMB"` // 0 = remainder
    FSType     string `json:"fsType"`
    MountPoint string `json:"mountPoint"`
}
```

### Configuration

```bash
# /deploy/vars
export PARTITION_LAYOUT='[
  {"number":1, "label":"efi",  "sizeMB":512,   "fsType":"vfat", "mountPoint":"/boot/efi", "flags":"boot,esp"},
  {"number":2, "label":"boot", "sizeMB":1024,  "fsType":"ext4", "mountPoint":"/boot"},
  {"number":3, "label":"root", "sizeMB":51200, "fsType":"ext4", "mountPoint":"/"},
  {"number":4, "label":"data", "sizeMB":0,     "fsType":"xfs",  "mountPoint":"/var/lib/containerd"}
]'
```

Or via CAPRF `ProvisionerConfig`:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: partition-layout
stringData:
  config: |
    partitionLayout:
      device: auto  # auto-detect root disk
      table: gpt
      partitions:
        - label: efi
          sizeMB: 512
          fsType: vfat
          mountPoint: /boot/efi
          flags: boot,esp
        - label: root
          sizeMB: 0
          fsType: ext4
          mountPoint: /
```

### Implementation

```go
// pkg/disk/partition.go
func (m *Manager) ApplyPartitionLayout(ctx context.Context, layout PartitionLayout) error {
    device := layout.Device

    // 1. Wipe existing partition table
    if _, err := m.cmd.Run(ctx, "sgdisk", "--zap-all", device); err != nil {
        return fmt.Errorf("wipe partition table: %w", err)
    }

    // 2. Create partitions
    for _, p := range layout.Partitions {
        args := []string{}
        if p.SizeMB > 0 {
            args = append(args, fmt.Sprintf("--new=%d:0:+%dM", p.Number, p.SizeMB))
        } else {
            args = append(args, fmt.Sprintf("--new=%d:0:0", p.Number)) // remainder
        }
        if p.Label != "" {
            args = append(args, fmt.Sprintf("--change-name=%d:%s", p.Number, p.Label))
        }
        if strings.Contains(p.Flags, "esp") {
            args = append(args, fmt.Sprintf("--typecode=%d:EF00", p.Number))
        }
        args = append(args, device)
        if _, err := m.cmd.Run(ctx, "sgdisk", args...); err != nil {
            return fmt.Errorf("create partition %d: %w", p.Number, err)
        }
    }

    // 3. Probe for new partitions
    m.cmd.Run(ctx, "partprobe", device)

    // 4. Format partitions
    for _, p := range layout.Partitions {
        partDev := fmt.Sprintf("%sp%d", device, p.Number)
        // Handle non-NVMe device naming
        if !strings.Contains(device, "nvme") {
            partDev = fmt.Sprintf("%s%d", device, p.Number)
        }
        switch p.FSType {
        case "vfat":
            m.cmd.Run(ctx, "mkfs.vfat", "-F", "32", partDev)
        case "ext4":
            m.cmd.Run(ctx, "mkfs.ext4", "-F", "-L", p.Label, partDev)
        case "xfs":
            m.cmd.Run(ctx, "mkfs.xfs", "-f", "-L", p.Label, partDev)
        case "swap":
            m.cmd.Run(ctx, "mkswap", "-L", p.Label, partDev)
        }
    }

    return nil
}
```

### Integration with Provisioning

When a partition layout is configured, the provisioning pipeline changes:

```
Without custom partitioning (current):
  Stream image → whole disk → partprobe → mount → configure

With custom partitioning:
  Apply partition layout → format → mount all partitions →
  extract rootfs tarball → configure → generate fstab
```

This means the image format also changes: instead of a raw disk image,
a rootfs tarball (`.tar.gz` / `.tar.zst`) is extracted into the
mounted root partition.

### Auto-generated fstab

```go
func (m *Manager) GenerateFstab(layout PartitionLayout) string {
    var lines []string
    lines = append(lines, "# Generated by BOOTy provisioner")
    for _, p := range layout.Partitions {
        if p.MountPoint == "" || p.FSType == "swap" {
            continue
        }
        lines = append(lines, fmt.Sprintf(
            "LABEL=%s\t%s\t%s\tdefaults\t0\t%d",
            p.Label, p.MountPoint, p.FSType, fstabPass(p.MountPoint),
        ))
    }
    return strings.Join(lines, "\n")
}
```

## Required Binaries in Initramfs

All required binaries are already present in the initramfs:

| Binary | Package | Purpose | Initramfs Flavor | Already Present? |
|--------|---------|---------|-----------------|------------------|
| `sgdisk` | `gdisk` | GPT partition table creation | all | **Yes** |
| `parted` | `parted` | MBR/GPT partitioning (alternative) | all | **Yes** |
| `partprobe` | `parted` | Re-read partition table after changes | all | **Yes** |
| `mkfs.ext4` | `e2fsprogs` | ext4 filesystem formatting | all | **Yes** (via `e2fsck`) |
| `mkfs.xfs` | `xfsprogs` | XFS filesystem formatting | all | **Yes** (via `xfs_growfs`) |
| `mkfs.vfat` | `dosfstools` | FAT32 formatting (EFI partition) | full, gobgp | **No — add** |
| `mkswap` | `util-linux` / busybox | Swap partition formatting | all | **Yes** (busybox) |
| `lvm` | `lvm2` | LVM logical volume management | all | **Yes** |
| `wipefs` | `util-linux` | Wipe filesystem signatures | all | **Yes** |

**Note**: `mkfs.vfat` is only needed if custom partition layouts include
EFI System Partitions. For whole-disk imaging (current default), it is
not required.

## Affected Files

| File | Change |
|------|--------|
| `pkg/disk/partition.go` | New — partition layout types and logic |
| `pkg/disk/partition_test.go` | New — unit tests |
| `pkg/disk/manager.go` | Add `ApplyPartitionLayout()` method |
| `pkg/provision/orchestrator.go` | Add partition branch in provisioning |
| `pkg/config/provider.go` | Add `PartitionLayout` config field |
| `initrd.Dockerfile` | Ensure `sgdisk`, `mkfs.xfs` available |

## Risks

- **Image format change**: Custom partitioning requires rootfs tarball
  instead of raw disk image. Both formats must be supported. Auto-detect
  via image file extension or magic bytes.
- **LVM complexity**: LVM adds `lvm2` tooling to the initrd (~5 MB).
  Consider making it optional.
- **Partition numbering**: NVMe devices use `p1`, `p2` while SATA/SAS
  use `1`, `2`. Must handle both naming conventions.

## Effort Estimate

- Partition layout types + `sgdisk` execution: **3 days**
- Filesystem formatting: **2 days**
- Rootfs tarball extraction: **2-3 days**
- fstab generation: **1 day**
- LVM support: **3-4 days**
- Testing: **3 days**
- Total: **14-17 days**
