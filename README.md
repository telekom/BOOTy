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
4. The provisioning pipeline runs 31 steps: status reporting → RAID cleanup → disk detection → image streaming → partition management → OS configuration → cloud-init injection → kexec.
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
- **FRR/EVPN networking** — BGP underlay with VXLAN overlay for data center fabrics (FRR-based)
- **GoBGP/EVPN networking** — Pure-Go BGP stack with VXLAN overlay (no external daemons)
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
- **31-step provisioning pipeline** — RAID cleanup, disk detection, image streaming, partition growth, LVM, filesystem resize, OS configuration, cloud-init injection, EFI boot, Mellanox SR-IOV, post-provision hooks
- **Kexec support** — Fast reboot into installed kernel without full BIOS POST (auto-disabled after firmware changes)
- **Remote logging** — Real-time log and debug shipping to CAPRF controller
- **Hard/soft deprovisioning** — Full disk wipe or GRUB rename for reprovisioning
- **Standby mode** — Hot standby with heartbeats and command polling for sub-second provisioning
- **Multi-architecture** — Builds for `linux/amd64` and `linux/arm64`
- **Multiple build flavors** — Full (FRR+tools), GoBGP (pure Go BGP), slim (DHCP-only), micro (pure Go), ISO (bootable)

## Prerequisites

- Go **1.26+**
- Docker (for building the initramfs)
- A DHCP/PXE environment (legacy mode) or Redfish BMC with ISO virtual media (CAPRF mode)

### Build Environment

| Requirement          | Version | Notes |
|----------------------|---------|-------|
| Go                   | 1.26+   | `GOOS=linux` for cross-compilation on macOS/Windows |
| Docker / Buildx      | 20.10+  | Multi-arch builds (`linux/amd64`, `linux/arm64`) |
| golangci-lint        | v2.10+  | `make lint` — config in `.golangci.yml` |
| GNU Make             | 4.0+    | Build automation |
| ContainerLab         | 0.44+   | E2E tests only (Linux) |
| KVM / QEMU           | —       | E2E boot tests only (Linux) |

## Building

### Build Binary

Compile BOOTy for a single target architecture (defaults to host `GOARCH`):

```bash
make build
```

Cross-compile BOOTy for both supported architectures:

```bash
make build-all
```

`make build-all` writes binaries to `dist/amd64/booty` and `dist/arm64/booty`.

To extract an initramfs from a published image:

```bash
docker run ghcr.io/telekom/booty:latest tar -cf - /initramfs.cpio.gz | tar xf -
```

### Build Flavors

The `initrd.Dockerfile` supports multiple build targets via `--target`:

| Target | Size | Networking | Disk Tools | Use Case |
|--------|------|-----------|------------|----------|
| *(default)* | ~80 MB | FRR/EVPN + DHCP | Full (LVM, sfdisk, mdadm) | Production bare-metal provisioning |
| `gobgp` | ~45 MB | GoBGP/EVPN + DHCP | Full (LVM, sfdisk, mdadm) | Production without FRR dependency |
| `iso` | ~100 MB | FRR/EVPN + DHCP | Full | Bootable ISO for Redfish virtual media |
| `gobgp-iso` | ~65 MB | GoBGP/EVPN + DHCP | Full | Bootable GoBGP ISO for Redfish virtual media |
| `slim` | ~15 MB | DHCP only | Minimal (e2fsck, resize2fs) | Lightweight provisioning without BGP |
| `micro` | ~10 MB | None (pure Go) | None | Minimal agent, custom network stack |

#### ARM64 Targets

| Make Target | Description |
|-------------|-------------|
| `make arm64` | Full initramfs Docker image for ARM64 |
| `make arm64-slim` | Slim initramfs for ARM64 (output to `dist/arm64/`) |
| `make arm64-gobgp` | GoBGP initramfs for ARM64 (output to `dist/arm64/`) |
| `make build-all` | Cross-compile Go binary for both amd64 and arm64 |
| `make oci-push` | Push binary + initramfs as OCI artifacts to GHCR |
| `make oci-push-initramfs` | Push initramfs only as OCI artifact |
| `make oci-push-binary` | Push binary only as OCI artifact |

