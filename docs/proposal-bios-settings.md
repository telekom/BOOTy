# Proposal: BIOS Settings Management via Redfish

## Status: Fully Implemented

Phase 1 implements BIOS information collection and inclusion in debug dump output.
CAPRF-side Redfish BIOS methods are designed but not yet called from the provisioning flow.

## Priority: P2

## Summary

Add the ability to read, validate, and apply BIOS/UEFI settings to bare-metal
machines via the Redfish `Bios` resource (`/redfish/v1/Systems/1/Bios`). This
enables declarative BIOS configuration as part of the provisioning pipeline —
ensuring consistent settings across fleet machines without manual BIOS console
access.

## Motivation

Inconsistent BIOS settings cause hard-to-diagnose issues:

- **Hyper-threading** disabled on some machines → CPU count mismatch in k8s
- **Virtualization (VT-x/VT-d)** off → KVM/kubevirt failures
- **SR-IOV** disabled → VF creation fails for DPDK workloads
- **Boot order** wrong → machine boots from wrong device after provisioning
- **Power policy** set to "stay off" → machine doesn't auto-power-on after
  outage

Currently, operators must manually configure BIOS on each machine via the iLO
or XCC web console — a process that doesn't scale for large fleets.

### Industry Context

| Tool | BIOS Management |
|------|----------------|
| **Ironic** | `bios` interface with `apply_configuration` step; stores settings in node's `bios` field |
| **MAAS** | Limited — can set boot order via commissioning scripts |
| **Tinkerbell** | No built-in BIOS support |
| **CAPRF + BOOTy** | Not yet supported |

## Design

### Architecture

```
┌──────────────────────────────────────────────────┐
│ CAPRF Controller                                 │
│                                                  │
│  RedfishHost CR:                                 │
│    spec.biosSettings:                            │
│      - name: "HyperThreading"                    │
│        value: "Enabled"                          │
│      - name: "BootMode"                          │
│        value: "Uefi"                             │
│      - name: "SriovGlobalEnable"                 │
│        value: "Enabled"                          │
│                                                  │
│  Provisioning pipeline:                          │
│    applyBIOSSettings() ← new step                │
│    waitForBIOSSettingsPending()                   │
│    rebootForBIOSApply()                          │
│    verifyBIOSSettings()                          │
└──────────────────────────────────────────────────┘
```

### Redfish BIOS API

The standard Redfish BIOS resource provides:

```
GET  /redfish/v1/Systems/1/Bios              → Current BIOS attributes
GET  /redfish/v1/Systems/1/Bios/Settings     → Pending BIOS changes
PATCH /redfish/v1/Systems/1/Bios/Settings    → Apply new settings (pending)
```

BIOS changes are **pending** until the next reboot. The apply flow:

1. `PATCH /Bios/Settings` with desired attributes
2. Reboot the machine
3. BMC applies settings during POST
4. `GET /Bios` to verify new values

### CAPRF Implementation

```go
// internal/redfish/system.go — new interface method
type System interface {
    // ... existing ...
    GetBIOSAttributes() (map[string]string, error)
    SetBIOSAttributes(attrs map[string]interface{}) error
}

func (s *baseSystem) GetBIOSAttributes() (map[string]string, error) {
    bios, err := s.ComputerSystem.Bios()
    if err != nil {
        return nil, fmt.Errorf("get BIOS resource: %w", err)
    }
    result := make(map[string]string, len(bios.Attributes))
    for k, v := range bios.Attributes {
        result[k] = fmt.Sprintf("%v", v)
    }
    return result, nil
}

func (s *baseSystem) SetBIOSAttributes(attrs map[string]interface{}) error {
    bios, err := s.ComputerSystem.Bios()
    if err != nil {
        return fmt.Errorf("get BIOS resource: %w", err)
    }
    return bios.UpdateBiosAttributes(attrs)
}
```

### Provisioning Step

