# Proposal: Mellanox/NVIDIA ConnectX NIC Firmware Management

## Status: Implemented

## Priority: P1

## Dependencies: [NIC Firmware Common Framework](proposal-nic-firmware-common.md)

## Summary

Implement full Mellanox ConnectX-4/5/6/7 NIC firmware parameter management:
capture all FW flags, store baselines, diff against live state, and set/unset
individual parameters. Uses pure Go PCI config space reads where possible,
with `mstconfig`/`mstflint` (already in initramfs) as fallback.

## Motivation

Mellanox ConnectX NICs are the dominant high-speed NIC family in data center
deployments. Their firmware has 100+ configurable parameters that affect
SR-IOV, link type, RoCE, DPDK, and other critical capabilities. BOOTy already
includes `mstconfig` and `mstflint` in the full/gobgp initramfs
(`initrd.Dockerfile` lines 117–118) but doesn't use them programmatically.

Key parameters that need fleet-wide consistency:

| Parameter | Impact |
|-----------|--------|
| `SRIOV_EN` | Enables SR-IOV (required for VF-based networking) |
| `NUM_OF_VFS` | Maximum VFs per port (must match scheduler config) |
| `LINK_TYPE_P1` / `LINK_TYPE_P2` | Ethernet vs InfiniBand mode |
| `ROCE_MODE` | RoCEv1 vs RoCEv2 (impacts RDMA workloads) |
| `PCI_WR_ORDERING` | PCIe write ordering (affects DPDK performance) |
| `CQE_COMPRESSION` | Completion queue compression (performance tuning) |
| `MEMIC_BAR_SIZE` | Device memory BAR size for OVS offload |
| `LAG_RESOURCE_ALLOCATION` | LACP bond offload support |
| `ARI_NODE_EN` | Alternative Routing-ID for VF address space |
| `FLEX_PARSER_PROFILE_ENABLE` | Custom protocol parsing for eSwitch hardware offload |

### Existing Support in BOOTy

| Binary | In Initramfs? | Location |
|--------|--------------|----------|
| `mstconfig` | **Yes** | `bin/mstconfig` (from `mstflint` package) |
| `mstflint` | **Yes** | `bin/mstflint` (from `mstflint` package) |
| `mlx5_core` module | **Yes** | `modules/mlx5_core.ko*` |
| `mlxfw` module | **Yes** | `modules/mlxfw.ko*` |
| `mlx4_core` module | **Yes** | `modules/mlx4_core.ko*` (ConnectX-3 legacy) |
| `devlink` | **No** | Needs adding for devlink param interface |

## Design

### Pure Go Capture (Preferred Path)

ConnectX FW parameters are accessible via:

1. **devlink** — Linux devlink netlink API (`DEVLINK_CMD_PARAM_GET`). The
   `mlx5_core` driver exposes most parameters via devlink since kernel 5.1.
2. **PCI VSEC** — Vendor-Specific Extended Capability in PCI config space,
   used by `mstconfig` internally. Requires direct PCI BAR access.
3. **sysfs** — Some parameters exposed via `/sys/class/net/*/device/sriov_*`
   and `/sys/bus/pci/devices/*/sriov_totalvfs`.

#### Go devlink Implementation

```go
// pkg/firmware/nic/mellanox/manager.go
package mellanox

import (
    "context"
    "fmt"
    "log/slog"

    "github.com/vishvananda/netlink"
    "github.com/telekom/BOOTy/pkg/firmware/nic"
)

// Manager implements nic.FirmwareManager for Mellanox ConnectX NICs.
type Manager struct {
    log *slog.Logger
}

func New(log *slog.Logger) *Manager {
    return &Manager{log: log}
}

func (m *Manager) Vendor() nic.Vendor { return nic.VendorMellanox }

func (m *Manager) Supported(n nic.NICIdentifier) bool {
    return n.VendorID == "15b3" || n.Driver == "mlx5_core" || n.Driver == "mlx4_core"
}

func (m *Manager) Capture(ctx context.Context, n nic.NICIdentifier) (*nic.FirmwareState, error) {
    state := &nic.FirmwareState{
        NIC:        n,
        Vendor:     nic.VendorMellanox,
        Parameters: make(map[string]nic.Parameter),
    }

    // Try devlink first (kernel 5.1+)
    if err := m.captureViaDevlink(ctx, n, state); err != nil {
        m.log.Info("devlink capture failed, falling back to mstconfig", "error", err)
        if err := m.captureViaMstconfig(ctx, n, state); err != nil {
            return nil, fmt.Errorf("capture mellanox firmware: %w", err)
        }
    }

    // Supplement with sysfs data
    m.captureViaSysfs(n, state)

    return state, nil
}

func (m *Manager) captureViaDevlink(ctx context.Context, n nic.NICIdentifier, state *nic.FirmwareState) error {
    // Use netlink devlink API to query device params
    // DEVLINK_CMD_PARAM_GET for pci/<addr>
    params, err := netlink.DevlinkGetDeviceParams("pci", n.PCIAddr)
    if err != nil {
        return fmt.Errorf("devlink param get %s: %w", n.PCIAddr, err)
    }
    for _, p := range params {
        state.Parameters[p.Name] = nic.Parameter{
            Name:         p.Name,
            CurrentValue: fmt.Sprintf("%v", p.Values[0].Data),
            Type:         devlinkTypeToString(p.Type),
        }
    }
    return nil
}
```

#### Mstconfig Fallback