```bash
# Build ISO (for Redfish BMC virtual media boot)
docker build --target=iso -f initrd.Dockerfile -o type=local,dest=. .

# Build slim initramfs
docker build --target=slim -f initrd.Dockerfile -o type=local,dest=. .

# Build ARM64 GoBGP initramfs
make arm64-gobgp
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

# JWT authentication (optional — omit for static bearer token)
export TOKEN_URL="http://caprf:8080/auth/token"  # JWT token endpoint
export TOKEN_ALGORITHM="RS256"                    # RS256 or ES256
# When TOKEN_URL is set, JWT acquisition/renewal failures are treated as fatal
# and BOOTy reboots to avoid running with stale credentials.

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
| `MODE` | `provision` | `provision`, `deprovision`, `soft-deprovision`, `standby`, or `dry-run` |
| `DRY_RUN` | `false` | When `true`, forces `MODE=dry-run`: validates prerequisites without destructive writes |
| `DISABLE_KEXEC` | `false` | Skip kexec, always hard-reboot |
| `MIN_DISK_SIZE_GB` | `0` | Minimum disk size filter (0 = no minimum) |
| `MACHINE_EXTRA_KERNEL_PARAMS` | — | Additional kernel cmdline parameters |
| `HEARTBEAT_URL` | — | Standby mode: URL for periodic keepalives |
| `COMMANDS_URL` | — | Standby mode: URL to poll for pending commands |
| `SECURE_ERASE` | `false` | Use NVMe format / ATA secure erase instead of partition wipe |
| `POST_PROVISION_CMDS` | — | Semicolon-separated commands to run in chroot after provisioning |
| `RESCUE_MODE` | `reboot` | Failure recovery strategy: `reboot`, `retry`, `shell`, `wait` |
| `RESCUE_TIMEOUT` | `0` | *(Phase 2)* Rescue wait timeout in seconds (0 = indefinite) |
| `RESCUE_SSH_PUBKEY` | — | *(Phase 2)* SSH public key for rescue shell access |
| `RESCUE_AUTO_MOUNT` | `false` | *(Phase 2)* Auto-mount disks in rescue shell mode |
| `EVPN_L2_ENABLED` | `false` | Enable EVPN L2 overlay (Type-2/3 route origination and handling) in GoBGP mode. Default is Type-5 only (L3) |
| `HEALTH_CHECKS_ENABLED` | `false` | Run pre-provisioning hardware health checks |
| `HEALTH_MIN_MEMORY_GB` | `0` | Minimum RAM (GiB) for health check (0 = skip check) |
| `HEALTH_MIN_CPUS` | `0` | Minimum CPU count for health check (0 = skip check) |
| `HEALTH_SKIP_CHECKS` | — | Comma-separated check names to skip (e.g., `thermal,disk-smart`) |
| `HEALTH_CHECK_URL` | — | POST endpoint for health check results |
| `INVENTORY_ENABLED` | `false` | Collect and report hardware inventory to CAPRF |
| `INVENTORY_URL` | — | POST endpoint for hardware inventory JSON |
| `FIRMWARE_REPORT` | `false` | Enable firmware version collection and reporting |
| `FIRMWARE_URL` | — | POST endpoint for firmware report |
| `FIRMWARE_MIN_BIOS` | — | Minimum BIOS version (vendor-specific string) |
| `FIRMWARE_MIN_BMC` | — | Minimum BMC version (vendor-specific string) |
| `TELEMETRY_ENABLED` | `false` | Enable provisioning metrics and telemetry collection |
| `TELEMETRY_URL` | — | POST endpoint for telemetry snapshot |
| `METRICS_URL` | — | POST endpoint for provisioning metrics |
| `EVENT_URL` | — | POST endpoint for provisioning lifecycle events |
| `SECUREBOOT_REENABLE` | `false` | Signal CAPRF to re-enable Secure Boot after provisioning |
| `MOK_CERT_PATH` | — | *(Phase 2)* Path to DER-encoded MOK certificate for custom kernel signing |
| `MOK_PASSWORD` | — | *(Phase 2)* One-time password for MokManager confirmation |
| `IMAGE_CHECKSUM` | — | Expected hex digest of the raw disk image |
| `IMAGE_CHECKSUM_TYPE` | — | Checksum algorithm: `sha256` or `sha512` |
| `IMAGE_SIGNATURE_URL` | — | URL to detached GPG signature for image verification |
| `IMAGE_GPG_PUBKEY` | — | Path to GPG public key for image signature verification |
| `LUKS_ENABLED` | `false` | *(Planned)* Enable LUKS2 encryption for target partitions |
| `LUKS_PASSPHRASE` | — | *(Planned)* Passphrase for initial LUKS volume creation |
| `LUKS_UNLOCK_METHOD` | `passphrase` | *(Planned)* Unlock method: `passphrase`, `tpm2`, `clevis`, `keyfile` |
| `LUKS_CIPHER` | `aes-xts-plain64` | *(Planned)* LUKS2 cipher algorithm |
| `LUKS_KEY_SIZE` | `512` | *(Planned)* LUKS2 key size in bits |
| `LUKS_HASH` | `sha256` | *(Planned)* LUKS2 hash algorithm |
| `NUM_VFS` | `0` | Number of SR-IOV virtual functions for Mellanox NICs (0 = skip) |

#### Network Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `NETWORK_MODE` | — | Network mode override: `gobgp` for pure-Go BGP stack |

### Debugging

| Flag | Description |
|------|-------------|
| `-shell` | Drop to a BusyBox shell if something fails |
| `-wipe` | Wipe the provisioned disk on failure |
| `-dryRun` | Log actions without writing to disk |

### Resilient Provisioning

BOOTy includes built-in retry and checkpoint support ensuring provisioning
survives transient failures (network timeouts, temporary disk errors).

**Automatic retries** — Steps with known transient failure modes (DNS, image
streaming, disk detection, status reporting) use exponential backoff with jitter.
Policies are configured in `DefaultPolicies`:

| Step | Max Retries | Initial Delay | Max Delay |
|------|-------------|---------------|-----------|
| `report-init` | 5 | 2 s | 30 s |
| `configure-dns` | 5 | 1 s | 15 s |
| `stream-image` | 3 | 5 s | 60 s |
| `detect-disk` | 3 | 2 s | 10 s |
| `partprobe` | 3 | 1 s | 5 s |
| `report-success` | 5 | 2 s | 30 s |

Permanent errors (e.g., `PermanentError`) are never retried. Unclassified errors
are retried only when the step policy has `Transient: true`.

**Checkpoint resume** — Enable with `BOOTY_RESUME=true`. On each step
completion/failure the checkpoint is persisted to `/tmp/booty-checkpoint.json`.
On restart, previously completed steps are skipped — except runtime-state steps
(`setup-mellanox`, `detect-disk`, `parse-partitions`) which always re-execute to
rebuild in-memory state (target disk path, partition info).

```bash
# Enable checkpoint resume
export BOOTY_RESUME=true
```

### Rescue Mode

When provisioning fails, BOOTy's rescue mode determines what happens next.
Configure via `RESCUE_MODE`:

| Mode | Behavior |
|------|----------|
| `reboot` | (Default) Reboot immediately — relies on external retry orchestration |
| `retry` | Retry provisioning with a 30-second delay between attempts (max 3 retries before falling back to reboot) |
| `shell` | Drop to an interactive rescue shell on the console |
| `wait` | Hold the system up and wait for manual intervention; reboots on context cancellation |

```bash
# Retry up to 3 times before rebooting
export RESCUE_MODE=retry

