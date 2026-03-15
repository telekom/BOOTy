# Proposal: BGP Networking Modes — Full Mode Matrix

## Status: Phase 1 Implemented (PR #53)

## Priority: P1

## Dependencies: [GoBGP](proposal-gobgp.md)

## Summary

Formalize and extend BOOTy's BGP networking mode matrix: document all
peering mode × VRF × address family × overlay combinations, add IPv6
underlay support, implement multi-VRF routing, add BGP community/policy
support, and provide a complete configuration reference. Covers both
GoBGP and FRR backends.

## Motivation

The existing GoBGP proposal (implemented) established three peering modes.
This companion proposal extends the networking matrix for production
datacenter deployments:

| Gap | Impact |
|-----|--------|
| No IPv6 underlay documentation | Can't deploy in IPv6 fabrics |
| No multi-VRF | Can't isolate management from data traffic |
| No BGP communities | Can't implement routing policy |
| No graceful restart | BGP sessions flap on BOOTy restart |
| Incomplete mode matrix | Operators unsure which combinations work |

### Current Mode Matrix (GoBGP)

| Mode | Underlay | Overlay | Status |
|------|---------|---------|--------|
| Unnumbered | eBGP unnumbered (link-local) | EVPN Type-2 + VXLAN | Implemented |
| Dual | eBGP numbered (loopback) | EVPN Type-2 + VXLAN | Implemented |
| Numbered | eBGP numbered (interface IP) | EVPN Type-2 + VXLAN | Implemented |

### Proposed Extended Matrix

| Mode | Underlay AF | Overlay | VRF | Backend |
|------|------------|---------|-----|---------|
| Unnumbered | IPv4 link-local | EVPN VXLAN | Default | GoBGP ✓ / FRR ✓ |
| Unnumbered | IPv6 link-local | EVPN VXLAN | Default | GoBGP / FRR |
| Numbered | IPv4 | EVPN VXLAN | Default | GoBGP ✓ / FRR ✓ |
| Numbered | IPv6 | EVPN VXLAN | Default | GoBGP / FRR |
| Numbered | Dual-stack | EVPN VXLAN | Default | GoBGP / FRR |
| Numbered | IPv4 | EVPN VXLAN | Multi-VRF | GoBGP / FRR |
| Unnumbered | IPv4 | L3VPN | Multi-VRF | GoBGP / FRR |
| Static | N/A (no BGP) | None | Default | N/A ✓ |
| DHCP | N/A (no BGP) | None | Default | N/A ✓ |
| Bond + BGP | IPv4 | EVPN VXLAN | Default | GoBGP / FRR |

## Design

### Multi-VRF Architecture

```
┌────────────────────────────────────────────────────────┐
│ BOOTy Server                                           │
│                                                        │
│  ┌──────────────────────┐  ┌────────────────────────┐ │
│  │ VRF: management      │  │ VRF: provisioning      │ │
│  │  eth0 (IPMI network) │  │  vxlan100 (EVPN)       │ │
│  │  → CAPRF, monitoring │  │  → OS image download   │ │
│  │  → BMC access        │  │  → Disk streaming      │ │
│  └──────────────────────┘  └────────────────────────┘ │
│                                                        │
│  ┌──────────────────────────────────────────────────┐ │
│  │ Default VRF                                       │ │
│  │  loopback (BGP router-id)                        │ │
│  │  → BGP underlay sessions                         │ │
│  └──────────────────────────────────────────────────┘ │
└────────────────────────────────────────────────────────┘
```

### VRF Manager

```go
// pkg/network/vrf/manager.go
//go:build linux

package vrf

import (
    "context"
    "fmt"
    "log/slog"

    "github.com/vishvananda/netlink"
)

// VRFConfig defines a VRF instance.
type VRFConfig struct {
    Name     string `json:"name"`
    TableID  int    `json:"tableId"`
    Members  []string `json:"members"` // interfaces to assign
}

// Manager handles VRF creation and interface assignment.
type Manager struct {
    log *slog.Logger
}

func New(log *slog.Logger) *Manager {
    return &Manager{log: log}
}

// Create sets up a VRF device and assigns interfaces.
func (m *Manager) Create(ctx context.Context, cfg VRFConfig) error {
    vrf := &netlink.Vrf{
        LinkAttrs: netlink.LinkAttrs{Name: cfg.Name},
        Table:     uint32(cfg.TableID),
    }

    if err := netlink.LinkAdd(vrf); err != nil {
        return fmt.Errorf("create VRF %s: %w", cfg.Name, err)
    }
    if err := netlink.LinkSetUp(vrf); err != nil {
        return fmt.Errorf("bring up VRF %s: %w", cfg.Name, err)
    }

    // Assign member interfaces
    for _, member := range cfg.Members {
        link, err := netlink.LinkByName(member)
        if err != nil {
            return fmt.Errorf("find interface %s: %w", member, err)
        }
        if err := netlink.LinkSetMasterByIndex(link, vrf.Index); err != nil {
            return fmt.Errorf("assign %s to VRF %s: %w", member, cfg.Name, err)
        }
    }

    m.log.Info("created VRF", "name", cfg.Name, "table", cfg.TableID, "members", cfg.Members)
    return nil
}
```

### IPv6 Underlay

