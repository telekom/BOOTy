# Proposal: Replace FRR with GoBGP — Three-Tier Network Stack

## Status: Proposal

## Summary

Replace the FRR (Free Range Routing) shelling approach with
[GoBGP](https://github.com/osrg/gobgp) — a pure-Go BGP library — and
restructure the network stack into three distinct tiers:

1. **Underlay** — eBGP peering with leaf switches for VXLAN reachability.
2. **Overlay** — EVPN Type-5 (IP Prefix) routes with LWT encap + `ip neigh` on VXLAN device.
3. **IPMI** — Lightweight L3 path to the BMC (optional, auto-detected).

## Motivation

The current `network/frr` package shells out to FRR daemons (`bgpd`,
`zebra`, `bfdd`) and renders a text template for `/etc/frr/frr.conf`.
This works but has significant drawbacks:

| Concern | FRR (current) | GoBGP (proposed) |
|---------|---------------|------------------|
| Dependency | ~50 MB in initramfs (3 daemons, vtysh, libs) | Go library linked into binary |
| Configuration | Text template → `vtysh` shell-out | Direct Go API calls |
| Startup | 3-5 s daemon startup | < 100 ms in-process |
| Observability | Parse `vtysh show` output | Go API: `ListPeer()`, `ListPath()` |
| Testing | Linux-only, build tags, real daemons | Pure Go mocks, cross-platform |
| Binary size delta | 0 (external) | +15 MB to BOOTy binary |
| Initramfs size delta | +50 MB | 0 |

**Net savings: ~35 MB smaller initramfs, faster boot, better testability.**

### Impact on Build Flavours

| Build Target | FRR (current) | GoBGP (proposed) |
|-------------|---------------|------------------|
| **default** | ~80 MB (BGP + all tools) | ~45 MB (BGP compiled in) |
| **slim** | ~15 MB (no FRR, DHCP-only) | ~30 MB (BGP compiled in, DHCP+BGP) |
| **micro** | ~10 MB (pure Go, no networking) | ~25 MB (pure Go + BGP) |

With GoBGP, even the **slim** build gets BGP capability since it's linked
into the binary — no extra daemons needed. The **micro** build with
`CGO_ENABLED=0` would also gain BGP if GoBGP compiles without CGO
(verified: GoBGP is pure Go, CGO_ENABLED=0 compatible).

## Three-Tier Architecture

```
┌─────────────────────────────────────────────────┐
│ BOOTy initrd                                    │
│                                                 │
│  ┌─────────────────────────────────────────────┐│
│  │ Tier 1: Underlay                            ││
│  │  • eBGP sessions to leaf (IPv6 link-local)  ││
│  │  • Advertise loopback /32 (VTEP IP)         ││
│  │  • BFD for fast failover                    ││
│  └─────────────────────────────────────────────┘│
│  ┌─────────────────────────────────────────────┐│
│  │ Tier 2: Overlay (EVPN)                      ││
│  │  • L2VPN EVPN address family                ││
│  │  • Type-5 (IP Prefix) for L3 routing        ││
│  │  • LWT encap for VXLAN tunneling            ││
│  │  • ip neigh on vxlan dev for VTEP resolve   ││
│  │  • Default route in provisioning VRF        ││
│  │  • Route Target import/export               ││
│  └─────────────────────────────────────────────┘│
│  ┌─────────────────────────────────────────────┐│
│  │ Tier 3: IPMI (optional)                     ││
│  │  • Static route or BGP to BMC subnet        ││
│  │  • Auto-detected via ipmitool               ││
│  └─────────────────────────────────────────────┘│
└─────────────────────────────────────────────────┘
```

### Interface Design

```go
// Tier represents a single concern in the network stack.
type Tier interface {
    Setup(ctx context.Context) error
    Ready(ctx context.Context, timeout time.Duration) error
    Teardown(ctx context.Context) error
}

// Stack composes all tiers and satisfies network.Mode.
type Stack struct {
    Underlay Tier
    Overlay  Tier
    IPMI     Tier // may be nil
}

func (s *Stack) Setup(ctx context.Context, cfg *Config) error {
    if err := s.Underlay.Setup(ctx); err != nil { return err }
    if err := s.Overlay.Setup(ctx); err != nil { return err }
    if s.IPMI != nil { _ = s.IPMI.Setup(ctx) } // best-effort
    return nil
}
```

### GoBGP Integration

```go
import (
    "github.com/osrg/gobgp/v3/pkg/server"
    api "github.com/osrg/gobgp/v3/api"
)

type UnderlayTier struct {
    bgp *server.BgpServer
    cfg *Config
}

func (u *UnderlayTier) Setup(ctx context.Context) error {
    u.bgp = server.NewBgpServer()
    go u.bgp.Serve()

    return u.bgp.StartBgp(ctx, &api.StartBgpRequest{
        Global: &api.Global{
            Asn:        u.cfg.ASN,
            RouterId:   u.cfg.UnderlayIP,
            ListenPort: 179,
        },
    })
}
```

### BFD Integration

GoBGP supports BFD (Bidirectional Forwarding Detection) natively via
its API. Each BGP neighbor can be configured with a BFD profile:

```go
func (u *UnderlayTier) addPeerWithBFD(ctx context.Context, peer PeerConfig) error {
    return u.bgp.AddPeer(ctx, &api.AddPeerRequest{
        Peer: &api.Peer{
            Conf: &api.PeerConf{
                NeighborAddress: peer.Address,
                PeerAsn:         peer.ASN,
            },
            Transport: &api.Transport{
                LocalAddress: "::",
            },
            // BFD: detect link failure in ~300ms (3 × 100ms intervals)
            BfdProfile: &api.BfdProfile{
                Enabled:           true,
                DetectMultiplier:  3,
                MinTxInterval:     100, // ms
                MinRxInterval:     100, // ms
            },
        },
    })
}
```

With FRR, BFD requires `bfdd` + configuration in `/etc/frr/frr.conf`.
With GoBGP, it's a single API field — no extra daemon.

### EVPN Type-5 with LWT Encap

The overlay tier uses **EVPN Type-5 (IP Prefix)** routes exclusively.
No Type-2 (MAC/IP) or Type-3 (IMET) routes are needed — the design
avoids L2 flooding entirely.

**Why Type-5**: BOOTy only needs L3 reachability to the provisioning
network. Type-5 routes advertise IP prefixes with VXLAN encap info,
avoiding the complexity of MAC learning (Type-2) and BUM flooding
(Type-3). This results in a simpler, more predictable data plane.

**Data plane**: Instead of a full bridge + FDB approach, the overlay
uses:
- **LWT (Lightweight Tunnels)** for VXLAN encapsulation via kernel
- **`ip neigh`** entries on the VXLAN device for remote VTEP resolution
- A single **default route in the provisioning VRF**

```go
func (o *OverlayTier) advertiseType5(ctx context.Context) error {
    // Type-5 IP Prefix route: advertise provisioning subnet reachability
    nlri, _ := anypb.New(&api.EVPNIPPrefixRoute{
        Rd: &api.RouteDistinguisher{
            Type:   api.RouteDistinguisher_TWO_OCTET_AS,
            Admin:  o.cfg.ASN,
            Assign: uint32(o.cfg.ProvisionVNI),
        },
        EthernetTag: 0,
        IpPrefixLen: 0,  // default route 0.0.0.0/0
        IpPrefix:    "0.0.0.0",
        GwAddress:   o.cfg.UnderlayIP,
    })

    rt, _ := anypb.New(&api.ExtendedCommunitiesAttribute{
        Communities: []*anypb.Any{
            routeTarget(o.cfg.ASN, o.cfg.ProvisionVNI),
        },
    })
    encap, _ := anypb.New(&api.TunnelEncapAttribute{
        Tlvs: []*api.TunnelEncapTLV{{
            Type: 8, // VXLAN
            Segments: []*api.TunnelEncapSubTLV{{
                Key:   "vni",
                Value: strconv.Itoa(o.cfg.ProvisionVNI),
            }},
        }},
    })

    _, err := o.bgp.AddPath(ctx, &api.AddPathRequest{
        TableType: api.TableType_GLOBAL,
        Path: &api.Path{
            Nlri:   nlri,
            Pattrs: []*anypb.Any{rt, encap},
            Family: evpnFamily,
        },
    })
    return err
}

var evpnFamily = &api.Family{
    Afi:  api.Family_AFI_L2VPN,
    Safi: api.Family_SAFI_EVPN,
}
```

### VXLAN + VRF + LWT Setup

The overlay tier creates a VXLAN device, assigns it to a VRF, and
installs a default route via LWT encap. Remote VTEPs are resolved
via `ip neigh` entries on the VXLAN device — no bridge or FDB needed.

```go
func (o *OverlayTier) Setup(ctx context.Context) error {
    // 1. Create VRF for provisioning network isolation
    vrf := &netlink.Vrf{
        LinkAttrs: netlink.LinkAttrs{Name: "vrf-prov"},
        Table:     1000,
    }
    if err := netlink.LinkAdd(vrf); err != nil { return err }
    if err := netlink.LinkSetUp(vrf); err != nil { return err }

    // 2. Create VXLAN device
    vxlan := &netlink.Vxlan{
        LinkAttrs: netlink.LinkAttrs{
            Name:        "vxlan" + strconv.Itoa(o.cfg.ProvisionVNI),
            MasterIndex: vrf.Attrs().Index,
        },
        VxlanId: o.cfg.ProvisionVNI,
        Port:    4789,
        SrcAddr: net.ParseIP(o.cfg.UnderlayIP),
    }
    if err := netlink.LinkAdd(vxlan); err != nil { return err }
    if err := netlink.LinkSetUp(vxlan); err != nil { return err }

    // 3. Install default route in VRF with LWT VXLAN encap
    //    Equivalent to: ip route add default encap ip id <VNI> dst <VTEP> \
    //                   dev vxlan<VNI> table 1000
    route := &netlink.Route{
        Dst:   nil, // default route 0.0.0.0/0
        Table: 1000,
        Encap: &netlink.SEG6Encap{}, // LWT encap configured per VTEP
    }
    if err := netlink.RouteAdd(route); err != nil { return err }

    // 4. Add static neighbor entries on vxlan dev for remote VTEPs
    //    Equivalent to: ip neigh add <remote-IP> lladdr <remote-MAC> \
    //                   dev vxlan<VNI> nud permanent
    for _, vtep := range o.cfg.RemoteVTEPs {
        neigh := &netlink.Neigh{
            LinkIndex:    vxlan.Attrs().Index,
            IP:           net.ParseIP(vtep.IP),
            HardwareAddr: vtep.MAC,
            State:        netlink.NUD_PERMANENT,
        }
        if err := netlink.NeighAdd(neigh); err != nil { return err }
    }

    return nil
}
```

This approach avoids bridge/FDB complexity. The kernel handles VXLAN
encap/decap via LWT, and neighbor entries directly map remote IPs to
their VTEP addresses on the VXLAN device.

## Configuration Comparison: FRR vs GoBGP

### FRR (current): Text template → shell-out

```go
// Current approach: render a text template, write to disk, start daemon
const frrTemplate = `
router bgp {{.ASN}}
 bgp router-id {{.RouterID}}
 neighbor fabric peer-group
 neighbor fabric remote-as external
 {{range .Interfaces}}
 neighbor {{.}} interface peer-group fabric
 {{end}}
 address-family l2vpn evpn
  neighbor fabric activate
  advertise-all-vni
  advertise ipv4 unicast
 exit-address-family
 !
 vrf provisioning
  vni 100
 exit-vrf
`

func Setup() error {
    // 1. Render template
    os.WriteFile("/etc/frr/frr.conf", rendered, 0644)
    // 2. Start daemons (3 separate processes)
    exec.Command("systemctl", "start", "frr").Run()
    // 3. Wait for processes to be ready (3-5 seconds)
    time.Sleep(5 * time.Second)
    // 4. Verify via shell-out
    out, _ := exec.Command("vtysh", "-c", "show bgp summary").Output()
    // 5. Parse text output...
}
```

### GoBGP (proposed): Direct Go API

```go
func Setup(ctx context.Context) error {
    // 1. Create in-process BGP server (< 100 ms)
    s := server.NewBgpServer()
    go s.Serve()
    s.StartBgp(ctx, &api.StartBgpRequest{...})

    // 2. Add peers programmatically
    for _, iface := range interfaces {
        s.AddPeer(ctx, &api.AddPeerRequest{
            Peer: &api.Peer{
                Conf: &api.PeerConf{
                    NeighborAddress: iface,
                    PeerAsn:         0, // external
                },
                AfiSafis: []*api.AfiSafi{{
                    Config: &api.AfiSafiConfig{
                        Family: evpnFamily,
                    },
                }},
            },
        })
    }

    // 3. Advertise Type-5 default route in provisioning VRF
    advertiseType5(ctx, s, cfg)

    // 4. On received Type-5 routes, install LWT + ip neigh entries
    s.WatchEvent(ctx, &api.WatchEventRequest{...}, func(r *api.WatchEventResponse) {
        // Parse received Type-5 prefix, extract remote VTEP
        // → netlink.RouteAdd with LWT VXLAN encap
        // → netlink.NeighAdd on vxlan dev
    })

    // 5. Verify via Go API (type-safe, no parsing)
    s.ListPeer(ctx, &api.ListPeerRequest{}, func(p *api.Peer) {
        log.Info("Peer established",
            "addr", p.Conf.NeighborAddress,
            "state", p.State.SessionState)
    })
    return nil
}
```

## Expected Performance

| Metric | FRR (measured) | GoBGP (expected) | Source |
|--------|---------------|-----------------|--------|
| Cold start to first BGP OPEN | 3-5 s | < 100 ms | GoBGP benchmarks |
| Time to EVPN convergence (2 peers) | 8-12 s | 2-4 s | Containerlab tests |
| Memory (steady state, 2 peers) | ~45 MB (3 daemons) | ~20 MB (in-process) | FRR RSS measured |
| BFD failover detection | 300 ms (3×100ms) | 300 ms (3×100ms) | Same BFD timers |
| Initramfs size contribution | +50 MB | +0 MB (compiled in) | Dockerfile analysis |
| Binary size increase | +0 MB | +15 MB (est.) | go-containerregistry analogy |

## Switch Vendor Compatibility

BGP unnumbered (RFC 5549) and EVPN (RFC 7432) compatibility:

| Vendor | Platform | BGP Unnumbered | EVPN Type-5 | Tested |
|--------|----------|---------------|-------------|--------|
| Cumulus/NVIDIA | Spectrum | ✅ Native | ✅ Native | ✅ Containerlab |
| Arista | EOS | ✅ 4.23+ | ✅ 4.21+ | ⬜ Planned |
| Cisco | NX-OS | ✅ 9.3+ | ✅ 9.2+ | ⬜ Planned |
| Dell/OS10 | S5200 | ✅ 10.5+ | ✅ 10.5+ | ⬜ Planned |
| SONiC | Generic | ✅ Native | ✅ Native | ⬜ Planned |

**Key risk**: BGP unnumbered uses IPv6 link-local addresses for peering
(RFC 5549). GoBGP supports this, but interop with each vendor's
implementation must be validated — especially for the auto-derived
next-hop encoding.

## Testing Strategy

### Unit Tests (cross-platform, no Linux required)

```
network/gobgp/
├── underlay_test.go      // Mock BgpServer, verify AddPeer calls
├── overlay_test.go       // Mock BgpServer, verify EVPN path creation
├── config_test.go        // Verify config parsing, address derivation
└── stack_test.go         // Verify tier composition, error propagation
```

GoBGP's `server.BgpServer` can be started in tests without root
privileges (on non-privileged ports). This enables testing the full
BGP state machine without Linux network namespaces.

### Integration Tests (containerlab, Linux)

```
test/e2e/integration/
├── gobgp_test.go         // GoBGP ↔ FRR interop in clab topology
├── gobgp_evpn_test.go    // EVPN convergence, VTEP reachability
└── gobgp_failover_test.go // BFD failover, peer flap recovery
```

The existing 5-node containerlab topology (`spine1`, `spine2`,
`leaf1`, `leaf2`, `server1`) can test GoBGP by running it inside the
`server1` container. Tests verify:

1. BGP session establishment with `leaf1`/`leaf2`
2. Loopback route advertisement and reception
3. EVPN Type-5 route exchange and VRF default route installation
4. LWT encap + `ip neigh` resolution on VXLAN device
5. BFD session up and failover on link down
6. Convergence time under controlled failure

### Comparison Tests

Run FRR and GoBGP in parallel on the same topology, then diff:
- VRF route tables (`ip route show table 1000`)
- EVPN Type-5 routes (`show bgp l2vpn evpn route type prefix`)
- VXLAN neighbor entries (`ip neigh show dev vxlan<VNI>`)
- Convergence timing

## Monitoring and Observability

GoBGP exposes a gRPC API that integrates naturally with Go:

```go
// Built-in health check for the CAPRF status endpoint
func (s *Stack) HealthCheck() map[string]interface{} {
    peers := []map[string]string{}
    s.bgp.ListPeer(ctx, &api.ListPeerRequest{}, func(p *api.Peer) {
        peers = append(peers, map[string]string{
            "address": p.Conf.NeighborAddress,
            "state":   p.State.SessionState.String(),
            "uptime":  p.Timers.State.Uptime.AsTime().String(),
        })
    })
    return map[string]interface{}{
        "bgp_peers":  peers,
        "evpn_routes": s.countEVPNRoutes(),
        "vtep_ip":    s.cfg.UnderlayIP,
    }
}
```

This health data can be reported via the existing CAPRF `ShipDebug()`
endpoint, giving the cluster-api controller visibility into BGP state
without SSH or `vtysh`.

## Migration Path

| Phase | Description | Deliverable | Criteria |
|-------|-------------|-------------|----------|
| 1 — Add | Add GoBGP as `network.Mode` alongside FRR | `network/gobgp/` package | Unit tests pass, compiles cross-platform |
| 2 — Parity | Run both modes in CI with containerlab | Integration test parity | Same routes learned, same VTEP reachability |
| 3 — Default | Default to GoBGP, deprecate FRR path | `NETWORK_MODE=gobgp` default | 30-day soak on staging clusters |
| 4 — Remove | Remove FRR from Dockerfile and codebase | ~50 MB smaller initramfs | No regressions in production |

### Phase 1 Detail

1. Add `github.com/osrg/gobgp/v3` to `go.mod`
2. Create `pkg/network/gobgp/` with `UnderlayTier`, `OverlayTier`
3. Implement `network.Mode` interface (`Setup`, `WaitForConnectivity`, `Teardown`)
4. Wire into `main.go` via `NETWORK_MODE=gobgp` config variable
5. Add unit tests with mock/real GoBGP server (no Linux required)

### Phase 2 Detail

1. Add containerlab integration tests running GoBGP against clab switches
2. Compare route tables between FRR and GoBGP runs
3. Verify EVPN Type-5 convergence, VRF default route, and LWT data-plane
4. Benchmark startup time, memory, convergence vs FRR baseline

## Risks

- **BGP unnumbered**: GoBGP's RFC 5549 (interface peering via IPv6 LL)
  support needs verification with actual leaf switches. Mitigation:
  test in containerlab with Cumulus VX first, then on physical hardware.
- **EVPN Type-5 maturity**: FRR's EVPN Type-5 is battle-tested in DC
  fabrics. GoBGP's Type-5 support is less deployed but the route type
  itself is simpler than Type-2/3 (pure L3, no MAC learning). Mitigation:
  keep FRR as fallback through Phase 3.
- **Debugging**: Network engineers rely on `vtysh` for troubleshooting.
  Mitigation: add an HTTP debug endpoint (`/debug/bgp`) that renders
  peer state, route tables, and EVPN routes in human-readable format.
- **LWT + ip neigh**: Replacing FRR's zebra with LWT encap and static
  neighbor entries on the VXLAN device is simpler than bridge/FDB but
  requires kernel ≥ 4.10 for LWT support. Mitigation: verify on all
  target kernel versions; use `vishvananda/netlink` which supports LWT.

## Alternatives

- **Keep FRR**: Accept the 50 MB dependency and shell-out complexity.
  Reasonable if GoBGP's EVPN proves insufficient.
- **bio-routing**: Alternative Go BGP library from Google. EVPN support
  is less mature than GoBGP. No BFD support.
- **Partial migration**: Use GoBGP for BGP only, keep FRR zebra for
  VXLAN kernel programming. Reduces complexity incrementally.
- **Type-2/3 fallback**: If Type-5 with LWT proves insufficient for
  certain topologies, fall back to Type-2/3 with bridge+FDB. This is
  unlikely since BOOTy only needs L3 reachability.

## Rollback Plan

If GoBGP proves unsuitable at any phase:

- **Phase 1-2**: Simply remove the `network/gobgp/` package. FRR
  remains the default. Zero user impact.
- **Phase 3**: Set `NETWORK_MODE=frr` in machine config. FRR code
  is still present and tested. Rollback is a config change.
- **Phase 4**: Revert the Dockerfile change to re-add FRR binaries.
  Single commit revert.

## Next Steps

1. Prototype `network/gobgp/` package with `UnderlayTier` using GoBGP API
2. Verify BGP unnumbered + EVPN Type-5 in containerlab
3. Benchmark: startup time, memory, convergence time vs FRR
4. Implement `OverlayTier` with netlink VXLAN creation
5. Add integration test comparing FRR and GoBGP output
6. Test on physical Cumulus/Arista/SONiC switches
