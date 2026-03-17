# Proposal: ARM64 / Multi-Architecture Support

## Status: Phase 1 Implemented

## Priority: P4

## Summary

Extend BOOTy and CAPRF to support **ARM64 (aarch64)** bare-metal servers
alongside the existing AMD64 architecture. This includes cross-compilation
of the BOOTy binary, ARM64 kernel/initrd builds, architecture-aware image
selection, and ARM64-specific kernel module handling.

## Motivation

ARM64 servers are gaining adoption in data centers:

- **Ampere Altra** — used in Azure, Oracle Cloud, and on-premises
- **AWS Graviton** — not bare-metal relevant but indicates ARM momentum
- **Fujitsu A64FX** — HPC workloads
- **Lenovo ThinkSystem SR670 V2** — ARM64-capable
- **NVIDIA Grace** — ARM CPU + NVIDIA GPU for AI workloads

CAPRF already has an `Architecture` field on `RedfishHostSpec`:

```go
// +kubebuilder:validation:enum="amd64,aarch64"
Architecture string `json:"architecture"`
```

But BOOTy only builds for AMD64 today.

### Industry Context

| Tool | ARM64 Support |
|------|--------------|
| **Ironic** | Yes — IPA builds for ARM64, architecture-aware scheduling |
| **MAAS** | Yes — ARM64 images and commissioning |
| **Tinkerbell** | Partial — community ARM64 support |

## Design

### Build Changes

```makefile
# Makefile (current implementation)
TARGETARCH ?= $(shell go env GOARCH)

.PHONY: build
build:
    CGO_ENABLED=0 GOOS=linux GOARCH=$(TARGETARCH) go build -o dist/$(TARGETARCH)/booty .

.PHONY: build-all
build-all:
    $(MAKE) build TARGETARCH=amd64
    $(MAKE) build TARGETARCH=arm64

# Initramfs targets per arch:
#   make gobgp           (current TARGETARCH)
#   make arm64-gobgp     (ARM64 variant)
```

### Kernel + Initramfs

ARM64 requires a different kernel and boot sequence:

| Component | AMD64 | ARM64 |
|-----------|-------|-------|
| Kernel | `vmlinuz` | `Image` (uncompressed) or `Image.gz` |
| Bootloader | GRUB + shim (BIOS/UEFI) | GRUB (UEFI only) |
| Boot method | `isolinux` / `grub.cfg` | `grub.cfg` only (no BIOS boot) |
| Console | `ttyS0` | `ttyAMA0` or `ttyS0` (platform-dependent) |
| ACPI | Standard | ACPI + device tree (some platforms) |

### Architecture-Aware Image Selection

CAPRF selects the correct provisioner image based on the host's architecture:

```go
// CAPRF internal/ramdisk/builder.go
func (b *Builder) EnsureImage(nn types.NamespacedName, spec ImageSpec, arch string) error {
    // Select arch-specific base image
    baseImage := fmt.Sprintf("booty-initrd:%s", arch)
    // ... build ramdisk with arch-specific kernel + initrd
}
```

BOOTy OS image selection also considers architecture:

```go
// pkg/provision/orchestrator.go
func (o *Orchestrator) selectImage() string {
    arch := runtime.GOARCH
    for _, url := range o.cfg.ImageURLs {
        if strings.Contains(url, arch) {
            return url
        }
    }
    return o.cfg.ImageURLs[0] // fallback to first
}
```

### ARM64-Specific Modules

| Category | AMD64 Module | ARM64 Equivalent |
|----------|-------------|-----------------|
| NIC | `ixgbe`, `i40e`, `ice` | `thunder_bgx`, `octeontx2`, `mlx5_core` |
| Storage | `ahci`, `megaraid_sas` | `ahci_platform`, `nvme` |
| Console | `pcspkr` | N/A |
| GPIO | N/A | `gpio-thunderx`, `gpio-dwapb` |
| USB | `xhci_hcd` | `xhci_hcd` (same) |

### EFI Boot Differences

ARM64 servers are UEFI-only (no Legacy BIOS). The EFI boot entry setup
in BOOTy must handle:

```go
// pkg/disk/efi.go
func (m *Manager) CreateEFIBootEntry(ctx context.Context, espPart, rootDisk string) error {
    arch := runtime.GOARCH

    var shimPath, grubPath string
    switch arch {
    case "amd64":
        shimPath = "shimx64.efi"
        grubPath = "grubx64.efi"
    case "arm64":
        shimPath = "shimaa64.efi"
        grubPath = "grubaa64.efi"
    }
    // ... existing logic with arch-specific paths
}
```

### CI/CD

```yaml
# .github/workflows/ci.yml
jobs:
  build:
    strategy:
      matrix:
        arch: [amd64, arm64]
    steps:
      - uses: docker/setup-qemu-action@v3  # for ARM64 cross-build
      - run: make build TARGETARCH=${{ matrix.arch }}
      - run: make gobgp TARGETARCH=${{ matrix.arch }}
```

## Required Binaries in Initramfs

ARM64 builds use the same binaries as AMD64 but compiled for `aarch64`.
The multi-arch Dockerfile handles this via `--platform linux/arm64`.
No new binaries are needed beyond the existing set.

**ARM64-specific kernel modules** (replace some AMD64-only modules):

| Module | Purpose | AMD64 Equivalent |
|--------|---------|-----------------|
| `thunder_bgx` | Cavium ThunderX NIC | `ixgbe` |
| `octeontx2` | Marvell OcteonTX2 NIC | `i40e` |
| `mlx5_core` | Mellanox ConnectX (same) | `mlx5_core` |
| `ahci_platform` | Platform AHCI controller | `ahci` |
| `gpio-dwapb` | DesignWare GPIO | N/A |

**Dockerfile change** (multi-arch build):

```dockerfile
# Multi-arch base images (already supported by Alpine/Debian)
FROM --platform=$TARGETPLATFORM alpine:3.19 AS base
```

## Affected Files

| File | Change |
|------|--------|
| `Makefile` | Added `TARGETARCH` variable, `build-all`, `arm64-*` targets (implemented) |
| `initrd.Dockerfile` | Multi-arch kernel package selection (implemented) |
| `main.go` | Architecture-conditional module loading |
| `pkg/provision/configurator.go` | ARM64 EFI bootloader paths — `efiLoaderPath()` (implemented) |
| `pkg/provision/configurator_test.go` | EFI loader path tests for amd64/arm64 (implemented) |
| `pkg/provision/orchestrator.go` | Architecture-aware image selection |
| `.github/workflows/` | Multi-arch CI matrix |
| CAPRF `internal/ramdisk/builder.go` | Architecture-aware image building |

## Risks

- **QEMU cross-compile performance**: Building ARM64 images via QEMU on
  AMD64 CI runners is 5-10x slower. Consider native ARM64 runners or
  cross-compilation without QEMU.
- **Testing**: Need access to ARM64 bare-metal hardware for E2E testing.
  QEMU emulation can verify boot but not driver/firmware interactions.
- **Module availability**: ARM64 kernel config may not include all needed
  modules. Need a separate ARM64 kernel config.

## Effort Estimate

- Go cross-compilation setup: **1 day**
- ARM64 initrd Dockerfile: **2-3 days**
- Architecture-aware boot/EFI: **2 days**
- CI/CD multi-arch pipeline: **2 days**
- Testing (QEMU + real hardware): **3-5 days**
- Total: **10-14 days**
