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
4. The provisioning pipeline runs 41 steps: status reporting -> provisioning input validation -> RAID cleanup -> NVMe namespace setup -> disk detection -> partition layout -> image streaming -> shared data mounting -> optional sysext loading -> OS configuration -> EFI fallback installation -> cloud-init injection -> kexec.
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
- **41-step provisioning pipeline** — provisioning input validation, RAID cleanup, NVMe namespace setup, disk detection, partition layout, image streaming, shared data mounting, partition growth, LVM, filesystem resize, optional sysext loading, OS configuration, EFI fallback installation, cloud-init injection, EFI boot, Mellanox SR-IOV, post-provision hooks
- **systemd-sysext provisioning** — Optional digest-checked sysext preload or active loading into the provisioned OS image
- **Kexec support** — Fast reboot into installed kernel without full BIOS POST (auto-disabled after firmware changes)
- **Remote logging** — Real-time log and debug shipping to CAPRF controller
- **Startup crash artifact upload** — Best-effort pre-wipe collection of existing OS crash logs, dumps, and host metadata for CAPRF/S3 correlation
- **Hard/soft deprovisioning** — Full disk wipe or GRUB rename for reprovisioning
- **Standby mode** — Hot standby with heartbeats and command polling for sub-second provisioning
- **Cloud-init injection** — NoCloud and ConfigDrive seed generation from provisioning identity and network config
- **Netplan overlay** — Drop-in netplan YAML config from provisioner overrides `/deploy/vars` network settings
- **Network persistence renderers** — Library support for netplan, NetworkManager, and systemd-networkd config generation
- **IPMI operations** — BMC network config, boot device control, chassis power, sensor readings
- **TPM 2.0 support** — Detection, PCR reading, metadata collection, LUKS2 TPM enrollment (Phase 2)
- **Bootloader helpers** — GRUB-oriented provisioning plus experimental bootloader detection helpers
- **BGP policy engine** — Import/export filtering, community tagging, graceful restart
- **Multi-architecture builds** — CI builds `linux/amd64` and `linux/arm64` artifacts
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

### Supported and Tested Scope

BOOTy is Linux-only runtime software: it runs as PID 1 inside an initramfs and
uses Linux syscalls, mounts, block devices, netlink, KVM/QEMU test coverage,
and ContainerLab networking tests. macOS and Windows are supported only as
cross-compilation hosts for the Go binary.

| Scope | Current CI proof |
|-------|------------------|
| Go binary | `linux/amd64` and `linux/arm64` build jobs |
| Initramfs artifacts | `linux/amd64` default, slim, micro, and GoBGP build-flavor jobs |
| Boot/provisioning behavior | x86_64 KVM/QEMU jobs on Ubuntu GitHub runners |
| Network integration | ContainerLab and vrnetlab jobs on privileged Linux runners |
| Network persistence | Unit tests for Ubuntu/netplan, RHEL/NetworkManager, and Flatcar/systemd-networkd writers plus explicit provisioning opt-in wiring |

CI does not currently prove macOS/Windows runtime behavior, non-Linux boot
targets, vendor Flatcar images, VMware ESXi provisioning, Windows target
provisioning, SUSE/openSUSE-specific provisioning, Fedora-specific
provisioning, RHEL/Rocky/Alma target bootloader behavior, Debian target image
first boot, or automatic target-OS detection for persistent network
configuration.

Target OS support is limited to behavior the repository implements and tests:

