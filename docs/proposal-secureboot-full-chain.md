# Proposal: Full Secure Boot Chain

## Status: Implemented (PR #48)

## Priority: P0

## Dependencies: [SecureBoot Lifecycle](proposal-secureboot.md)

## Summary

Extend the existing SecureBoot lifecycle proposal with full chain-of-trust
management: signed shim → signed GRUB → signed kernel verification, MOK
(Machine Owner Key) enrollment from BOOTy, signing infrastructure
documentation, and per-image chain validation before creating EFI boot
entries. This ensures every component in the boot chain is cryptographically
verified from UEFI firmware through kernel execution.

## Motivation

The existing SecureBoot proposal covers re-enabling SecureBoot after
provisioning and basic MOK enrollment. This companion proposal addresses the
complete signing chain:

| Gap | Impact |
|-----|--------|
| No shim/GRUB signature validation | Tampered bootloader could bypass SecureBoot |
| No MOK enrollment automation | Manual key enrollment doesn't scale |
| No signing infrastructure docs | Operators can't sign custom kernels/modules |
| No per-image chain audit | No visibility into which images have valid chains |

### Trust Chain Architecture

```
UEFI Firmware PK (Platform Key)
  └─ Microsoft UEFI CA
       └─ shim (signed by Microsoft)
            └─ MOK (Machine Owner Key, enrolled by BOOTy)
                 └─ GRUB (signed by MOK key)
                      └─ Kernel (signed by MOK key)
                           └─ Kernel modules (signed by MOK key)
```

### Industry Context

| Tool | Secure Boot Chain |
|------|------------------|
| **Ironic** | Toggle SecureBoot; no chain management |
| **MAAS** | Manages shim/GRUB signing via Ubuntu infrastructure |
| **Tinkerbell** | No SecureBoot support |
| **Ubuntu** | Canonical-signed shim+GRUB; MOK for custom kernels |
| **RHEL** | Red Hat-signed shim+GRUB; MOK for third-party modules |

## Design

### Phase 1: Chain Verification (Pre-Boot Audit)

Before creating an EFI boot entry, BOOTy verifies the signing chain:

```go
// pkg/secureboot/verify.go
package secureboot

import (
    "context"
    "crypto/x509"
    "fmt"
    "log/slog"
)

// ChainVerifier validates the SecureBoot signing chain of a provisioned OS.
type ChainVerifier struct {
    rootCerts *x509.CertPool
    mokCerts  *x509.CertPool
    log       *slog.Logger
}

// ChainResult holds the verification outcome for each component.
type ChainResult struct {
    Shim   ComponentStatus `json:"shim"`
    GRUB   ComponentStatus `json:"grub"`
    Kernel ComponentStatus `json:"kernel"`
    Valid  bool            `json:"valid"` // true only if entire chain is valid
}

type ComponentStatus struct {
    Path       string `json:"path"`
    Signed     bool   `json:"signed"`
    SignedBy   string `json:"signedBy,omitempty"`
    Valid      bool   `json:"valid"`
    Error      string `json:"error,omitempty"`
}

// VerifyChain checks the signing chain of the provisioned OS.
func (v *ChainVerifier) VerifyChain(ctx context.Context, rootPath string) (*ChainResult, error) {
    result := &ChainResult{Valid: true}

    // 1. Find and verify shim
    shimPath := findShim(rootPath)
    result.Shim = v.verifyPE(shimPath)
    if !result.Shim.Valid {
        result.Valid = false
    }

    // 2. Find and verify GRUB
    grubPath := findGRUB(rootPath)
    result.GRUB = v.verifyPE(grubPath)
    if !result.GRUB.Valid {
        result.Valid = false
    }

    // 3. Find and verify kernel
    kernelPath := findKernel(rootPath)
    result.Kernel = v.verifyPE(kernelPath)
    if !result.Kernel.Valid {
        result.Valid = false
    }

    return result, nil
}
```

### Phase 2: MOK Key Enrollment

```go
// pkg/secureboot/mok.go
package secureboot

import (
    "context"
    "fmt"
    "os"
)

// MOKEnroller manages Machine Owner Key enrollment via efivarfs.
type MOKEnroller struct {
    log *slog.Logger
}

// EnrollMOK enrolls a MOK certificate into the UEFI firmware.
// This writes to efivarfs directly (Go-first) or falls back to mokutil.
func (e *MOKEnroller) EnrollMOK(ctx context.Context, certDER []byte, password string) error {
    // Method 1: Direct efivarfs write
    // The MokNew EFI variable accepts DER-encoded certificates
    mokNewPath := "/sys/firmware/efi/efivars/MokNew-605dab50-e046-4300-abb6-3dd810dd8b23"
    if err := e.writeEFIVar(mokNewPath, certDER); err != nil {
        e.log.Info("direct efivarfs write failed, falling back to mokutil", "error", err)
        return e.enrollViaMokutil(ctx, certDER, password)
    }
    return nil
}

func (e *MOKEnroller) enrollViaMokutil(ctx context.Context, certDER []byte, password string) error {
    // Write cert to temp file, use mokutil --import
    // mokutil --import <cert.der> --root-pw
    return nil
}
```