# Drop to interactive rescue shell
export RESCUE_MODE=shell

# Phase 2 (planned): SSH access and disk auto-mount
# export RESCUE_SSH_PUBKEY="ssh-ed25519 AAAAC3NzaC1... admin@ops"
# export RESCUE_AUTO_MOUNT=true
```

In standby mode (agent mode), rescue actions also apply to hot-provisioning
commands received via the command poll loop. Failed provisions are ACKed back
to the controller with the error message.

### Dry-Run Mode

Dry-run mode runs the full provisioning pipeline without making destructive
changes to disk or EFI. Use it for pre-flight validation of machine
configuration, network connectivity, and image availability.

```bash
export MODE="dry-run"
# or
export DRY_RUN=true
```

Dry-run validates: network connectivity, image URL reachability, disk detection,
partition layout fit, EFI variable access, and health checks. Results are
reported back to the CAPRF controller as a structured validation report.

### Health Checks

Pre-provisioning hardware health checks validate the machine before any
destructive operations. Enable with `HEALTH_CHECKS_ENABLED=true`.

```bash
export HEALTH_CHECKS_ENABLED=true
export HEALTH_MIN_MEMORY_GB=64     # Abort if less than 64 GiB RAM
export HEALTH_MIN_CPUS=16          # Abort if fewer than 16 CPUs
export HEALTH_SKIP_CHECKS=thermal  # Skip thermal checks (comma-separated)
export HEALTH_CHECK_URL="http://caprf:8080/health"
```

| Check | Type | Behavior |
|-------|------|----------|
| `disk-presence` | Critical | Abort if no eligible disk found |
| `minimum-memory` | Critical | Abort if RAM < `HEALTH_MIN_MEMORY_GB` |
| `minimum-cpu` | Critical | Abort if CPUs < `HEALTH_MIN_CPUS` |
| `memory-ecc` | Warning | Log if ECC errors detected |
| `nic-link-state` | Warning | Log if no NIC link detected |
| `disk-smart` | Warning | Log SMART health status |
| `thermal-state` | Warning | Log thermal sensor readings |

Health check results are posted to `HEALTH_CHECK_URL` (if set). Critical
failures abort provisioning; warnings are logged but do not block.

### Hardware Inventory

Collects CPU, memory, disk, NIC, and NVMe hardware details from sysfs/procfs
and reports them to the CAPRF controller. Runs as an early provisioning step.

```bash
export INVENTORY_ENABLED=true
export INVENTORY_URL="http://caprf:8080/inventory"
```

Inventory collection is best-effort — failures are logged but do not block
provisioning. The JSON payload includes: CPU model/count/cores, total memory,
disk devices (size, model, serial, transport), NIC details (driver, MAC, speed,
firmware), and NVMe namespaces.

### Firmware Reporting

Collects BIOS, BMC, and NIC firmware versions and optionally enforces minimum
version requirements.

```bash
export FIRMWARE_REPORT=true
export FIRMWARE_URL="http://caprf:8080/firmware"
export FIRMWARE_MIN_BIOS="U46"           # Abort if BIOS older than U46
export FIRMWARE_MIN_BMC="2.72"           # Abort if BMC older than 2.72
```

Firmware versions are read from sysfs (`/sys/class/dmi/id/`). NIC firmware
is collected per-driver for Broadcom, Intel, and Mellanox adapters via ethtool.
When minimum versions are set, provisioning aborts if the running firmware is
below the threshold.

### Image Verification

BOOTy supports checksum and GPG signature verification for streamed images.

```bash
# IMAGE_CHECKSUM must be the raw hex digest (no "sha256:" prefix)
export IMAGE_CHECKSUM="a1b2c3d4e5f6..."
export IMAGE_CHECKSUM_TYPE="sha256"           # sha256 or sha512

