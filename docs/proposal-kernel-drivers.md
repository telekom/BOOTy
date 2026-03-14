# Proposal: Kernel Driver/Module Extensibility

## Status: Implemented

## Priority: P2

## Summary

Improve kernel module management in BOOTy's initramfs: declarative module
manifests per flavor, runtime PCI-ID-based auto-loading, custom module
injection at build time, proper dependency resolution, and comprehensive
per-driver compatibility documentation.

## Motivation

The current kernel module selection in `initrd.Dockerfile` is a hardcoded
list (lines 76–93). Adding or removing drivers requires editing the
Dockerfile and understanding which modules are needed. There's no runtime
auto-detection — all modules are loaded via `insmod` scripts.

| Gap | Impact |
|-----|--------|
| Hardcoded module list | Adding a new NIC model requires Dockerfile change |
| No auto-loading by PCI ID | Must load all modules even if hardware doesn't match |
| No module dependencies | `insmod` doesn't resolve `.ko` dependencies |
| No documentation | Operators don't know which hardware is supported |
| No custom module injection | Can't add out-of-tree drivers without forking Dockerfile |

### Current Module Coverage

From `initrd.Dockerfile`:

| Category | Modules | Coverage |
|----------|---------|----------|
| QEMU/KVM | virtio, virtio_ring, virtio_pci*, virtio_net, failover, net_failover | Complete |
| VXLAN | dummy, vxlan, udp_tunnel, ip6_udp_tunnel, bridge, stp, llc | Complete |
| Intel | e1000e, igb, igc, ixgbe, i40e, ice, iavf | Good |
| Broadcom | tg3, bnxt_en | Good |
| Mellanox | mlx4_core, mlx4_en, mlx5_core, mlxfw | Good |
| Emulex | be2net | Minimal |
| Storage | — | **None (gap)** |
| USB | — | **None (gap)** |

## Design

### Module Manifest

```yaml
# modules/manifest.yml — declarative module list per flavor
common:
  # VXLAN/bridge networking (always needed)
  - dummy
  - vxlan
  - udp_tunnel
  - ip6_udp_tunnel
  - bridge
  - stp
  - llc
  # Crypto (LUKS, SecureBoot)
  - dm_crypt
  - dm_mod
  - aes_generic
  - aesni_intel
  - xts
  # TPM
  - tpm
  - tpm_crb
  - tpm_tis
  - tpm_tis_core

full:
  includes: [common]
  nics:
    # Intel
    - e1000e
    - igb
    - igc
    - ixgbe
    - i40e
    - ice
    - iavf
    # Broadcom
    - tg3
    - bnxt_en
    # Mellanox
    - mlx4_core
    - mlx4_en
    - mlx5_core
    - mlxfw
    # Emulex
    - be2net
    # Realtek (edge/lab)
    - r8169
  storage:
    # NVMe
    - nvme
    - nvme_core
    # AHCI/SATA
    - ahci
    - libahci
    - sd_mod
    - sr_mod
    # RAID
    - raid0
    - raid1
    - raid456
    - raid10
    - md_mod
    # Megaraid (Dell PERC, Lenovo ThinkSystem)
    - megaraid_sas
    # HPE Smart Array
    - hpsa
    # LSI/Broadcom SAS
    - mpt3sas
  usb:
    - xhci_hcd
    - xhci_pci
    - ehci_hcd
    - ehci_pci
    - usb_storage

slim:
  includes: [common]
  nics:
    - virtio_net
    - e1000e
    - igb
    - bnxt_en
    - mlx5_core

gobgp:
  includes: [full]  # same as full, no FRR binaries

micro:
  nics: []  # pure-Go only, no kernel modules
```

### PCI ID → Module Mapping