| Target OS family | Current status |
|------------------|----------------|
| Generic Linux images | Supported at the image/disk/provisioning level when the target image is compatible with the GRUB-oriented provisioning flow and required target-side tools/files are present. Current CI uses synthetic Linux images on Ubuntu runners rather than real distro cloud images. |
| Ubuntu/Debian-like images | Common examples and GRUB/update-grub assumptions exist, but CI does not prove first boot of a real Ubuntu or Debian target image with cloud-init, netplan, or systemd applying the generated files. |
| RHEL/Rocky/Alma/Fedora/SUSE/openSUSE/SLES | Not target-OS proven. Some label and renderer helpers exist, but active provisioning does not implement native GRUB2/BLS/vendor EFI paths, SELinux relabeling, or distro-specific first-boot validation. |
| Flatcar | Unit tests cover systemd-networkd rendering and source-root selection can handle explicit `USR-A`/`USR-B` labels. The existing KVM test uses a Flatcar-like synthetic source layout, not a real Flatcar vendor image, Ignition, update-engine, or Nebraska flow. |
| VMware ESXi | Unsupported and unclaimed. The repository has no ESXi/VMware/vSphere/VMFS provisioning path or CI coverage. |
| Windows | Unsupported as a BOOTy runtime or provisioned target OS. Windows is mentioned only as a possible Go cross-compilation host. |

## Building

### BOOTy Binary

Compile the BOOTy binary for the configured Go target:

```bash
make build
```

### Initramfs (recommended)

Build the full amd64 initramfs container image with Docker:

```bash
make dockerx86
```

To emit a local bootable artifact instead of loading a Docker image, use one of
the build-flavor targets below, for example `make gobgp` or `make iso`. These
Docker targets compile BOOTy and assemble a bootable initramfs or ISO; the
included networking stack, disk tools, firmware tools, and kernel modules vary
by flavor.

To extract the initramfs to the local filesystem:

```bash
ID=$(docker create ghcr.io/telekom/booty:latest null)
docker cp "$ID:/initramfs.cpio.zst" initramfs.cpio.zst
docker rm "$ID"
```

### Build Flavors

The `initrd.Dockerfile` supports multiple build targets via `--target`:

| Target | Size | Networking | Disk Tools | Use Case |
|--------|------|-----------|------------|----------|
| *(default)* | ~80 MB | FRR/EVPN + DHCP | Full (LVM, sfdisk, mdadm) | Production bare-metal provisioning |
| `gobgp` | ~45 MB | GoBGP/EVPN + DHCP | Full (LVM, sfdisk, mdadm) | Production without FRR dependency |
| `iso` | ~100 MB | FRR/EVPN + DHCP | Full | Bootable ISO for Redfish virtual media |
| `gobgp-iso` | ~65 MB | GoBGP/EVPN + DHCP | Full | Bootable GoBGP ISO for Redfish virtual media |
| `slim` | ~15 MB | DHCP only | Provisioning basics, no LVM/FRR | Lightweight provisioning without BGP |
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
| `BOOTY_RESUME` | `false` | Enable checkpoint resume — skip previously completed steps on restart |
| `DISABLE_KEXEC` | `false` | Skip kexec, always hard-reboot. Must stay `false` for A/B preserve-existing upgrades |
| `MIN_DISK_SIZE_GB` | `0` | Minimum disk size filter (0 = no minimum) |
| `BOOTY_ALLOW_REMOVABLE` | `false` | Allow USB/removable media as provisioning target |
| `MACHINE_EXTRA_KERNEL_PARAMS` | — | Additional kernel cmdline parameters |
| `INIT_URL` | — | CAPRF init status endpoint |
| `SUCCESS_URL` | — | CAPRF success status endpoint |
| `ERROR_URL` | — | CAPRF error status endpoint |
| `LOG_URL` | — | CAPRF structured log endpoint |
| `DEBUG_URL` | — | CAPRF debug payload endpoint |
| `HEARTBEAT_URL` | — | Standby mode: URL for periodic keepalives |
| `COMMANDS_URL` | — | Standby mode: URL to poll for pending commands |
| `TOKEN_URL` | — | JWT token acquisition endpoint (HTTPS required except localhost) |
| `TOKEN_ALGORITHM` | — | JWT algorithm override: `RS256` or `ES256` |
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
| `CRASH_ARTIFACTS_ENABLED` | `false` | Inspect the existing OS before destructive actions and upload crash artifacts when evidence is found |
| `CRASH_ARTIFACTS_PREPARE_URL` | — | CAPRF endpoint that receives crash metadata and returns upload instructions |
| `CRASH_ARTIFACTS_UPLOAD_URL` | — | Direct CAPRF proxy endpoint for crash archive uploads when no prepare endpoint is used |
| `CRASH_ARTIFACTS_MAX_MB` | `256` | Maximum crash artifact payload size in MiB |
| `CRASH_ARTIFACTS_UPLOAD_TIMEOUT_SEC` | `120` | Timeout for crash artifact prepare/upload requests |
| `CRASH_ARTIFACTS_INCLUDE_MEMORY_DUMPS` | `false` | Include raw `vmcore` and systemd coredump files; disabled by default because these can contain process memory and secrets |
| `SECUREBOOT_REENABLE` | `false` | Signal CAPRF to re-enable Secure Boot after provisioning |
| `MOK_CERT_PATH` | — | *(Phase 2)* Path to DER-encoded MOK certificate for custom kernel signing |
| `MOK_PASSWORD` | — | *(Phase 2)* One-time password for MokManager confirmation |
| `IMAGE_CHECKSUM` | — | Expected hex digest of the raw disk image |
| `IMAGE_CHECKSUM_TYPE` | — | Checksum algorithm: `sha256` or `sha512` |
| `IMAGE_MODE` | `whole-disk` | Image write mode: `whole-disk`, `partition`, or `ab` |
| `AB_SCHEME` | `dual-root` | A/B partitioning scheme for `IMAGE_MODE=ab` |
| `AB_ACTIVE_SLOT` | — | Currently booted A/B slot, `a` or `b` |
| `AB_TARGET_SLOT` | `inactive` | A/B slot to write: `inactive`, `a`, or `b` |
| `AB_PRESERVE_EXISTING` | `false` | Reuse an existing A/B layout and write only the target root slot |
| `AB_BOOT_SIZE_MB` | `512` | Shared EFI partition size for generated A/B layouts |
| `AB_ROOT_SIZE_MB` | `32768` | Size of each generated A/B root slot |
| `AB_STATE_SIZE_MB` | `0` | Persistent state partition size; `0` fills remaining disk |
| `AB_DATA_PARTITIONS` | — | JSON list of shared data partitions for `AB_SCHEME=system-ab`; the single `sizeMB:0` fill-remaining partition must be last |
| `AB_SOURCE_ROOT_LABEL` | — | Source-image GPT partition label to copy into the target root slot |
| `AB_SOURCE_ROOT_PARTITION` | — | 1-based source-image partition number to copy when no stable label exists |
| `DISK_DEVICE` | auto-detect | Explicit disk device path override (e.g. `/dev/sda`) |
| `DISK_SERIAL_NUMBER` | — | Select the target disk by exact sysfs serial number when `DISK_DEVICE` is unset |
| `IMAGE_SIGNATURE_URL` | — | URL to detached GPG signature for image verification |
| `IMAGE_GPG_PUBKEY` | — | Path to GPG public key for image signature verification |
| `LUKS_ENABLED` | `false` | *(Planned)* Enable LUKS2 encryption for target partitions |
| `LUKS_PASSPHRASE` | — | *(Planned)* Passphrase for initial LUKS volume creation |
| `LUKS_UNLOCK_METHOD` | `passphrase` | *(Planned)* Unlock method: `passphrase`, `tpm2`, `clevis`, `keyfile` |
| `LUKS_CIPHER` | `aes-xts-plain64` | *(Planned)* LUKS2 cipher algorithm |
| `LUKS_KEY_SIZE` | `512` | *(Planned)* LUKS2 key size in bits |
| `LUKS_HASH` | `sha256` | *(Planned)* LUKS2 hash algorithm |
| `NUM_VFS` | `0` | Number of SR-IOV virtual functions for Mellanox NICs (0 = skip) |
| `NVME_NAMESPACES` | — | JSON config for NVMe namespace creation (e.g. `[{"controller":"/dev/nvme0","namespaces":[{"label":"os","sizePct":100}]}]`) |
| `CLOUDINIT_ENABLED` | `false` | Generate and inject cloud-init config |
| `CLOUDINIT_DATASOURCE` | `nocloud` | Cloud-init datasource type: `nocloud` or `configdrive` |
| `SYSEXT_ENABLED` | `false` | Copy configured systemd-sysext layers into the provisioned root |
| `SYSEXT_DEFAULT_MODE` | `preload` | Default sysext layer mode: `preload` or `active` |
| `SYSEXT_CATALOG_DIR` | `/usr/lib/tcaas-sysext/preloaded` | Target catalog directory for preloaded sysext layers |
| `SYSEXT_ACTIVE_DIR` | `/var/lib/extensions` | Target directory for active sysext layers |
| `SYSEXT_LAYERS` | — | JSON array of sysext layer objects |
| `SYSEXT_ALLOW_INSECURE_HTTP` | `false` | Allow plain HTTP sysext sources on controlled provisioning networks |

