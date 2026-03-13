# BOOTy — Project Guidelines

Lightweight initramfs agent for bare-metal OS provisioning. Boots as PID 1, orchestrates disk imaging, network setup, and OS configuration. Two modes: **CAPRF** (Cluster API + Redfish) and **Legacy** (standalone HTTP server).

## Architecture

- `cmd/` — CLI entry point (Cobra)
- `pkg/provision/` — 25-step provisioning orchestrator
- `pkg/network/` — Pluggable networking: DHCP, static, FRR/EVPN, GoBGP, LACP bonds
- `pkg/network/gobgp/` — Pure-Go BGP stack (underlay eBGP + overlay EVPN), three peering modes: unnumbered, dual, numbered
- `pkg/network/frr/` — FRR config rendering (legacy)
- `pkg/image/` — Multi-format image streaming (gzip, lz4, xz, zstd) + OCI registry
- `pkg/disk/` — Disk detection, partitioning, RAID, LVM, mount
- `pkg/caprf/` — CAPRF controller client (status/log shipping)
- `pkg/realm/` — Low-level syscalls (devices, mounts, networking)
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

## Conventions

- **Tests**: table-driven with `t.Helper()` in helpers; E2E tests use build tags (`e2e`, `e2e_integration`, `e2e_boot`, `e2e_vrnetlab`, `e2e_gobgp`)
- **Network code**: all `netlink` and raw-socket code is Linux-only (build-tagged)
- **Error handling**: return errors for fatal conditions, log and continue for optional features
- **Concurrency**: use `atomic` operations (not mutexes) for simple counters; channels for signaling
- **Security**: gosec enabled — exceptions documented in `.golangci.yml` for intentional device ops (G115, G204, G301, G304, G306, G706)
- **Function limits**: max 80 lines / 50 statements (exceptions: `main.go`, `pkg/ux/captain.go`)

## Related Repositories

This workspace includes companion repos — see their own README files for details:

- **cluster-api-provider-redfish** — Kubernetes CAPI provider that BOOTy reports to (Go, kubebuilder, controller-runtime)
- **virtual-bm4x** — Virtual bare-metal test environment (Kind + ContainerLab topologies)
- **gcp-dev-env** — GCP infrastructure with GKE and CI runners (OpenTofu, Ansible, Packer)
