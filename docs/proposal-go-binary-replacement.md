# Proposal: Go Binary Replacement for Shell Tools

## Status: Proposal

## Priority: P3

## Summary

Systematically replace external shell tool invocations with pure Go
implementations where feasible. This reduces initramfs size, eliminates
binary compatibility issues, improves error handling, and moves toward the
`micro` flavor vision of a single-binary provisioner. Prioritized by impact
and implementation difficulty.

## Motivation

BOOTy currently shells out to ~25 external binaries. Each adds to initramfs
size, introduces versioning concerns, and requires error parsing from
command output. Many of these operations have Go equivalents via `syscall`,
`os`, `net`, or well-maintained Go libraries:

| External Binary | Approx Size | Go Replacement Feasible? |
|----------------|------------|------------------------|
| `ip` (iproute2) | 800 KB | Partial (vishvananda/netlink covers 80%) |
| `bridge` (iproute2) | 600 KB | Yes (vishvananda/netlink) |
| `ethtool` | 200 KB | Partial (mdlayher/ethtool) |
| `dmidecode` | 100 KB | Yes (digitalocean/go-smbios) |
| `sgdisk` | 300 KB | Partial (diskfs/go-diskfs) |
| `parted` | 500 KB | Partial (same) |
| `hdparm` | 150 KB | Partial (sysfs + ioctl) |
| `curl` | 400 KB | Yes (net/http) |
| `efibootmgr` | 50 KB | Yes (efivarfs direct) |
| `lldpcli` | 200 KB | Partial (Go LLDP parser) |
| `wipefs` | 50 KB | Yes (zero first/last 1MB) |
| `sfdisk` | 300 KB | Partial (go-diskfs) |
| `partprobe` | 50 KB | Yes (BLKRRPART ioctl) |
| `busybox` | 2 MB | Partial (u-root project) |
| `mstconfig` | 15 MB | No (Mellanox proprietary) |
| `mstflint` | 10 MB | No (Mellanox proprietary) |

### Replacement Priority Matrix

| Priority | Binary | Go Library | Savings | Risk |
|----------|--------|-----------|---------|------|
| P1 | `curl` | `net/http` (stdlib) | 400 KB | None — already unused |
| P1 | `wipefs` | Direct I/O (stdlib) | 50 KB | Low |
| P1 | `partprobe` | BLKRRPART ioctl | 50 KB | Low |
| P1 | `efibootmgr` | efivarfs direct | 50 KB | Medium |
| P2 | `dmidecode` | `go-smbios` | 100 KB | Low |
| P2 | `ethtool` | `mdlayher/ethtool` | 200 KB | Medium (partial coverage) |
| P2 | `hdparm` | Sysfs + ioctl | 150 KB | Medium |
| P3 | `sgdisk`/`sfdisk` | `go-diskfs` | 600 KB | High (partition table edits) |
| P3 | `parted` | `go-diskfs` | 500 KB | High |
| Never | `mstconfig` | Vendor-proprietary | N/A | Not feasible |
| Never | `mstflint` | Vendor-proprietary | N/A | Not feasible |
| Never | `lvm` | Kernel DM interface | N/A | Too complex |

## Design

### Phase 1: Easy Wins (P1)

#### wipefs → Go

```go
// pkg/disk/wipe.go
package disk

import (
    "fmt"
    "os"
)

// WipeFS removes filesystem/partition signatures from a device.
// Equivalent to `wipefs -af <device>`.
func WipeFS(device string) error {
    f, err := os.OpenFile(device, os.O_WRONLY, 0)
    if err != nil {
        return fmt.Errorf("open %s for wipe: %w", device, err)
    }
    defer f.Close()

    // Zero first 1 MiB (covers GPT, MBR, and most FS superblocks)
    zeros := make([]byte, 1<<20)
    if _, err := f.Write(zeros); err != nil {
        return fmt.Errorf("zero start of %s: %w", device, err)
    }

    // Zero last 1 MiB (covers backup GPT header)
    stat, err := f.Stat()
    if err != nil {
        return fmt.Errorf("stat %s: %w", device, err)
    }
    if stat.Size() > 1<<20 {
        if _, err := f.WriteAt(zeros, stat.Size()-int64(1<<20)); err != nil {
            return fmt.Errorf("zero end of %s: %w", device, err)
        }
    }

    return nil
}
```

#### partprobe → Go

```go
// pkg/disk/partprobe.go
//go:build linux

package disk

import (
    "fmt"
    "os"

    "golang.org/x/sys/unix"
)

// RereadPartitions triggers kernel re-read of partition table.
// Equivalent to `partprobe <device>`.
func RereadPartitions(device string) error {
    f, err := os.Open(device)
    if err != nil {
        return fmt.Errorf("open %s: %w", device, err)
    }
    defer f.Close()

    // BLKRRPART ioctl
    if err := unix.IoctlSetInt(int(f.Fd()), unix.BLKRRPART, 0); err != nil {
        return fmt.Errorf("BLKRRPART ioctl on %s: %w", device, err)
    }
    return nil
}
```

#### efibootmgr → Go

