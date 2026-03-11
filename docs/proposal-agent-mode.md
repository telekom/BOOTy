# Proposal: Standby Mode for BOOTy

## Status: Partially Implemented

The core standby loop, heartbeat, and command polling are implemented in
`main.go` (`runStandby`) and `pkg/caprf/client.go` (`Heartbeat`,
`FetchCommands`). The CAPRF server-side endpoints and hot-pool scheduling
remain future work.

## Summary

Extend BOOTy to support a **hot standby** mode where machines boot into the
initrd, establish network connectivity, and idle — sending periodic heartbeats
while polling for commands. When a `provision`, `deprovision`, or `reboot`
command arrives, the machine executes it immediately without a full
PXE-boot/DHCP cycle, reducing provisioning latency from minutes to seconds.

## Motivation

The current flow requires a full iPXE → DHCP → HTTP download → provision
cycle for every operation. For large fleets, this creates a thundering-herd
problem and leaves machines idle in BIOS while waiting. Standby mode keeps
machines "warm" in the ramdisk so provisioning is just a command dispatch away.

Key benefits:
- **Sub-second provisioning start**: No PXE/DHCP/download cycle.
- **Hot pool scheduling**: CAPRF controller can maintain a pool of ready
  machines and assign them to clusters on demand.
- **Graceful drain workflows**: Move machines to standby before reassignment.
- **Firmware updates**: Issue firmware commands to idle standby machines.

## Design

### Activation

Standby mode is activated via `/deploy/vars`:

```bash
export MODE="standby"
export HEARTBEAT_URL="http://caprf-server/status/heartbeat"
export COMMANDS_URL="http://caprf-server/commands"
```

When `MODE=standby`, `runCAPRF()` enters the `runStandby()` loop instead of
the normal provision/deprovision path.

### Architecture

```
┌──────────────────────────────────────────┐
│  BOOTy (initrd, persistent ramdisk)      │
│                                          │
│  runCAPRF()                              │
│    ├─ parse /deploy/vars                 │
│    ├─ setup networking (FRR or DHCP)     │
│    ├─ wait for connectivity              │
│    └─ switch cfg.Mode                    │
│         ├─ "standby" → runStandby()      │
│         ├─ "provision" → orch.Provision()│
│         └─ "deprovision" → orch.Deprov() │
│                                          │
│  runStandby()                            │
│    ├─ heartbeat ticker (30s)             │
│    ├─ command poll ticker (10s)          │
│    └─ command dispatch:                  │
│         ├─ "provision" → Provision+kexec │
│         ├─ "deprovision" → Deprovision   │
│         └─ "reboot" → reboot             │
└──────────────────────────────────────────┘
```

### Server-Side Contract

**Heartbeat endpoint** (`POST /status/heartbeat`):
- Machine sends periodic keepalive with auth token.
- Server updates last-seen timestamp in machine status.
- `204 No Content` on success.

**Commands endpoint** (`GET /commands`):
- Returns a JSON array of pending commands.
- `204 No Content` when nothing is pending.
- `200 OK` with body:

```json
[{"ID": "cmd-abc", "Type": "provision", "Payload": null}]
```

### Command Types

| Type          | Action                                      |
|---------------|---------------------------------------------|
| `provision`   | Execute full provisioning, then kexec/reboot|
| `deprovision` | Wipe disks and reboot                       |
| `reboot`      | Immediate reboot (e.g. to re-enter standby) |

Future command types (not yet implemented):
- `firmware-update`: Apply NIC/BIOS firmware from a given URL.
- `health-check`: Run hardware diagnostics and report results.

### Client Implementation

The `caprf.Client` implements real HTTP calls for both endpoints:

- `Heartbeat(ctx)`: POST to `HeartbeatURL` via `postWithAuth`. Returns nil
  when no URL is configured (backward-compatible no-op).
- `FetchCommands(ctx)`: GET `CommandsURL` with Authorization header. Decodes
  JSON array of `config.Command`. Returns nil on `204`/`404`.

Both methods are tested with `httptest.Server` mocks.

### Existing Implementation

| File | What |
|------|------|
| `main.go` `runStandby()` | Heartbeat + poll loop with select, command dispatch |
| `pkg/caprf/client.go` | `Heartbeat()`, `FetchCommands()` with real HTTP calls |
| `pkg/config/provider.go` | `HeartbeatURL`, `CommandsURL` fields on `MachineConfig` |
| `pkg/caprf/client.go` | `applyVar()` wires `HEARTBEAT_URL` and `COMMANDS_URL` |

## Risks

- **Stale standby**: If heartbeat stops, server must detect and handle the
  timeout (e.g., power-cycle via Redfish).
- **Network partition**: Machine may miss commands; server should re-queue.
- **Memory pressure**: Long-running ramdisk processes accumulate memory.
  Periodic reboots or memory limits may be needed.

## Future Work

1. **CAPRF server endpoints**: Implement `/status/heartbeat` and `/commands`
   in the CAPRF controller.
2. **Hot pool scheduler**: Maintain a pool of standby machines, assign to
   clusters via `provision` command when capacity is needed.
3. **Command acknowledgement**: After executing a command, report result back
   so the server can remove it from the queue.
4. **Exponential backoff**: On repeated heartbeat failures, back off to avoid
   hammering a down server.
5. **Additional command types**: Firmware updates, health checks, config drift.
