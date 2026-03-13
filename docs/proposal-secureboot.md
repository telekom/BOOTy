# Proposal: SecureBoot Lifecycle Management

## Status: Phase 1 Implemented (Status Reporting)

Phase 1 implements: `SECUREBOOT_REENABLE` config flag, `requestSecureBootReEnable`
provisioning step that reports the re-enable request to CAPRF via status.
The actual Redfish-based re-enablement happens on the CAPRF controller side.
Not yet implemented: MOK enrollment, efivarfs direct manipulation.

## Priority: P0

## Summary

Implement full SecureBoot lifecycle management in BOOTy: **re-enable SecureBoot
after provisioning** and optionally **enroll MOK (Machine Owner Key) keys** for
custom kernel/module signing. Currently, CAPRF disables SecureBoot before
booting the provisioner ramdisk (`disableSecureBoot()` step in
`internal/provision/manager.go`) but never re-enables it post-provisioning.
This leaves production machines running without SecureBoot — a significant
security gap.

## Motivation

SecureBoot ensures that only cryptographically signed boot code executes during
the UEFI boot process. Leaving it disabled after provisioning exposes machines
to bootkits, rootkits, and supply-chain attacks on the boot chain.

| Concern | Current State | Proposed |
|---------|--------------|----------|
| Pre-provision | CAPRF disables via Redfish | No change |
| Post-provision | Stays disabled forever | BOOTy re-enables via `efivarfs` |
| MOK keys | Not supported | Optional enrollment via `mokutil` |
| Key rotation | Not supported | Future: CAPRF-driven key update |

### Industry Context

- **Ironic**: Supports SecureBoot toggle via `secure_boot` deploy step.
- **MAAS**: Manages SecureBoot at commissioning time.
- **Tinkerbell**: No built-in SecureBoot support.

BOOTy can exceed Ironic by not just toggling, but also enrolling custom keys.

## Design

### Phase 1: Re-enable SecureBoot (P0)

After provisioning completes (image written, EFI boot entry created), BOOTy
re-enables SecureBoot via `efivarfs`:

```go
// pkg/provision/orchestrator.go — added as final provisioning step

func (o *Orchestrator) EnableSecureBoot(ctx context.Context) error {
    // Check if SecureBoot was previously enabled (stored in /deploy/vars)
    if !o.cfg.SecureBootReEnable {
        return nil
    }

    // Write SecureBoot-enable via efivarfs
    // The EFI variable SecureBoot is read-only, but SetupMode can be toggled
    // by writing to the PK (Platform Key) variable.
    //
    // Simpler approach: the CAPRF controller re-enables via Redfish AFTER
    // BOOTy reports success. BOOTy just signals readiness.
    return nil
}
```

**Recommended approach**: BOOTy signals provisioning success → CAPRF controller
re-enables SecureBoot via Redfish API before the final reboot into the
production OS:

```go
// CAPRF side — new step in provision() pipeline, after handleEvents()
func enableSecureBoot() step {
    return func(j *job) error {
        j.Log.V(1).Info("re-enabling secure boot")
        if err := j.system.EnableSecureBoot(); err != nil {
            return fmt.Errorf("failed to re-enable secure boot: %w", err)
        }
        return nil
    }
}
```

The `System` interface gains a new method:

```go
// internal/redfish/system.go
type System interface {
    // ... existing methods ...
    EnableSecureBoot() error
}

func (s *baseSystem) EnableSecureBoot() error {
    secBoot, err := s.SecureBoot()
    if err != nil {
        return fmt.Errorf("get secure boot status: %w", err)
    }
    if secBoot.SecureBootEnable {
        return nil // already enabled
    }
    secBoot.SecureBootEnable = true
    secBoot.DisableEtagMatch(true)
    return secBoot.Update()
}
```

### Phase 2: MOK Key Enrollment (P3)

For environments using custom-signed kernels or out-of-tree modules:

```
┌──────────────────────────────────────────────┐
│ CAPRF Controller                             │
│  1. Stores MOK DER cert in ConfigMap/Secret  │
│  2. Passes cert path via /deploy/vars        │
│  3. Disables SecureBoot for provisioning      │
└──────────────┬───────────────────────────────┘
               │ Redfish boot
┌──────────────▼───────────────────────────────┐
│ BOOTy (initrd)                               │
│  1. Provisions OS image                      │
│  2. Chroots into target                      │
│  3. mokutil --import /path/to/cert.der       │
│  4. Sets MOK password for next-boot confirm  │
│  5. Reports success                          │
└──────────────┬───────────────────────────────┘
               │ Reboot
┌──────────────▼───────────────────────────────┐
│ Shim UEFI bootloader                         │
│  MokManager auto-enrolls key (password)      │
│  Reboots into production OS                  │
└──────────────────────────────────────────────┘
```

**Challenge**: MokManager requires interactive confirmation at the UEFI
console. Options to automate:

1. **Pre-enrolled keys**: Include MOK in the UEFI firmware via Redfish
   SecureBoot database endpoints (`/redfish/v1/Systems/1/SecureBoot/
   SecureBootDatabases/db`). Avoids MokManager entirely.
2. **mokutil --disable-validation**: Skip MOK for testing environments.
3. **Custom shim**: Build shim with pre-embedded keys — requires signing
   with the UEFI CA or Microsoft's third-party CA.

**Recommended**: Use Redfish SecureBoot database API when available (HPE iLO 5+
supports it). Fall back to `mokutil` for older firmware.

## Required Binaries in Initramfs

| Binary | Package | Purpose | Initramfs Flavor | Already Present? |
|--------|---------|---------|-----------------|------------------|
| `efibootmgr` | `efibootmgr` | Read/write EFI boot entries, check SecureBoot state | all | **Yes** |
| `mokutil` | `mokutil` | MOK key enrollment/listing (Phase 2) | full, gobgp | **No — add** |

Phase 1 (re-enable via Redfish) requires no new BOOTy binaries — the
Redfish call is made by CAPRF. Phase 2 (MOK enrollment) needs `mokutil`
in the initramfs for `mokutil --import`.

**Dockerfile change** (tools stage, Phase 2 only):

```dockerfile
RUN apt-get update && apt-get install -y --no-install-recommends \
    ... existing packages ... \
    mokutil \
    && rm -rf /var/lib/apt/lists/*

COPY --from=tools /usr/bin/mokutil bin/mokutil
```

## Affected Files

| File | Change |
|------|--------|
| `pkg/config/provider.go` | Add `SecureBootReEnable bool`, `MOKCertPath string` |
| `pkg/provision/orchestrator.go` | Add `EnableSecureBoot()` step |
| `pkg/provision/orchestrator.go` | Add `EnrollMOK()` step (Phase 2) |
| CAPRF `internal/redfish/system.go` | Add `EnableSecureBoot()` method |
| CAPRF `internal/provision/manager.go` | Add `enableSecureBoot()` step after success |

## Configuration

```bash
# /deploy/vars
export SECUREBOOT_REENABLE="true"
export MOK_CERT_PATH="/deploy/file-system/mok.der"  # Phase 2
export MOK_PASSWORD="auto-generated"                  # Phase 2
```

## Risks

- **Vendor variance**: Some BMCs require a power cycle (not just reboot) for
  SecureBoot changes to take effect. Test on HPE iLO 5/6 and Lenovo XCC.
- **Bricked boot**: If SecureBoot is re-enabled but the installed OS doesn't
  have signed bootloaders, the machine won't boot. The EFI boot entry must
  point to `shimx64.efi` (which BOOTy already handles).
- **MOK automation**: MokManager's interactive prompt is hard to automate
  without Redfish SecureBoot database support or pre-built shims.

## Effort Estimate

- Phase 1 (re-enable via Redfish): **2-3 days** — mostly CAPRF changes
- Phase 2 (MOK enrollment): **5-7 days** — needs vendor-specific testing
