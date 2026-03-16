# Proposal: NVMe Namespace Management

## Status: Implemented

## Priority: P4

## Summary

Add support for NVMe namespace creation, deletion, and formatting as part
of the provisioning pipeline. Modern NVMe drives support multiple namespaces
— logical partitions of the physical drive with independent block sizes and
protection settings. This enables optimal drive configuration for specific
workloads (e.g., 4K block size for databases, separate namespaces for OS
and data).

## Motivation

Enterprise NVMe drives (Intel P5800X, Samsung PM9A3, Micron 7450) support:

- **Multiple namespaces**: Split a 3.84 TB drive into a 200 GB OS namespace
  and a 3.6 TB data namespace
- **Variable block sizes**: Use 4096-byte blocks for database workloads
  instead of the default 512 bytes
- **Secure erase per namespace**: Wipe only specific namespaces without
  affecting others
- **Namespace sharing**: In multi-path setups, assign namespaces to specific
  controllers

Currently, BOOTy uses NVMe drives as-is (single namespace, default block
size). Deprovisioning calls `blkdiscard` which only erases data, not the
namespace configuration.

### Industry Context

| Tool | NVMe Namespace |
|------|---------------|
| **Ironic** | No namespace management; treats NVMe as simple block device |
| **MAAS** | No namespace support |
| **Tinkerbell** | No namespace support |

This would be a unique capability not offered by any existing provisioner.

## Design

### NVMe Namespace Operations

```go
// pkg/disk/nvme.go
package disk

import (
    "encoding/json"
    "fmt"
)

type NVMeNamespace struct {
    NSID      int    `json:"nsid"`
    Size      uint64 `json:"size"`       // in 512-byte blocks
    Capacity  uint64 `json:"capacity"`
    BlockSize int    `json:"blockSize"`  // 512 or 4096
    Device    string `json:"device"`     // e.g., /dev/nvme0n1
}

type NVMeController struct {
    Device     string `json:"device"`    // e.g., /dev/nvme0
    Model      string `json:"model"`
    Serial     string `json:"serial"`
    Firmware   string `json:"firmware"`
    TotalSize  uint64 `json:"totalSize"`
}

// ListNamespaces returns all namespaces on an NVMe controller.
func (m *Manager) ListNVMeNamespaces(ctx context.Context, controller string) ([]NVMeNamespace, error) {
    out, err := m.cmd.Run(ctx, "nvme", "list-ns", controller, "-o", "json")
    if err != nil {
        return nil, fmt.Errorf("list NVMe namespaces: %w", err)
    }
    var namespaces []NVMeNamespace
    if err := json.Unmarshal(out, &namespaces); err != nil {
        return nil, fmt.Errorf("parse NVMe namespace list: %w", err)
    }
    return namespaces, nil
}

// DeleteNamespace removes an NVMe namespace.
func (m *Manager) DeleteNVMeNamespace(ctx context.Context, controller string, nsid int) error {
    _, err := m.cmd.Run(ctx, "nvme", "delete-ns", controller,
        "--namespace-id", fmt.Sprintf("%d", nsid))
    if err != nil {
        return fmt.Errorf("delete NVMe namespace %d: %w", nsid, err)
    }
    return nil
}

// CreateNamespace creates a new NVMe namespace with the given size and block size.
func (m *Manager) CreateNVMeNamespace(ctx context.Context, controller string, sizeBlocks uint64, blockSize int) (int, error) {
    // Determine the LBA format index for the desired block size
    lbaFormat := 0 // default 512-byte
    if blockSize == 4096 {
        lbaFormat = 1 // typically LBA format 1 for 4K
    }

    out, err := m.cmd.Run(ctx, "nvme", "create-ns", controller,
        "--nsze", fmt.Sprintf("%d", sizeBlocks),
        "--ncap", fmt.Sprintf("%d", sizeBlocks),
        "--flbas", fmt.Sprintf("%d", lbaFormat),
        "--dps", "0",
        "--nmic", "0",
    )
    if err != nil {
        return 0, fmt.Errorf("create NVMe namespace: %w", err)
    }

    // Parse NSID from output
    var nsid int
    fmt.Sscanf(string(out), "create-ns: Success, created nsid:%d", &nsid)
    return nsid, nil
}

// AttachNamespace attaches a namespace to a controller.
func (m *Manager) AttachNVMeNamespace(ctx context.Context, controller string, nsid int) error {
    _, err := m.cmd.Run(ctx, "nvme", "attach-ns", controller,
        "--namespace-id", fmt.Sprintf("%d", nsid),
        "--controllers", "0")
    return err
}

// FormatNamespace securely erases and reformats a namespace.
func (m *Manager) FormatNVMeNamespace(ctx context.Context, device string, blockSize int) error {
    lbaFormat := 0
    if blockSize == 4096 {
        lbaFormat = 1
    }
    _, err := m.cmd.Run(ctx, "nvme", "format", device,
        "--lbaf", fmt.Sprintf("%d", lbaFormat),
        "--ses", "1") // 1 = user data erase
    return err
}
```