### Phase 3: Key Distribution via CAPRF

```yaml
# RedfishHost CR — key distribution
apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: RedfishHost
metadata:
  name: server-001
spec:
  secureBootConfig:
    enabled: true
    mokCertificate: |
      -----BEGIN CERTIFICATE-----
      <base64-encoded DER certificate>
      -----END CERTIFICATE-----
    verifyChain: true
    allowUnsignedKernel: false
```

### Required Binaries in Initramfs

| Binary | Package | Purpose | Initramfs Flavor | Already Present? |
|--------|---------|---------|-----------------|-----------------|
| `mokutil` | `mokutil` | MOK key enrollment/list (fallback) | full, gobgp | **No — add** |
| `sbverify` | `sbsigntool` | PE binary signature verification (fallback) | full, gobgp | **No — add** |
| `sbsign` | `sbsigntool` | PE binary signing (utility only) | none (build-time only) | N/A |
| `efibootmgr` | `efibootmgr` | EFI boot entry management | full, gobgp | **Yes** |
| `pesign` | `pesign` | Alternative PE signing/verification | none (optional) | N/A |

**Dockerfile change** (tools stage):

```dockerfile
RUN apt-get update && apt-get install -y --no-install-recommends \
    ... existing packages ... \
    mokutil sbsigntool \
    && rm -rf /var/lib/apt/lists/*

# SecureBoot tools
COPY --from=tools /usr/bin/mokutil bin/mokutil
COPY --from=tools /usr/bin/sbverify bin/sbverify
```

**Go-first approach**: BOOTy uses direct efivarfs operations and Go PE
parsing (`debug/pe` + signature extraction) for verification. The binaries
are fallback only.

### Signing Infrastructure Documentation

The proposal includes documentation for operators setting up their own
signing infrastructure:

1. **Generate CA**: `openssl req -new -x509 -newkey rsa:2048 -keyout MOK.key -out MOK.crt`
2. **Sign shim**: `sbsign --key MOK.key --cert MOK.crt --output shimx64.efi shimx64.efi`
3. **Sign GRUB**: `sbsign --key MOK.key --cert MOK.crt --output grubx64.efi grubx64.efi`
4. **Sign kernel**: `sbsign --key MOK.key --cert MOK.crt --output vmlinuz vmlinuz`
5. **Sign modules**: `kmodsign sha512 MOK.key MOK.crt module.ko`

## Files Changed

| File | Change |
|------|--------|
| `pkg/secureboot/verify.go` | PE signature chain verification |
| `pkg/secureboot/mok.go` | MOK key enrollment (efivarfs + mokutil fallback) |
| `pkg/secureboot/efivarfs.go` | Low-level EFI variable read/write |
| `pkg/secureboot/types.go` | Types and constants |
| `pkg/provision/orchestrator.go` | `verifySecureBootChain()` + `enrollMOK()` steps |
| `pkg/caprf/client.go` | `ReportSecureBootStatus()` method |
| `pkg/config/provider.go` | SecureBoot config fields |
| `initrd.Dockerfile` | Add `mokutil`, `sbverify` binaries |

## Testing

### Unit Tests

- `secureboot/verify_test.go` — Chain verification with test-signed PE
  binaries (generated at test time with `sbsign`). Table-driven: valid
  chain, broken chain (unsigned GRUB), expired certificate, wrong signer.
- `secureboot/mok_test.go` — MOK enrollment with mock efivarfs directory
  (`t.TempDir()`). Verify correct EFI variable format written.
- `secureboot/efivarfs_test.go` — EFI variable read/write with mock
  filesystem (attribute bytes + data).

### E2E / KVM Tests

- **KVM matrix** (`kvm-matrix.yml`, tag `e2e_kvm`):
  - QEMU with OVMF firmware (SecureBoot enabled variant: `OVMF_CODE.secboot.fd`)
  - Test certificates/keys generated by CI job
  - Scenario 1: Boot with SecureBoot enabled → BOOTy verifies chain → success
  - Scenario 2: Boot with unsigned kernel → BOOTy detects invalid chain → error
  - Scenario 3: Enroll MOK → verify enrollment via efivarfs read-back
  - Scenario 4: Full flow — enroll MOK → sign kernel → verify chain → create EFI entry

## Risks

| Risk | Mitigation |
|------|------------|
| efivarfs not mounted in initramfs | BOOTy's realm package handles mounts |
| MOK enrollment requires reboot | Two-pass flow with CAPRF coordination |
| Different distros use different shim/GRUB paths | Configurable search paths per OS |
| Microsoft CA certificate rotation | Document update procedure |

## Effort Estimate

10–15 engineering days (chain verification + MOK enrollment + efivarfs +
CAPRF integration + KVM tests).
