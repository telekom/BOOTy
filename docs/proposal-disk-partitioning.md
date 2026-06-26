# Proposal: Custom Disk Partitioning

## Status: Accepted

Implementation status: Phase 1 groundwork and root image streaming are implemented; tarball extraction and generic non-root mount orchestration remain pending.

Implemented in Phase 1:
- `ParsePartitionLayout` validation (GPT-only schema, root/mountpoint checks,
  fill-remaining position validation)
- `ApplyPartitionLayout` with input guards (nil, empty) plus orchestrator-side
  device validation before destructive steps
  and sgdisk-based GPT partitioning + filesystem formatting
- Optional LVM creation from layout (`pvcreate`/`vgcreate`/`lvcreate`) with
  full input validation (PV bounds, VG/LV name checks)
- `GenerateFstab` + `GenerateLVMFstab` using strings.Builder
- Orchestrator integration: `apply-partition-layout` step, layout-based
  root/ESP resolution, root filesystem streaming into the declared root
  partition, and write-fstab after mount-root
- Comprehensive unit tests with mockCommander for command-sequence verification

Current runtime behavior:
- BOOTy creates the declared GPT layout and, in default `whole-disk` image
  mode, streams the selected root filesystem/source-root partition into the
  declared root mountpoint.
- `provision.image.mode=partition` is rejected with `PARTITION_LAYOUT` because
  that mode copies the source partition table and conflicts with a declarative
  target layout.

Still pending for full proposal closure:
- End-to-end rootfs tarball extraction flow (instead of raw image assumptions)
- Explicit mount orchestration for all non-root declared mountpoints during provisioning

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
    Filesystem string `json:"filesystem"` // ext4, xfs, vfat, swap
    Mountpoint string `json:"mountpoint"` // e.g., "/", "/boot/efi"
    TypeGUID   string `json:"typeGUID"`   // optional explicit GPT type GUID
}

type LVMConfig struct {
    VGName     string      `json:"vgName"`
    PVDevices  []string    `json:"pvDevices"`  // e.g., ["/dev/sda3"]
    Volumes    []LVVolume  `json:"volumes"`
}

type LVVolume struct {
    Name       string `json:"name"`
    SizeMB     int    `json:"sizeMB"` // 0 = remainder
    Filesystem string `json:"filesystem"`
    Mountpoint string `json:"mountpoint"`
}
```

### Configuration

```bash
# /deploy/vars
export PARTITION_LAYOUT='{
  "table": "gpt",
  "partitions": [
    {"label":"efi",  "sizeMB":512,   "filesystem":"vfat", "mountpoint":"/boot/efi"},
    {"label":"root", "sizeMB":51200, "filesystem":"ext4", "mountpoint":"/"}
  ]
}'
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
      table: gpt
      partitions:
        - label: efi
          sizeMB: 512
          filesystem: vfat
          mountpoint: /boot/efi
        - label: root
          sizeMB: 0
          filesystem: ext4
          mountpoint: /
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
Without custom partitioning:
  Stream image → whole disk → partprobe → mount → configure

With custom partitioning:
  Apply partition layout → format → stream selected source root →
  partprobe → mount root and /boot/efi → configure → generate fstab
```

In the implemented path, the image remains a raw/qcow2/compressed/OCI source.
BOOTy selects the source root partition, or copies a plain root filesystem
image, and writes it into the declared root partition. Rootfs tarball extraction
and arbitrary non-root mount orchestration remain future work; non-A/B layouts
currently reject mountpoints other than `/` and `/boot/efi`.

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

- **Image payload scope**: Custom partitioning currently streams raw,
  qcow2, compressed, or OCI source-root payloads into the declared root
  partition. Rootfs tarball extraction remains separate future work.
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
