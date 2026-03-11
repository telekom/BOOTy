# Proposal: Agent Mode for BOOTy

## Summary

Extend BOOTy to operate as a persistent agent on provisioned machines, enabling ongoing management tasks beyond the initial provisioning lifecycle.

## Motivation

Currently BOOTy runs once during provisioning and then kexecs/reboots into the installed OS. Post-provisioning tasks (firmware updates, configuration drift remediation, re-imaging) require re-mounting the ISO and re-running the full provisioning flow.

An agent mode would allow BOOTy to remain resident (or be invoked on-demand) for:
- Rolling firmware updates coordinated by the CAPRF controller
- Configuration drift detection and remediation
- Health checks and hardware diagnostics
- Coordinated drain-and-reimage workflows

## Design

### API Extension

The `Provider` interface already includes `Heartbeat()` and `FetchCommands()` as no-op placeholders:

```go
type Provider interface {
    // ... existing methods ...
    Heartbeat(ctx context.Context) error
    FetchCommands(ctx context.Context) ([]Command, error)
}
```

Agent mode would implement these:
- `Heartbeat`: Periodic keepalive to CAPRF controller (every 30s)
- `FetchCommands`: Poll for pending commands (firmware update, config apply, health check)

### Command Types

```go
type Command struct {
    ID      string
    Type    string  // "firmware-update", "config-apply", "health-check", "reimage"
    Payload []byte  // JSON-encoded parameters
}
```

### Activation

Agent mode would be activated via `/deploy/vars`:

```bash
export MODE="agent"
export AGENT_HEARTBEAT_INTERVAL="30s"
export AGENT_COMMAND_POLL_INTERVAL="10s"
```

### Architecture

```
┌─────────────────────────────┐
│  BOOTy Agent (systemd unit) │
│                             │
│  ┌───────────┐  ┌─────────┐│
│  │ Heartbeat │  │ Command ││
│  │  Loop     │  │ Executor││
│  └─────┬─────┘  └────┬────┘│
│        │              │     │
│  ┌─────▼──────────────▼───┐ │
│  │   CAPRF Client         │ │
│  └────────────────────────┘ │
└─────────────────────────────┘
```

### Deployment

Agent mode BOOTy would be installed as a systemd unit on the provisioned OS, started after the initial provision completes. The CAPRF controller would manage the lifecycle.

## Risks

- **Security**: Agent has root access; command execution must be restricted to known safe operations
- **Resource usage**: Persistent agent on every node adds overhead
- **Complexity**: State management for long-running operations

## Alternatives

- **SSH-based management**: Use existing SSH access for post-provisioning tasks. Simpler but less integrated with CAPRF state machine.
- **Kubernetes DaemonSet**: Deploy management agent as a DaemonSet. Requires the cluster to be healthy first.

## Next Steps

1. Define the full command type catalog
2. Implement heartbeat loop with exponential backoff on failure
3. Implement command executor with idempotency guarantees
4. Add agent mode systemd unit to the ramdisk build
5. Extend CAPRF controller to manage agent lifecycle
