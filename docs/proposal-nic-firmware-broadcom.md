# Proposal: Broadcom NIC Firmware Management

## Status: Phase 1 Implemented (PR #47)

## Priority: P2

## Dependencies: [NIC Firmware Common Framework](proposal-nic-firmware-common.md)

## Summary

Implement Broadcom BCM57xx (tg3) and BCM57414/57508 (bnxt_en) NIC firmware
parameter management: capture, baseline, diff, and set/unset firmware flags.
Go-first approach using `devlink` netlink API for `bnxt_en` and `ethtool`
ioctls for `tg3`; fallback to `bnxtnvm` for NVM operations.

## Motivation

Broadcom NICs are common in HPE ProLiant, Lenovo ThinkSystem, and Dell
PowerEdge servers (often as LOM — LAN on Motherboard).

| Parameter | Driver | Impact |
|-----------|--------|--------|
| SR-IOV VF count | bnxt_en | VF-based networking |
| Multi-host mode | bnxt_en | NPAR (NIC partitioning) for blade servers |
| NCSI mode | bnxt_en | BMC shared management NIC |
| PXE boot enable | tg3, bnxt_en | UEFI PXE boot capability |
| Wake-on-LAN | tg3 | Remote power management |
| Hardware timestamping | bnxt_en | PTP support |
| RoCE enable | bnxt_en | RDMA-capable NICs (P2100D) |
| TruFlow offload | bnxt_en | Hardware flow offload for OVS |

### Existing Support in BOOTy

| Binary / Module | In Initramfs? | Purpose |
|-----------------|--------------|---------|
| `ethtool` | **Yes** | Driver info, link state, features |
| `devlink` | **No — add** | Devlink parameter management (bnxt_en) |
| `bnxtnvm` | **No — optional** | Broadcom NVM tool (proprietary) |
| `tg3.ko` | **Yes** | BCM57xx 1G driver |
| `bnxt_en.ko` | **Yes** | BCM57414/57508 10/25/50/100G driver |

## Design

### Broadcom Manager

```go
// pkg/firmware/nic/broadcom/manager.go
package broadcom

import (
    "context"
    "fmt"
    "log/slog"

    "github.com/telekom/BOOTy/pkg/firmware/nic"
)

type Manager struct {
    log *slog.Logger
}

func New(log *slog.Logger) *Manager {
    return &Manager{log: log}
}

func (m *Manager) Vendor() nic.Vendor { return nic.VendorBroadcom }

func (m *Manager) Supported(n nic.NICIdentifier) bool {
    return n.VendorID == "14e4"
}

func (m *Manager) Capture(ctx context.Context, n nic.NICIdentifier) (*nic.FirmwareState, error) {
    state := &nic.FirmwareState{
        NIC:        n,
        Vendor:     nic.VendorBroadcom,
        Parameters: make(map[string]nic.Parameter),
    }

    switch n.Driver {
    case "bnxt_en":
        // bnxt_en supports devlink params (kernel 5.8+)
        if err := m.captureViaDevlink(ctx, n, state); err != nil {
            m.log.Info("devlink capture failed for bnxt_en", "error", err)
        }
    case "tg3":
        // tg3 has limited sysfs exposure, use ethtool
        if err := m.captureViaEthtool(ctx, n, state); err != nil {
            m.log.Info("ethtool capture failed for tg3", "error", err)
        }
    }

    // Common sysfs capture for both drivers
    m.captureViaSysfs(n, state)

    return state, nil
}

func (m *Manager) Apply(ctx context.Context, n nic.NICIdentifier, changes []nic.FlagChange) error {
    switch n.Driver {
    case "bnxt_en":
        return m.applyViaDevlink(ctx, n, changes)
    default:
        return fmt.Errorf("apply not supported for driver %s", n.Driver)
    }
}
```

### Key Broadcom Parameters

```go
// pkg/firmware/nic/broadcom/params.go
var criticalParams = []string{
    // bnxt_en devlink params
    "enable_sriov",           // SR-IOV master switch
    "ignore_ari",             // Alternative Routing-ID
    "msix_vec_per_pf_max",   // MSI-X vectors allocation
    "msix_vec_per_pf_min",   // MSI-X vectors minimum
    // sysfs SR-IOV
    "sriov_totalvfs",        // Max VFs (read-only)
    "sriov_numvfs",          // Current VF count
    // ethtool features
    "rx-gro-hw",             // Hardware GRO
    "tx-udp_tnl-segmentation", // VXLAN TSO
    "hw-tc-offload",         // TC flower offload
}
```

### Required Binaries in Initramfs

| Binary | Package | Initramfs Flavor | Already Present? |
|--------|---------|-----------------|-----------------|
| `ethtool` | `ethtool` | all | **Yes** |
| `devlink` | `iproute2` | full, gobgp | **No — add** (shared) |
| `bnxtnvm` | Broadcom download | full only | **No — optional** |

**Note**: `bnxtnvm` is Broadcom's proprietary NVM tool. It is only needed
for deep NVM operations (firmware flash, NVM configuration beyond kernel
driver capabilities). The Go devlink + ethtool approach covers the common
parameter management use cases.

## Files Changed

| File | Change |
|------|--------|
| `pkg/firmware/nic/broadcom/manager.go` | `Manager` implementing `FirmwareManager` |
| `pkg/firmware/nic/broadcom/params.go` | Critical parameter list |
| `pkg/firmware/nic/broadcom/manager_test.go` | Unit tests |

## Testing

### Unit Tests

- `broadcom/manager_test.go`:
  - Table-driven `Capture()` with mock sysfs for BCM57414 (bnxt_en)
    and BCM5720 (tg3)
  - Driver-switch logic: verify bnxt_en uses devlink, tg3 uses ethtool
  - Sysfs SR-IOV parameter reading
  - Apply via devlink with mock netlink

### E2E / KVM Tests

- **KVM matrix** (`kvm-matrix.yml`, tag `e2e_kvm`):
  - QEMU with virtio-net (Broadcom can't be emulated in QEMU)
  - Mock sysfs overlay with BCM57414 PCI vendor/device IDs
  - Verify capture logic exercises both tg3 and bnxt_en code paths

## Risks

| Risk | Mitigation |
|------|------------|
| bnxt_en devlink support requires kernel 5.8+ | Fall back to ethtool-only capture |
| tg3 driver has very limited FW param exposure | Capture what's available, mark limitations |
| bnxtnvm is proprietary | Optional; devlink covers most operations |
| BCM NICs are often LOMs with shared BMC access | Document NCSI interaction risks |

## Effort Estimate

4–6 engineering days (devlink + ethtool capture + tests).