```go
// pkg/network/gobgp/ipv6.go
//go:build linux

package gobgp

import (
    "context"
    "fmt"
    "net"
)

// configureIPv6Underlay sets up IPv6 link-local BGP peering.
func (s *Server) configureIPv6Underlay(ctx context.Context, iface string) error {
    // Get link-local IPv6 address for the interface
    addrs, err := net.InterfaceByName(iface)
    if err != nil {
        return fmt.Errorf("get interface %s: %w", iface, err)
    }

    unicast, err := addrs.Addrs()
    if err != nil {
        return fmt.Errorf("get addrs for %s: %w", iface, err)
    }

    var linkLocal net.IP
    for _, addr := range unicast {
        ip, _, _ := net.ParseCIDR(addr.String())
        if ip.IsLinkLocalUnicast() && ip.To4() == nil {
            linkLocal = ip
            break
        }
    }

    if linkLocal == nil {
        return fmt.Errorf("no IPv6 link-local address on %s", iface)
    }

    // Configure BGP peer with link-local address
    // IPv6 unnumbered uses fe80::X%interface format
    return s.addPeer(ctx, fmt.Sprintf("%s%%%s", linkLocal, iface), 0) // 0 = auto-detect ASN
}
```

### BGP Communities

```go
// pkg/network/gobgp/policy.go
package gobgp

// CommunityConfig specifies BGP community tagging.
type CommunityConfig struct {
    Standard []string `json:"standard,omitempty"` // "65000:100"
    Extended []string `json:"extended,omitempty"` // "RT:65000:100"
    Large    []string `json:"large,omitempty"`    // "65000:1:100"
}

// PolicyConfig specifies BGP route policy (import/export).
type PolicyConfig struct {
    ImportCommunities CommunityConfig `json:"importCommunities,omitempty"`
    ExportCommunities CommunityConfig `json:"exportCommunities,omitempty"`
    LocalPref         uint32          `json:"localPref,omitempty"`
    MED               uint32          `json:"med,omitempty"`
}
```

### Graceful Restart

```go
// pkg/network/gobgp/graceful_restart.go
package gobgp

import (
    api "github.com/osrg/gobgp/v3/api"
)

// EnableGracefulRestart configures BGP graceful restart.
func (s *Server) EnableGracefulRestart(ctx context.Context, restartTime uint32) error {
    // GoBGP supports graceful restart natively
    // Set restart time (default: 120 seconds)
    // Preserve forwarding state during BOOTy process restart
    return s.server.EnableGracefulRestart(ctx, &api.EnableGracefulRestartRequest{
        Time: restartTime,
    })
}
```

### Required Binaries in Initramfs

| Binary | Package | Purpose | Initramfs Flavor | Already Present? |
|--------|---------|---------|-----------------|-----------------|
| `ip` | `iproute2` | VRF creation, interface management | all | **Yes** |
| `bridge` | `iproute2` | VXLAN FDB management | full, gobgp | **Yes** |
| `sysctl` | busybox | IPv6 forwarding, VRF strict mode | all | **Yes** (busybox) |

**Kernel modules needed** (for multi-VRF):

```dockerfile
# VRF kernel module
for m in ... \
    vrf; do \
    find "$MDIR" -name "${m}.ko*" -exec cp {} /modules/ \; 2>/dev/null || true; \
done
```

**No new binaries needed** — all networking operations use existing
tools and Go (vishvananda/netlink, GoBGP).

### Configuration

```bash
# /deploy/vars — extended networking config
export NETWORK_MODE="gobgp"           # existing
export PEER_MODE="unnumbered"         # existing: "unnumbered", "dual", "numbered"
export UNDERLAY_AF="ipv4"             # "ipv4", "ipv6", "dual-stack"
export OVERLAY_TYPE="evpn-vxlan"      # "evpn-vxlan", "l3vpn", "none"

# Multi-VRF
export VRF_ENABLED="true"
export VRF_MANAGEMENT='{"name":"mgmt","tableId":100,"members":["eth0"]}'
export VRF_PROVISIONING='{"name":"prov","tableId":200,"members":["vxlan100"]}'

# BGP policy
export BGP_COMMUNITIES="65000:100"
export BGP_LOCAL_PREF="100"

# Graceful restart
export BGP_GRACEFUL_RESTART="true"
export BGP_RESTART_TIME="120"
```

## Files Changed

| File | Change |
|------|--------|
| `pkg/network/vrf/manager.go` | VRF creation and management |
| `pkg/network/gobgp/ipv6.go` | IPv6 underlay support |
| `pkg/network/gobgp/policy.go` | BGP community/policy types |
| `pkg/network/gobgp/graceful_restart.go` | Graceful restart |
| `pkg/network/gobgp/server.go` | Extended peering mode support |
| `pkg/network/mode.go` | Updated Mode interface for multi-VRF |
| `initrd.Dockerfile` | Add `vrf` kernel module |

## Testing

### Unit Tests

- `vrf/manager_test.go` — VRF creation with mock netlink (table-driven:
  single VRF, multi-VRF, invalid table ID).
- `gobgp/ipv6_test.go` — IPv6 link-local address discovery and peer
  configuration.
- `gobgp/policy_test.go` — Community string parsing and validation.

### E2E Tests

- **ContainerLab** (tag `e2e_gobgp`):
  - Existing GoBGP topologies + IPv6 underlay variant
  - Multi-VRF topology: management + provisioning VRFs
  - Verify: traffic isolation between VRFs
  - Verify: BGP communities propagated to leaf switches
- **New topology** (`e2e_gobgp_ipv6`):
  - IPv6-only fabric with unnumbered peering
  - Verify: EVPN overlay establishes over IPv6 underlay

## Risks

| Risk | Mitigation |
|------|------------|
| IPv6 link-local address instability | Use EUI-64 derived addresses |
| VRF kernel module not loaded | Auto-load via module manifest |
| Multi-VRF routing leak | Strict VRF sysctl (`net.vrf.strict_mode=1`) |
| GoBGP graceful restart not stable | Test extensively; document limitations |

## Effort Estimate

10–14 engineering days (Multi-VRF + IPv6 + communities + graceful restart +
E2E topologies).
