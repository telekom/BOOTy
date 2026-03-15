# Proposal: Rescue Mode

## Status: In Progress

## Priority: P2

## Summary

Add a **rescue mode** to BOOTy that drops the machine into an interactive
debug shell instead of provisioning, while maintaining full network
connectivity and disk access. This enables remote troubleshooting of
machines that fail provisioning, have hardware issues, or need manual
inspection.

## Motivation

When provisioning fails or a machine exhibits hardware problems, the current
options are:

1. SSH into the BMC console (iLO/XCC) — limited tooling
2. Physical access to the machine — not scalable
3. Re-provision with debug flags — destroys evidence of the failure

Rescue mode provides a fully-equipped Linux shell running in the BOOTy initrd
with:
- Network connectivity (DHCP, static, or EVPN — same as normal provisioning)
- Access to all local disks (mount, inspect, repair)
- SSH server for remote access
- All BOOTy tooling available (`lsblk`, `sgdisk`, `mokutil`, etc.)
- CAPRF heartbeat so the controller knows the machine is in rescue mode

### Industry Context

| Tool | Rescue Mode |
|------|------------|
| **Ironic** | `rescue` provision state with configurable rescue ramdisk |
| **MAAS** | "Rescue mode" boots ephemeral Ubuntu with SSH |
| **Tinkerbell** | No built-in rescue mode |

## Design

### Activation

Rescue mode is activated via the CAPRF controller or `/deploy/vars`:

```bash
# /deploy/vars
export MODE="rescue"
export RESCUE_SSH_PUBKEY="ssh-ed25519 AAAA... user@admin"
export RESCUE_PASSWORD_HASH="$6$..."  # optional console password
```

Or via standby command dispatch:

```json
{"ID": "cmd-rescue", "Type": "rescue", "Payload": {"sshPubKey": "ssh-ed25519..."}}
```

### Architecture

```
┌──────────────────────────────────────────────┐
│ BOOTy (initrd) — Rescue Mode                 │
│                                              │
│  1. Parse /deploy/vars (MODE=rescue)         │
│  2. Setup networking (same as provision)     │
│  3. Mount all local disks (read-only)        │
│  4. Start dropbear SSH server on port 22     │
│  5. Send heartbeat (status: "rescue")        │
│  6. Drop to interactive shell (/bin/sh)      │
│  7. Wait for "reboot" command or manual exit │
└──────────────────────────────────────────────┘
```

### SSH Server

Use `dropbear` — a lightweight SSH server (~110 KB binary) suitable for
initrd environments:

```go
// main.go — rescue mode entry
func runRescue(cfg *config.MachineConfig) error {
    slog.Info("entering rescue mode")

    // Setup networking
    if err := setupNetworking(cfg); err != nil {
        return fmt.Errorf("setup networking for rescue: %w", err)
    }

    // Write SSH authorized_keys
    if cfg.RescueSSHPubKey != "" {
        if err := os.MkdirAll("/root/.ssh", 0700); err != nil {
            return err
        }
        if err := os.WriteFile("/root/.ssh/authorized_keys",
            []byte(cfg.RescueSSHPubKey), 0600); err != nil {
            return err
        }
    }

    // Start dropbear SSH daemon
    cmd := exec.Command("dropbear", "-R", "-F", "-p", "22")
    if err := cmd.Start(); err != nil {
        slog.Warn("failed to start SSH server", "error", err)
    }

    // Auto-mount local disks read-only
    mountLocalDisks()

    // Heartbeat loop (rescue status)
    go rescueHeartbeat(cfg)

    // Interactive shell or wait for reboot command
    shell := exec.Command("/bin/sh")
    shell.Stdin = os.Stdin
    shell.Stdout = os.Stdout
    shell.Stderr = os.Stderr
    return shell.Run()
}
```

### Disk Access

In rescue mode, all detected block devices are mounted read-only under
`/rescue/`:

```
/rescue/sda1  → first partition of sda
/rescue/sda2  → second partition
/rescue/nvme0n1p1 → first NVMe partition
```

The operator can remount read-write if needed:

```bash
mount -o remount,rw /rescue/sda2
```

### CAPRF Integration

The controller tracks rescue mode as a distinct machine phase:

```go
// api/v1alpha1/redfishmachine_phases.go
const PhaseRescue MachinePhase = "Rescue"
```

The heartbeat includes rescue status:

```json
POST /status/heartbeat
{
    "status": "rescue",
    "ssh_host": "10.0.1.42",
    "ssh_port": 22,
    "uptime_seconds": 3600
}
```

### Exit from Rescue

1. **CAPRF command**: Send `reboot` or `provision` command via standby channel
2. **Manual reboot**: Operator types `reboot` at the rescue shell
3. **Timeout**: Optional auto-reboot after configurable duration

## Required Binaries in Initramfs

| Binary | Package | Purpose | Initramfs Flavor | Already Present? |
|--------|---------|---------|-----------------|------------------|
| `dropbear` | `dropbear-bin` | Lightweight SSH server (~110 KB) | full, gobgp | **No — add** |
| `mount` | busybox | Mount local disks in rescue mode | all | **Yes** (busybox) |
| `lsblk` | busybox | List block devices for auto-mount | all | **Yes** (busybox) |

**Dockerfile change** (tools stage):

```dockerfile
RUN apt-get update && apt-get install -y --no-install-recommends \
    ... existing packages ... \
    dropbear-bin \
    && rm -rf /var/lib/apt/lists/*

COPY --from=tools /usr/sbin/dropbear bin/dropbear
```

**Note**: `dropbear` adds only ~110 KB to the initramfs. It is included
in full and gobgp flavors only. Slim and micro flavors do not support
rescue mode.

## Affected Files

| File | Change |
|------|--------|
| `main.go` | Add `runRescue()` function, wire to `MODE=rescue` |
| `pkg/config/provider.go` | Add `RescueSSHPubKey`, `RescuePasswordHash` |
| `initrd.Dockerfile` | Add `dropbear` SSH server binary |
| `pkg/caprf/client.go` | Extend heartbeat with rescue status fields |
| CAPRF `api/v1alpha1/redfishmachine_phases.go` | Add `PhaseRescue` |

## Configuration

```bash
# /deploy/vars
export MODE="rescue"
export RESCUE_SSH_PUBKEY="ssh-ed25519 AAAA..."
export RESCUE_TIMEOUT="3600"  # auto-reboot after 1 hour (0 = no timeout)
```

## Risks

- **Security**: SSH access to a bare-metal machine with disk access is
  sensitive. The SSH key must be tightly controlled. Consider allowing only
  keys from the CAPRF secret store.
- **Dropbear size**: Adds ~110 KB to the initrd. Acceptable.
- **Disk corruption**: Mounting production disks in rescue mode risks
  accidental writes. Default to read-only mounts.

## Effort Estimate

- Rescue mode entry point + networking: **2 days**
- Dropbear integration + SSH key setup: **1-2 days**
- Disk auto-mount: **1 day**
- CAPRF phase + heartbeat: **1-2 days**
- Total: **5-7 days**
