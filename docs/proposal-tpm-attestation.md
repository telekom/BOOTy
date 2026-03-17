# Proposal: TPM Measured Boot and Attestation

## Status: Phase 1 Implemented (detection + TPM primitives)

Implemented now:

- Linux TPM discovery via sysfs/device nodes (`Detect()`) and sysfs PCR reads (`ReadPCRs()`)
- Go TPM device operations (`Open`, `ExtendPCR`, `ReadPCRDevice`, `MeasureReader`)
- Quote/signature helper primitives (`Quote`, `VerifyQuoteSignature`, `MarshalQuote`)
- Secret seal/unseal primitives with PCR policy (`SealSecret`, `UnsealSecret`)
- Provisioning integration for TPM presence reporting (`detect-tpm` step in orchestrator)

Not yet implemented end-to-end:

- CAPRF nonce/verification workflow for remote attestation admission control
- Automatic quote submission from the provisioning success path
- Automatic LUKS key sealing during provisioning

## Priority: P4

## Summary

Integrate TPM 2.0 (Trusted Platform Module) support into BOOTy for:

1. **Measured boot**: Extend PCR (Platform Configuration Register) values
   during provisioning to create a tamper-evident boot chain
2. **Remote attestation**: Allow the CAPRF controller to verify that a
   machine booted the expected software stack before admitting it to a
   cluster
3. **Disk encryption key sealing**: Seal LUKS encryption keys to PCR values
   so disks are only decryptable on machines with the expected boot chain

## Motivation

In zero-trust environments, merely provisioning a machine is insufficient —
the controller needs cryptographic proof that the machine is running the
expected software. Without measured boot, a compromised machine could:

- Boot a modified kernel that exfiltrates secrets
- Run a modified initramfs that intercepts network traffic
- Present fake health check results while running malware

TPM 2.0 is present in all modern server hardware (HPE ProLiant Gen10+,
Lenovo ThinkSystem SR650 V2+) and provides hardware-rooted trust.

### Industry Context

| Tool | TPM Support |
|------|------------|
| **Ironic** | No built-in TPM support; IPA can read TPM PCRs |
| **MAAS** | Can read TPM PCRs during commissioning; no attestation |
| **Tinkerbell** | No TPM support |
| **Keylime** | Full attestation framework — can integrate with any provisioner |
| **Microsoft Azure** | Measured boot + vTPM for all VMs; attestation via MAA |

This is an area where BOOTy could differentiate significantly from all
existing bare-metal provisioners.

## Design

### TPM Architecture

```
┌────────────────────────────────────────────────────────────┐
│ UEFI Firmware                                              │
│  PCR[0] = UEFI firmware hash                               │
│  PCR[1] = UEFI configuration                               │
│  PCR[4] = Boot loader hash (GRUB/shim)                     │
│  PCR[7] = SecureBoot state                                 │
└────────────────────┬───────────────────────────────────────┘
                     │ kexec / boot
┌────────────────────▼───────────────────────────────────────┐
│ BOOTy initrd (provisioner)                                 │
│  PCR[8] = BOOTy binary hash (self-measurement)             │
│  PCR[9] = OS image checksum (what was written to disk)     │
│  PCR[10] = Provisioning config hash                        │
│  PCR[14] = Custom measurement (provisioner identity)       │
└────────────────────┬───────────────────────────────────────┘
                     │ success
┌────────────────────▼───────────────────────────────────────┐
│ CAPRF Controller                                           │
│  1. Receive attestation quote from BOOTy                   │
│  2. Verify quote signature (TPM endorsement key)           │
│  3. Compare PCR values against expected ("golden") values  │
│  4. Admit machine to cluster only if attestation passes    │
└────────────────────────────────────────────────────────────┘
```

### BOOTy TPM Operations

