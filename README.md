# BOOTy

[![CI](https://github.com/telekom/BOOTy/actions/workflows/ci.yml/badge.svg)](https://github.com/telekom/BOOTy/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/telekom/BOOTy)](https://goreportcard.com/report/github.com/telekom/BOOTy)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

A lightweight initrd for Operating System image deployment over the network.

BOOTy boots as the init process inside a minimal initramfs, contacts a provisioning server, and either **writes** a disk image to a local device or **reads** a local disk and uploads it to the server.

> **Warning** — This software has **no guard rails**. Incorrect use can overwrite an existing Operating System.

## Architecture

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

## Prerequisites

- Go **1.26+**
- Docker (for building the initramfs)
- A DHCP/PXE environment for network booting

## Building

### Initramfs (recommended)

Build the complete initramfs with Docker:

```bash
make build
```

This compiles BOOTy for `linux/amd64` and `linux/arm64`, then packages BusyBox, LVM2, and cloud-utils into a bootable initramfs.

To extract the initramfs to the local filesystem:

```bash
docker run ghcr.io/telekom/booty:latest tar -cf - /initramfs.cpio.gz | tar xf -
```

### Binary only

```bash
GOOS=linux go build -o booty .
```

## Usage

### Server

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

### Static Network Configuration

Set a static IP and gateway on the provisioned OS:

```bash
go run server/server.go \
  -action writeImage \
  -mac 00:50:56:a5:0e:0f \
  -sourceImage http://192.168.0.95:3000/images/ubuntu.img \
  -destinationDevice /dev/sda \
  -growPartition 1 \
  -lvmRoot /dev/ubuntu-vg/root \
  -address 172.16.1.126/24 \
  -gateway 172.16.1.1
```

### Debugging

| Flag | Description |
|------|-------------|
| `-shell` | Drop to a BusyBox shell if something fails |
| `-wipe` | Wipe the provisioned disk on failure |
| `-dryRun` | Log actions without writing to disk |

## Development

```bash
# Run tests
make test

# Run linter
make lint

# Build binary
make build
```

## Project Structure

```
├── cmd/booty.go          # CLI entry point & orchestration
├── main.go               # Binary entry point
├── server/server.go      # Provisioning server
├── pkg/
│   ├── image/            # Disk image read/write (HTTP, gzip)
│   ├── plunderclient/    # HTTP client for config retrieval
│   ├── realm/            # Device, disk, mount, network, shell ops
│   ├── utils/            # Cmdline parsing, helpers
│   └── ux/               # ASCII art & system info display
├── initrd.Dockerfile     # Multi-stage initramfs build
├── .github/workflows/    # CI, KVM test, release pipelines
└── .golangci.yml         # Linter configuration
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, coding standards, and the PR process.

## License

This project is licensed under the Apache License 2.0 — see [LICENSE](LICENSE) for details.
