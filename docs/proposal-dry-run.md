# Proposal: Dry-Run Mode

## Status: Proposal

## Priority: P3

## Summary

Add a **dry-run mode** to BOOTy that simulates the full provisioning pipeline
without making any destructive changes to disks, UEFI boot entries, or
network configuration. This enables pre-flight validation of provisioning
configurations on real hardware without risk.

## Motivation

Provisioning failures are expensive — a failed attempt can take 10-20 minutes
and may leave the machine in an inconsistent state. Common causes of failure
that could be caught in dry-run:

- Image URL unreachable or returns 404
- Root disk not found (wrong device name or serial)
- Insufficient disk space
- Missing kernel modules for NIC/storage
- Network misconfiguration (wrong VLAN, no DHCP response)
- Invalid partition layout
- Missing EFI shim or GRUB binaries in image

### Industry Context

| Tool | Dry-Run |
|------|---------|
| **Ironic** | No direct dry-run; uses `inspect` as pre-flight |
| **MAAS** | "Commissioning" serves as a hardware validation pass |
| **Tinkerbell** | No built-in dry-run |
| **Ansible** | `--check` mode for dry-run playbook execution |

## Design

### Dry-Run Scope

Each provisioning step has a dry-run equivalent:

| Step | Normal | Dry-Run |
|------|--------|---------|
| Network setup | Configure interfaces | Verify link state, attempt DHCP discovery |
| Image download | Stream to disk | HEAD request (verify URL, check content-length) |
| Disk selection | Select root disk | Verify disk exists, check size |
| Disk wipe | `blkdiscard` / `dd` | Skip (report current state) |
| Image streaming | Write to disk | Skip (report estimated time) |
| Partitioning | `sgdisk` + `mkfs` | Validate layout fits on disk |
| EFI setup | Write boot entries | List current entries, verify shim exists |
| Chroot config | Hostname, kubelet | Skip (report intended actions) |
| Health checks | Run checks | Run checks (non-destructive already) |
| Inventory | Collect hardware | Collect hardware (non-destructive) |

### Implementation

```go
// pkg/provision/orchestrator.go
type Orchestrator struct {
    // ... existing fields ...
    dryRun bool
}

func (o *Orchestrator) Provision(ctx context.Context) error {
    steps := o.buildSteps()

    if o.dryRun {
        return o.executeDryRun(ctx, steps)
    }
    return o.execute(ctx, steps)
}

func (o *Orchestrator) executeDryRun(ctx context.Context, steps []Step) error {
    slog.Info("=== DRY-RUN MODE ===")
    var results []DryRunResult

    for _, step := range steps {
        result := o.dryRunStep(ctx, step)
        results = append(results, result)
        slog.Info("dry-run step",
            "step", step.Name,
            "status", result.Status,
            "message", result.Message,
        )
    }

    // Report results
    return o.client.ReportDryRun(ctx, results)
}

type DryRunResult struct {
    Step    string `json:"step"`
    Status  string `json:"status"`  // "ok", "warning", "error"
    Message string `json:"message"`
}
```

### Pre-Flight Checks

```go
func (o *Orchestrator) dryRunImageCheck(ctx context.Context) DryRunResult {
    // HEAD request to verify image is accessible
    req, _ := http.NewRequestWithContext(ctx, "HEAD", o.cfg.ImageURL, nil)
    resp, err := o.httpClient.Do(req)
    if err != nil {
        return DryRunResult{
            Step:    "image-download",
            Status:  "error",
            Message: fmt.Sprintf("image unreachable: %v", err),
        }
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return DryRunResult{
            Step:    "image-download",
            Status:  "error",
            Message: fmt.Sprintf("image returned HTTP %d", resp.StatusCode),
        }
    }

    size := resp.ContentLength
    return DryRunResult{
        Step:    "image-download",
        Status:  "ok",
        Message: fmt.Sprintf("image accessible, size: %d MB", size/(1024*1024)),
    }
}

func (o *Orchestrator) dryRunDiskCheck(ctx context.Context) DryRunResult {
    disk, err := o.disk.FindRootDisk(ctx, o.cfg)
    if err != nil {
        return DryRunResult{
            Step:    "disk-selection",
            Status:  "error",
            Message: fmt.Sprintf("root disk not found: %v", err),
        }
    }

    size, _ := utils.GetBlockDeviceSize(disk)
    return DryRunResult{
        Step:    "disk-selection",
        Status:  "ok",
        Message: fmt.Sprintf("root disk: %s (%d GB)", disk, size/(1024*1024*1024)),
    }
}
```

### Configuration

```bash
# /deploy/vars
export MODE="dry-run"
# or
export DRY_RUN="true"
```

### Output

Dry-run produces a structured report:

```
=== DRY-RUN REPORT ===
✓ network-setup     : Link up on eno1 (25Gbps), DHCP offered 10.0.1.42/24
✓ image-download    : Image accessible, size: 4096 MB
✓ disk-selection    : Root disk: /dev/nvme0n1 (894 GB)
✓ partition-validate: Layout fits (used: 54 GB, available: 894 GB)
⚠ efi-boot-check   : No shimx64.efi found — will fall back to grubx64.efi
✓ health-checks     : All 6 checks passed
✓ inventory         : CPU: 2×Xeon 8380, RAM: 512 GB, NICs: 4×25G
✗ image-checksum    : No checksum configured — image integrity not verified

Result: READY (1 warning, 1 info)
```

## Required Binaries in Initramfs

No additional binaries needed. Dry-run mode reuses existing tools for
non-destructive checks:

| Binary | Package | Purpose | Initramfs Flavor | Already Present? |
|--------|---------|---------|-----------------|------------------|
| `ip` | `iproute2` | Verify link state, check DHCP | all | **Yes** |
| `sgdisk` | `gdisk` | Validate partition layout fits on disk | all | **Yes** |
| `efibootmgr` | `efibootmgr` | Check EFI boot entries | all | **Yes** |
| `curl` | `curl` | HEAD request to verify image URL | all | **Yes** |

Dry-run is a pure orchestration mode — it calls existing tools in
read-only/non-destructive mode.

## Affected Files

| File | Change |
|------|--------|
| `pkg/provision/orchestrator.go` | Add `executeDryRun()`, dry-run check methods |
| `pkg/provision/dryrun.go` | New — dry-run result types and logic |
| `pkg/provision/dryrun_test.go` | New — unit tests |
| `main.go` | Handle `MODE=dry-run` |
| `pkg/config/provider.go` | Add `DryRun bool` field |
| `pkg/caprf/client.go` | Add `ReportDryRun()` |

## Risks

- **False confidence**: Dry-run can't catch all failure modes (e.g., disk
  firmware bugs, transient network issues, kernel panics during imaging).
  Must clearly communicate that dry-run is "best effort."
- **UEFI access**: Checking EFI boot entry compatibility requires mounting
  the ESP, which may not exist on a fresh disk. Fall back to reporting
  "unable to verify."
- **Network timing**: DHCP discovery in dry-run may timeout if the machine
  isn't on the right VLAN. This is still useful information.

## Effort Estimate

- Dry-run framework: **2 days**
- Per-step dry-run implementations: **3-4 days**
- Report formatting + CAPRF endpoint: **2 days**
- Testing: **2 days**
- Total: **9-12 days**
