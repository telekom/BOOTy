# BOOTy — Project Guidelines

Lightweight initramfs agent for bare-metal OS provisioning. Boots as PID 1, orchestrates disk imaging, network setup, and OS configuration. Two modes: **CAPRF** (Cluster API + Redfish) and **Legacy** (standalone HTTP server). Supports **dry-run** (`MODE=dry-run` or `DRY_RUN=true`) for non-destructive pre-flight validation.

## Architecture

- `cmd/` — CLI entry point (Cobra)
- `pkg/provision/` — 30-step provisioning orchestrator
- `pkg/config/` — MachineConfig, Provider interface, Status types
- `pkg/network/` — Pluggable networking: DHCP, static, FRR/EVPN, GoBGP, LACP bonds
- `pkg/network/gobgp/` — Pure-Go BGP stack (underlay eBGP + overlay EVPN), three peering modes: unnumbered, dual, numbered
- `pkg/network/frr/` — FRR config rendering (legacy)
- `pkg/network/lldp/` — LLDP frame listener (raw AF_PACKET)
- `pkg/network/vlan/` — VLAN 802.1Q tagging via netlink
- `pkg/image/` — Multi-format image streaming (gzip, lz4, xz, zstd) + OCI registry
- `pkg/disk/` — Disk detection, partitioning, RAID, LVM, mount
- `pkg/caprf/` — CAPRF controller client (status/log shipping)
- `pkg/events/` — Provisioning event types and emitters
- `pkg/firmware/` — Firmware version collection from sysfs
- `pkg/health/` — Pre-provisioning hardware health checks
- `pkg/inventory/` — Hardware inventory from sysfs/procfs
- `pkg/kexec/` — GRUB config parsing, kexec load/execute
- `pkg/metrics/` — Provisioning metrics collection
- `pkg/observability/` — Logging, tracing, and observability helpers
- `pkg/realm/` — Low-level syscalls (devices, mounts, networking)
- `pkg/ux/` — ASCII art and system info display
- `server/` — Legacy HTTP provisioning server
- `test/e2e/` — E2E tests with ContainerLab and vrnetlab topologies

## Build and Test

```bash
make build              # Compile binary
make test               # Unit tests with 40% coverage gate
make lint               # golangci-lint v2
make fmt                # gofmt + goimports

# Initramfs build flavors (Linux/Docker)
make dockerx86          # Full FRR+tools (~80 MB)
make gobgp              # GoBGP variant (no FRR)
make slim               # DHCP-only (~15 MB)
make micro              # Pure Go (~10 MB)
make iso                # Bootable ISO
make gobgp-iso          # GoBGP ISO
make build-all          # Cross-compile binary for amd64+arm64
make arm64              # Full ARM64 initramfs image
make arm64-slim         # ARM64 slim initramfs
make arm64-gobgp        # ARM64 GoBGP initramfs

# E2E tests (require ContainerLab, Linux only)
make clab-up && make test-e2e-integration
make clab-gobgp-up && make test-e2e-gobgp
make clab-vrnetlab-up && make test-e2e-vrnetlab
```

## Code Style

- **Go 1.26+**, Linux-only code uses `//go:build linux`
- **Logging**: `log/slog` only — never `fmt.Print` or `logrus`
- **Errors**: `fmt.Errorf("lowercase context: %w", err)` — lowercase first letter, always wrap
- **Naming**: `ctx` for context, `cfg` for config, `mgr` for manager
- **Imports**: stdlib → external → internal (blank line between groups)

## Testing Requirements

Every feature or bug fix **must** include tests at the appropriate level. Do not
consider work complete until tests are written, passing, and cover the change.