# GPG signature verification (optional)
export IMAGE_SIGNATURE_URL="http://images.local/ubuntu.img.gz.sig"
export IMAGE_GPG_PUBKEY="/deploy/signing-key.gpg"
```

Checksum verification runs after image streaming — the raw bytes are hashed
during the write and compared against the expected digest. GPG verification
downloads the detached signature and verifies it against the provided public key.

### Telemetry and Metrics

Provisioning telemetry tracks step-level timing, image throughput, retry
counts, and error rates. Enable with `TELEMETRY_ENABLED=true`.

```bash
export TELEMETRY_ENABLED=true
export TELEMETRY_URL="http://caprf:8080/telemetry"
export METRICS_URL="http://caprf:8080/metrics"
export EVENT_URL="http://caprf:8080/events"
```

| Endpoint | Payload | Frequency |
|----------|---------|-----------|
| `TELEMETRY_URL` | Full metrics snapshot (JSON) | On completion/failure |
| `METRICS_URL` | Step timing, throughput, error counts | On completion/failure |
| `EVENT_URL` | Step progress events | Per-step |

Telemetry reporting is best-effort — failures do not block provisioning.

### Secure Boot

BOOTy can signal the CAPRF controller to re-enable Secure Boot after
provisioning completes. The OS image must include signed bootloaders.

```bash
export SECUREBOOT_REENABLE=true
```

When enabled, BOOTy reports `SECUREBOOT_REENABLE=true` in its provisioning
success status. The CAPRF controller then re-enables Secure Boot via Redfish
before the final reboot. If the installed OS does not have signed bootloaders,
the machine will fail to boot.

### LUKS Encryption (Experimental)

BOOTy includes a `pkg/disk/luks` library for LUKS2 full-disk encryption of
provisioned volumes. The library is functional but **not yet integrated into the
provisioning orchestrator** — environment variable wiring and step ordering are
planned for a future release.

```bash
# Planned environment variables (not yet wired):
export LUKS_ENABLED=true
export LUKS_PASSPHRASE="initial-setup-passphrase"
export LUKS_UNLOCK_METHOD=tpm2       # passphrase | tpm2 | clevis | keyfile
export LUKS_CIPHER=aes-xts-plain64   # optional, default: aes-xts-plain64
export LUKS_KEY_SIZE=512              # optional, default: 512
export LUKS_HASH=sha256              # optional, default: sha256
```

**Lifecycle:**

1. **Format** — Creates LUKS2 volume on target device (`cryptsetup luksFormat --type luks2`)
2. **Open** — Maps the encrypted volume to `/dev/mapper/<name>` for filesystem creation
3. **Crypttab** — Generates `/etc/crypttab` with the appropriate unlock method options
4. **Close** — Unmaps volume after provisioning completes; OS unlocks on next boot

**Unlock Methods:**

| Method | Description | Crypttab Options |
|--------|-------------|-----------------|
| `passphrase` | Manual entry at boot | `luks,discard` |
| `tpm2` | TPM2 PCR-bound key (Phase 2: enrollment) | `tpm2-device=auto,discard` |
| `clevis` | Network-bound via tang server (Phase 2: enrollment) | `_netdev,discard` |
| `keyfile` | Key file at path | `luks,discard,keyfile-timeout=30s` |

LUKS format always requires a passphrase for initial volume creation. Post-format
enrollment (TPM2 PCR binding, clevis tang enrollment) is handled separately after
the OS is installed. Invalid targets (empty device or mapped name) are silently
skipped during crypttab generation.

### JWT Authentication

BOOTy supports JWT-based authentication with the CAPRF controller. Set
`TOKEN_URL` to enable automatic token acquisition and background renewal:

```bash
export TOKEN="bootstrap-token"                    # Initial bootstrap token
export TOKEN_URL="http://caprf:8080/auth/token"   # JWT token endpoint
export TOKEN_ALGORITHM="RS256"                    # Optional: RS256 (default) or ES256
```

**Lifecycle:**
1. BOOTy starts with the bootstrap `TOKEN` for initial authentication
2. After network connectivity is established, `TOKEN_URL` is called to exchange
   the bootstrap token for a short-lived JWT
3. A background goroutine renews the JWT at 80% of its lifetime
4. On renewal failure, exponential backoff retries up to 5 times before rebooting

When `TOKEN_URL` is not set, BOOTy uses the static `TOKEN` for all requests
(no renewal). When `TOKEN_URL` is set, acquisition and renewal failures are
fatal — BOOTy reboots to avoid operating with stale credentials.

### VLAN Support

BOOTy supports 802.1Q VLAN tagging via netlink. Configure VLANs with the
`VLANS` variable as a comma-separated list of `VID:IFACE:IP/MASK` tuples:

```bash
export VLANS="200:eno1:10.200.0.42/24,300:eno2"
```

Each VLAN creates a tagged sub-interface (`eno1.200`), assigns the IP address
(if provided), and brings the link up. VLANs are created after the primary
network mode is established.

### Kexec Boot

BOOTy uses kexec for fast reboots into the provisioned OS without full firmware
re-initialization. After provisioning, BOOTy parses the installed GRUB config,
loads the kernel and initramfs, then executes the kexec syscall.

```bash
# Disable kexec (force hard reboot via firmware)
export DISABLE_KEXEC=true
```

Kexec is automatically disabled when firmware changes are detected (e.g.,
Mellanox SR-IOV configuration), since firmware re-initialization is required.
If GRUB parsing fails or the kernel/initramfs is not found, BOOTy falls back
to a standard `reboot(2)` syscall.

### SR-IOV Configuration

BOOTy automatically detects Mellanox ConnectX NICs via PCI vendor ID and
configures SR-IOV virtual functions using `mstconfig`:

```bash
# Set number of virtual functions (default: 32 when Mellanox detected)
export NUM_VFS=16
```

For each detected Mellanox device, BOOTy runs
`mstconfig -d <device> set NUM_OF_VFS=<n>`. This modifies NIC firmware and
requires a hard reboot — kexec is automatically disabled when SR-IOV is
configured. If `mstconfig` fails or no Mellanox NICs are found, provisioning
continues normally.

### Secure Erase

BOOTy supports hardware-level disk erasure before provisioning. When enabled,
it uses NVMe format or ATA Security Erase instead of quick partition table
clearing:

```bash
export SECURE_ERASE=true
```

| Drive Type | Erase Method |
|-----------|--------------|
| NVMe | `nvme format --ses=1` (User Data Erase) |
| SATA/SAS | ATA Security Erase via `hdparm` |
| Fallback | `wipefs -af` (partition table + filesystem signatures) |

Secure erase is non-fatal — if hardware erase fails on a drive (e.g., SATA
security state is frozen), BOOTy falls back to `wipefs` and continues.

### Post-Provision Commands

Execute custom shell commands in the provisioned rootfs chroot after all
provisioning steps complete:

```bash
# Semicolon-separated list of commands
export POST_PROVISION_CMDS="apt-get update; apt-get install -y custom-pkg; systemctl enable my-service"
```

Commands run as root inside the chroot (`/newroot/`). If any command fails,
provisioning is marked as failed. Use this for OS customizations that don't
warrant rebuilding the entire disk image (hostname, packages, service enablement).

### Standby Mode

Persistent agent mode that polls for provisioning commands from CAPRF:

```bash
export MODE=standby
export HEARTBEAT_URL="http://caprf-server/status/heartbeat"
export COMMANDS_URL="http://caprf-server/commands"
```

In standby mode, BOOTy sends periodic heartbeats (every 30s) and polls for
pending commands (every 10s). When a `provision` command is received, the full
provisioning orchestrator runs. On failure, the configured `RESCUE_MODE` policy
applies (retry, shell, wait, or reboot).

### BIOS Settings

BOOTy captures BIOS configuration state from vendor-specific sysfs paths.
Supported vendors are auto-detected via DMI (`/sys/class/dmi/id/sys_vendor`):

| Vendor | Key Settings Captured |
|--------|----------------------|
| Dell | LogicalProc, VirtualizationTechnology, SriovGlobalEnable, BootMode, SecureBoot |
| HPE | Hyperthreading, Virtualization, SRIOV, BootMode, SecureBootStatus |
| Lenovo | OperatingMode, HyperThreading, VirtualizationTechnology, SRIOVSupport, BootMode |
| Supermicro | Generic BIOS capture |

BIOS state is collected early in provisioning and reported to the CAPRF
controller. No environment variables are required — vendor detection is
automatic.


**BGP Policy** configuration supports community tagging on import/export routes:
- **Standard communities**: `ASN:value` (16-bit each, e.g. `65000:100`)
- **Extended communities**: `TYPE:ASN:value` with 4-octet ASN support (e.g. `RT:4200000001:100`)
- **Large communities**: `GA:LD1:LD2` (32-bit each, e.g. `65000:1:100`)

**VRF isolation** supports multi-VRF configurations with separate management and provisioning routing tables. VRF configs are validated for unique names, non-zero table IDs, and no conflicts.

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
# Build binary
make build

# Run unit tests with coverage (40% coverage gate)
make test

# Run linter (golangci-lint v2)
make lint

# Format code
make fmt

# E2E tests — ContainerLab (Linux only)
make clab-up && make test-e2e-integration       # FRR/EVPN topology
make clab-gobgp-up && make test-e2e-gobgp        # GoBGP topology
make clab-boot-up && make test-e2e-boot          # Boot orchestrator

# E2E tests — KVM/QEMU vrnetlab (Linux + KVM)
make clab-vrnetlab-up && make test-e2e-vrnetlab   # Full EVPN boot flow
make clab-gobgp-vrnetlab-up && make test-e2e-gobgp-vrnetlab  # GoBGP + real switches

# Linux-only E2E (disk/mount/loop device, requires root)
go test -tags linux_e2e -v ./pkg/disk/...
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for coding standards, test requirements,
and the PR process.

## Project Structure

```
├── main.go                     # Entry point: CAPRF vs legacy mode, kernel module loading
├── cmd/booty.go                # Legacy CLI orchestration
├── server/server.go            # Legacy provisioning server
├── initrd.Dockerfile           # Multi-stage initramfs build (default, iso, slim, micro)
├── pkg/
│   ├── auth/                   # JWT token manager (acquisition, renewal, backoff)
│   ├── bios/                   # BIOS settings management (Dell, HPE, Lenovo, Supermicro)
│   ├── bootloader/             # Bootloader detection (GRUB, systemd-boot)
│   ├── buildinfo/              # Binary build information (version, commit, date)
│   ├── caprf/                  # CAPRF client (status, log, debug, vars parsing)
│   ├── cloudinit/              # Cloud-init NoCloud/ConfigDrive generation
│   ├── config/                 # MachineConfig, Provider interface, Status types
│   ├── disk/                   # Disk detection, partitioning, RAID, LVM, mount
│   ├── firmware/               # Firmware version collection from sysfs
│   ├── health/                 # Pre-provisioning hardware health checks
│   ├── image/                  # Image streaming (HTTP, OCI, gzip/lz4/xz/zstd auto-detect)
│   ├── inventory/              # Hardware inventory from sysfs/procfs
│   ├── kexec/                  # GRUB parsing, kexec load/execute
│   ├── network/                # Network mode abstraction (FRR, GoBGP, DHCP, Static, Bond)
│   │   ├── frr/               # FRR/EVPN: config rendering, address derivation
│   │   ├── gobgp/             # Pure-Go BGP stack (3-tier: Underlay, Overlay, IPMI)
│   │   ├── lldp/              # LLDP frame listener (raw AF_PACKET sockets)
│   │   └── vlan/              # VLAN 802.1Q tagging via netlink
│   ├── provision/              # Orchestrator (31-step provision, deprovision)
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

## Documentation

All design proposals live in `docs/proposal-*.md`. See `docs/roadmap.md` for the
full feature roadmap with priorities and status tracking.

| Resource | Description |
|----------|-------------|
| [CONTRIBUTING.md](CONTRIBUTING.md) | Development setup, coding standards, PR process |
| [docs/roadmap.md](docs/roadmap.md) | Feature roadmap (P0–P4 priorities) |
| [.github/AGENTS.md](.github/AGENTS.md) | Copilot agents, review personas, prompts |
| [.github/copilot-instructions.md](.github/copilot-instructions.md) | Project guidelines for Copilot |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, coding standards, and the PR process.

## License

This project is licensed under the Apache License 2.0 — see [LICENSE](LICENSE) for details.