```go
// pkg/tpm/tpm.go
package tpm

import (
    "crypto/sha256"
    "fmt"
    "io"

    "github.com/google/go-tpm/tpm2"
    "github.com/google/go-tpm/tpm2/transport/linuxTPM"
)

type TPM struct {
    transport io.ReadWriteCloser
}

func Open() (*TPM, error) {
    t, err := linuxTPM.Open()
    if err != nil {
        return nil, fmt.Errorf("open TPM device: %w", err)
    }
    return &TPM{transport: t}, nil
}

func (t *TPM) Close() error {
    return t.transport.Close()
}

// ExtendPCR extends a PCR register with the given data.
// Used to record each provisioning step in the TPM.
func (t *TPM) ExtendPCR(pcrIndex int, data []byte) error {
    digest := sha256.Sum256(data)
    pcrHandle := tpm2.PCRExtend{
        PCRHandle: tpm2.AuthHandle{
            Handle: tpm2.HandlePCR(pcrIndex),
            Auth:   tpm2.PasswordAuth(nil),
        },
        Digests: tpm2.TPMLDigestValues{
            Digests: []tpm2.TPMTHA{
                {HashAlg: tpm2.AlgSHA256, Digest: digest[:]},
            },
        },
    }
    _, err := pcrHandle.Execute(t.transport)
    return err
}

// Quote generates a signed attestation quote from the TPM.
func (t *TPM) Quote(pcrSelection []int, nonce []byte) ([]byte, []byte, error) {
    // Creates an AIK (Attestation Identity Key), signs PCR values
    // Returns: quote blob, signature blob
    // ... implementation using tpm2.Quote ...
    return nil, nil, fmt.Errorf("not yet implemented")
}

// ReadPCR reads the current value of a PCR register.
func (t *TPM) ReadPCR(pcrIndex int) ([]byte, error) {
    pcrRead := tpm2.PCRRead{
        PCRSelectionIn: tpm2.TPMLPCRSelection{
            PCRSelections: []tpm2.TPMSPCRSelection{
                {
                    Hash:      tpm2.AlgSHA256,
                    PCRSelect: pcrBitmask(pcrIndex),
                },
            },
        },
    }
    resp, err := pcrRead.Execute(t.transport)
    if err != nil {
        return nil, err
    }
    if len(resp.PCRValues.Digests) == 0 {
        return nil, fmt.Errorf("no PCR values returned")
    }
    return resp.PCRValues.Digests[0].Buffer, nil
}
```

### Provisioning Integration

```go
// pkg/provision/orchestrator.go
func (o *Orchestrator) MeasureProvisioningStep(ctx context.Context, stepName string, data []byte) error {
    if o.tpm == nil {
        return nil // TPM not available, skip measurement
    }

    measurement := fmt.Sprintf("booty:%s:%x", stepName, sha256.Sum256(data))
    return o.tpm.ExtendPCR(14, []byte(measurement))
}

// After successful provisioning:
func (o *Orchestrator) Attest(ctx context.Context) error {
    if o.tpm == nil {
        return nil
    }

    nonce, err := o.client.GetAttestationNonce(ctx)
    if err != nil {
        return fmt.Errorf("get attestation nonce: %w", err)
    }

    quote, sig, err := o.tpm.Quote([]int{0, 1, 4, 7, 8, 9, 10, 14}, nonce)
    if err != nil {
        return fmt.Errorf("generate TPM quote: %w", err)
    }

    return o.client.SubmitAttestation(ctx, quote, sig)
}
```

### CAPRF Attestation Verification

The CAPRF controller verifies attestation before transitioning the machine
to `PhaseProvisioned`:

```go
// CAPRF internal/provision/attestation.go
func VerifyAttestation(quote, sig []byte, expectedPCRs map[int][]byte, ekCert *x509.Certificate) error {
    // 1. Verify quote signature against TPM endorsement key
    // 2. Extract PCR values from quote
    // 3. Compare against expected "golden" values
    // 4. Return error if any PCR doesn't match
    return nil
}
```

### Disk Encryption (Future)

Seal a LUKS encryption key to TPM PCR values:

```go
// pkg/tpm/seal.go
func (t *TPM) SealSecret(pcrSelection []int, secret []byte) ([]byte, error) {
    // Creates a sealed blob that can only be unsealed when
    // PCR values match the current state
    return nil, nil
}

func (t *TPM) UnsealSecret(sealedBlob []byte) ([]byte, error) {
    // Unseals the blob only if PCR values match what was
    // recorded at seal time
    return nil, nil
}
```

## Configuration

```bash
# /deploy/vars
export TPM_ENABLED="true"
export TPM_ATTESTATION_URL="https://caprf.example/attestation"
export TPM_PCR_SELECTION="0,1,4,7,8,9,10,14"
export TPM_SEAL_PCRS="7,9,10"
```

Current runtime usage: `TPM_ENABLED` is consumed by provisioning for TPM
presence detection/reporting. The other variables are parsed and stored in
`MachineConfig` for upcoming attestation/sealing workflow integration.

## Required Binaries in Initramfs

TPM operations use **pure Go** (`google/go-tpm`) via `/dev/tpmrm0`, so no
userspace binaries are strictly required. Debug/fallback tools are optional:

| Binary | Package | Purpose | Initramfs Flavor | Already Present? |
|--------|---------|---------|-----------------|------------------|
| `tpm2_pcrread` | `tpm2-tools` | Debug: read PCR values from CLI | full (optional) | **No — optional** |
| `tpm2_quote` | `tpm2-tools` | Debug: generate attestation quote from CLI | full (optional) | **No — optional** |
| `tpm2_eventlog` | `tpm2-tools` | Debug: parse TPM event log | full (optional) | **No — optional** |

**Go Dependencies**:

| Package | Purpose |
|---------|---------|
| `github.com/google/go-tpm` | Pure Go TPM 2.0 operations |
| `golang.org/x/net` | Required by go-tpm transport dependencies |

**Kernel modules** (must be loaded):

| Module | Purpose |
|--------|---------|
| `tpm_tis` | TPM Interface Specification driver |
| `tpm_crb` | TPM Command Response Buffer driver |
| `tpm_tis_core` | Core TIS support |

## Affected Files

| File | Change |
|------|--------|
| `pkg/tpm/tpm.go` | New — sysfs-based TPM detection and PCR reading |
| `pkg/tpm/device.go` | New — TPM device open + PCR extend/read/measure operations |
| `pkg/tpm/attest.go` | New — attestation quote/signature helper primitives |
| `pkg/tpm/seal.go` | New — secret sealing/unsealing with PCR policy |
| `pkg/tpm/tpm_test.go` | New — Linux fixture tests for TPM helpers |
| `pkg/provision/orchestrator.go` | Add `detect-tpm` provisioning step and status reporting |
| `pkg/caprf/client.go` | Add `SubmitAttestation()` and TPM vars parsing |
| `pkg/caprf/client_test.go` | Add TPM parser and attestation client coverage |
| `pkg/config/provider.go` | Add TPM config fields |
| `go.mod` / `go.sum` | Add TPM dependencies |

## Risks

- **TPM availability**: Not all servers have TPM 2.0 enabled. Must gracefully
  degrade when TPM is absent or disabled in BIOS.
- **PCR brittleness**: Any firmware update changes PCR[0], invalidating all
  sealed keys and expected attestation values. Need a PCR policy update
  workflow.
- **go-tpm maturity**: The `google/go-tpm` library is well-maintained but
    TPM operations are inherently complex. Keep wrappers small and heavily tested.
- **Performance**: TPM operations are slow (~100ms per operation). Batch PCR
  extends where possible.

## Dependencies

- `github.com/google/go-tpm` — pure Go TPM 2.0 library
- `golang.org/x/net` — transport dependency used by go-tpm
- Kernel: `tpm_tis`, `tpm_crb` modules loaded
- Device: `/dev/tpmrm0` (resource manager interface)

## Effort Estimate

- TPM basic operations (extend/read PCR): **3-4 days**
- Attestation quote generation: **3-4 days**
- CAPRF attestation verification: **3-4 days**
- Disk encryption integration: **5-7 days** (separate phase)
- Testing (fixtures + hardware-backed validation): **3 days**
- Total: **17-22 days**
