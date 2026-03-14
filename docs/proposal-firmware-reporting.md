# Proposal: Firmware Version Reporting and Validation

## Status: Implemented (PR #19)

## Priority: P2

## Summary

Collect and report firmware versions (BIOS, BMC/iLO/XCC, NIC, storage
controller) from both Redfish and sysfs during provisioning. Optionally
validate that firmware meets minimum version requirements before
provisioning proceeds.

## Implementation Details

The sysfs-based firmware collection is fully implemented. Key decisions
and deviations from the original proposal:

- **Package**: `pkg/firmware/` (not `pkg/inventory/`) to keep firmware
  concerns separate from hardware inventory.
- **Types**: `Report`, `Version`, `NICFirmware`, `StorageFirmware` in
  `pkg/firmware/types.go`.
- **Collection**: `Collect()` reads BIOS, BMC, NIC, and storage firmware
  from sysfs (`/sys/class/dmi/id`, `/sys/class/net`, `/sys/class/scsi_host`).
- **NIC filtering**: Physical NICs identified by presence of `device/`
  directory (PCI-backed), virtual interfaces filtered automatically.
- **CAPRF reporting**: `Client.ReportFirmware()` POSTs JSON to the
  configured firmware URL with retry logic.
- **Orchestrator integration**: Firmware collection runs as a provisioning
  step, with best-effort reporting.
- **Validation**: Firmware version validation (minimum version policy) is
  implemented via `firmware.Validate()` — checks BIOS and BMC versions
  against configurable minimums.
- **Redfish-side collection**: CAPRF-side Redfish firmware enrichment is
  **not yet implemented** — deferred to CAPRF work.

### Files Changed

| File | Change |
|------|--------|
| `pkg/firmware/collector.go` | Sysfs firmware collection |
| `pkg/firmware/collector_test.go` | Unit tests with mock sysfs |
| `pkg/firmware/types.go` | Data types |
| `pkg/caprf/client.go` | `ReportFirmware()`, `withRetry()` helper |
| `pkg/caprf/client_test.go` | Tests for firmware reporting |
| `pkg/config/provider.go` | `FirmwareURL` config field |
| `pkg/provision/orchestrator.go` | Firmware step integration |

## Motivation

Outdated firmware causes:

- **Security vulnerabilities**: Unpatched BMC firmware (e.g., CVE-2023-39266
  for HPE iLO, CVE-2023-4218 for Lenovo XCC)
- **Compatibility issues**: Certain kernel versions require minimum NIC
  firmware for features like SR-IOV or RDMA
- **Hardware bugs**: Known firmware bugs cause random reboots, memory errors,
  or storage corruption
- **Compliance**: Some standards require documented firmware versions

Currently, there is no visibility into what firmware versions are running
across the fleet.

### Industry Context

| Tool | Firmware Reporting |
|------|-------------------|
| **Ironic** | Reads firmware info via IPMI/Redfish during inspection; `firmware` interface for updates |
| **MAAS** | Collects firmware versions during commissioning via `fwupd` |
| **Tinkerbell** | No firmware support |

## Design

### Data Collection

Firmware versions come from two sources:

**Source 1: Redfish API (BMC-side)**

```
GET /redfish/v1/Systems/1           → BIOS version
GET /redfish/v1/Managers/1          → BMC firmware version
GET /redfish/v1/Systems/1/Storage   → Storage controller firmware
GET /redfish/v1/Systems/1/NetworkInterfaces → NIC firmware
```

**Source 2: sysfs/procfs (OS-side, during BOOTy initrd)**

```
/sys/class/dmi/id/bios_version          → BIOS version
/sys/class/dmi/id/bios_date             → BIOS date
/sys/class/net/*/device/firmware_version → NIC firmware
/sys/class/scsi_host/*/firmware_rev      → HBA firmware
```

### Data Model

