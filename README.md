# BOOTy

[![CI](https://github.com/telekom/BOOTy/actions/workflows/ci.yml/badge.svg)](https://github.com/telekom/BOOTy/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/telekom/BOOTy)](https://goreportcard.com/report/github.com/telekom/BOOTy)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

A lightweight initramfs agent for bare-metal OS provisioning over the network.

BOOTy boots as the init process inside a minimal initramfs, contacts a provisioning server, and orchestrates the full lifecycle of a bare-metal machine: disk imaging, OS configuration, network setup, and reboot. It uses **CAPRF** (Cluster API Provider Redfish) for Kubernetes cluster provisioning.

> **Warning** — This software has **no guard rails**. Incorrect use can overwrite an existing Operating System.

## Architecture

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
4. The provisioning pipeline runs 32 steps: status reporting → RAID cleanup → disk detection → EFI setup → image verification (signature/checksum) → image streaming → partition management → OS configuration → kexec.
5. Status, logs, and debug info are shipped back to the CAPRF controller throughout.

## Features
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
- **32-step provisioning pipeline** — RAID cleanup, disk detection, EFI variable setup, image verification (signature/checksum), image streaming, partition growth, LVM, filesystem resize, OS configuration, EFI boot, Mellanox SR-IOV, post-provision hooks
- **Kexec support** — Fast reboot into installed kernel without full BIOS POST (auto-disabled after firmware changes)
- **Remote logging** — Real-time log and debug shipping to CAPRF controller
- **Hard/soft deprovisioning** — Full disk wipe or GRUB rename for reprovisioning
- **Standby mode** — Hot standby with heartbeats and command polling for sub-second provisioning
- **Multi-architecture** — Builds for `linux/amd64` and `linux/arm64`
- **Multiple build flavors** — Full (FRR+tools), GoBGP (pure Go BGP), slim (DHCP-only), micro (pure Go), ISO (bootable)
- **BIOS settings management** — Vendor-specific BIOS capture, apply, and reset (Dell, HPE, Lenovo, Supermicro)
- **Bootloader detection** — Automatic detection and configuration for GRUB and systemd-boot
- **Secure Boot** — Full Secure Boot chain setup with EFI variable management
- **TPM/TPM2 support** — TPM attestation and LUKS cryptenroll for disk encryption
- **Kernel driver management** — Architecture-aware module loading from initramfs
- **Telemetry** — Provisioning metrics collection and reporting
- **Hardware inventory** — CPU, memory, disk, NIC, and NVMe enumeration from sysfs/procfs
- **Hardware health checks** — Pre-provisioning validation of CPU, memory, disk, network, and thermal state
- **NIC firmware collection** — Firmware version reporting for Broadcom, Intel, and Mellanox NICs
- **Rescue mode** — Configurable failure recovery: reboot, retry with backoff, interactive shell, or wait
- **Checkpoint resume** — Persist provisioning progress and skip completed steps on restart
- **Dry-run mode** — Non-destructive pre-flight validation of provisioning prerequisites

## Prerequisites

- Go **1.26+**
- Docker (for building the initramfs)
- A Redfish BMC with ISO virtual media (CAPRF mode)

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

# Network (FRR/EVPN mode — omit for DHCP fallback)
underlay_subnet="10.0.0.0/24"
asn_server="65001"
provision_vni="10100"
overlay_subnet="fd00::/64"
dns_resolver="8.8.8.8"

# GoBGP/EVPN mode (alternative to FRR)
export NETWORK_MODE="gobgp"
export BGP_PEER_MODE="unnumbered"
export underlay_ip="10.0.0.20"
export asn_server="65020"
export provision_vni="100"
export provision_ip="10.100.0.20/24"
export provision_gateway="10.0.0.1"      # Gateway VTEP for VXLAN data plane
export dns_resolver="8.8.8.8"
```

### Feature Gates

| Variable | Default | Description |
|----------|---------|-------------|
| `MODE` | `provision` | `provision`, `deprovision`, `soft-deprovision`, `standby`, or `dry-run` |
| `DRY_RUN` | `false` | When `true`, forces `MODE=dry-run`: validates prerequisites without destructive writes |
| `BOOTY_RESUME` | `false` | When `true`, enables checkpoint persistence at `/tmp/booty-checkpoint.json` and resume mode that skips previously completed non-state steps (runtime-state steps like `setup-mellanox`, `detect-disk`, and `parse-partitions` always rerun). Parsed via `strconv.ParseBool` — accepts `1`, `t`, `true`, `TRUE`, `0`, `f`, `false`, `FALSE` |
| `DISABLE_KEXEC` | `false` | Skip kexec, always hard-reboot |
| `MIN_DISK_SIZE_GB` | `0` | Minimum disk size filter (0 = no minimum) |
| `DISK_DEVICE` | — | Override auto-detected target disk (e.g., `/dev/sda`, `/dev/loop0`) |
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
| `LUKS_ENABLED` | `false` | Enable LUKS2 encryption for target partitions |
| `LUKS_PASSPHRASE` | — | Passphrase for initial LUKS volume creation |
| `LUKS_UNLOCK_METHOD` | `passphrase` | Unlock method: `passphrase`, `tpm2`, `clevis`, `keyfile` |
| `LUKS_CIPHER` | `aes-xts-plain64` | LUKS2 cipher algorithm |
| `LUKS_KEY_SIZE` | `512` | LUKS2 key size in bits |
| `LUKS_HASH` | `sha256` | LUKS2 hash algorithm |
| `NUM_VFS` | `0` | Number of SR-IOV virtual functions for Mellanox NICs (0 = skip) |

#### Network Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `NETWORK_MODE` | — | Network mode override: `gobgp` for pure-Go BGP stack |
| `STATIC_IP` | — | Static IP in CIDR notation (e.g. `10.0.0.5/24`) |
| `STATIC_GATEWAY` | — | Default gateway for static networking |
| `STATIC_IFACE` | — | Interface for static IP (auto-detect if empty) |
| `BOND_INTERFACES` | — | Comma-separated interfaces for LACP bond (e.g. `eth0,eth1`) |
| `BOND_MODE` | `802.3ad` | Bond mode: `802.3ad`/`lacp`, `balance-rr`, `active-backup`, `balance-xor` |
| `VLANS` | — | Multi-VLAN config (e.g. `200:eno1:10.200.0.42/24,300:eno2`) |
| `BGP_PEER_MODE` | `unnumbered` | GoBGP peering mode: `unnumbered`, `dual`, `numbered` |
| `BGP_NEIGHBORS` | — | Comma-separated peer IPs (required for `dual` and `numbered` modes) |
| `BGP_REMOTE_ASN` | — | Remote ASN for numbered peers (0 or omitted = iBGP) |
| `underlay_subnet` | — | Underlay CIDR for FRR mode (e.g. `192.168.4.0/24`) |
| `underlay_ip` | — | Underlay loopback / router-ID for GoBGP mode |
| `asn_server` | — | Local BGP autonomous system number |
| `provision_vni` | — | VXLAN VNI for the provisioning network |
| `provision_ip` | — | IP/mask on the provisioning bridge (e.g. `10.100.0.20/24`) |
| `provision_gateway` | — | Gateway VTEP IP for VXLAN BUM flooding and kernel route |
| `overlay_subnet` | — | Overlay CIDR (e.g. `2a01:598:40a:5481::/64`) |
| `dns_resolver` | — | Comma-separated DNS server IPs |

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

### LUKS Encryption

BOOTy supports LUKS2 full-disk encryption for provisioned volumes. When enabled,
target partitions are formatted as LUKS2 volumes and unlocked before filesystem
creation.

```bash
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
| `keyfile` | Key file in initramfs | `luks,discard,keyscript=/etc/luks/keyfile` |

LUKS format always requires a passphrase for initial volume creation. Post-format
enrollment (TPM2 PCR binding, clevis tang enrollment) is handled separately after
the OS is installed. Invalid targets (empty device or mapped name) are silently
skipped during crypttab generation.

### VLAN Support

BOOTy supports 802.1Q VLAN tagging via netlink. Configure VLANs with the
`VLANS` variable as a comma-separated list of `VID:IFACE:IP/MASK` tuples:

```bash
export VLANS="200:eno1:10.200.0.42/24,300:eno2"
```

Each VLAN creates a tagged sub-interface (`eno1.200`), assigns the IP address
(if provided), and brings the link up. VLANs are created after the primary
network mode is established.

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
| 2 | **GoBGP/EVPN** | `NETWORK_MODE=gobgp` | Pure-Go BGP+EVPN via GoBGP (see below) |
| 3 | **FRR/EVPN** | `underlay_subnet`+`asn_server` set | BGP underlay with VXLAN overlay (FRR) |
| 4 | **Static** | `STATIC_IP` set | Assigns IP via netlink, adds default route |
| 5 | **DHCP** | Default | Tries DHCP on all physical interfaces |

Bond mode creates the bond interface first, then the selected upper mode (FRR/Static/DHCP)
runs on top of it. Each mode falls back to DHCP on failure.

### GoBGP Mode

Set `NETWORK_MODE=gobgp` to use the pure-Go BGP stack instead of FRR. GoBGP mode
uses a three-tier architecture:

1. **Underlay** — eBGP peering with leaf switches for VXLAN reachability
2. **Overlay** — EVPN Type-5 route advertisement with VXLAN encapsulation; dynamic FDB installation from received Type-2/3 routes via `watchRoutes()`
3. **IPMI** — Optional L3 path to the BMC (planned)

The overlay tier advertises Type-5 (IP Prefix) routes and processes incoming EVPN routes:
- **Type-2 (MAC/IP)** routes install unicast FDB entries (MAC → remote VTEP)
- **Type-3 (Inclusive Multicast)** routes install BUM FDB entries for flood replication
- A static BUM FDB entry and /32 kernel route to `provision_gateway` ensure baseline connectivity

#### EVPN L2 Overlay

Set `EVPN_L2_ENABLED=true` to enable full L2 EVPN overlay support. When enabled,
BOOTy additionally **originates** Type-2 and Type-3 routes:

- **Type-3 (IMET)** — Announces this VTEP for BUM flooding via ingress replication,
  so remote VTEPs include it in broadcast/unknown-unicast/multicast flooding
- **Type-2 (MAC/IP)** — Advertises the local bridge MAC and provision IP so remote
  VTEPs learn the FDB entry via BGP control-plane rather than data-plane flooding

Without `EVPN_L2_ENABLED`, the overlay only advertises Type-5 routes and processes
incoming Type-2/3 routes passively (receive-only mode).

```bash
# Enable L2 EVPN overlay (GoBGP mode)
export NETWORK_MODE=gobgp
export EVPN_L2_ENABLED=true
export provision_vni=4000
export provision_ip=10.100.0.20/24
export provision_gateway=10.0.0.1
```

The `BGP_PEER_MODE` environment variable controls session establishment:

| Mode | Description |
|------|-------------|
| `unnumbered` (default) | Link-local interface peers — IPv4 + L2VPN-EVPN over unnumbered sessions |
| `dual` | Unnumbered underlay (IPv4) + numbered peers for L2VPN-EVPN (route reflectors or DCGWs) |
| `numbered` | Explicit neighbor IPs only — requires DHCP or static underlay for initial connectivity |

Additional environment variables for GoBGP mode:
- `BGP_NEIGHBORS` — Comma-separated peer IPs (required for `dual` and `numbered` modes)
- `BGP_REMOTE_ASN` — Remote ASN for numbered peers (0 or omitted = iBGP)
- `provision_gateway` — Gateway VTEP IP (e.g. spine loopback) for BUM flooding and kernel route
- `underlay_ip` — Local VTEP / router-ID IP
- `provision_ip` — Provisioning overlay IP in CIDR (e.g. `10.100.0.20/24`)
- `provision_vni` — VXLAN VNI for the provisioning network
- `asn_server` — Local BGP ASN

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

# KVM E2E tests — provisioning, LUKS, boot (Linux + KVM + root)
make test-kvm

# Linux-only E2E (disk/mount/loop device, requires root)
go test -tags linux_e2e -v ./pkg/disk/...
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for coding standards, test requirements,
and the PR process.

## Project Structure

```
├── main.go                     # Entry point: CAPRF mode, kernel module loading
├── cmd/booty.go                # CLI version command
├── initrd.Dockerfile           # Multi-stage initramfs build (default, iso, slim, micro)
├── pkg/
│   ├── bios/                   # BIOS settings management (Dell, HPE, Lenovo, Supermicro)
│   ├── bootloader/             # Bootloader detection (GRUB, systemd-boot)
│   ├── buildinfo/              # Binary build information (version, commit, date)
│   ├── caprf/                  # CAPRF client (status, log, debug, vars parsing)
│   ├── config/                 # MachineConfig, Provider interface, Status types
│   ├── debug/                  # Debug dump utilities
│   ├── disk/                   # Disk detection, partitioning, RAID, LVM, mount
│   ├── drivers/                # Architecture-aware kernel driver management
│   ├── efi/                    # EFI variable operations
│   ├── executil/               # Centralized command execution + PATH diagnostics
│   ├── firmware/               # Firmware version collection from sysfs
│   │   └── nic/               # NIC firmware (Broadcom, Intel, Mellanox)
│   ├── grubcfg/                # GRUB config file parsing
│   ├── health/                 # Pre-provisioning hardware health checks
│   ├── image/                  # Image streaming (HTTP, OCI, gzip/lz4/xz/zstd auto-detect)
│   │   ├── oci/               # OCI registry client
│   │   └── verify/            # Image checksum and GPG signature verification
│   ├── inventory/              # Hardware inventory from sysfs/procfs
│   ├── kexec/                  # GRUB parsing, kexec load/execute
│   ├── network/                # Network mode abstraction (FRR, GoBGP, DHCP, Static, Bond)
│   │   ├── frr/               # FRR/EVPN: config rendering, address derivation
│   │   ├── gobgp/             # Pure-Go BGP stack (3-tier: Underlay, Overlay, IPMI)
│   │   ├── lldp/              # LLDP frame listener (raw AF_PACKET sockets)
│   │   ├── netplan/           # Netplan YAML + FRR config parser for EVPN auto-detection
│   │   ├── persist/           # Network configuration persistence across reboots
│   │   └── vlan/              # VLAN 802.1Q tagging via netlink
│   ├── provision/              # Orchestrator (32-step provision, deprovision)
│   │   └── configurator.go    # OS config: hostname, kubelet, GRUB, DNS, EFI, Mellanox SR-IOV
│   ├── realm/                  # Device, mount, shell operations
│   ├── rescue/                 # Rescue mode types, retry state, action resolution
│   ├── retry/                  # Retry policy framework with exponential backoff
│   ├── secureboot/             # Secure Boot chain setup
│   ├── system/                 # System-level operations
│   ├── telemetry/              # Provisioning metrics and telemetry collection
│   ├── tpm/                    # TPM/TPM2 operations
│   │   └── cryptenroll/       # LUKS key sealing to TPM2 PCR policies
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
