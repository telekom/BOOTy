# Proposal: NIC Firmware Management — Common Framework

## Status: Implemented

## Priority: P1

## Summary

Introduce a vendor-agnostic NIC firmware management framework that supports
four operating modes: **capture** (read all FW parameters), **baseline**
(store a golden config), **diff** (compare live vs baseline), and
**apply** (set/unset individual FW flags). Vendor-specific implementations
(Mellanox, Intel, Broadcom) plug into this common interface via PCI vendor
ID auto-detection.

## Motivation

NIC firmware parameters directly affect provisioning outcomes:

- **SR-IOV not enabled** → VF creation fails for DPDK/network workloads
- **Wrong link type** (Ethernet vs InfiniBand) → NIC invisible to provisioner
- **LLDP agent enabled in NIC FW** → conflicts with OS-level LLDP daemon
- **Stale DDP profile** → Intel ice driver doesn't support required protocols
- **Incorrect VF count** → scheduler admits too many/few network-bound pods

Per-NIC FW drift across a fleet causes hard-to-diagnose failures. A capture +
baseline + diff workflow enables fleet consistency validation before
provisioning, and targeted flag setting during provisioning.

### Industry Context

| Tool | NIC FW Management |
|------|-------------------|
| **Ironic** | None — NIC firmware is out of scope |
| **MAAS** | Limited — can run custom commissioning scripts with vendor tools |
| **Tinkerbell** | None — would require custom actions |
| **Vendors** | Each provides CLI tools: `mstconfig` (Mellanox), `nvmupdate64e` (Intel), `bnxtnvm` (Broadcom) |

BOOTy can be the first bare-metal provisioner with built-in NIC FW management.

## Design

### Common Interface

```go
// pkg/firmware/nic/manager.go
package nic

import "context"

// Vendor identifies a NIC silicon vendor by PCI vendor ID.
type Vendor string

const (
    VendorMellanox Vendor = "mellanox" // PCI 0x15b3
    VendorIntel    Vendor = "intel"    // PCI 0x8086
    VendorBroadcom Vendor = "broadcom" // PCI 0x14e4
)

// FirmwareManager is the vendor-agnostic NIC firmware interface.
type FirmwareManager interface {
    // Vendor returns the vendor this manager handles.
    Vendor() Vendor

    // Capture reads all firmware parameters from the NIC and returns
    // a structured state snapshot.
    Capture(ctx context.Context, nic NICIdentifier) (*FirmwareState, error)

    // Apply sets or unsets firmware flags on the NIC. Changes typically
    // require a cold reboot (not kexec) to take effect.
    Apply(ctx context.Context, nic NICIdentifier, changes []FlagChange) error

    // Supported returns true if this manager can handle the given NIC.
    Supported(nic NICIdentifier) bool
}

// NICIdentifier locates a NIC by PCI address and interface name.
type NICIdentifier struct {
    PCIAddr   string // e.g. "0000:03:00.0"
    Interface string // e.g. "enp3s0f0"
    VendorID  string // PCI vendor ID hex, e.g. "15b3"
    DeviceID  string // PCI device ID hex, e.g. "1017"
    Driver    string // kernel driver, e.g. "mlx5_core"
}

// FirmwareState is a vendor-agnostic snapshot of NIC firmware parameters.
type FirmwareState struct {
    NIC        NICIdentifier          `json:"nic"`
    Vendor     Vendor                 `json:"vendor"`
    FWVersion  string                 `json:"fwVersion"`
    Parameters map[string]Parameter   `json:"parameters"`
    Raw        map[string]string      `json:"raw,omitempty"` // vendor-specific extras
}

// Parameter represents a single firmware parameter.
type Parameter struct {
    Name         string `json:"name"`
    CurrentValue string `json:"currentValue"`
    DefaultValue string `json:"defaultValue,omitempty"`
    Type         string `json:"type"` // "bool", "int", "enum", "string"
    ReadOnly     bool   `json:"readOnly,omitempty"`
}

// FlagChange requests a parameter modification.
type FlagChange struct {
    Name  string `json:"name"`
    Value string `json:"value"` // empty string = reset to default
}
```