```go
// pkg/inventory/firmware.go
type FirmwareReport struct {
    BIOS       FirmwareVersion   `json:"bios"`
    BMC        FirmwareVersion   `json:"bmc"`
    NICs       []NICFirmware     `json:"nics"`
    Storage    []StorageFirmware `json:"storage"`
    CollectedAt time.Time        `json:"collectedAt"`
}

type FirmwareVersion struct {
    Component string `json:"component"`
    Version   string `json:"version"`
    Date      string `json:"date,omitempty"`
    Vendor    string `json:"vendor,omitempty"`
}

type NICFirmware struct {
    Interface string `json:"interface"`
    Driver    string `json:"driver"`
    Version   string `json:"version"`
    PCIAddr   string `json:"pciAddr"`
}

type StorageFirmware struct {
    Controller string `json:"controller"`
    Model      string `json:"model"`
    Version    string `json:"version"`
}
```

### Validation

Optional minimum version enforcement:

```go
// pkg/health/firmware.go
type FirmwarePolicy struct {
    MinBIOSVersion string            `json:"minBiosVersion,omitempty"`
    MinBMCVersion  string            `json:"minBmcVersion,omitempty"`
    MinNICVersions map[string]string `json:"minNicVersions,omitempty"` // driver → version
}

func ValidateFirmware(report FirmwareReport, policy FirmwarePolicy) []CheckResult {
    var results []CheckResult

    if policy.MinBIOSVersion != "" {
        if semver.Compare(report.BIOS.Version, policy.MinBIOSVersion) < 0 {
            results = append(results, CheckResult{
                Name:    "firmware-bios",
                Status:  "fail",
                Message: fmt.Sprintf("BIOS %s < minimum %s", report.BIOS.Version, policy.MinBIOSVersion),
            })
        }
    }
    // ... similar for BMC, NICs, storage
    return results
}
```

### Configuration

```bash
# /deploy/vars
export FIRMWARE_REPORT="true"
export FIRMWARE_MIN_BIOS="U46"    # HPE iLO BIOS version
export FIRMWARE_MIN_BMC="2.72"     # HPE iLO firmware version
```

### CAPRF Integration

Firmware report stored in the `RedfishHost` status and available for
fleet-wide firmware dashboards:

```go
// api/v1alpha1/redfishhost_types.go
type RedfishHostStatus struct {
    Conditions []metav1.Condition `json:"conditions,omitempty"`
    Firmware   *FirmwareReport    `json:"firmware,omitempty"`
}
```

## Required Binaries in Initramfs

All required binaries are already present:

| Binary | Package | Purpose | Initramfs Flavor | Already Present? |
|--------|---------|---------|-----------------|------------------|
| `dmidecode` | `dmidecode` | BIOS/BMC version from SMBIOS tables | all | **Yes** |
| `ethtool` | `ethtool` | NIC firmware version (`ethtool -i`) | all | **Yes** |
| `mstflint` | `mstflint` | Mellanox NIC firmware version | full, gobgp | **Yes** |
| `nvme` | `nvme-cli` | NVMe controller firmware version | all | **Yes** |
| `hdparm` | `hdparm` | SATA disk firmware version | all | **Yes** |

## Affected Files

| File | Change |
|------|--------|
| `pkg/inventory/firmware.go` | New — firmware collection from sysfs |
| `pkg/inventory/firmware_test.go` | New — unit tests |
| `pkg/health/firmware.go` | New — version validation checks |
| `pkg/caprf/client.go` | Add `ReportFirmware()` |
| `pkg/config/provider.go` | Add firmware policy config fields |
| CAPRF `internal/redfish/firmware.go` | New — Redfish firmware collection |
| CAPRF `api/v1alpha1/redfishhost_types.go` | Add `Firmware` to status |

## Risks

- **Version format variance**: BIOS versions are not always semver-compatible
  (e.g., HPE uses "U46 v2.72", Lenovo uses "IVE156X-2.93"). Need
  vendor-specific parsing.
- **Redfish firmware endpoints**: Not all BMCs expose storage/NIC firmware
  via Redfish. Fall back to sysfs data.

## Effort Estimate

- sysfs firmware collection: **2 days**
- Redfish firmware collection (CAPRF): **2-3 days**
- Version validation: **2 days**
- Testing: **2 days**
- Total: **8-10 days**
