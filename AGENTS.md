# BOOTy — Coding Agent Guidance

## OVERVIEW
Lightweight initramfs agent for bare-metal OS provisioning (not a K8s operator). Boots as PID 1, orchestrates 36 provisioning steps including disk imaging, BGP/EVPN networking, and kexec.

## STRUCTURE
| Directory | Purpose |
|-----------|---------|
| `pkg/network/` | Pluggable networking: `gobgp` (pure Go), `frr` (legacy), `lldp` (raw frames) |
| `pkg/provision/` | The 36-step state machine for image/disk/network/kexec flow |
| `pkg/realm/` | Low-level Linux primitives: syscalls, mounts, device creation |
| `pkg/bios/` | Vendor-specific BIOS management (Dell, HPE, Lenovo, Supermicro) |
| `test/e2e/clab/` | ContainerLab topologies for network/provisioning integration tests |

## WHERE TO LOOK
| Task | Location |
|------|----------|
| Entry point / PID 1 init | `main.go` (orchestrator logic) |
| Network stack selection | `main.go` (setupNetworkMode) |
| Disk / Partitioning | `pkg/disk/` |
| Image streaming/OCI | `pkg/image/` |
| BGP/EVPN logic | `pkg/network/gobgp/` or `pkg/network/frr/` |
| E2E tests | `test/e2e/integration/` |

## CONVENTIONS
- **Linux-only**: Files must have `//go:build linux` at the top.
- **Logging**: Use `log/slog` exclusively. Never use `fmt.Print` or `logrus`.
- **Complexity**: Max 80 lines / 50 statements per function (strict `funlen` lint).
- **Concurrency**: Prefer `atomic` operations over mutexes for simple state.
- **E2E Tests**: Use specific build tags: `e2e_integration`, `e2e_gobgp`, `e2e_boot`, `e2e_vrnetlab`.

## ANTI-PATTERNS
- **No interactive prompts**: The agent runs unattended; all logic must be automated.
- **No `time.Sleep` in tests**: Use channels, tickers, or context cancellation.
- **No shell-outs**: Prefer pure Go or direct syscalls (e.g. `unix.FinitModule`) over `exec.Command`.

## COMMANDS
| Action | Command |
|--------|---------|
| Compile | `make build` |
| Unit Tests | `make test` (40% coverage gate) |
| Full Initramfs | `make dockerx86` |
| Build ISO | `make iso` |
| E2E Network | `make clab-up && make test-e2e-integration` |
| E2E Boot | `make clab-boot-up && make test-e2e-boot` |
| E2E QEMU/KVM | `make clab-vrnetlab-up && make test-e2e-vrnetlab` |

## NOTES
- **PID 1**: BOOTy manages its own mounts/devices in early init. See `main.go:setupMountsAndDevices`.
- **Dry Run**: Supports `MODE=dry-run` or `DRY_RUN=true` for non-destructive validation.
- **Copilot**: See `.github/AGENTS.md` for specialized review personas (Security, Networking, Provisioning).
