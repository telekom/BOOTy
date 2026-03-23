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
- Overlay tests (Type-5 builders, `extractNextHop`) live in `gobgp/overlay_test.go`
- E2E network tests require ContainerLab — see `test/e2e/integration/`

## EVPN Route Processing

`watchRoutes()` in `overlay.go` monitors GoBGP's `WatchEvent` stream for EVPN routes
and installs corresponding kernel state:

- **Type-2 (MAC/IP Advertisement)** — installs/updates unicast FDB entries (`MAC → remote VTEP`) via
  `netlink.NeighSet` on the VXLAN device (and `netlink.NeighDel` on withdraw); tracks MAC→VTEP
  mappings so withdrawals without next-hop can still clean up; skips routes from our own RouterID
- **Type-3 (Inclusive Multicast)** — installs BUM FDB entries (`00:00:00:00:00:00 → remote VTEP`)
  for flood replication via `netlink.NeighSet` (and `netlink.NeighDel` on withdraw); skips own RouterID
- **NextHop extraction** — `extractNextHop()` walks `MpReachNLRIAttribute` path attributes
  to find the originating VTEP IP

BOOTy **advertises** Type-5 (IP Prefix) routes for provisioning subnet reachability.
When `EVPN_L2_ENABLED` is set, it also **advertises**:
- **Type-3** (IMET) route so remote VTEPs include this node in BUM flooding
- **Type-2** (MAC/IP) route for the local bridge MAC and provision IP

Type-2/3 routes are also **received** from the spine/fabric for dynamic FDB population.
A static BUM FDB entry and /32 kernel route to `provision_gateway` ensure baseline
connectivity before dynamic routes arrive.

### provision_gateway

Set `provision_gateway` in the vars file to the spine/DCGW loopback IP (VTEP address).
This triggers:
1. `installGatewayRoute()` — /32 host route to the VTEP via the first physical NIC
2. `addGatewayFDB()` — BUM FDB entry on the VXLAN device for ARP flooding