| Test Level | When Required | How to Run |
|------------|---------------|------------|
| **Unit tests** | All code changes — minimum 40% coverage gate | `make test` |
| **Linux E2E** (`linux_e2e`) | Disk, mount, loop device, or partition code | `go test -tags linux_e2e` (requires root) |
| **ContainerLab E2E** (`e2e_integration`) | Network modes, FRR, DHCP, bonds, static | `make clab-up && make test-e2e-integration` |
| **GoBGP E2E** (`e2e_gobgp`) | GoBGP peering, tiers, PeerMode changes | `make clab-gobgp-up && make test-e2e-gobgp` |
| **Boot E2E** (`e2e_boot`) | Provisioning orchestrator, step ordering | `make clab-boot-up && make test-e2e-boot` |
| **vrnetlab / QEMU** (`e2e_vrnetlab`) | Full boot flow, kexec, EVPN fabric, ISO boot | `make clab-vrnetlab-up && make test-e2e-vrnetlab` |
| **GoBGP vrnetlab** (`e2e_gobgp_vrnetlab`) | GoBGP with real switch VMs (all PeerModes) | `make clab-gobgp-vrnetlab-up && make test-e2e-gobgp-vrnetlab` |

### When to Use KVM / QEMU Testing

Use vrnetlab (QEMU-backed) E2E tests when the change:
- Affects the **boot sequence** (PID 1 init, kexec, reboot)
- Modifies **image streaming** to block devices (not just unit-testable decompression)
- Changes **EVPN overlay** behavior that requires real switch control planes
- Alters **bootloader installation** (GRUB, systemd-boot, ESP partitions)
- Touches **Redfish integration** or ISO boot paths
- Cannot be validated with ContainerLab containers alone

vrnetlab tests run QEMU VMs inside ContainerLab — they require a Linux host
with KVM support. CI runs these on `ubuntu-latest` GitHub Actions runners.

### Test Checklist

- [ ] Unit tests cover new/changed logic with table-driven style
- [ ] Test helpers call `t.Helper()` as first line
- [ ] E2E tests use correct build tags
- [ ] KVM/QEMU tests included when touching boot flow, disk imaging, or EVPN
- [ ] No `time.Sleep` in unit tests — use channels, tickers, or mocks
- [ ] `make test` passes locally before pushing

## Conventions

- **Tests**: table-driven with `t.Helper()` in helpers; E2E tests use build tags (`e2e`, `e2e_integration`, `e2e_boot`, `e2e_vrnetlab`, `e2e_gobgp`)
- **Network code**: all `netlink` and raw-socket code is Linux-only (build-tagged)
- **Error handling**: return errors for fatal conditions, log and continue for optional features
- **Concurrency**: use `atomic` operations (not mutexes) for simple counters; channels for signaling
- **Security**: gosec enabled — exceptions documented in `.golangci.yml` for intentional device ops (G115, G204, G301, G304, G306, G706)
- **Function limits**: max 80 lines / 50 statements (exceptions: `main.go`, `pkg/ux/captain.go`)

## Code Review

Copilot PR reviews use **review personas** for focused feedback. See
[AGENTS.md](AGENTS.md) for the full setup.

- **Security** — vulns, auth, crypto, file permissions, gosec exceptions
- **Networking** — BGP, EVPN, netlink, VRF, build tags, PeerMode coverage
- **Provisioning** — disk safety, image checksums, step ordering, kexec
- **General** — slog, error wrapping, imports, tests, function complexity

Review instructions: [`instructions/review.instructions.md`](instructions/review.instructions.md)

## Agents & Prompts

| Type | Name | Purpose |
|------|------|---------|
| Agent | `caprf` | Cross-repo BOOTy ↔ CAPRF coordination |
| Agent | `security-reviewer` | Security-focused PR review |
| Agent | `networking-reviewer` | Networking-focused PR review |
| Agent | `provisioning-reviewer` | Provisioning-focused PR review |
| Prompt | `proposal` | Scaffold a design proposal document |
| Prompt | `review` | Run a structured code review |

## Related Repositories

This workspace includes companion repos — see their own README files for details:

- **cluster-api-provider-redfish** — Kubernetes CAPI provider that BOOTy reports to (Go, kubebuilder, controller-runtime)
- **virtual-bm4x** — Virtual bare-metal test environment (Kind + ContainerLab topologies)
- **gcp-dev-env** — GCP infrastructure with GKE and CI runners (OpenTofu, Ansible, Packer)
