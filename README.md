# BOOTy

[![CI](https://github.com/telekom/BOOTy/actions/workflows/ci.yml/badge.svg)](https://github.com/telekom/BOOTy/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/telekom/BOOTy)](https://goreportcard.com/report/github.com/telekom/BOOTy)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

A lightweight initramfs agent for bare-metal OS provisioning over the network.

BOOTy boots as the init process inside a minimal initramfs, contacts a provisioning server, and orchestrates the full lifecycle of a bare-metal machine: disk imaging, OS configuration, network setup, and reboot. It supports two operating modes — **CAPRF integration** for Kubernetes cluster provisioning and **legacy mode** for standalone image deployment.

> **Warning** — This software has **no guard rails**. Incorrect use can overwrite an existing Operating System.

## Architecture

BOOTy operates in two modes depending on the boot environment:

### CAPRF Mode (Cluster API Provider Redfish)

```
┌─────────────┐     ┌─────────────────┐     ┌──────────────────────┐
│  Redfish BMC │────▶│  BOOTy initrd   │────▶│   CAPRF Controller   │
│  (ISO boot)  │     │  /deploy/vars   │     │   (status/log/debug) │
└─────────────┘     └───────┬─────────┘     └──────────────────────┘
                            │
               ┌────────────┼────────────┐
               │            │            │
        ┌──────▼──┐  ┌──────▼──┐  ┌──────▼──────┐
        │ Network │  │  Disk   │  │    OS       │
        │ FRR/DHCP│  │ Stream  │  │ Configure   │
        └─────────┘  └─────────┘  └─────────────┘
```

1. A Redfish BMC mounts an ISO containing a kernel, BOOTy initramfs, and `/deploy/vars` config.
2. BOOTy reads `/deploy/vars` for machine config, image URLs, and CAPRF server endpoints.
3. Network connectivity is established via **FRR/EVPN** (BGP underlay) or **DHCP** fallback.
4. The provisioning pipeline runs 24 steps: status reporting → RAID cleanup → disk detection → image streaming → partition management → OS configuration → kexec.
5. Status, logs, and debug info are shipped back to the CAPRF controller throughout.

### Legacy Mode

```
┌──────────────┐         ┌──────────────────┐
│  PXE / iPXE  │────────▶│   BOOTy initrd   │
│  Boot loader │         │  (kernel + cpio)  │
└──────────────┘         └───────┬──────────┘
                                 │ DHCP / HTTP
                         ┌───────▼──────────┐
                         │  BOOTy Server     │
                         │  (config + images)│
                         └──────────────────┘
```

1. A bare-metal server PXE-boots with a kernel and the BOOTy initramfs.
2. BOOTy obtains an IP via DHCP and fetches its configuration from the provisioning server using its MAC address.
3. Depending on the `action` field in the config, BOOTy either writes an image to disk or reads a disk and uploads it.

## Features

- **Dual-mode provisioning** — CAPRF (Kubernetes) and legacy (standalone) modes
- **FRR/EVPN networking** — BGP underlay with VXLAN overlay for data center fabrics
- **Static IP networking** — Direct IP assignment via netlink (no external tools)
- **LACP bond** — 802.3ad link aggregation with configurable bond modes
- **DHCP fallback** — Automatic DHCP on all physical interfaces with connectivity check
- **Broad NIC driver support** — Intel (e1000e, igb, igc, ixgbe, i40e, ice), Broadcom (tg3, bnxt_en), Mellanox/NVIDIA (mlx4, mlx5), plus virtio for VMs
- **Multi-format image streaming** — Gzip, lz4, xz, zstd decompression with auto-detection
- **OCI registry support** — Pull images from OCI registries (authenticated & unauthenticated) via `oci://` URLs
- **HTTP retry with backoff** — Automatic exponential backoff retry for image downloads and OCI pulls
- **Secure erase** — NVMe format (SES1) and ATA Security Erase for full disk sanitization
- **Software RAID** — mdadm array creation (RAID 0/1/5/6/10)
- **Filesystem support** — ext2, ext3, ext4, xfs, btrfs, vfat mount/resize
- **LLDP discovery** — Raw AF_PACKET-based LLDP listener for switch topology discovery
- **Post-provision hooks** — Execute arbitrary commands in chroot after OS configuration
- **25-step provisioning pipeline** — RAID cleanup, disk detection, image streaming, partition growth, LVM, filesystem resize, OS configuration, EFI boot, Mellanox SR-IOV, post-provision hooks
- **Kexec support** — Fast reboot into installed kernel without full BIOS POST (auto-disabled after firmware changes)
- **Remote logging** — Real-time log and debug shipping to CAPRF controller
- **Hard/soft deprovisioning** — Full disk wipe or GRUB rename for reprovisioning
- **Standby mode** — Hot standby with heartbeats and command polling for sub-second provisioning
- **Multi-architecture** — Builds for `linux/amd64` and `linux/arm64`
- **Multiple build flavors** — Full (FRR+tools), slim (DHCP-only), micro (pure Go), ISO (bootable)

## Prerequisites

- Go **1.26+**
- Docker (for building the initramfs)
- A DHCP/PXE environment (legacy mode) or Redfish BMC with ISO virtual media (CAPRF mode)

## Building

### Initramfs (recommended)

Build the complete initramfs with Docker:

```bash
make build
```

This compiles BOOTy for `linux/amd64` and `linux/arm64`, then packages BusyBox, LVM2, FRR, and kernel modules for common server NICs into a bootable initramfs.

To extract the initramfs to the local filesystem:

```bash
docker run ghcr.io/telekom/booty:latest tar -cf - /initramfs.cpio.gz | tar xf -
```

### Build Flavors

The `initrd.Dockerfile` supports multiple build targets via `--target`:

| Target | Size | Networking | Disk Tools | Use Case |
|--------|------|-----------|------------|----------|
| *(default)* | ~80 MB | FRR/EVPN + DHCP | Full (LVM, sfdisk, mdadm) | Production bare-metal provisioning |
| `iso` | ~100 MB | FRR/EVPN + DHCP | Full | Bootable ISO for Redfish virtual media |
| `slim` | ~15 MB | DHCP only | Minimal (e2fsck, resize2fs) | Lightweight provisioning without BGP |
| `micro` | ~10 MB | None (pure Go) | None | Minimal agent, custom network stack |

```bash
# Build ISO (for Redfish BMC virtual media boot)
docker build --target=iso -f initrd.Dockerfile -o type=local,dest=. .

# Build slim initramfs
docker build --target=slim -f initrd.Dockerfile -o type=local,dest=. .
```

### Binary only

```bash
GOOS=linux go build -o booty .
```

## Hardware Compatibility

### Network Interface Cards

BOOTy includes kernel modules for common data center NICs. Modules are loaded
automatically at boot from the `/modules/` directory in the initramfs.

| Vendor | Driver | Hardware | Speed |
|--------|--------|----------|-------|
| **Intel** | `e1000e` | I217/I218/I219 | 1G |
| **Intel** | `igb` | I350, I210/I211 | 1G |
| **Intel** | `igc` | I225/I226 | 2.5G |
| **Intel** | `ixgbe` | X520, X540, X550 (82599) | 10G |
| **Intel** | `i40e` | X710, XL710, XXV710 | 10/25/40G |
| **Intel** | `ice` | E810 | 25/50/100G |
| **Intel** | `iavf` | Adaptive Virtual Function | VF |
| **Broadcom** | `tg3` | NetXtreme BCM57xx | 1G |
| **Broadcom** | `bnxt_en` | NetXtreme-E/C BCM573xx/574xx | 10/25/50/100G |
| **NVIDIA/Mellanox** | `mlx4_core`/`mlx4_en` | ConnectX-3 | 10/40G |
| **NVIDIA/Mellanox** | `mlx5_core` | ConnectX-4/5/6/7, BlueField | 10/25/40/50/100/200/400G |
| **Emulex/Broadcom** | `be2net` | OneConnect OCe14xxx | 10G |
| **QEMU/KVM** | `virtio_net` | VirtIO NIC | Virtual |

**Mellanox SR-IOV**: ConnectX-4+ cards are automatically detected via sysfs PCI vendor
ID (`/sys/bus/pci/devices/*/vendor`) — no `lspci` binary needed. SR-IOV is configured
using `mstconfig` during provisioning (requires a hard reboot to apply firmware changes).

## Usage

### CAPRF Mode

CAPRF mode is activated automatically when `/deploy/vars` exists (ISO-booted). The vars file is generated by the CAPRF controller and contains:

```bash
export IMAGE="http://images.local/ubuntu-22.04.img.gz"
export HOSTNAME="worker-01"
export TOKEN="bearer-token-for-auth"
export MODE="provision"                    # provision | deprovision | soft-deprovision
export PROVIDER_ID="redfish://bmc/Systems/1"
export FAILURE_DOMAIN="az-1"
export REGION="eu-central"
export INIT_URL="http://caprf:8080/status/init"
export SUCCESS_URL="http://caprf:8080/status/success"
export ERROR_URL="http://caprf:8080/status/error"
export LOG_URL="http://caprf:8080/log"

# Network (FRR/EVPN mode — omit for DHCP fallback)
underlay_subnet="10.0.0.0/24"
asn_server="65001"
provision_vni="10100"
overlay_subnet="fd00::/64"
dns_resolver="8.8.8.8"
```

### Legacy Mode

The provisioning server serves configuration files and (optionally) disk images over HTTP.

#### Write an image to a remote server

```bash
go run server/server.go \
  -action writeImage \
  -mac 00:50:56:a5:0e:0f \
  -sourceImage http://192.168.0.95:3000/images/ubuntu.img \
  -destinationDevice /dev/sda
```

#### Read a disk from a remote server

```bash
go run server/server.go \
  -action readImage \
  -mac 00:50:56:a5:0e:0f \
  -destinationAddress http://192.168.0.95:3000/image \
  -sourceDevice /dev/sda
```

### LVM & Disk Growth

Write an image, grow partition 1, and expand the root LVM volume:

```bash
go run server/server.go \
  -action writeImage \
  -mac 00:50:56:a5:0e:0f \
  -sourceImage http://192.168.0.95:3000/images/ubuntu.img \
  -destinationDevice /dev/sda \
  -growPartition 1 \
  -lvmRoot /dev/ubuntu-vg/root \
  -shell
```

### Feature Gates

| Variable | Default | Description |
|----------|---------|-------------|
| `MODE` | `provision` | `provision`, `deprovision`, `soft-deprovision`, or `standby` |
| `DISABLE_KEXEC` | `false` | Skip kexec, always hard-reboot |
| `MIN_DISK_SIZE_GB` | `0` | Minimum disk size filter (0 = no minimum) |
| `MACHINE_EXTRA_KERNEL_PARAMS` | — | Additional kernel cmdline parameters |
| `HEARTBEAT_URL` | — | Standby mode: URL for periodic keepalives |
| `COMMANDS_URL` | — | Standby mode: URL to poll for pending commands |
| `SECURE_ERASE` | `false` | Use NVMe format / ATA secure erase instead of partition wipe |
| `STATIC_IP` | — | Static IP in CIDR notation (e.g. `10.0.0.5/24`) |
| `STATIC_GATEWAY` | — | Default gateway for static networking |
| `STATIC_IFACE` | — | Interface for static IP (auto-detect if empty) |
| `BOND_INTERFACES` | — | Comma-separated interfaces for LACP bond (e.g. `eth0,eth1`) |
| `BOND_MODE` | `802.3ad` | Bond mode: `802.3ad`/`lacp`, `balance-rr`, `active-backup`, `balance-xor` |
| `POST_PROVISION_CMDS` | — | Semicolon-separated commands to run in chroot after provisioning |

### Debugging

| Flag | Description |
|------|-------------|
| `-shell` | Drop to a BusyBox shell if something fails |
| `-wipe` | Wipe the provisioned disk on failure |
| `-dryRun` | Log actions without writing to disk |

## OCI Image Sources

BOOTy supports pulling disk images from OCI-compliant container registries. Use the
`oci://` URL scheme in the `IMAGE` variable:

```bash
# Unauthenticated registry
export IMAGE="oci://ghcr.io/myorg/os-images:ubuntu-22.04"

# Authenticated registry (credentials from standard Docker config)
export IMAGE="oci://registry.example.com/images:rhel-9"
```

The OCI image must contain exactly one layer with the disk image (optionally compressed).
Authentication uses the standard Docker credential chain (`~/.docker/config.json`,
`DOCKER_CONFIG`, credential helpers). Both `docker.io` and OCI-spec registries are
supported via [go-containerregistry](https://github.com/google/go-containerregistry).

HTTP and OCI fetches are retried up to 3 times with exponential backoff (1s, 2s, 4s).

## Network Modes

BOOTy supports multiple network modes with automatic fallback:

| Priority | Mode | Trigger | Description |
|----------|------|---------|-------------|
| 1 | **Bond** | `BOND_INTERFACES` set | Creates bond0 (LACP/802.3ad) from listed interfaces |
| 2 | **FRR/EVPN** | `underlay_subnet`+`asn_server` set | BGP underlay with VXLAN overlay |
| 3 | **Static** | `STATIC_IP` set | Assigns IP via netlink, adds default route |
| 4 | **DHCP** | Default | Tries DHCP on all physical interfaces |

Bond mode creates the bond interface first, then the selected upper mode (FRR/Static/DHCP)
runs on top of it. Each mode falls back to DHCP on failure.

## Extending Bundled Binaries

The initramfs is built in `initrd.Dockerfile` using a multi-stage Docker build. To add
a new binary:

1. **Install the package** in the `tools` build stage:
```dockerfile
# In the 'tools' stage (FROM alpine AS tools)
RUN apk add --no-cache your-package
```

2. **Copy the binary** into the final initramfs:
```dockerfile
# In the builder stage
COPY --from=tools /usr/sbin/your-binary sbin/your-binary
```

3. **Verify all shared library dependencies** are satisfied:
```bash
# Inside the container
ldd /usr/sbin/your-binary
```

Currently bundled binaries: `mdadm`, `wipefs`, `sfdisk`, `sgdisk`, `e2fsck`,
`resize2fs`, `xfs_growfs`, `xfs_repair`, `btrfs`, `parted`, `kpartx`, `lvm`,
`hdparm`, `nvme`, `mstconfig`, `mstflint`, `lldpcli`, `lldpd`, `efibootmgr`.

> **Prefer Go libraries**: Where possible, use Go syscalls or libraries instead of
> shelling out to external binaries. Examples: `unix.FinitModule()` instead of `insmod`,
> `syscall.SysProcAttr{Chroot}` instead of `chroot`, sysfs reads instead of `lspci`.

## Development

```bash
# Run unit tests with coverage
make test

# Run E2E tests (Linux only)
go test -tags e2e -v -race ./test/e2e/...

# Run linter
make lint

# Build binary
make build
```

## Project Structure

```
├── main.go                     # Entry point: CAPRF vs legacy mode, kernel module loading
├── cmd/booty.go                # Legacy CLI orchestration
├── server/server.go            # Legacy provisioning server
├── initrd.Dockerfile           # Multi-stage initramfs build (default, iso, slim, micro)
├── pkg/
│   ├── caprf/                  # CAPRF client (status, log, debug, vars parsing)
│   ├── config/                 # MachineConfig, Provider interface, Status types
│   ├── disk/                   # Disk detection, partitioning, RAID, LVM, mount
│   ├── image/                  # Image streaming (HTTP, OCI, gzip/lz4/xz/zstd auto-detect)
│   ├── kexec/                  # GRUB parsing, kexec load/execute
│   ├── network/                # Network mode abstraction (FRR, DHCP, Static, Bond)
│   │   ├── frr/               # FRR/EVPN: config rendering, address derivation
│   │   └── lldp/              # LLDP frame listener (raw AF_PACKET sockets)
│   ├── provision/              # Orchestrator (24-step provision, deprovision)
│   │   └── configurator.go    # OS config: hostname, kubelet, GRUB, DNS, EFI, Mellanox SR-IOV
│   ├── plunderclient/          # Legacy HTTP client for config retrieval
│   ├── realm/                  # Device, mount, network, shell operations
│   ├── utils/                  # Cmdline parsing, helpers
│   └── ux/                     # ASCII art & system info display
├── test/e2e/                   # E2E tests (ContainerLab + vrnetlab EVPN fabric)
│   ├── clab/                   # ContainerLab topologies and FRR configs
│   │   └── vrnetlab/          # QEMU VM image builder for vrnetlab testing
│   └── integration/           # Integration test suites
├── docs/                       # Design documents and proposals
├── .github/workflows/          # CI (lint, test, build, E2E clab, E2E vrnetlab, KVM boot)
└── .golangci.yml               # Linter configuration
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, coding standards, and the PR process.

## License

This project is licensed under the Apache License 2.0 — see [LICENSE](LICENSE) for details.
