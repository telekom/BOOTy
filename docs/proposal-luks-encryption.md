# Proposal: Full LUKS Disk Encryption

## Status: Implemented

## Priority: P1

## Summary

Add LUKS2 disk encryption support to BOOTy's provisioning pipeline. Encrypt
root and/or data partitions during provisioning with key delivery from CAPRF.
Configurable auto-unlock via clevis/tang (network-bound), TPM2 (hardware-bound),
or passphrase (manual). Generate `/etc/crypttab` and initramfs hooks in the
provisioned OS for seamless encrypted boot.

## Motivation

Disk encryption at rest is a compliance requirement for many environments:

| Regulation | Requirement |
|-----------|-------------|
| PCI DSS 4.0 | Requirement 3.5: Protect stored account data with encryption |
| HIPAA | §164.312(a)(2)(iv): Encryption of data at rest |
| SOC 2 | CC6.1: Logical and physical access controls |
| BSI IT-Grundschutz | SYS.1.1.A23: Disk encryption for servers |
| GDPR | Article 32: Appropriate technical measures (encryption) |

Without disk-level encryption, physical disk theft or improper disposal
exposes all data. This is especially critical for bare-metal Kubernetes
nodes that store etcd data, container images, and secrets on local disks.

### Industry Context

| Tool | Disk Encryption |
|------|----------------|
| **Ironic** | No built-in LUKS support; can be done via configdrive scripts |
| **MAAS** | LUKS encryption during installation (via curtin) |
| **Tinkerbell** | No built-in encryption; custom actions possible |
| **Flatcar** | Built-in LUKS2 + TPM2 support |
| **Talos** | System partitions encrypted with machine-specific keys |

## Design

### Encryption Architecture

```
┌────────────────────────────────────────────────────────┐
│ BOOTy (initrd)                                         │
│                                                        │
│  1. Receive LUKS config from CAPRF (/deploy/vars)      │
│  2. Create GPT partitions (from proposal-disk-part.)   │
│  3. luksFormat target partitions (LUKS2)               │
│  4. luksOpen → map to /dev/mapper/<name>               │
│  5. mkfs on mapped device                              │
│  6. Stream OS image / extract rootfs                   │
│  7. Generate /etc/crypttab in provisioned OS           │
│  8. Install clevis/tang or TPM2 unlock hooks           │
│  9. Regenerate provisioned OS initramfs                │
│  10. Close LUKS + report success                       │
└────────────────────────────────────────────────────────┘
```

### LUKS Manager

