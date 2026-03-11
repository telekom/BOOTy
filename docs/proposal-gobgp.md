# Proposal: Replace FRR with GoBGP — Three-Tier Network Stack

## Status: Proposal

## Summary

Replace the FRR (Free Range Routing) shelling approach with
[GoBGP](https://github.com/osrg/gobgp) — a pure-Go BGP library — and
restructure the network stack into three distinct tiers:

1. **Underlay** — eBGP peering with leaf switches for VXLAN reachability.
2. **Overlay** — EVPN Type-2/3 routes for provisioning VXLAN (VNI).
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
│  │  • Type-3 (IMET) for BUM flooding           ││
│  │  • Type-2 (MAC/IP) when needed              ││
│  │  • VXLAN tunnel to provisioning VNI         ││
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

### VXLAN Setup

The overlay tier creates the VXLAN interface and bridge using netlink
(replacing the current `ip link` shell-outs):

```go
func (o *OverlayTier) Setup(ctx context.Context) error {
    // Create VXLAN device
    vxlan := &netlink.Vxlan{
        LinkAttrs: netlink.LinkAttrs{Name: "vxlan" + strconv.Itoa(o.cfg.ProvisionVNI)},
        VxlanId:   o.cfg.ProvisionVNI,
        Port:      4789,
        SrcAddr:   net.ParseIP(o.cfg.UnderlayIP),
    }
    if err := netlink.LinkAdd(vxlan); err != nil { return err }
    return netlink.LinkSetUp(vxlan)
}
```

## Migration Path

| Phase | Description | Deliverable |
|-------|-------------|-------------|
| 1 | Add GoBGP as `network.Mode` alongside FRR | `network/gobgp/` package |
| 2 | Run both in CI with containerlab topology | Integration test parity |
| 3 | Default to GoBGP, deprecate FRR path | Config toggle `NETWORK_MODE` |
| 4 | Remove FRR from Dockerfile and codebase | Smaller initramfs |

## Risks

- **BGP unnumbered**: GoBGP's RFC 5549 (interface peering via IPv6 LL)
  support needs verification with actual leaf switches.
- **EVPN maturity**: FRR's EVPN is more battle-tested in DC fabrics.
  GoBGP's EVPN works but has fewer production deployments.
- **Debugging**: Network engineers rely on `vtysh` for troubleshooting.
  GoBGP's `gobgp` CLI (or a built-in HTTP debug endpoint) can substitute.
- **Kernel VXLAN**: Replacing FRR's zebra with direct netlink calls
  requires careful testing of VXLAN encap/decap.

## Alternatives

- **Keep FRR**: Accept the 50 MB dependency and shell-out complexity.
  Reasonable if GoBGP's EVPN proves insufficient.
- **bio-routing**: Alternative Go BGP library from Google. EVPN support
  is less mature than GoBGP.
- **Partial migration**: Use GoBGP for BGP only, keep FRR zebra for
  VXLAN kernel programming. Reduces complexity incrementally.

## Next Steps

1. Prototype `network/gobgp/` package with `UnderlayTier` using GoBGP API
2. Verify BGP unnumbered + EVPN Type-3 in containerlab
3. Benchmark: startup time, memory, convergence time vs FRR
4. Implement `OverlayTier` with netlink VXLAN creation
5. Add integration test comparing FRR and GoBGP output
