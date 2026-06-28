# Proposal: ARM64 / Multi-Architecture Support

## Status: Phase 1 Implemented (Phases 2-4 pending)

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
TARGETOS=linux
TARGETARCH ?= $(shell go env GOARCH)

.PHONY: build
build:
  GOOS=$(TARGETOS) GOARCH=$(TARGETARCH) go build -o booty .

.PHONY: build-all
build-all:
  mkdir -p dist/amd64 dist/arm64
  GOOS=$(TARGETOS) GOARCH=amd64 go build -o dist/amd64/booty .
  GOOS=$(TARGETOS) GOARCH=arm64 go build -o dist/arm64/booty .

# Initramfs targets per arch:
#   make gobgp           (AMD64)
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

### ARM64 Kernel Module Scope

The current Dockerfile uses one explicit module list for both amd64 and arm64
builds. That list covers virtio, common filesystems, bridge/vxlan, Intel,
Broadcom, Mellanox, dm-crypt, and IPMI modules. ARM64 platform-specific NIC,
storage, console, GPIO, and USB modules require Dockerfile and CI proof before
they can be listed as bundled support.

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
      - run: |
          if [ "${{ matrix.arch }}" = "arm64" ]; then
            make arm64-gobgp
          else
            make gobgp
          fi
```

## Required Binaries in Initramfs

ARM64 builds use the same binaries as AMD64 but compiled for `aarch64`.
The multi-arch Dockerfile handles this via `--platform linux/arm64`.
No new binaries are needed beyond the existing set.

**Current kernel module bundle**

ARM64 builds currently use the same explicit module bundle as AMD64. The
Dockerfile copies modules such as virtio, ext4, xfs, btrfs, bridge/vxlan,
Intel/Broadcom/Mellanox NIC drivers, dm-crypt, and IPMI support. ARM64-specific
platform modules such as `thunder_bgx`, `octeontx2`, `ahci_platform`, and
`gpio-dwapb` are not currently bundled and should be treated as planned support
until the Dockerfile and CI prove them.

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
| `main.go` | Architecture-conditional module loading (planned, not yet implemented in this proposal phase) |
| `pkg/provision/configurator.go` | ARM64 EFI bootloader paths — `efiLoaderPath()` (implemented) |
| `pkg/provision/configurator_test.go` | EFI loader path tests for amd64/arm64 (implemented) |
| `pkg/provision/orchestrator.go` | Architecture-aware image selection (planned, not yet implemented in this proposal phase) |
| `.github/workflows/` | Multi-arch CI matrix (partially implemented) |
| CAPRF `internal/ramdisk/builder.go` | Architecture-aware image building (cross-repo follow-up) |

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
