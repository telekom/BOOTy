# Proposal: Vendor-Specific BMC Integrations (HPE iLO & Lenovo XCC)

## Status: Proposal

## Priority: P0 (HPE), P2 (Lenovo)

## Summary

Enable and extend vendor-specific BMC integrations for **HPE iLO** (ProLiant)
and **Lenovo XCC** (ThinkSystem). CAPRF already has an `HPESystem` struct in
`internal/redfish/system_hpe.go` with a vendor-specific `SetBootTargetOnce()`
override, but it is **disabled** — `system.go` lines 28-30 return `&base`
instead of `&HPESystem{baseSystem: base}`. This proposal covers:

1. **Re-enable HPE iLO detection** in CAPRF
2. **Add Lenovo XCC vendor type** with ThinkSystem-specific overrides
3. **Vendor-aware Redfish quirks** in BOOTy's CAPRF client
4. **NIC detection heuristics** per vendor in BOOTy's initrd

## Motivation

Standard Redfish is sufficient for basic operations, but HPE and Lenovo BMCs
have vendor-specific behaviors that require special handling:

| Vendor | Quirk | Impact |
|--------|-------|--------|
| HPE iLO 5 | Virtual media requires `Oem.Hpe.BootOnNextServerReset` | Boot fails without it |
| HPE iLO 5 | ETag matching is broken for SecureBoot PATCH | Disabled via `DisableEtagMatch(true)` |
| HPE iLO 6 | Session timeout uses different OEM path | Session may expire during long provisions |
| Lenovo XCC | Virtual media CD path differs (`/redfish/v1/Managers/1/VirtualMedia/2`) | Wrong media slot selected |
| Lenovo XCC | Boot override uses `UefiTarget` instead of  `BootSourceOverrideTarget` for UEFI | Boot target not set correctly |
| Lenovo XCC | Power state polling needs longer intervals | Transient errors during power transitions |

### Current State in CAPRF

```go
// internal/redfish/system.go lines 27-33
func NewSystem(cs *redfish.ComputerSystem) System {
    base := baseSystem{cs}
    if cs.Manufacturer == "HPE" {
        return &base                           // ← DISABLED
        //return &HPESystem{baseSystem: base}  // ← should be this
    }
    return &base
}
```

The `HPESystem` override in `system_hpe.go` correctly patches virtual media
with the OEM `BootOnNextServerReset` flag, but is never instantiated.

## Design

### Phase 1: Re-enable HPE iLO (P0)

1. Uncomment the HPE return path in `NewSystem()`.
2. Add integration tests with HPE iLO mock responses.
3. Verify `SetBootTargetOnce()` works with iLO 5 and iLO 6.

```go
func NewSystem(cs *redfish.ComputerSystem) System {
    base := baseSystem{cs}
    switch cs.Manufacturer {
    case "HPE":
        return &HPESystem{baseSystem: base}
    case "Lenovo":
        return &LenovoSystem{baseSystem: base}
    default:
        return &base
    }
}
```

### Phase 2: Add Lenovo XCC Support (P2)

```go
// internal/redfish/system_lenovo.go
package redfish

import "fmt"

type LenovoSystem struct {
    baseSystem
}

// Lenovo XCC requires UefiTarget for UEFI boot override
func (s *LenovoSystem) SetBootTargetOnce(vm VirtualMedium) error {
    if err := s.ForceOff(); err != nil {
        return fmt.Errorf("shut down server: %w", err)
    }
    if err := s.WaitForPowerState(redfish.OffPowerState); err != nil {
        return fmt.Errorf("wait for power off: %w", err)
    }
    if err := s.Refresh(); err != nil {
        return fmt.Errorf("refresh system: %w", err)
    }
    return s.SetBoot(redfish.Boot{
        BootSourceOverrideEnabled: redfish.OnceBootSourceOverrideEnabled,
        BootSourceOverrideTarget:  redfish.CdBootSourceOverrideTarget,
        UefiTargetBootSourceOverride: vm.ODataID,
    })
}
```

### Phase 3: BOOTy NIC Detection per Vendor (P1)

BOOTy's initrd needs to load the correct NIC drivers. Vendor detection
via DMI/SMBIOS data:

```go
// pkg/system/vendor.go
func DetectVendor() string {
    data, err := os.ReadFile("/sys/class/dmi/id/sys_vendor")
    if err != nil {
        return "unknown"
    }
    vendor := strings.TrimSpace(string(data))
    switch {
    case strings.Contains(vendor, "HPE"), strings.Contains(vendor, "HP"):
        return "hpe"
    case strings.Contains(vendor, "Lenovo"):
        return "lenovo"
    default:
        return "generic"
    }
}
```

Vendor-specific module sets:

| Vendor | Extra Modules |
|--------|--------------|
| HPE | `hpilo`, `hpwdt`, `ilo_hwmon` |
| Lenovo | `ibm_rtl`, `lenovo-sl-laptop` (server variants) |

### Phase 4: Vendor OEM Redfish Extensions

For advanced operations (firmware update, health monitoring), both vendors
expose OEM extensions under `/redfish/v1/Systems/1/Oem/`:

**HPE iLO**:
- `Oem.Hpe.Links.SmartStorage` — storage controller details
- `Oem.Hpe.Links.NetworkAdapters` — detailed NIC info
- `Oem.Hpe.AggregateHealthStatus` — overall health
- `Oem.Hpe.PowerOnMinutes` — uptime

**Lenovo XCC**:
- `Oem.Lenovo.SystemStatus` — overall status
- `Oem.Lenovo.FrontPanelUSB` — USB management
- `Oem.Lenovo.BIOSSettings` — direct BIOS attribute access

## Affected Files

| File | Change |
|------|--------|
| CAPRF `internal/redfish/system.go` | Uncomment HPE, add Lenovo switch |
| CAPRF `internal/redfish/system_hpe.go` | Extend with iLO 6 quirks |
| CAPRF `internal/redfish/system_lenovo.go` | New file — Lenovo overrides |
| BOOTy `pkg/system/vendor.go` | New file — DMI vendor detection |
| BOOTy `main.go` | Vendor-specific module loading |
| BOOTy `initrd.Dockerfile` | Add HPE/Lenovo kernel modules |

## Risks

- **iLO version matrix**: iLO 5 (Gen10) and iLO 6 (Gen10+/Gen11) have
  different OEM schemas. Need test coverage for both.
- **XCC firmware versions**: Lenovo XCC 1.x vs 2.x may differ in Redfish
  conformance. Early XCC versions had incomplete Redfish support.
- **Module availability**: Vendor-specific kernel modules may not be in the
  initrd's kernel build. Verify against the kernel config.

## Testing

- Mock Redfish responses per vendor in CAPRF unit tests.
- BOOTy integration tests with DMI override (`/sys/class/dmi/id/` can be
  mocked via bind mounts in test containers).
- E2E validation on real HPE ProLiant DL360 Gen10+ and Lenovo ThinkSystem
  SR650 V2 hardware.

## Effort Estimate

- Phase 1 (HPE re-enable): **1 day** — uncomment + test
- Phase 2 (Lenovo XCC): **3-5 days** — new vendor type + quirk testing
- Phase 3 (NIC detection): **2 days** — DMI parsing + module sets
- Phase 4 (OEM extensions): **5-7 days** — per-vendor OEM parsing
