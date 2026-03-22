# GoBGP EVPN Configuration Guide

This guide covers configuring BOOTy's pure-Go BGP stack for EVPN/VXLAN
overlay networking in bare-metal provisioning environments.

## Overview

BOOTy's GoBGP mode replaces FRR with an in-process BGP stack using a
three-tier architecture:

```
┌──────────────────────────────────────────────────┐
│ BOOTy (PID 1 in initramfs)                       │
│                                                  │
│  ┌──────────────────────────────────────────────┐│
│  │ Tier 1: Underlay                             ││
│  │  • eBGP peering with leaf/spine switches     ││
│  │  • Advertises local VTEP /32 (router-id)     ││
│  │  • Discovers peers via IPv6 link-local NDP   ││
│  └──────────────────────────────────────────────┘│
│  ┌──────────────────────────────────────────────┐│
│  │ Tier 2: Overlay (EVPN)                       ││
│  │  • Advertises Type-5 (IP Prefix) routes      ││
│  │  • Processes Type-2/3 routes → FDB entries   ││
│  │  • VXLAN + bridge for provisioning network   ││
│  │  • Gateway VTEP route for baseline reach     ││
│  └──────────────────────────────────────────────┘│
│  ┌──────────────────────────────────────────────┐│
│  │ Tier 3: IPMI (optional, planned)             ││
│  │  • L3 path to BMC                            ││
│  └──────────────────────────────────────────────┘│
└──────────────────────────────────────────────────┘
```

## Quick Start

Add these variables to your `/deploy/vars` file:

```bash
# Required for GoBGP mode
export NETWORK_MODE="gobgp"
export BGP_PEER_MODE="unnumbered"
export underlay_ip="10.0.0.20"
export asn_server="65020"
export provision_vni="100"
export provision_ip="10.100.0.20/24"
export provision_gateway="10.0.0.1"
export dns_resolver="8.8.8.8"
```

## Configuration Reference

### Core Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `NETWORK_MODE` | Yes | — | Must be `gobgp` to activate GoBGP stack |
| `BGP_PEER_MODE` | No | `unnumbered` | Peering mode (see below) |
| `underlay_ip` | Yes | — | Local VTEP IP / BGP router-id (e.g. `10.0.0.20`) |
| `asn_server` | Yes | — | Local BGP AS number |
| `provision_vni` | Yes | — | VXLAN VNI for provisioning network |
| `provision_ip` | Yes | — | Overlay IP in CIDR notation (e.g. `10.100.0.20/24`) |
| `provision_gateway` | Yes | — | Remote VTEP IP (spine/DCGW loopback) for gateway route + BUM FDB |
| `dns_resolver` | No | — | DNS resolver IP |
| `overlay_subnet` | No | — | IPv6 overlay subnet (optional) |

### Peering Mode Variables

| Variable | Modes | Description |
|----------|-------|-------------|
| `BGP_NEIGHBORS` | `dual`, `numbered` | Comma-separated peer IPs |
| `BGP_REMOTE_ASN` | `numbered` | Remote ASN (0 = iBGP) |

## Peering Modes

### Unnumbered (Default)

Best for leaf-spine topologies with IPv6 link-local peering:

```bash
export BGP_PEER_MODE="unnumbered"
```

- Discovers peers automatically via IPv6 NDP on all physical NICs
- Carries both IPv4 unicast and L2VPN-EVPN on the same sessions
- No explicit neighbor configuration needed

### Dual

Separate underlay and overlay peering — underlay on link-local, overlay
on numbered sessions to route reflectors:

```bash
export BGP_PEER_MODE="dual"
export BGP_NEIGHBORS="10.0.0.1,10.0.0.2"
```

- Underlay: unnumbered eBGP (IPv4 unicast only)
- Overlay: numbered eBGP to `BGP_NEIGHBORS` (L2VPN-EVPN)

### Numbered

Traditional IP-based peering — requires the machine to already have
underlay connectivity (e.g. via DHCP or static IP):

```bash
export BGP_PEER_MODE="numbered"
export BGP_NEIGHBORS="10.0.0.1"
export BGP_REMOTE_ASN="65000"
```

## EVPN Data Plane

### What BOOTy Advertises

BOOTy advertises **Type-5 (IP Prefix)** routes only. These announce
the provisioning subnet reachability with VXLAN encapsulation info,
allowing the fabric to route traffic toward BOOTy's VTEP.

