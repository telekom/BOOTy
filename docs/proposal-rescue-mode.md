# Proposal: Rescue Mode

## Status: In Progress

## Priority: P2

## Summary

Add a **rescue mode** to BOOTy that provides configurable failure recovery
when provisioning fails. Phase 1 implements a failure-recovery decision
engine with four modes (reboot, retry, shell, wait). Phase 2 will add an
interactive SSH-accessible rescue environment.

## Motivation

When provisioning fails or a machine exhibits hardware problems, the current
options are:

1. SSH into the BMC console (iLO/XCC) — limited tooling
2. Physical access to the machine — not scalable
3. Re-provision with debug flags — destroys evidence of the failure

Rescue mode provides configurable recovery behavior after provisioning
failure, allowing operators to choose the best strategy for their
environment.

### Industry Context

| Tool | Rescue Mode |
|------|------------|
| **Ironic** | `rescue` provision state with configurable rescue ramdisk |
| **MAAS** | "Rescue mode" boots ephemeral Ubuntu with SSH |
| **Tinkerbell** | No built-in rescue mode |

## Design

### Phase 1: Failure Recovery (Implemented)

Phase 1 provides a configurable recovery strategy when provisioning fails,
controlled via the `RESCUE_MODE` variable in CAPRF `/deploy/vars`:

```bash
# /deploy/vars
export RESCUE_MODE="retry"   # reboot | retry | shell | wait
```

#### Recovery Modes

| Mode | Behavior |
|------|----------|
| `reboot` | (Default) Reboot the machine immediately |
| `retry` | Retry provisioning up to MaxRetries times (default: 3) with 30s delay |
| `shell` | Drop to a local debug shell (via `realm.Shell()`) |
| `wait` | Wait indefinitely for manual intervention |

#### Architecture

```
┌─────────────────────────────────────────────┐
│ BOOTy (initrd) — Failure Recovery           │
│                                             │
│  1. Run provisioning steps                  │
│  2. On failure:                             │
│     a. Call RescueAction(retryState)        │
│     b. Decide action from RESCUE_MODE       │
│     c. retry → loop back to step 1         │
│     d. shell → realm.Shell()               │
│     e. wait  → block indefinitely          │
│     f. reboot → fall through to reboot     │
└─────────────────────────────────────────────┘
```

#### Implementation

- `pkg/rescue/rescue.go` — Config, Validate, ApplyDefaults, Decide, RetryState
- `pkg/rescue/rescue_test.go` — Comprehensive unit tests
- `pkg/provision/orchestrator.go` — `RescueAction()` method bridges config to rescue
- `main.go` — Wires `RescueAction()` into provisioning failure paths
- `pkg/config/provider.go` — `RescueMode` field parsed from CAPRF vars
- `pkg/caprf/client.go` — `RESCUE_MODE` env var mapping

### Phase 2: Full Rescue Mode (Future)

Phase 2 will add a full interactive rescue environment with:
- SSH server (dropbear) for remote access
- Auto-mount local disks under `/rescue/`
- CAPRF heartbeat with rescue status
- Configurable auto-reboot timeout

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
