# Proposal: Bootloader Management — GRUB Enhancement + systemd-boot

## Status: Proposal

## Priority: P2

## Summary

Unified bootloader management via a `Bootloader` interface supporting both
GRUB and systemd-boot. Improves existing GRUB config parsing in `pkg/kexec/`,
adds GRUB installation into provisioned OS, and introduces full systemd-boot
support as an alternative. Auto-detects which bootloader the provisioned OS
image uses.

## Motivation

BOOTy currently has basic GRUB config parsing in `pkg/kexec/grub.go` for
kexec kernel loading, but doesn't manage the bootloader installation or
configuration in the provisioned OS. Several modern Linux distributions
(Fedora, Arch, systemd-first distros) use systemd-boot instead of GRUB.

| Gap | Impact |
|-----|--------|
| No GRUB installation management | Manual GRUB setup needed post-provision |
| No multi-kernel support | Can't select between kernel versions |
| No systemd-boot support | Can't provision systemd-boot distros |
| No bootloader auto-detection | Operator must know which bootloader the image uses |

### Industry Context

| Tool | Bootloader Management |
|------|----------------------|
| **Ironic** | Minimal — relies on image having bootloader pre-configured |
| **MAAS** | Full GRUB management via curtin; no systemd-boot |
| **Tinkerbell** | No built-in bootloader management |
| **Flatcar** | Uses systemd-boot + GRUB fallback |
| **Talos** | Uses systemd-boot exclusively |

## Design

### Common Interface

```go
// pkg/bootloader/bootloader.go
package bootloader

import "context"

// Bootloader abstracts bootloader installation and configuration.
type Bootloader interface {
    // Name returns the bootloader type ("grub", "systemd-boot").
    Name() string

    // Install installs the bootloader into the provisioned OS.
    Install(ctx context.Context, rootPath string, espPath string) error

    // Configure sets default kernel, cmdline, timeout.
    Configure(ctx context.Context, cfg BootConfig) error

    // ListEntries returns available boot entries.
    ListEntries(ctx context.Context, rootPath string) ([]BootEntry, error)

    // SetDefault sets the default boot entry.
    SetDefault(ctx context.Context, entryID string) error
}

type BootConfig struct {
    DefaultKernel string            `json:"defaultKernel"`
    KernelCmdline string            `json:"kernelCmdline"`
    ExtraParams   string            `json:"extraParams"`
    Timeout       int               `json:"timeout"`     // seconds
    RootDevice    string            `json:"rootDevice"`   // e.g., "UUID=..."
    Entries       []BootEntry       `json:"entries,omitempty"`
}

type BootEntry struct {
    ID         string `json:"id"`
    Title      string `json:"title"`
    Kernel     string `json:"kernel"`
    Initrd     string `json:"initrd"`
    Cmdline    string `json:"cmdline"`
    IsDefault  bool   `json:"isDefault"`
}
```

### GRUB Manager

```go
// pkg/bootloader/grub/grub.go
package grub

import (
    "context"
    "fmt"
    "os/exec"
)

type GRUB struct {
    log *slog.Logger
}

func (g *GRUB) Name() string { return "grub" }

func (g *GRUB) Install(ctx context.Context, rootPath, espPath string) error {
    // grub-install --target=x86_64-efi --efi-directory=<esp> --boot-directory=<boot>
    cmd := exec.CommandContext(ctx, "chroot", rootPath,
        "grub-install",
        "--target=x86_64-efi",
        fmt.Sprintf("--efi-directory=%s", espPath),
    )
    if out, err := cmd.CombinedOutput(); err != nil {
        return fmt.Errorf("grub-install: %s: %w", string(out), err)
    }
    return nil
}
```

### systemd-boot Manager