#### Network Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `NETWORK_MODE` | — | Network mode override: `gobgp` for pure-Go BGP stack |
| `STATIC_IP` | — | Static IP in CIDR notation (e.g. `10.0.0.5/24`) |
| `STATIC_GATEWAY` | — | Default gateway for static networking |
| `STATIC_IFACE` | — | Target interface for static IP and non-bond cloud-init network config |
| `PERSIST_NETWORK` | `false` | Write configured target OS network files during provisioning |
| `OS_FAMILY` | — | Required when `PERSIST_NETWORK=true`; one of `ubuntu`, `rhel`, `flatcar` |
| `BOND_INTERFACES` | — | Comma-separated interfaces for LACP bond (e.g. `eth0,eth1`) |
| `BOND_MODE` | `802.3ad` | Bond mode: `802.3ad`/`lacp`, `balance-rr`, `active-backup`, `balance-xor` |
| `VLANS` | — | Multi-VLAN config (e.g. `200:eno1:10.200.0.42/24,300:eno2`) |
| `BGP_PEER_MODE` | `unnumbered` | GoBGP peering mode: `unnumbered`, `dual`, `numbered` |
| `BGP_INTERFACES` | — | Optional comma-separated interface allowlist for unnumbered/dual GoBGP peers; empty means all detected physical NICs |
| `BGP_NEIGHBORS` | — | Comma-separated peer IPs (required for `dual` and `numbered` modes) |
| `BGP_REMOTE_ASN` | — | Remote ASN for numbered peers (0 or omitted = iBGP) |
| `BGP_UNDERLAY_AF` | `ipv4` | Underlay address family: `ipv4`, `ipv6`, `dual-stack` |
| `BGP_OVERLAY_TYPE` | `evpn-vxlan` | Overlay encapsulation: `evpn-vxlan`, `l3vpn`, `none` |
| `BGP_AUTH_PASSWORD` | — | Optional TCP-MD5 password for all BGP peers (empty = no authentication) |
| `VRF_TABLE_ID` | `1` | VRF routing table ID (0 uses default of 1) |
| `VRF_NAME` | — | Explicit VRF name for GoBGP stack isolation (auto-generated if empty) |
| `BGP_KEEPALIVE` | `0` | Optional BGP keepalive timer in seconds (0 = stack default) |
| `BGP_HOLD` | `0` | Optional BGP hold timer in seconds (0 = stack default) |
| `BGP_MIN_PEERS` | `1` | Minimum number of BGP peers that must reach ESTABLISHED state before underlay is considered ready |
| `BFD_TRANSMIT_MS` | `0` | Optional BFD transmit interval in milliseconds (0 = disabled) |
| `BFD_RECEIVE_MS` | `0` | Optional BFD receive interval in milliseconds (0 = disabled) |
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
(`setup-mellanox`, `detect-disk`, `parse-partitions`, `mount-root`, `mount-boot`,
`mount-shared-data`, `setup-chroot-binds`) which always re-execute to rebuild
in-memory state and mount state (target disk path, partition info, target
mounts, shared data mount cleanup list, chroot bind mounts).

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
OCI image sources must either be digest-pinned (`oci://repo/image@sha256:...`)
or provide `IMAGE_CHECKSUM`; mutable OCI tags without a checksum are rejected
before destructive storage steps.

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
downloads the detached signature and verifies it against the provided public key
before destructive storage setup starts.

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