```go
// pkg/firmware/nic/mellanox/mstconfig.go
package mellanox

import (
    "context"
    "fmt"
    "os/exec"
    "strings"

    "github.com/telekom/BOOTy/pkg/firmware/nic"
)

func (m *Manager) captureViaMstconfig(ctx context.Context, n nic.NICIdentifier, state *nic.FirmwareState) error {
    // mstconfig -d <pciAddr> query — outputs "KEY = VALUE (DEFAULT)" lines
    cmd := exec.CommandContext(ctx, "mstconfig", "-d", n.PCIAddr, "query")
    out, err := cmd.Output()
    if err != nil {
        return fmt.Errorf("mstconfig query %s: %w", n.PCIAddr, err)
    }
    for _, line := range strings.Split(string(out), "\n") {
        name, param, ok := parseMstconfigLine(line)
        if ok {
            state.Parameters[name] = param
        }
    }
    return nil
}

func (m *Manager) Apply(ctx context.Context, n nic.NICIdentifier, changes []nic.FlagChange) error {
    // Try devlink first for supported params
    var mstconfigArgs []string
    for _, c := range changes {
        if err := m.applyViaDevlink(ctx, n, c); err != nil {
            // Queue for mstconfig fallback
            mstconfigArgs = append(mstconfigArgs, fmt.Sprintf("%s=%s", c.Name, c.Value))
        }
    }
    if len(mstconfigArgs) == 0 {
        return nil
    }
    // mstconfig -d <pciAddr> -y set KEY1=VAL1 KEY2=VAL2
    args := append([]string{"-d", n.PCIAddr, "-y", "set"}, mstconfigArgs...)
    cmd := exec.CommandContext(ctx, "mstconfig", args...)
    if out, err := cmd.CombinedOutput(); err != nil {
        return fmt.Errorf("mstconfig set on %s: %s: %w", n.PCIAddr, string(out), err)
    }
    return nil
}
```

### Key Mellanox Parameters

Full parameter list captured by default:

```go
// pkg/firmware/nic/mellanox/params.go
var criticalParams = []string{
    "SRIOV_EN",
    "NUM_OF_VFS",
    "LINK_TYPE_P1",
    "LINK_TYPE_P2",
    "ROCE_MODE",
    "PCI_WR_ORDERING",
    "CQE_COMPRESSION",
    "MEMIC_BAR_SIZE",
    "LAG_RESOURCE_ALLOCATION",
    "ARI_NODE_EN",
    "FLEX_PARSER_PROFILE_ENABLE",
    "REAL_TIME_CLOCK_ENABLE",
    "PF_LOG_BAR_SIZE",
    "VF_LOG_BAR_SIZE",
    "UCTX_EN",
    "EXP_ROM_UEFI_x86_ENABLE",
    "BOOT_OPTION_ROM_EN_P1",
    "BOOT_OPTION_ROM_EN_P2",
}
```

### Required Binaries in Initramfs

| Binary | Package | Initramfs Flavor | Already Present? |
|--------|---------|-----------------|-----------------|
| `mstconfig` | `mstflint` | full, gobgp | **Yes** |
| `mstflint` | `mstflint` | full, gobgp | **Yes** |
| `devlink` | `iproute2` | full, gobgp | **No — add** |

**Kernel modules** (already present):
- `mlx5_core.ko` — ConnectX-4/5/6/7 driver
- `mlxfw.ko` — Mellanox firmware flash module
- `mlx4_core.ko` — ConnectX-3 (legacy)

**Dockerfile change** (tools stage — shared with common framework):

```dockerfile
RUN apt-get update && apt-get install -y --no-install-recommends \
    ... existing packages ... \
    iproute2 \   # already present — devlink binary is part of iproute2
    && rm -rf /var/lib/apt/lists/*

# devlink is provided by iproute2 but needs explicit COPY
COPY --from=tools /sbin/devlink bin/devlink
```

## Files Changed

| File | Change |
|------|--------|
| `pkg/firmware/nic/mellanox/manager.go` | `Manager` implementing `FirmwareManager` |
| `pkg/firmware/nic/mellanox/mstconfig.go` | `mstconfig` fallback capture/apply |
| `pkg/firmware/nic/mellanox/params.go` | Critical parameter list |
| `pkg/firmware/nic/mellanox/manager_test.go` | Unit tests |
| `initrd.Dockerfile` | Add `devlink` binary to full/gobgp flavors |

## Testing

### Unit Tests

- `mellanox/manager_test.go`:
  - Table-driven `Capture()` with mock sysfs trees for ConnectX-5/6/7
  - Table-driven `Apply()` with mock `exec.CommandContext` via Commander interface
  - `parseMstconfigLine()` tests with real mstconfig output samples
  - Devlink capture with mock netlink responses

### E2E / KVM Tests

- **KVM matrix** (`kvm-matrix.yml`, tag `e2e_kvm`):
  - QEMU with mlx5 vfio-pci passthrough (when hardware available)
  - QEMU with virtio-net + mock sysfs overlay for CI without real hardware
  - Verify: BOOTy boots → captures NIC FW state → diffs against baseline →
    reports to mock CAPRF HTTP server
  - Verify: mstconfig fallback executes when devlink unavailable

### Integration Tests

- **ContainerLab** (tag `e2e_integration`): Exercise capture code path
  on virtio interfaces. Expected result: unsupported vendor (validates
  error handling).

## Risks

| Risk | Mitigation |
|------|------------|
| devlink API differs between kernel versions | Fall back to mstconfig binary |
| mstconfig output format changes | Pin mstflint version in Dockerfile, add format tests |
| FW parameter change requires cold reboot | CAPRF coordination: report "reboot-required" flag |
| ConnectX-3 (mlx4) uses different tool | Detect via PCI device ID, use different code path |
| QEMU can't emulate mlx5 without passthrough | Mock sysfs approach for CI |

## Effort Estimate

6–10 engineering days (Go devlink + mstconfig fallback + parameter list +
tests + CAPRF integration).