```go
// pkg/realm/modules.go
package realm

// pciModuleMap maps PCI vendor:device IDs to kernel module names.
var pciModuleMap = map[string]string{
    // Intel NICs
    "8086:15b8": "e1000e",   // I219-V
    "8086:1533": "igb",      // I210
    "8086:1572": "i40e",     // X710
    "8086:158b": "i40e",     // XXV710
    "8086:1592": "ice",      // E810-C
    "8086:1593": "ice",      // E810-XXV
    // Broadcom NICs
    "14e4:165f": "tg3",      // BCM5720
    "14e4:16d8": "bnxt_en",  // BCM57414
    "14e4:1750": "bnxt_en",  // BCM57508
    // Mellanox NICs
    "15b3:1013": "mlx5_core", // ConnectX-4
    "15b3:1015": "mlx5_core", // ConnectX-4 Lx
    "15b3:1017": "mlx5_core", // ConnectX-5
    "15b3:101b": "mlx5_core", // ConnectX-6
    "15b3:101d": "mlx5_core", // ConnectX-6 Dx
    "15b3:101f": "mlx5_core", // ConnectX-6 Lx
    "15b3:1021": "mlx5_core", // ConnectX-7
    // Storage
    "1000:005d": "megaraid_sas", // MegaRAID SAS-3
    "103c:323a": "hpsa",         // HP Smart Array P420i
    "1000:0097": "mpt3sas",      // LSI SAS 3008
}

// AutoLoadModules scans PCI bus and loads matching modules.
func AutoLoadModules(ctx context.Context) error {
    // Read /sys/bus/pci/devices/*/vendor and device
    // Match against pciModuleMap
    // insmod matching modules in dependency order
    return nil
}
```

### Custom Module Injection

```dockerfile
# Operators can add custom modules at build time:
# Place .ko files in modules/custom/ directory
COPY modules/custom/*.ko modules/
```

### Required Binaries in Initramfs

| Binary | Package | Purpose | Initramfs Flavor | Already Present? |
|--------|---------|---------|-----------------|-----------------|
| `insmod` | busybox | Load kernel modules | all | **Yes** (busybox symlink) |
| `modprobe` | kmod | Load with dependency resolution | full, gobgp | **No — add** |
| `depmod` | kmod | Generate module dependency database | full, gobgp | **No — add** |
| `lspci` | pciutils | PCI device enumeration (debug) | full, gobgp | **No — add** |

**Dockerfile change** (tools stage):

```dockerfile
RUN apt-get update && apt-get install -y --no-install-recommends \
    ... existing packages ... \
    kmod pciutils \
    && rm -rf /var/lib/apt/lists/*

# Module management tools
COPY --from=tools /sbin/modprobe bin/modprobe
COPY --from=tools /sbin/depmod bin/depmod
COPY --from=tools /usr/bin/lspci bin/lspci
```

**Kernel stage changes** (expanded module list):

```dockerfile
# Add storage controller and USB modules
for m in ... existing NIC modules ... \
    # Storage
    nvme nvme_core ahci libahci sd_mod sr_mod \
    raid0 raid1 raid456 raid10 md_mod \
    megaraid_sas hpsa mpt3sas \
    # USB
    xhci_hcd xhci_pci ehci_hcd ehci_pci usb_storage \
    # Crypto/DM (LUKS)
    dm_crypt dm_mod aes_generic aesni_intel xts \
    # TPM
    tpm tpm_crb tpm_tis tpm_tis_core; do \
    find "$MDIR" -name "${m}.ko*" -exec cp {} /modules/ \; 2>/dev/null || true; \
done

# Generate modules.dep for modprobe
depmod -b /tmp/kernel "$KVER" 2>/dev/null || true
```

## Files Changed

| File | Change |
|------|--------|
| `modules/manifest.yml` | Declarative module manifest |
| `pkg/realm/modules.go` | PCI ID → module auto-loading |
| `pkg/realm/modules_test.go` | Unit tests for auto-loading |
| `initrd.Dockerfile` | Expanded module list, add modprobe/depmod/lspci |
| `docs/HARDWARE-SUPPORT.md` | Driver compatibility matrix |

## Testing

### Unit Tests

- `realm/modules_test.go`:
  - PCI ID mapping tests (table-driven)
  - Mock sysfs PCI bus for auto-load logic
  - Module dependency resolution

### E2E / KVM Tests

- **KVM matrix** (`kvm-matrix.yml`, tag `e2e_kvm`):
  - QEMU with multiple NIC models (`-device e1000e`, `-device virtio-net`)
  - Verify auto-loading selects correct modules
  - Verify unknown PCI device handled gracefully

## Risks

| Risk | Mitigation |
|------|------------|
| Module dependencies not available | Include depmod database in initramfs |
| Initramfs size bloat from storage modules | Only include for full/gobgp flavors |
| PCI ID map goes stale | Automate generation from kernel modules.alias |
| modprobe needs modules.dep | Generate at build time |

## Effort Estimate

6–9 engineering days (manifest system + auto-loading + docs + expanded
module coverage).
