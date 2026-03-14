# Proposal: VLAN Support

## Status: Implemented (PR #20)

## Priority: P1

## Summary

Add VLAN tagging support to BOOTy's network stack, enabling provisioning
traffic to traverse tagged VLANs. This is essential for data-center
deployments where provisioning, management, and production traffic are
isolated on separate VLANs via 802.1Q tagging.

## Implementation Details

VLAN support is fully implemented. Key decisions and deviations from the
original proposal:

- **Package**: `pkg/network/vlan/` with `Setup()` and `Teardown()`.
- **Config parsing**: `VLANConfig` parsed from `/deploy/vars` env vars
  (`VLAN_ID`, `VLAN_PARENT`, `VLAN_IP`, `VLAN_GATEWAY`, and multi-VLAN
  `VLANS` format) in `pkg/network/config.go`.
- **Integration**: VLAN setup runs before network mode selection in
  `main.go`. The created VLAN interface name replaces the parent in
  subsequent network operations.
- **8021q module**: Added to `loadModules()` in `main.go`.
- **CAPRF**: `InventoryURL` config plumbed through for completeness.
- **MTU handling**: Not explicitly handled — relies on network defaults.
  Jumbo frame support deferred to future work.
- **Multi-VLAN**: Compact `VLANS` format supported for multiple VLANs.

### Files Changed

| File | Change |
|------|--------|
| `pkg/network/vlan/vlan.go` | VLAN interface creation and teardown |
| `pkg/network/vlan/vlan_test.go` | Unit tests |
| `pkg/network/config.go` | `VLANConfig` type and parsing |
| `pkg/network/config_test.go` | Config parsing tests |
| `main.go` | 8021q module loading, VLAN setup integration |
| `pkg/caprf/client.go` | Minor config additions |
| `pkg/caprf/client_test.go` | Tests |
| `pkg/config/provider.go` | VLAN env var parsing |

## Motivation

Many enterprise networks use VLAN segmentation:

- **VLAN 100**: Out-of-band management (BMC/iLO/XCC)
- **VLAN 200**: Provisioning network (BOOTy ↔ CAPRF)
- **VLAN 300**: Production overlay (Kubernetes workloads)

Currently, BOOTy assumes untagged access to the provisioning network. If
the switch port is configured as a trunk with multiple VLANs, BOOTy has
no way to tag its packets — provisioning fails with "no DHCP response."

### Industry Context

| Tool | VLAN Support |
|------|-------------|
| **Ironic** | `provisioning_network` and `cleaning_network` with VLAN config; Neutron manages VLAN assignments |
| **MAAS** | Full VLAN management — tagged/untagged per interface, fabric awareness |
| **Tinkerbell** | No built-in VLAN support |

## Design

### Configuration

VLAN configuration via `/deploy/vars`:

```bash
# /deploy/vars
export VLAN_ID="200"
export VLAN_PARENT="eno1"          # physical NIC
export VLAN_IP="10.200.0.42/24"    # static IP on VLAN (optional, DHCP if empty)
export VLAN_GATEWAY="10.200.0.1"   # gateway (optional, from DHCP if empty)
```

Or multiple VLANs for multi-network setups:

```bash
export VLANS="200:eno1:10.200.0.42/24,300:eno2"
```

### Implementation

VLAN interfaces are created via netlink (already available in Go via
`vishvananda/netlink`):