```go
// pkg/disk/luks/manager.go
package luks

import (
    "context"
    "fmt"
    "log/slog"
    "os/exec"
)

// UnlockMethod specifies how LUKS volumes auto-unlock on boot.
type UnlockMethod string

const (
    UnlockPassphrase UnlockMethod = "passphrase" // manual entry
    UnlockTPM2       UnlockMethod = "tpm2"       // TPM2 PCR-bound
    UnlockClevis     UnlockMethod = "clevis"     // network-bound (tang)
    UnlockKeyFile    UnlockMethod = "keyfile"     // key in initramfs
)

// Config holds LUKS encryption configuration.
type Config struct {
    Enabled      bool           `json:"enabled"`
    Partitions   []LUKSTarget   `json:"partitions"`   // which partitions to encrypt
    UnlockMethod UnlockMethod   `json:"unlockMethod"`
    Passphrase   string         `json:"passphrase,omitempty"`  // for passphrase/keyfile methods
    TangURL      string         `json:"tangUrl,omitempty"`     // for clevis method
    TPMPCRs      []int          `json:"tpmPcrs,omitempty"`     // for TPM2 method (default: [7])
    Cipher       string         `json:"cipher,omitempty"`      // default: aes-xts-plain64
    KeySize      int            `json:"keySize,omitempty"`     // default: 512 (bits)
    Hash         string         `json:"hash,omitempty"`        // default: sha256
}

// LUKSTarget identifies a partition to encrypt.
type LUKSTarget struct {
    Device     string `json:"device"`     // e.g., "/dev/sda3"
    MappedName string `json:"mappedName"` // e.g., "root_crypt"
    MountPoint string `json:"mountPoint"` // e.g., "/"
}

// Manager handles LUKS encryption operations.
type Manager struct {
    log *slog.Logger
}

func New(log *slog.Logger) *Manager {
    return &Manager{log: log}
}

// Format creates a LUKS2 volume on the target device.
// Go-first: uses Go LUKS2 header creation; falls back to cryptsetup binary.
func (m *Manager) Format(ctx context.Context, target LUKSTarget, cfg Config) error {
    passphrase := cfg.Passphrase
    if passphrase == "" {
        return fmt.Errorf("LUKS passphrase required for initial format")
    }

    cipher := cfg.Cipher
    if cipher == "" {
        cipher = "aes-xts-plain64"
    }
    keySize := cfg.KeySize
    if keySize == 0 {
        keySize = 512
    }

    // Go-first attempt: LUKS2 header creation via Go crypto
    if err := m.formatGo(ctx, target.Device, passphrase, cipher, keySize); err != nil {
        m.log.Info("Go LUKS format failed, falling back to cryptsetup", "error", err)
        return m.formatCryptsetup(ctx, target, passphrase, cipher, keySize)
    }
    return nil
}

func (m *Manager) formatCryptsetup(ctx context.Context, target LUKSTarget, passphrase, cipher string, keySize int) error {
    // cryptsetup luksFormat --type luks2 --cipher <cipher> --key-size <size> --batch-mode <device>
    cmd := exec.CommandContext(ctx, "cryptsetup", "luksFormat",
        "--type", "luks2",
        "--cipher", cipher,
        "--key-size", fmt.Sprintf("%d", keySize),
        "--batch-mode",
        target.Device,
    )
    cmd.Stdin = strings.NewReader(passphrase)
    if out, err := cmd.CombinedOutput(); err != nil {
        return fmt.Errorf("cryptsetup luksFormat %s: %s: %w", target.Device, string(out), err)
    }
    return nil
}

// Open maps a LUKS volume to /dev/mapper/<name>.
func (m *Manager) Open(ctx context.Context, target LUKSTarget, passphrase string) error {
    cmd := exec.CommandContext(ctx, "cryptsetup", "luksOpen",
        target.Device, target.MappedName,
    )
    cmd.Stdin = strings.NewReader(passphrase)
    if out, err := cmd.CombinedOutput(); err != nil {
        return fmt.Errorf("cryptsetup luksOpen %s: %s: %w", target.Device, string(out), err)
    }
    return nil
}

// Close unmaps a LUKS volume.
func (m *Manager) Close(ctx context.Context, mappedName string) error {
    cmd := exec.CommandContext(ctx, "cryptsetup", "luksClose", mappedName)
    if out, err := cmd.CombinedOutput(); err != nil {
        return fmt.Errorf("cryptsetup luksClose %s: %s: %w", mappedName, string(out), err)
    }
    return nil
}
```

### Crypttab Generation

```go
// pkg/disk/luks/crypttab.go
package luks

import (
    "fmt"
    "strings"
)

// GenerateCrypttab creates /etc/crypttab entries for the provisioned OS.
func GenerateCrypttab(targets []LUKSTarget, method UnlockMethod) string {
    var lines []string
    lines = append(lines, "# Generated by BOOTy provisioner")
    for _, t := range targets {
        var options string
        switch method {
        case UnlockPassphrase:
            options = "luks,discard"
        case UnlockTPM2:
            options = "luks,discard,tpm2-device=auto"
        case UnlockClevis:
            options = "luks,discard,_netdev"
        case UnlockKeyFile:
            options = "luks,discard,keyfile-timeout=30s"
        }
        lines = append(lines, fmt.Sprintf("%s UUID=%s none %s",
            t.MappedName, getUUID(t.Device), options))
    }
    return strings.Join(lines, "\n")
}
```

### Required Binaries in Initramfs

