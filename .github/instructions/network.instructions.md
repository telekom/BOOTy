---
applyTo: "pkg/network/**"
description: "Network code conventions for BOOTy — Linux build tags, netlink patterns, GoBGP tiers, VRF isolation, PeerMode handling. USE WHEN: writing or modifying networking code in pkg/network/."
---

# Network Code

## Build Tags

All code using `netlink`, raw sockets, or Linux-specific APIs **must** start with:

```go
//go:build linux
```

Cross-platform logic (config parsing, DHCP mode selection) lives in untagged files.

## Architecture

- **Mode interface** — `Setup(ctx, cfg)`, `WaitForConnectivity(ctx, target, timeout)`, `Teardown(ctx)` for pluggable network modes
- **GoBGP Tier interface** — `Setup(ctx)`, `Ready(ctx, timeout)`, `Teardown(ctx)` for composable tiers (Underlay, Overlay, IPMI)
- **Stack** composes tiers and shares a single `*server.BgpServer` from the underlay tier

## Key Patterns

- **PeerMode enum** — `PeerModeUnnumbered` (link-local IPv6), `PeerModeDual` (interface + numbered), `PeerModeNumbered` (explicit IP). Always handle all three in switch statements.
- **VRF isolation** — NICs and dummy interfaces assigned to VRF via `netlink.LinkSetMasterByIndex`. VRFTableID defaults to 1000 in GoBGP config.
- **NIC detection** — `DetectPhysicalNICs()` with retry loop for containerlab veth timing. Expect multiple NICs on bare metal.
- **Config defaults** — Always call `ApplyDefaults()` before using Config. Use `IsFRRMode()`, `IsStaticMode()`, `IsBondMode()` helpers.

## Imports

Use the established dependency set:

```go
"github.com/vishvananda/netlink"          // Link, Addr, Route operations
"github.com/osrg/gobgp/v3/pkg/server"    // BgpServer
apipb "github.com/osrg/gobgp/v3/api"     // Protobuf API (always aliased as apipb)
"golang.org/x/net/ipv6"                   // Multicast, hop limits
```

## Testing

- FRR manager uses a `Commander` interface to mock `exec.CommandContext`
- GoBGP peering tests live in `gobgp/peering_test.go`; config tests in `gobgp/config_test.go`
- E2E network tests require ContainerLab — see `test/e2e/integration/`