### Startup Crash Artifact Upload

BOOTy can inspect the existing OS on the target disk before provisioning or
deprovisioning performs destructive disk operations. When crash evidence is
found, BOOTy uploads a `tar.gz` archive containing allowlisted crash artifacts
plus `manifest.json` and `metadata.json` for fleet correlation.

```bash
export CRASH_ARTIFACTS_ENABLED=true
export CRASH_ARTIFACTS_PREPARE_URL="https://caprf.example.com/crash-artifacts/prepare"
export CRASH_ARTIFACTS_MAX_MB=256
```

The collector searches for kernel crash and dump evidence in paths such as
`/var/crash`, `/var/lib/systemd/coredump`, `/var/log/journal`, kernel logs, and
`/sys/fs/pstore`. The archive metadata reuses BOOTy's hardware inventory,
firmware report, debug dump, and build information. Upload failures are
best-effort and do not block provisioning.
Text logs and JSON metadata are redacted before upload. Raw `vmcore` and
systemd coredump files are skipped unless
`CRASH_ARTIFACTS_INCLUDE_MEMORY_DUMPS=true` is set.

CAPRF can either return presigned S3 upload instructions from
`CRASH_ARTIFACTS_PREPARE_URL` or accept direct proxy uploads at
`CRASH_ARTIFACTS_UPLOAD_URL`. BOOTy never sends its bearer token to presigned
S3 URLs and does not log presigned query strings.

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
export VLANS="200:eno1:[2001:db8:200::42/64]:[2001:db8:200::1]"
```

Each VLAN creates a tagged sub-interface (`eno1.200`), assigns the IP address
(if provided), and brings the link up. VLANs are created after the primary
network mode is established. IPv6 address or gateway fields in `VLANS` must
be wrapped in brackets because `:` separates fields in the compact format.

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
# Set a positive number of virtual functions. NUM_VFS=0 skips this step.
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
pending commands (every 10s). On failure, the configured `RESCUE_MODE` policy
applies (retry, shell, wait, or reboot).

| Command | Behavior |
|---------|----------|
| `provision` | Run the full provisioning orchestrator, then kexec/reboot |
| `deprovision` | Run deprovisioning (hard or soft), then reboot |
| `reboot` | Immediate reboot |
| `health-check` | Liveness probe — ACKs with `"healthy"` to confirm agent is responsive |

Each command is acknowledged back to the controller with its completion
status (`completed` or `failed` with error message).

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


### BGP Policy

GoBGP mode supports BGP policy configuration with community tagging on
import/export routes. Policies are applied to control route distribution
across the EVPN fabric.

**Community formats:**
- **Standard communities**: `ASN:value` (16-bit each, e.g. `65000:100`)
- **Extended communities**: `TYPE:ASN:value` with 4-octet ASN support (e.g. `RT:4200000001:100`)
- **Large communities**: `GA:LD1:LD2` (32-bit each, e.g. `65000:1:100`)

The policy engine in `pkg/network/gobgp/policy.go` supports import/export
filtering, community attachment, and graceful restart configuration for
session resilience.

**VRF isolation** supports multi-VRF configurations with separate management
and provisioning routing tables. VRF configs are validated for unique names,
non-zero table IDs, and no conflicts. Set `VRF_NAME` to explicitly name the
VRF (auto-generated if empty) and `VRF_TABLE_ID` to assign the routing
table.

### Cloud-Init

BOOTy generates and injects cloud-init configuration into the provisioned
OS. Supported datasource types are `nocloud` and `configdrive`.

```bash
export CLOUDINIT_ENABLED=true
export CLOUDINIT_DATASOURCE=nocloud    # nocloud (default) or configdrive
```

When enabled, BOOTy writes cloud-init seed data to the appropriate path
on the provisioned root filesystem. `nocloud` writes
`/var/lib/cloud/seed/nocloud/`, and `configdrive` writes OpenStack ConfigDrive
v2 seed files under `/var/lib/cloud/seed/config_drive/openstack/latest/`.
For non-bond cloud-init network config, `STATIC_IFACE` is required so the
generated v2 network-config names the target OS interface instead of an
initramfs-only fallback.
The active provisioning integration currently generates:

- **Instance metadata** — instance-id, local-hostname, and platform (`booty`).
  When a provider-id is configured, it is used as the source for instance-id
  and is not written as a separate metadata field.
- **User data** — hostname and `manage_etc_hosts`
- **Network config v2** — IPv4/IPv6 addresses and gateways, bonds, VLANs,
  and nameservers
  (generated from the active provisioning network config)

The cloud-init generator package has fields for richer user-data such as users,
packages, NTP, file writes, and `runcmd`, but the provisioning configuration
surface does not currently populate those fields from CAPRF or environment
configuration.

### Netplan Overlay

BOOTy supports provisioner-supplied netplan configuration as a drop-in
alternative to `/deploy/vars` network settings. When netplan YAML files
are present in `/deploy/file-system/etc/netplan/`, BOOTy parses them
and uses the derived network config, with netplan values overriding
`/deploy/vars` settings.

```
/deploy/file-system/
├── etc/
│   ├── netplan/
│   │   └── 01-network.yaml    # Netplan config (bonds, bridges, VLANs, tunnels, static routes)
│   └── frr/
│       └── frr.conf           # Optional FRR config (ASN, peers, EVPN settings)
```

The netplan parser supports ethernets, bonds, bridges, VLANs, tunnels
(VXLAN/GRE), dummy devices, VRFs, and static routes. When an FRR config
file is also present, BOOTy extracts BGP parameters (ASN, router-ID,
peers, EVPN settings) from it. Pure Type-5 configs that use
`advertise ipv4 unicast` stay L3-only; `advertise-all-vni` is the opt-in
signal for L2 EVPN Type-2/Type-3 handling.

### Network Persistence

BOOTy includes renderer helpers for persistent network configuration on the
target OS filesystem. Set `PERSIST_NETWORK=true` and an explicit `OS_FAMILY`
to write those files during provisioning. BOOTy does not auto-detect the
target OS family, and static address persistence without a bond requires
`STATIC_IFACE` because the initramfs auto-detected interface name may not be
stable in the provisioned OS. During provisioning, BOOTy also writes DNS
configuration and can generate cloud-init network config when cloud-init
injection is enabled.

Implemented renderer formats:

| OS Family | Format | Config Path | Scope |
|-----------|--------|-------------|-------|
| Ubuntu | Netplan YAML | `/etc/netplan/` | Unit-tested renderer and explicit provisioning opt-in |
| RHEL | NetworkManager keyfiles | `/etc/NetworkManager/system-connections/` | Unit-tested renderer and explicit provisioning opt-in, including static/DHCP interfaces, bonds, and VLANs |
| Flatcar | systemd-networkd units | `/etc/systemd/network/` | Unit-tested renderer and explicit provisioning opt-in, including static/DHCP interfaces, bonds, and VLANs |

The renderer package can write interface configs, bond settings, addresses,
gateways, DNS, and VLAN assignments where the selected writer supports them.

### IPMI Operations

BOOTy includes an IPMI manager (`pkg/ipmi/`) for local BMC operations
via `ipmitool`. IPMI capabilities are used during provisioning for
auto-detecting the BMC MAC address and IP, which are included in
debug/inventory payloads.

| Operation | Description |
|-----------|-------------|
| `GetBMCNetwork` | Read BMC IP, netmask, gateway, MAC, DHCP, and VLAN settings |
| `SetNextBoot` | Set next boot device: `pxe`, `disk`, `cdrom`, `bios` |
| `ChassisControl` | Power on/off/cycle/reset/soft |
| `GetChassisStatus` | Query power state, faults, and intrusion |
| `GetSensors` | Read sensor values (temperature, voltage, fan) |

No environment variables are required — IPMI is used automatically when
`ipmitool` is available in the initramfs.

### TPM Support

BOOTy detects TPM 2.0 hardware via sysfs and collects metadata for
inventory and debug payloads.

| Capability | Description |
|-----------|-------------|
| Detection | Reads `/sys/class/tpm/tpm0` for presence, version, manufacturer, firmware |
| PCR reading | Reads SHA256 PCR bank values from sysfs |
| Attestation | Quote-based TPM attestation (Phase 2) |
| Measurement | PCR extend operations (Phase 2) |
| LUKS enrollment | `systemd-cryptenroll` integration for TPM2-bound LUKS2 unlock (Phase 2) |

TPM info is included in hardware inventory and debug dumps when a TPM is
present. No environment variables are required — detection is automatic.

### Bootloader Helpers

BOOTy includes helper code for detecting the installed bootloader on a target
filesystem:

| Bootloader | Detection | Architecture |
|-----------|-----------|--------------|
| systemd-boot | Presence of `/boot/efi/EFI/systemd/systemd-bootx64.efi` inside the target root | x86_64 |
| systemd-boot | Presence of `/boot/efi/EFI/systemd/systemd-bootaa64.efi` inside the target root | ARM64 |
| GRUB | Default fallback when systemd-boot is not found | All |

The active provisioning pipeline is still GRUB-oriented. It writes GRUB drop-in
configuration, invokes `update-grub` in the target chroot, and creates EFI
entries using the currently implemented GRUB paths. The detector is not yet the
source of truth for provisioning, kexec parsing, or EFI boot-entry creation.

## Extending Bundled Binaries

The initramfs is built in `initrd.Dockerfile` using a multi-stage Docker build. To add
a new binary:

1. **Install the package** in the `tools` build stage:
```dockerfile
# In the 'tools' stage (FROM debian:bookworm-slim AS tools)
RUN apt-get update && apt-get install -y --no-install-recommends \
    your-package \
    && rm -rf /var/lib/apt/lists/*
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
make clab-test-image                         # Generate the shared test disk image
make clab-up && make test-e2e-integration       # FRR/EVPN topology
make clab-gobgp-up && make test-e2e-gobgp        # GoBGP topology
make clab-boot-up && make test-e2e-boot          # Boot orchestrator
make clab-dhcp-up && make test-e2e-dhcp          # DHCP mode topology
make clab-bond-up && make test-e2e-bond          # Bond mode topology
make clab-lacp-up && make test-e2e-lacp          # LACP-specific bond checks
make clab-static-up && make test-e2e-static      # Static IP topology
make clab-multi-nic-up && make test-e2e-multi-nic # Multi-NIC discovery and config

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
├── cmd/booty.go                # Cobra CLI entry point
├── initrd.Dockerfile           # Multi-stage initramfs build (default, iso, slim, micro)
├── pkg/
│   ├── auth/                   # JWT token manager (acquisition, renewal, backoff)
│   ├── bios/                   # BIOS settings management (Dell, HPE, Lenovo, Supermicro)
│   ├── bootloader/             # Bootloader detection (GRUB, systemd-boot)
│   ├── buildinfo/              # Binary build information (version, commit, date)
│   ├── caprf/                  # CAPRF client (status, log, debug, vars parsing)
│   ├── cloudinit/              # Cloud-init NoCloud and ConfigDrive generation
│   ├── config/                 # MachineConfig, Provider interface, Status types
│   ├── crash/                  # Startup crash artifact collection, metadata, upload contracts
│   ├── debug/                  # Structured debug dump collection
│   ├── disk/                   # Disk detection, partitioning, RAID, LVM, mount, NVMe namespaces
│   ├── drivers/                # Architecture-aware kernel module loading
│   ├── efi/                    # EFI variable and boot entry operations
│   ├── event/                  # Provisioning lifecycle event types + dispatcher
│   ├── executil/               # Shared command execution helpers
│   ├── firmware/               # Firmware version collection from sysfs
│   ├── grubcfg/                # GRUB configuration parser
│   ├── health/                 # Pre-provisioning hardware health checks
│   ├── image/                  # Image streaming (HTTP, OCI, gzip/lz4/xz/zstd auto-detect)
│   ├── inventory/              # Hardware inventory from sysfs/procfs
│   ├── ipmi/                   # IPMI operations and helpers
│   ├── kexec/                  # GRUB parsing, kexec load/execute
│   ├── logging/                # Structured log handlers and sinks
│   ├── network/                # Network mode abstraction (FRR, GoBGP, DHCP, Static, Bond)
│   │   ├── frr/               # FRR/EVPN: config rendering, address derivation
│   │   ├── gobgp/             # Pure-Go BGP stack (3-tier: Underlay, Overlay, IPMI)
│   │   ├── lldp/              # LLDP frame listener (raw AF_PACKET sockets)
│   │   ├── netplan/           # Netplan YAML + FRR config parser (provisioner overlay)
│   │   ├── persist/           # Persist network config into target OS (netplan, NM, systemd-networkd)
│   │   ├── vrf/               # VRF configuration and validation
│   │   └── vlan/              # VLAN 802.1Q tagging via netlink
│   ├── provision/              # Orchestrator (41-step provision, deprovision)
│   │   └── configurator.go    # OS config: hostname, kubelet, GRUB, DNS, EFI, Mellanox SR-IOV
│   ├── realm/                  # Device, mount, network, shell operations
│   ├── rescue/                 # Rescue mode behavior and retry policy
│   ├── retry/                  # Shared retry policy framework
│   ├── secureboot/             # Secure Boot setup and validation helpers
│   ├── system/                 # Host-level system operations
│   ├── telemetry/              # Telemetry models and collectors
│   ├── tpm/                    # TPM/TPM2 detection, PCR reading, attestation
│   │   └── cryptenroll/       # systemd-cryptenroll integration for TPM2 LUKS unlock
│   ├── utils/                  # Cmdline parsing, helpers
│   └── ux/                     # ASCII art & system info display
├── test/e2e/                   # E2E tests (ContainerLab + vrnetlab + KVM)
│   ├── clab/                   # ContainerLab topologies and FRR configs
│   │   └── vrnetlab/          # QEMU VM image builder for vrnetlab testing
│   ├── kvm/                    # KVM/QEMU boot tests (provisioning, network, hardware)
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
| [docs/sysext-provisioning.md](docs/sysext-provisioning.md) | Optional systemd-sysext loading while provisioning |
| [docs/ab-partitioning.md](docs/ab-partitioning.md) | Optional A/B partitioning and inactive-slot provisioning |
| [.github/AGENTS.md](.github/AGENTS.md) | Copilot agents, review personas, prompts |
| [.github/copilot-instructions.md](.github/copilot-instructions.md) | Project guidelines for Copilot |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, coding standards, and the PR process.

## License

This project is licensed under the Apache License 2.0 — see [LICENSE](LICENSE) for details.
