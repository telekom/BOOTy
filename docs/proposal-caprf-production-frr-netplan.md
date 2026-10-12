# Proposal: Production bm4x FRR and Netplan Files in CAPRF Legacy Compat

## Status: Draft (design only, no implementation yet)

## Problem

In production, CAPRF gives the legacy ramdisk an FRR configuration and a
netplan file from the bm4x-operator templates
(`t-dci-cloud/bm4x-operator`, `internal/controller/infra/redfish/provcfg/templates`,
commit 021868d). The legacy ramdisk does not parse these files. It copies
them into place (`copy_files`), runs `netplan apply`, and restarts FRR with
the file as-is (`wait_for_network`, `restart_frr` in
`internal/provision/files/common.sh`).

BOOTy parses the same files into its own `network.Config` and then runs GoBGP
or renders its own `frr.conf`. The parser handles only part of the production
files. With the production templates, rendered for one host (ASN
4210000017, VNI 3000, two uplinks), `netplan.ToNetworkConfig` currently
returns this:

| Construct in the production files | Legacy behavior | BOOTy today |
|---|---|---|
| `neighbor dcgw peer-group`, `remote-as internal`, `neighbor 10.10.10.1 peer-group dcgw` (iBGP to the DC gateway route reflectors, `l2vpn evpn` only) | FRR runs it | Not parsed: `DCGWIPs` empty, `BGPPeerMode=unnumbered`. EVPN routes have no route reflector. |
| `neighbor leaf remote-as 65500`, `local-as 65501 no-prepend replace-as` | FRR runs it | Not parsed: `LeafASN=0`, `LocalASN=0`. Unindented `neighbor` lines do not match the parser regexes (`^\s+neighbor`), except after a blank line. |
| `neighbor <nic> interface peer-group leaf` for every uplink | FRR runs it | Only the first uplink is found. NIC auto-detection hides this. |
| `route-target import/export <RT>` in `router bgp ... vrf default` | FRR runs it | Not parsed: `VPNRT` empty. BOOTy derives the RT from ASN and VNI. |
| IPv6 provisioning address on `lo` (`ethernets: lo: addresses: [<addr>/128]`), `network <addr>/128`, `network <prefix>`, `advertise ipv6 unicast` | netplan and FRR run it | `lo` is taken as the static interface: `StaticIP=<addr>/128`, `StaticIface=lo`. `ProvisionIP` stays empty. The Type-5 IPv6 prefix advertisement is not configured from the file. |
| `ipv6 route <prefix> blackhole` | FRR runs it | Not parsed |
| `bfd profile fabric` (150 ms / 150 ms) on both peer-groups | FRR runs it | Not parsed: BFD is off |
| `timers 30 90`, `timers connect 5` | FRR runs it | Not parsed: GoBGP defaults |
| `route-map TAG-FABRIC-IN` (local-pref 90, community 65169:200 additive), `DENY-TAG-FABRIC-OUT` (no re-advertisement of fabric-tagged routes), `allowas-in`, `soft-reconfiguration inbound` | FRR runs it | Not parsed. The ramdisk could re-advertise fabric routes. |
| `br.provision` bridge with `vx.<VNI>` | netplan creates it | `BridgeName` empty. Only the bridge MAC is taken. |
| `vx.<VNI>` with `link: dum.underlay` inside `Vrf_underlay` | netplan creates it | Parsed (`VXLANLink`), but CAPRF e2e notes that rc.12 does not bind the VTEP to the link. |

The CAPRF e2e suite therefore keeps separate BOOTy-only network fixtures
(`test/e2e/lib.sh`, `deploy_type` `booty` versus `old`).

## Options

### A. Parse every construct into GoBGP

Extend the netplan and FRR parsers and the GoBGP stack:

- Peer-groups with members, including `remote-as internal`.
- `local-as ... no-prepend replace-as`.
- Explicit route-targets.
- `lo` provisioning addresses.
- BFD profiles and timers.
- A route policy equivalent to the two route-maps.

Pros: one network stack. Works in the `slim` flavor without FRR.

Cons: FRR is a large config language. Every future template change needs
parser and GoBGP work, and a construct the parser does not know fails
silently. The parity risk does not end.

### B. FRR passthrough in legacy compat (proposed)

When `CAPRF_LEGACY_COMPAT=true`, the provisioner files contain
`/etc/frr/frr.conf` with `router bgp`, and the ramdisk has FRR (the `full`
flavor already has it):

1. BOOTy applies the netplan file with netlink, for the subset that the
   bm4x template uses:
   - ethernets: MTU, link-local, `eui64` address generation, `accept-ra`, addresses including `lo`
   - `vrfs` with table and members
   - `dummy-devices`
   - `tunnels` (mode `vxlan`, with `id`, `local`, `link`, `port`, `macaddress`, `hairpin`, `mac-learning`, `arp-proxy`, `port-mac-learning`)
   - `bridges` (MTU, `macaddress`, STP, members)

   Netplan keys outside this subset fail the run. They are not silently ignored.
2. BOOTy starts FRR with the provisioner `frr.conf` unchanged. It does not
   call `RenderConfig` and does not run GoBGP.
3. The connectivity wait is the legacy `wait_for_network` (#622): 20 second
   rounds, with an FRR restart between rounds unless a session is established.
4. Teardown stops FRR and removes the links that BOOTy created.

Pros: same behavior as legacy for every FRR construct, now and after
template changes. Smaller code: the netplan subset is mostly done in the FRR
mode link setup already.

Cons: needs the `full` flavor (FRR binaries). The netplan subset is a second
netplan implementation and must reject keys it does not know.

### C. Keep today's parser, document the gaps

Not proposed: production EVPN without the DCGW route reflectors does not
converge.

## Proposal

Option B, behind `CAPRF_LEGACY_COMPAT`, with auto-detection only from the
provisioner files. Without legacy compat, or without FRR in the ramdisk, BOOTy
keeps today's parser path.

Also fix the parser regexes for unindented `neighbor` lines in a separate PR.
The non-compat path then finds every uplink and the `local-as` value.

## Tests

- Unit tests:
  - The netplan subset applier, with a fake netlink layer.
  - Rejection of unknown keys.
  - The rendered bm4x templates as fixtures (golden test of the planned link operations).
- KVM `caprf-legacy-compat`:
  - Rendered bm4x netplan and FRR files with a containerlab or vrnetlab leaf.
  - The run reaches CAPRF.
  - Both EVPN fabric types: Type-2/3 plus Type-5, and Type-5 only.
- CAPRF e2e: drop the `booty` fixture branch in `test/e2e/lib.sh`. BOOTy then
  gets the same `old` netplan and FRR files. This needs a BOOTy release.

## Open questions

1. Is the `full` flavor (with FRR) acceptable as the CAPRF ramdisk, or must
   the `slim` flavor work too (option A)?
2. Do production hosts get other templates than the bm4x-operator ones
   (t-caas `bm4x-operator`, other producers)?