```go
// pkg/bootloader/systemdboot/systemdboot.go
package systemdboot

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
)

type SystemdBoot struct {
    log *slog.Logger
}

func (s *SystemdBoot) Name() string { return "systemd-boot" }

func (s *SystemdBoot) Install(ctx context.Context, rootPath, espPath string) error {
    // bootctl install --esp-path=<esp> --root=<root>
    cmd := exec.CommandContext(ctx, "bootctl", "install",
        "--esp-path="+espPath,
        "--root="+rootPath,
    )
    if out, err := cmd.CombinedOutput(); err != nil {
        return fmt.Errorf("bootctl install: %s: %w", string(out), err)
    }
    return nil
}

// GenerateEntry creates a systemd-boot loader entry file.
func (s *SystemdBoot) GenerateEntry(espPath string, entry BootEntry) error {
    // /boot/efi/loader/entries/<id>.conf
    entryPath := filepath.Join(espPath, "loader", "entries", entry.ID+".conf")
    content := fmt.Sprintf("title   %s\nlinux   %s\ninitrd  %s\noptions %s\n",
        entry.Title, entry.Kernel, entry.Initrd, entry.Cmdline)
    return os.WriteFile(entryPath, []byte(content), 0o644)
}

// GenerateLoaderConf creates the main loader.conf.
func (s *SystemdBoot) GenerateLoaderConf(espPath string, cfg BootConfig) error {
    loaderPath := filepath.Join(espPath, "loader", "loader.conf")
    content := fmt.Sprintf("default %s.conf\ntimeout %d\nconsole-mode max\n",
        cfg.DefaultKernel, cfg.Timeout)
    return os.WriteFile(loaderPath, []byte(content), 0o644)
}
```

### Auto-Detection

```go
// pkg/bootloader/detect.go
package bootloader

import "os"

// DetectBootloader auto-detects which bootloader is installed in the
// provisioned OS image.
func DetectBootloader(rootPath string) string {
    // Check for systemd-boot
    if _, err := os.Stat(rootPath + "/usr/lib/systemd/boot/efi/systemd-bootx64.efi"); err == nil {
        return "systemd-boot"
    }
    // Check for GRUB
    if _, err := os.Stat(rootPath + "/usr/sbin/grub-install"); err == nil {
        return "grub"
    }
    if _, err := os.Stat(rootPath + "/usr/sbin/grub2-install"); err == nil {
        return "grub" // RHEL/CentOS use grub2-*
    }
    return "unknown"
}
```

### Required Binaries in Initramfs

| Binary | Package | Purpose | Initramfs Flavor | Already Present? |
|--------|---------|---------|-----------------|-----------------|
| `efibootmgr` | `efibootmgr` | EFI boot entry management | all | **Yes** |
| `bootctl` | `systemd` | systemd-boot installation (fallback) | full, gobgp | **No — add** |
| `grub-install` | — | Runs in chroot of provisioned OS | N/A (from image) | N/A |

**Note**: `grub-install` and `update-grub` run inside the chroot of the
provisioned OS image, so they don't need to be in BOOTy's initramfs.
`bootctl` needs to be in the initramfs only as a fallback — the Go
implementation generates entry files directly.

**Dockerfile change** (tools stage):

```dockerfile
RUN apt-get update && apt-get install -y --no-install-recommends \
    ... existing packages ... \
    systemd-boot \
    && rm -rf /var/lib/apt/lists/*

# systemd-boot installer (fallback for systemd-boot setup)
COPY --from=tools /usr/bin/bootctl bin/bootctl
```

## Files Changed

| File | Change |
|------|--------|
| `pkg/bootloader/bootloader.go` | Common `Bootloader` interface |
| `pkg/bootloader/detect.go` | Auto-detection logic |
| `pkg/bootloader/grub/grub.go` | GRUB manager |
| `pkg/bootloader/systemdboot/systemdboot.go` | systemd-boot manager |
| `pkg/kexec/grub.go` | Enhanced GRUB config parsing |
| `pkg/provision/orchestrator.go` | `configureBootloader()` step |
| `initrd.Dockerfile` | Add `bootctl` binary |

## Testing

### Unit Tests

- `bootloader/detect_test.go` — Auto-detection with mock filesystem trees.
  Table-driven: GRUB, systemd-boot, GRUB2 (RHEL), unknown.
- `bootloader/grub/grub_test.go` — GRUB config generation, entry parsing.
- `bootloader/systemdboot/systemdboot_test.go` — Entry file generation,
  loader.conf generation. Verify output format matches systemd-boot spec.

### E2E / KVM Tests

- **KVM matrix** (`kvm-matrix.yml`, tag `e2e_kvm`):
  - QEMU + OVMF with GRUB-based image → verify boot entry created
  - QEMU + OVMF with systemd-boot image → verify loader entries
  - Verify kexec from both bootloader configs

## Risks

| Risk | Mitigation |
|------|------------|
| bootctl version mismatch with target OS | Run bootctl from chroot when possible |
| GRUB path differences (grub vs grub2) | Detection handles both |
| EFI partition layout varies | Configurable ESP path |

## Effort Estimate

8–12 engineering days (interface + GRUB manager + systemd-boot manager +
auto-detection + kexec integration + KVM tests).