```go
// pkg/bootloader/efi/vars.go
//go:build linux

package efi

import (
    "encoding/binary"
    "fmt"
    "os"
    "path/filepath"
)

const efiVarsPath = "/sys/firmware/efi/efivars"

// CreateBootEntry creates an EFI boot entry via efivarfs.
// Equivalent to `efibootmgr -c -d <disk> -p <part> -l <loader> -L <label>`.
func CreateBootEntry(num int, label string, diskGUID string, loaderPath string) error {
    // Build EFI_LOAD_OPTION structure:
    // Attributes (4 bytes) + FilePathListLength (2 bytes) +
    // Description (null-terminated UCS-2) + FilePath (EFI Device Path)

    varName := fmt.Sprintf("Boot%04X-8be4df61-93ca-11d2-aa0d-00e098032b8c", num)
    varPath := filepath.Join(efiVarsPath, varName)

    // Prepend 4-byte attribute flags (EFI_VARIABLE_NON_VOLATILE |
    // EFI_VARIABLE_BOOTSERVICE_ACCESS | EFI_VARIABLE_RUNTIME_ACCESS = 0x07)
    var buf []byte
    buf = binary.LittleEndian.AppendUint32(buf, 0x07)

    // Build EFI_LOAD_OPTION structure
    loadOption := buildLoadOption(label, diskGUID, loaderPath)
    buf = append(buf, loadOption...)

    return os.WriteFile(varPath, buf, 0o644)
}
```

### Phase 2: Medium Complexity (P2)

#### dmidecode → Go

```go
// pkg/inventory/smbios.go
package inventory

import (
    "github.com/digitalocean/go-smbios/smbios"
)

// ReadSMBIOS parses SMBIOS tables directly from /sys/firmware/dmi/tables/smbios_entry_point.
// Replaces `dmidecode -t <type>`.
func ReadSMBIOS() (*SystemInfo, error) {
    stream, entryPoint, err := smbios.Stream()
    if err != nil {
        return nil, fmt.Errorf("open SMBIOS stream: %w", err)
    }
    defer stream.Close()

    d := smbios.NewDecoder(stream)
    ss, err := d.Decode()
    if err != nil {
        return nil, fmt.Errorf("decode SMBIOS: %w", err)
    }

    info := &SystemInfo{}
    for _, s := range ss {
        switch s.Header.Type {
        case 1: // System Information
            info.Manufacturer = s.Strings[0]
            info.ProductName = s.Strings[1]
            info.SerialNumber = s.Strings[2]
        case 2: // Baseboard
            info.BoardVendor = s.Strings[0]
            info.BoardName = s.Strings[1]
        case 3: // Chassis
            info.ChassisType = s.Strings[0]
        }
    }
    return info, nil
}
```

### Phase 3: Validate Replacement

```go
// pkg/disk/wipe_test.go
package disk

import (
    "os"
    "testing"
)

func TestWipeFS(t *testing.T) {
    // Create a temp file simulating a block device
    f, err := os.CreateTemp(t.TempDir(), "wipe-test-*")
    if err != nil {
        t.Fatal(err)
    }
    // Write non-zero data
    data := make([]byte, 2<<20) // 2 MiB
    for i := range data {
        data[i] = 0xFF
    }
    f.Write(data)
    f.Close()

    // Wipe
    if err := WipeFS(f.Name()); err != nil {
        t.Fatal(err)
    }

    // Verify first 1 MiB is zeroed
    wiped, _ := os.ReadFile(f.Name())
    for i := 0; i < 1<<20; i++ {
        if wiped[i] != 0 {
            t.Fatalf("byte %d not zeroed: %02x", i, wiped[i])
        }
    }
}
```

### Required Binaries in Initramfs

This proposal *removes* binaries from initramfs (over time):

| Phase | Removed Binaries | Size Savings |
|-------|-----------------|-------------|
| Phase 1 | `wipefs`, `partprobe` | ~100 KB |
| Phase 2 | `dmidecode`, `efibootmgr` | ~150 KB |
| Phase 3 | `sgdisk`/`sfdisk` (micro only) | ~600 KB |

### Go Dependencies

| Package | Purpose | Already Used? |
|---------|---------|--------------|
| `golang.org/x/sys/unix` | Syscalls (ioctl) | **Yes** |
| `github.com/digitalocean/go-smbios` | SMBIOS parsing | **No — add** |
| `github.com/mdlayher/ethtool` | Ethtool operations | **No — add** |
| `github.com/diskfs/go-diskfs` | Partition table operations | **No — evaluate** |

## Files Changed

| File | Change |
|------|--------|
| `pkg/disk/wipe.go` | Go wipefs replacement |
| `pkg/disk/partprobe.go` | Go partprobe replacement |
| `pkg/bootloader/efi/vars.go` | Go efibootmgr replacement |
| `pkg/inventory/smbios.go` | Go dmidecode replacement |
| `initrd.Dockerfile` | Remove replaced binaries from COPY lines |

## Testing

### Unit Tests

- Each replacement must pass identical test scenarios as the binary:
  - `disk/wipe_test.go` — Verify FS signatures zeroed
  - `disk/partprobe_test.go` — Verify ioctl call (mock fd)
  - `bootloader/efi/vars_test.go` — EFI variable format correctness
  - `inventory/smbios_test.go` — SMBIOS parsing with fixture data

### E2E / KVM Tests

- **KVM matrix** (`kvm-matrix.yml`, tag `e2e_kvm`):
  - Full provisioning flow with Go replacements (no binary fallback)
  - Compare output with binary versions

### Validation Strategy

Each replacement goes through:
1. Implement Go version alongside binary version
2. Run both in parallel, compare results
3. Switch default to Go version
4. Remove binary from Dockerfile after one release cycle

## Risks

| Risk | Mitigation |
|------|------------|
| Go implementation misses edge cases | Parallel validation phase |
| SMBIOS table variations | Test on multiple vendors |
| EFI variable format errors | Extensive test with OVMF |
| Partition table corruption | Go implementation is read-only first |

## Effort Estimate

12–18 engineering days (Phase 1: 3 days, Phase 2: 5 days, Phase 3: 4 days,
validation: 4 days).