### Baseline and Diff

```go
// pkg/firmware/nic/baseline.go
package nic

// Baseline represents a golden NIC firmware configuration.
type Baseline struct {
    Vendor     Vendor               `json:"vendor"`
    DeviceID   string               `json:"deviceId"`
    FWVersion  string               `json:"fwVersion,omitempty"`
    Parameters map[string]string    `json:"parameters"` // name → expected value
}

// Diff compares a live FirmwareState against a Baseline.
type Diff struct {
    NIC     NICIdentifier `json:"nic"`
    Matches bool          `json:"matches"`
    Changes []DiffEntry   `json:"changes,omitempty"`
}

// DiffEntry describes one parameter difference.
type DiffEntry struct {
    Name     string `json:"name"`
    Expected string `json:"expected"`
    Actual   string `json:"actual"`
}

// Compare returns the diff between a baseline and the current NIC state.
func Compare(baseline *Baseline, state *FirmwareState) *Diff {
    diff := &Diff{NIC: state.NIC, Matches: true}
    for name, expected := range baseline.Parameters {
        param, ok := state.Parameters[name]
        if !ok || param.CurrentValue != expected {
            actual := ""
            if ok {
                actual = param.CurrentValue
            }
            diff.Changes = append(diff.Changes, DiffEntry{
                Name:     name,
                Expected: expected,
                Actual:   actual,
            })
            diff.Matches = false
        }
    }
    return diff
}
```

### Auto-Detection and Registry

```go
// pkg/firmware/nic/detect.go
package nic

import (
    "fmt"
    "log/slog"
    "os"
    "path/filepath"
    "strings"
)

// pciVendorMap maps PCI vendor IDs to vendor constants.
var pciVendorMap = map[string]Vendor{
    "0x15b3": VendorMellanox,
    "0x8086": VendorIntel,
    "0x14e4": VendorBroadcom,
}

// DetectVendor reads the PCI vendor ID from sysfs.
func DetectVendor(pciAddr string) (Vendor, error) {
    vendorPath := filepath.Join("/sys/bus/pci/devices", pciAddr, "vendor")
    data, err := os.ReadFile(vendorPath)
    if err != nil {
        return "", fmt.Errorf("read PCI vendor for %s: %w", pciAddr, err)
    }
    id := strings.TrimSpace(string(data))
    vendor, ok := pciVendorMap[id]
    if !ok {
        return "", fmt.Errorf("unsupported NIC vendor ID: %s", id)
    }
    return vendor, nil
}

// Registry holds vendor-specific firmware managers.
type Registry struct {
    managers map[Vendor]FirmwareManager
    log      *slog.Logger
}

// NewRegistry creates a registry with all compiled-in vendor managers.
func NewRegistry(log *slog.Logger) *Registry {
    return &Registry{
        managers: make(map[Vendor]FirmwareManager),
        log:      log,
    }
}

// Register adds a vendor-specific manager.
func (r *Registry) Register(mgr FirmwareManager) {
    r.managers[mgr.Vendor()] = mgr
}

// ForNIC returns the appropriate manager for a NIC, auto-detecting vendor.
func (r *Registry) ForNIC(nic NICIdentifier) (FirmwareManager, error) {
    vendor, err := DetectVendor(nic.PCIAddr)
    if err != nil {
        return nil, err
    }
    mgr, ok := r.managers[vendor]
    if !ok {
        return nil, fmt.Errorf("no firmware manager registered for vendor: %s", vendor)
    }
    return mgr, nil
}
```

### CAPRF Integration

```go
// pkg/caprf/client.go — new method
func (c *Client) ReportNICFirmware(ctx context.Context, data []byte) error {
    if c.cfg.NICFirmwareURL == "" {
        c.log.Debug("No NIC firmware URL configured, skipping report")
        return nil
    }
    return c.postJSONWithAuth(ctx, c.cfg.NICFirmwareURL, data)
}
```

### Configuration