```go
// pkg/network/vlan/vlan.go
package vlan

import (
    "fmt"
    "net"

    "github.com/vishvananda/netlink"
)

type Config struct {
    VlanID   int
    Parent   string
    Address  string  // CIDR, empty = use DHCP
    Gateway  string
}

func Setup(cfg Config) (string, error) {
    parent, err := netlink.LinkByName(cfg.Parent)
    if err != nil {
        return "", fmt.Errorf("parent interface %s not found: %w", cfg.Parent, err)
    }

    vlanName := fmt.Sprintf("%s.%d", cfg.Parent, cfg.VlanID)
    vlan := &netlink.Vlan{
        LinkAttrs: netlink.LinkAttrs{
            Name:        vlanName,
            ParentIndex: parent.Attrs().Index,
        },
        VlanId: cfg.VlanID,
    }

    if err := netlink.LinkAdd(vlan); err != nil {
        return "", fmt.Errorf("create VLAN %d on %s: %w", cfg.VlanID, cfg.Parent, err)
    }

    if err := netlink.LinkSetUp(vlan); err != nil {
        return "", fmt.Errorf("bring up VLAN interface: %w", err)
    }

    if cfg.Address != "" {
        addr, err := netlink.ParseAddr(cfg.Address)
        if err != nil {
            return "", fmt.Errorf("parse VLAN address: %w", err)
        }
        if err := netlink.AddrAdd(vlan, addr); err != nil {
            return "", fmt.Errorf("assign VLAN address: %w", err)
        }
    }

    if cfg.Gateway != "" {
        gw := net.ParseIP(cfg.Gateway)
        route := &netlink.Route{
            LinkIndex: vlan.Attrs().Index,
            Gw:        gw,
        }
        if err := netlink.RouteAdd(route); err != nil {
            return "", fmt.Errorf("add VLAN gateway route: %w", err)
        }
    }

    return vlanName, nil
}

func Teardown(parentName string, vlanID int) error {
    vlanName := fmt.Sprintf("%s.%d", parentName, vlanID)
    link, err := netlink.LinkByName(vlanName)
    if err != nil {
        return nil // already removed
    }
    return netlink.LinkDel(link)
}
```

### Integration with Network Modes

VLAN setup runs **before** the network mode (DHCP, static, EVPN):

```
┌────────────────────────────────────────────┐
│ Boot → loadModules → 8021q module loaded   │
│                                            │
│ VLAN Setup:                                │
│   ip link add link eno1 name eno1.200      │
│         type vlan id 200                   │
│   ip link set eno1.200 up                  │
│                                            │
│ Network Mode (operates on eno1.200):       │
│   DHCP: dhclient eno1.200                  │
│   Static: ip addr add ... dev eno1.200     │
│   EVPN: BGP peering over eno1.200          │
└────────────────────────────────────────────┘
```

The `network.Config` gains a VLAN field:

```go
// pkg/network/config.go
type Config struct {
    // ... existing fields ...
    VLANs []VLANConfig `json:"vlans,omitempty"`
}

type VLANConfig struct {
    ID      int    `json:"id"`
    Parent  string `json:"parent"`
    Address string `json:"address,omitempty"`
    Gateway string `json:"gateway,omitempty"`
}
```

### Kernel Module

The `8021q` kernel module must be loaded. Add to `loadModules()` in `main.go`:

```go
modules := []string{
    // ... existing modules ...
    "8021q",  // IEEE 802.1Q VLAN support
}
```

Verify the module is available in the initrd kernel build.

## Required Binaries in Initramfs

No additional binaries needed. VLAN creation uses the `vishvananda/netlink`
Go library (pure Go netlink). The `8021q` kernel module must be available:

| Binary | Package | Purpose | Initramfs Flavor | Already Present? |
|--------|---------|---------|-----------------|------------------|
| `ip` | `iproute2` | Fallback VLAN management (`ip link add type vlan`) | all | **Yes** |

**Kernel module** (must be in initramfs kernel):

| Module | Purpose |
|--------|---------|
| `8021q` | 802.1Q VLAN tagging support |

## Affected Files

| File | Change |
|------|--------|
| `pkg/network/vlan/vlan.go` | New — VLAN creation/teardown |
| `pkg/network/vlan/vlan_test.go` | New — unit tests |
| `pkg/network/config.go` | Add `VLANs` field |
| `main.go` | Add `8021q` to module list; call VLAN setup before network mode |
| `pkg/config/provider.go` | Parse `VLAN_ID`, `VLAN_PARENT`, etc. |
| `initrd.Dockerfile` | Ensure `8021q` module in kernel |

## Risks

- **DHCP on VLAN**: Some DHCP servers may not respond on tagged interfaces
  if the relay agent isn't configured. Test with tagged DHCP relay.
- **Module availability**: `8021q` must be compiled as a module in the initrd
  kernel. If built-in, `loadModules` will silently succeed.
- **MTU**: VLAN tagging adds 4 bytes to frame size. If the physical MTU is
  1500, the VLAN effective MTU is 1496. May need jumbo frames (9000 MTU) for
  VXLAN-over-VLAN setups.

## Effort Estimate

- VLAN setup/teardown: **2 days**
- Integration with network modes: **1-2 days**
- Kernel module verification: **1 day**
- Testing (tagged switch port): **2 days**
- Total: **6-8 days**