| Binary | Package | Purpose | Initramfs Flavor | Already Present? |
|--------|---------|---------|-----------------|-----------------|
| `cryptsetup` | `cryptsetup-bin` | LUKS format/open/close (fallback) | full, gobgp | **No — add** |
| `dmsetup` | `dmsetup` | Device-mapper status (debug) | full, gobgp | **No — add** |
| `sgdisk` | `gdisk` | GPT partitioning | full, gobgp | **Yes** |
| `mkfs.ext4` | `e2fsprogs` | Filesystem creation on mapped device | full, gobgp | Via `mke2fs` — **verify** |

**Dockerfile change** (tools stage):

```dockerfile
RUN apt-get update && apt-get install -y --no-install-recommends \
    ... existing packages ... \
    cryptsetup-bin \
    && rm -rf /var/lib/apt/lists/*

# LUKS disk encryption tools
COPY --from=tools /sbin/cryptsetup bin/cryptsetup
COPY --from=tools /sbin/dmsetup bin/dmsetup
```

**Kernel modules needed** (add to kernel stage):

```dockerfile
# dm-crypt for LUKS
for m in ... \
    dm_crypt dm_mod aes_generic aesni_intel xts; do \
    find "$MDIR" -name "${m}.ko*" -exec cp {} /modules/ \; 2>/dev/null || true; \
done
```

### Configuration

```bash
# /deploy/vars
export LUKS_ENABLED="true"
export LUKS_PARTITIONS='[{"device":"/dev/sda3","mappedName":"root_crypt","mountPoint":"/"}]'
export LUKS_UNLOCK_METHOD="tpm2"       # "passphrase", "tpm2", "clevis", "keyfile"
export LUKS_PASSPHRASE="<from CAPRF>"  # encrypted in transit via TLS
export LUKS_TANG_URL="http://tang.example.com:7500"
export LUKS_TPM_PCRS="7"              # PCR binding for TPM2 (default: 7 = SecureBoot state)
```

## Files Changed

| File | Change |
|------|--------|
| `pkg/disk/luks/manager.go` | LUKS format/open/close operations |
| `pkg/disk/luks/crypttab.go` | Crypttab generation |
| `pkg/disk/luks/config.go` | LUKS configuration types |
| `pkg/disk/luks/manager_test.go` | Unit tests |
| `pkg/provision/orchestrator.go` | LUKS steps in provisioning pipeline |
| `pkg/config/provider.go` | LUKS config fields |
| `initrd.Dockerfile` | Add `cryptsetup`, `dmsetup`, dm-crypt modules |

## Testing

### Unit Tests

- `luks/crypttab_test.go` — Table-driven crypttab generation for each
  unlock method. Verify correct options format.
- `luks/config_test.go` — Config validation: missing passphrase, invalid
  cipher, key size validation.
- `luks/manager_test.go` — Format/Open/Close with mock Commander interface.
  Verify correct cryptsetup argument construction.

### E2E / KVM Tests

- **KVM matrix** (`kvm-matrix.yml`, tag `e2e_kvm`):
  - QEMU with additional virtio-blk disk for LUKS testing
  - Scenario 1: luksFormat → luksOpen → mkfs → mount → write file → close → reopen → verify file
  - Scenario 2: Full provisioning flow with LUKS-encrypted root partition
  - Scenario 3: Incorrect passphrase → verify clean error handling
- **linux_e2e** (tag `linux_e2e`, requires root):
  - Loop device → luksFormat → luksOpen → mkfs → mount → verify → cleanup

## Risks

| Risk | Mitigation |
|------|------------|
| cryptsetup binary not in initramfs | Go-first LUKS2 implementation |
| dm-crypt kernel module missing | Add to kernel module list in Dockerfile |
| Passphrase in /deploy/vars visible in procfs | CAPRF delivers via sealed channel; zero after use |
| Emergency access to encrypted disk | Document recovery key procedure |
| Performance impact of encryption | Use AES-NI hardware acceleration (aesni_intel module) |

## Effort Estimate

10–14 engineering days (Go LUKS2 + cryptsetup fallback + crypttab +
unlock methods + KVM tests + initramfs changes).