### What BOOTy Receives

The `watchRoutes()` function monitors GoBGP's route stream and processes
incoming EVPN routes from the fabric:

| Route Type | Action | Purpose |
|------------|--------|---------|
| **Type-2** (MAC/IP) | Installs unicast FDB entry (`MAC → VTEP`) | Learn remote MACs via control plane |
| **Type-3** (Inclusive Multicast) | Installs BUM FDB entry (`00:00:00:00:00:00 → VTEP`) | Enable BUM flooding to remote VTEPs |

Routes originating from BOOTy's own router-id are skipped.

### Gateway Connectivity

The `provision_gateway` variable is critical for VXLAN data-plane
operation. When set, BOOTy:

1. **Installs a /32 kernel route** to the gateway VTEP via the first
   physical NIC (`installGatewayRoute()`). This ensures VXLAN-encapsulated
   packets can reach the remote VTEP.

2. **Adds a BUM FDB entry** on the VXLAN device pointing to the gateway
   VTEP (`addGatewayFDB()`). This ensures ARP/broadcast frames are
   flooded to the gateway before dynamic routes arrive.

Without `provision_gateway`, the VXLAN data plane will not function —
there is no kernel route to deliver encapsulated packets to the remote VTEP.

## Network Interfaces Created

| Interface | Description |
|-----------|-------------|
| `lo` (modified) | Router-id /32 added to loopback |
| `vx<VNI>` | VXLAN device (e.g. `vx100` for VNI 100) |
| `br.provision` | Linux bridge enslaving the VXLAN device |
| `dummy-prov` | Dummy interface for overlay loopback (in VRF) |
| `vrf-prov` | VRF device for provisioning network isolation |

## Topology Example

A minimal ContainerLab topology for testing:

```
         ┌─────────────┐
         │   spine01    │
         │ AS 65000     │
         │ lo: 10.0.0.1 │
         │ VTEP gateway │
         └──┬───────┬──┘
            │       │
     eth1-2 │       │ eth3-5
            │       │
    ┌───────┴─┐  ┌──┴────────┐
    │ leaf01  │  │  BOOTy    │
    │ AS 65001│  │  AS 65020 │
    │         │  │  VTEP:    │
    │         │  │ 10.0.0.20 │
    └─────────┘  └───────────┘
```

The spine acts as both the BGP route reflector and the VXLAN gateway.
Its loopback (`10.0.0.1`) is used as `provision_gateway` by BOOTy.

## Troubleshooting

### Verify BGP Sessions

```bash
# Inside the BOOTy container/VM
gobgp neighbor          # List BGP peers and state
gobgp global rib -a l2vpn-evpn   # Show EVPN routes
```

### Verify VXLAN Data Plane

```bash
# Check VXLAN interface
ip -d link show type vxlan

# Check bridge
ip addr show dev br.provision

# Check FDB entries (should see BUM entry for gateway)
bridge fdb show dev vx100

# Check kernel route to gateway VTEP
ip route show 10.0.0.1/32

# Test overlay connectivity
ping -c 1 -I br.provision <gateway_provision_ip>
```

### Common Issues

| Symptom | Cause | Fix |
|---------|-------|-----|
| No BGP sessions | NICs not detected | Check physical NIC availability, retry timing |
| BGP up but no EVPN routes | Wrong address family | Verify `L2VPN-EVPN` family is negotiated |
| VXLAN created but no connectivity | Missing `provision_gateway` | Set `provision_gateway` to spine loopback IP |
| ARP timeout on overlay | No BUM FDB entry | Check `bridge fdb show dev vx<VNI>` for `00:00:00:00:00:00` entry |
| Packets dropped after VXLAN encap | No route to VTEP | Check `ip route show <gateway_ip>/32` exists |

## E2E Testing

GoBGP E2E tests use ContainerLab with the `e2e_gobgp` build tag:

```bash
make clab-gobgp-up && make test-e2e-gobgp
```

For full EVPN validation with real switch VMs:

```bash
make clab-gobgp-vrnetlab-up && make test-e2e-gobgp-vrnetlab
```

See `test/e2e/integration/gobgp_e2e_test.go` for the test suite, which
covers BGP peering, EVPN route exchange, VXLAN interface creation,
FDB entries, gateway routes, and overlay ping connectivity.