### Configuration

```bash
# /deploy/vars
export NVME_NAMESPACE_LAYOUT='[
  {"controller": "/dev/nvme0", "namespaces": [
    {"sizePct": 10, "blockSize": 512, "label": "os"},
    {"sizePct": 90, "blockSize": 4096, "label": "data"}
  ]}
]'
```

### Provisioning Integration

NVMe namespace setup runs early, before disk partitioning:

```go
// pkg/provision/orchestrator.go
steps := []Step{
    {Name: "health-checks", Fn: o.RunHealthChecks},
    {Name: "nvme-namespace-setup", Fn: o.SetupNVMeNamespaces},  // new
    {Name: "disk-wipe", Fn: o.WipeDisk},
    {Name: "stream-image", Fn: o.StreamImage},
    // ...
}
```

### Deprovisioning

During deprovisioning, namespaces can optionally be reset to factory default
(single namespace, 512-byte blocks):

```go
func (o *Orchestrator) ResetNVMeNamespaces(ctx context.Context) error {
    controllers := o.disk.ListNVMeControllers(ctx)
    for _, ctrl := range controllers {
        namespaces, _ := o.disk.ListNVMeNamespaces(ctx, ctrl.Device)
        for _, ns := range namespaces {
            o.disk.DeleteNVMeNamespace(ctx, ctrl.Device, ns.NSID)
        }
        // Create single default namespace
        o.disk.CreateNVMeNamespace(ctx, ctrl.Device, ctrl.TotalSize, 512)
    }
    return nil
}
```

## Required Binaries in Initramfs

| Binary | Package | Purpose | Initramfs Flavor | Already Present? |
|--------|---------|---------|-----------------|------------------|
| `nvme` | `nvme-cli` | NVMe namespace CRUD, format, identify | all | **Yes** |

No new binaries needed. All NVMe namespace operations use the existing
`nvme-cli` tool already in the initramfs.

## Affected Files

| File | Change |
|------|--------|
| `pkg/disk/nvme.go` | New — NVMe namespace operations |
| `pkg/disk/nvme_test.go` | New — unit tests with mock commander |
| `pkg/disk/manager.go` | Wire NVMe operations |
| `pkg/provision/orchestrator.go` | Add `SetupNVMeNamespaces()` step |
| `pkg/config/provider.go` | Add `NVMeNamespaceLayout` config |
| `initrd.Dockerfile` | Ensure `nvme-cli` is available |

## Risks

- **Drive support**: Not all NVMe drives support multiple namespaces. Consumer
  drives typically support only one. Must detect capability first via
  `nvme id-ctrl` and check the `nn` (number of namespaces) field.
- **Data loss**: Namespace deletion is destructive and irreversible. Must
  only run during provisioning/deprovisioning, never during production.
- **LBA format**: The mapping of block sizes to LBA format indices varies by
  drive manufacturer. Must query `nvme id-ns --lba-format-index` to find
  the correct format.
- **Controller reset**: Some drives require a controller reset after namespace
  changes. This adds a brief delay.

## Effort Estimate

- NVMe namespace operations: **3-4 days**
- Capability detection: **1-2 days**
- Provisioning integration: **2 days**
- Testing (requires NVMe hardware): **2-3 days**
- Total: **8-11 days**