```bash
# /deploy/vars
export NIC_FIRMWARE_ENABLED="true"
export NIC_FIRMWARE_URL="https://caprf.example.com/status/nic-firmware"
export NIC_FIRMWARE_BASELINE='{"vendor":"mellanox","deviceId":"1017","parameters":{"SRIOV_EN":"True","NUM_OF_VFS":"32"}}'
export NIC_FIRMWARE_MODE="capture"          # "capture", "baseline", "diff", "apply"
export NIC_FIRMWARE_CHANGES='[{"name":"SRIOV_EN","value":"True"},{"name":"NUM_OF_VFS","value":"32"}]'
```

### Provisioning Integration

NIC firmware management runs as an early provisioning step (after inventory,
before health checks) to ensure NIC parameters are correct before
network setup:

```
report-init → collect-inventory → collect-firmware → **nic-firmware** →
health-checks → set-hostname → ...
```

### Required Binaries in Initramfs

All binaries listed below are fallbacks — the Go implementation is preferred.
Include in the `tools` build stage and the `full` / `gobgp` initramfs flavors:

| Binary | Package | Purpose | Already in initrd? |
|--------|---------|---------|-------------------|
| `mstconfig` | `mstflint` | Mellanox FW parameter query/set | **Yes** |
| `mstflint` | `mstflint` | Mellanox FW flash/query | **Yes** |
| `devlink` | `iproute2` | Intel/Broadcom NIC devlink params | **No — add** |
| `ethtool` | `ethtool` | NIC driver/FW info query | **Yes** |

**Dockerfile change** (tools stage):

```dockerfile
# Add devlink for NIC firmware parameter management (Intel ice/i40e, Broadcom bnxt_en)
COPY --from=tools /sbin/devlink bin/devlink
```

## Files Changed

| File | Change |
|------|--------|
| `pkg/firmware/nic/manager.go` | `FirmwareManager` interface, types |
| `pkg/firmware/nic/baseline.go` | Baseline/Diff types and `Compare()` |
| `pkg/firmware/nic/detect.go` | Vendor auto-detection from PCI sysfs |
| `pkg/firmware/nic/registry.go` | Manager registry |
| `pkg/caprf/client.go` | `ReportNICFirmware()` method |
| `pkg/config/provider.go` | NIC firmware config fields |
| `pkg/provision/orchestrator.go` | `nicFirmware()` step |
| `initrd.Dockerfile` | Add `devlink` binary |

## Testing

### Unit Tests

- `pkg/firmware/nic/detect_test.go` — Vendor detection from mock
  sysfs (`t.TempDir()` with vendor files). Table-driven: known vendor IDs,
  unknown vendor IDs, missing sysfs entries.
- `pkg/firmware/nic/baseline_test.go` — `Compare()` with table-driven
  cases: exact match, single diff, multiple diffs, missing parameters,
  extra live parameters.
- `pkg/firmware/nic/registry_test.go` — Register/ForNIC with mock
  managers. Verify correct manager dispatched per PCI vendor ID.

### E2E Tests

- **ContainerLab** (tag `e2e_integration`): Verify NIC firmware capture
  runs without error on virtio interfaces (will report "unsupported vendor"
  — test exercises the code path).
- **KVM matrix** (tag `e2e_kvm`, new `kvm-matrix.yml`):
  - QEMU with `-device virtio-net-pci` or passthrough NIC
  - BOOTy boots, runs NIC firmware capture, reports to mock CAPRF server
  - Verify JSON inventory contains NIC firmware state

## Risks

| Risk | Mitigation |
|------|------------|
| Vendor tool not in initramfs | Go-first approach; binary is fallback only |
| NIC FW change requires cold reboot | Document; step reports "reboot required" to CAPRF |
| Parameter names differ between FW versions | Vendor-specific normalization in each manager |
| PCI passthrough unavailable in CI | Mock sysfs for unit tests; KVM for integration |

## Effort Estimate

5–8 engineering days for framework + auto-detection + CAPRF integration.
Vendor-specific implementations estimated separately.