```go
// internal/provision/manager.go — new step
func applyBIOSSettings(settings map[string]interface{}) step {
    return func(j *job) error {
        if len(settings) == 0 {
            return nil
        }
        j.Log.V(1).Info("applying BIOS settings", "count", len(settings))

        current, err := j.system.GetBIOSAttributes()
        if err != nil {
            return fmt.Errorf("get current BIOS: %w", err)
        }

        // Only apply settings that differ
        delta := make(map[string]interface{})
        for k, v := range settings {
            if current[k] != fmt.Sprintf("%v", v) {
                delta[k] = v
            }
        }
        if len(delta) == 0 {
            j.Log.V(1).Info("all BIOS settings already match")
            return nil
        }

        if err := j.system.SetBIOSAttributes(delta); err != nil {
            return fmt.Errorf("apply BIOS settings: %w", err)
        }

        // Reboot to apply pending settings
        if err := j.system.Reboot(); err != nil {
            return fmt.Errorf("reboot for BIOS apply: %w", err)
        }
        return j.system.WaitForPowerState(redfish.OnPowerState)
    }
}
```

### CRD Extension

```go
// api/v1alpha1/redfishhost_types.go
type RedfishHostSpec struct {
    // ... existing fields ...

    // BIOSSettings defines desired BIOS attribute values.
    // Settings are applied before provisioning and verified after reboot.
    // +optional
    BIOSSettings []BIOSSetting `json:"biosSettings,omitempty"`
}

type BIOSSetting struct {
    Name  string `json:"name"`
    Value string `json:"value"`
}
```

### Vendor-Specific Attributes

BIOS attribute names differ per vendor:

| Setting | HPE iLO | Lenovo XCC |
|---------|---------|------------|
| Hyper-Threading | `ProcHyperthreading` | `HyperThreading` |
| Virtualization | `ProcVirtualization` | `VTdSupport` |
| SR-IOV | `SriovGlobalEnable` | `SR-IOV` |
| Boot Mode | `BootMode` | `BootMode` |
| Workload Profile | `WorkloadProfile` | N/A |

A mapping layer or vendor-specific presets can normalize these:

```go
// internal/redfish/bios_presets.go
var VendorPresets = map[string]map[string]string{
    "hpe-k8s-worker": {
        "ProcHyperthreading": "Enabled",
        "ProcVirtualization": "Enabled",
        "SriovGlobalEnable":  "Enabled",
        "BootMode":           "Uefi",
        "WorkloadProfile":    "Virtualization-MaxPerformance",
    },
    "lenovo-k8s-worker": {
        "HyperThreading": "Enable",
        "VTdSupport":     "Enable",
        "SR-IOV":         "Enable",
        "BootMode":       "UEFIMode",
    },
}
```

## Required Binaries in Initramfs

No BOOTy binary changes needed. This proposal is implemented entirely in
the **CAPRF controller** via Redfish HTTP API calls. BOOTy is not directly
involved in reading or writing BIOS settings in this flow.

For a Redfish-less variant where BOOTy handles BIOS locally, see
[Vendor-Specific BIOS Management](proposal-bios-management-vendors.md).

## Affected Files

| File | Change |
|------|--------|
| CAPRF `internal/redfish/system.go` | Add `GetBIOSAttributes()`, `SetBIOSAttributes()` |
| CAPRF `internal/provision/manager.go` | Add `applyBIOSSettings()` step |
| CAPRF `api/v1alpha1/redfishhost_types.go` | Add `BIOSSettings` field |
| CAPRF `internal/redfish/bios_presets.go` | New — vendor preset maps |
| BOOTy (not directly affected) | Could report current BIOS in debug dump |

## Risks

- **Reboot overhead**: Applying BIOS settings requires an extra reboot cycle,
  adding 2-5 minutes. Only apply when settings actually differ from current.
- **Attribute name instability**: Vendor BIOS attribute names can change
  between firmware versions. Need a versioned mapping or user-provided names.
- **Destructive settings**: Changing boot mode from Legacy to UEFI can brick
  existing OS installations. Validate boot mode compatibility before applying.

## Effort Estimate

- Core Redfish BIOS read/write: **3 days**
- CRD extension + controller wiring: **2 days**
- Vendor presets + testing: **3-5 days**
- Total: **8-10 days**
