# RFC compliance of the BOOTy networking stack

This document records an RFC compliance audit of the networking code of
BOOTy: the GoBGP stack (`pkg/network/gobgp`), BOOTy's own BFD
(`pkg/network/bfd`), LLDP (`pkg/network/lldp`), the Router Advertisements
of the unnumbered peering, the DHCP client mode and the VXLAN data plane
that BOOTy configures in the kernel.

| Item | Value |
|---|---|
| Audited tree | `main` at `03abb09f60556a28e4f781690a25d4b4f8466e09` |
| GoBGP | `github.com/osrg/gobgp/v3 v3.37.0` (go.mod), source read at that version; `v4.10.0` (#699) checked where it matters |
| Other libraries | `digineo/go-dhclient v1.0.2`, `google/gopacket v1.1.19`, `vishvananda/netlink v1.3.1` |
| Kernel | Linux VXLAN, bridge, neighbor and IPv6 ND/SLAAC code (not audited, owner noted) |
| Date | 2026-10-18 |

## Method

For each RFC, the normative sentences (MUST, MUST NOT, SHALL, SHOULD,
SHOULD NOT, REQUIRED, RECOMMENDED) were extracted from the RFC text and
checked against the role of BOOTy: a host-side BGP speaker (eBGP to the
leaves, iBGP to route reflectors) and a single-homed VXLAN VTEP with one
VNI. Requirements for other roles (route reflectors, multihomed PEs,
interconnect gateways, MPLS data planes) are grouped in one row per
section and marked not applicable. Requirements of the same section with
the same owner and the same result are grouped in one row; the row names
each of them.

Each row has an owner:

- **BOOTy**: code in this repository.
- **GoBGP**: the GoBGP library; checked in its source at the pinned
  version, with the file and line of GoBGP v3.37.0.
- **Kernel**: Linux, configured by BOOTy.
- **Library**: another Go dependency (named in the row).

Status values:

| Status | Meaning |
|---|---|
| COMPLIANT | The requirement is met. Evidence: a file and line at the audited tree plus a test, or a lab test. |
| DEVIATION | The requirement is not met. The row gives the impact and the fix (PR, fork branch, or proposal). |
| N/A | Not implemented and not needed for the role of BOOTy (or a requirement on a feature BOOTy does not use). |
| NOT IMPLEMENTED | Not implemented, and an implementation would be needed for full compliance. |
| GOBGP: COMPLIANT / GOBGP: DEVIATION | Owned by GoBGP; verified in the GoBGP source. |

Evidence: file references are relative to the repository root at the
audited tree, `gobgp@v3.37.0/...` to the GoBGP module. "Lab" means a
test of `test/e2e/integration` against FRR in containerlab (build tag
`e2e_prodtpl`, `e2e_gobgp` or `e2e_gobgp_type5`). New tests added by
this audit:

- `pkg/network/gobgp/rfc_wire_test.go`: `TestRFCWireType5`,
  `TestRFCWireType3`, `TestRFCWireType2` encode the EVPN routes BOOTy
  originates with GoBGP and check every field on the wire.
- `pkg/network/bfd/rfc_test.go`: `TestRFC5880PacketFields`,
  `TestRFC5881SourcePortAndTTL`.
- Fix PRs and fork branches add tests for each fixed deviation (see
  [Deviations](#deviations-ranked-by-impact)).

## Summary

Counts per RFC. GoBGP-owned rows are counted in the GoBGP columns.

| RFC | Topic | COMPLIANT | DEVIATION | N/A | NOT IMPL. | GOBGP: COMPLIANT | GOBGP: DEVIATION |
|---|---|---|---|---|---|---|---|
| 4271 | BGP-4 | 6 | 0 | 1 | 0 | 21 | 2 |
| 4760 | Multiprotocol BGP | 3 | 0 | 0 | 0 | 6 | 0 |
| 6793 | 4-octet AS | 3 | 0 | 0 | 0 | 7 | 0 |
| 7911 | ADD-PATH | 1 | 0 | 1 | 0 | 5 | 0 |
| 8950 / 5549 | IPv4 NLRI with IPv6 next hop | 3 | 0 | 1 | 0 | 2 | 2 |
| 4724 | Graceful restart | 1 | 0 | 2 | 0 | 8 | 1 |
| 7313 | Enhanced route refresh | 0 | 0 | 2 | 0 | 0 | 0 |
| 2918 | Route refresh | 1 | 0 | 0 | 0 | 3 | 0 |
| 8654 | Extended message | 0 | 0 | 1 | 0 | 1 | 0 |
| 9234 | BGP roles | 0 | 0 | 2 | 0 | 0 | 0 |
| 5492 | Capabilities | 0 | 0 | 0 | 0 | 5 | 0 |
| 7606 | UPDATE error handling | 2 | 0 | 0 | 0 | 4 | 2 |
| 5880 | BFD | 30 | 6 | 7 | 1 | 0 | 0 |
| 5881 | BFD single hop | 13 | 1 | 2 | 0 | 0 | 0 |
| 5883 | BFD multihop | 2 | 1 | 1 | 0 | 0 | 0 |
| 7432 | EVPN | 16 | 8 | 17 | 1 | 3 | 2 |
| 8365 | EVPN NVO (VXLAN) | 10 | 7 | 5 | 1 | 1 | 0 |
| 9135 | EVPN IRB | 6 | 2 | 5 | 0 | 0 | 0 |
| 9136 | EVPN IP prefix (Type-5) | 11 | 8 | 1 | 0 | 1 | 0 |
| 6514 | PMSI Tunnel attribute | 3 | 1 | 0 | 0 | 1 | 0 |
| 7209 | EVPN requirements | 3 | 0 | 6 | 1 | 0 | 0 |
| 8584 | DF election | 0 | 0 | 1 | 0 | 0 | 0 |
| 9161 | Proxy ARP/ND | 0 | 0 | 1 | 0 | 0 | 0 |
| 9014 | EVPN interconnect | 0 | 0 | 1 | 0 | 0 | 0 |
| 9251 | IGMP/MLD proxy | 1 | 0 | 1 | 0 | 0 | 1 |
| 7348 | VXLAN | 6 | 0 | 1 | 0 | 0 | 0 |
| 802.1AB | LLDP | 13 | 4 | 3 | 0 | 0 | 0 |
| 4861 | IPv6 ND (BOOTy RAs) | 8 | 4 | 2 | 0 | 0 | 0 |
| 4862 | IPv6 SLAAC | 2 | 0 | 0 | 0 | 0 | 0 |
| 2131 | DHCPv4 client | 5 | 5 | 2 | 0 | 0 | 0 |
| 8415 | DHCPv6 | 0 | 0 | 1 | 0 | 0 | 0 |
| **Total** | | **149** | **47** | **67** | **4** | **68** | **10** |

Kernel- and library-owned rows count as COMPLIANT or DEVIATION; the owner
column names them.

### Deviations ranked by impact

| Rank | ID | RFC | Owner | Impact | Fix |
|---|---|---|---|---|---|
| 1 | RFC7606-EVPN-PANIC | 7606 §5.3 | GoBGP 3.37 | High: one malformed EVPN NLRI (for example a Type-2 NLRI with length 18 to 25) from ANY BGP peer panics the GoBGP receive goroutine, also on an IPv4-only session: `ParseBGPBody` decodes the MP_REACH_NLRI before any check of the negotiated families. BOOTy is PID 1, so the kernel panics, and without `panic=` the host hangs unless the watchdog is armed. Found by a 3 s fuzz run. | Fixed in GoBGP 4.10.0 (#699); defence in depth #765 (`panic=10`: reboot instead of hang). Regression test stacked on #699: #755. Backport for 3.37.0: fork branch `MaxRink/gobgp` `fix/v3-evpn-decode-bounds` (upstream ac5a8894; 24 million fuzz inputs without a panic). |
| 2 | RFC7606-TYPED-NLRI | 7606 §5.4 | GoBGP 3.37 and 4.10 | Medium to high, fabric dependent: an EVPN route type that GoBGP does not decode (6 to 11, for example IGMP proxy SMET routes of RFC 9251 or the S-PMSI routes of RFC 9572) is handled as a malformed MP_REACH_NLRI, which disables L2VPN EVPN on the whole session (AFI/SAFI disable). BOOTy then loses every EVPN route of that peer. FRR does not originate these types; other vendors do. | Fork branch `fix/evpn-discard-unknown-route-types` (discard the NLRI, keep the rest; test `Test_MpReachNLRIDiscardsUnknownEVPNRouteType`). Applies to v4 the same way. |
| 3 | RFC7432-MACIP-WITHDRAW | 7432 §10 | BOOTy | Medium (L2 mode): the withdraw of a MAC/IP route removed the FDB entry although the MAC-only route was still advertised. FRR advertises both, so after an ARP timeout on the remote VTEP the MAC falls back to flooding. | #748 |
| 4 | RFC8365-ENCAP | 8365 §5.1.3 | BOOTy | Medium (L2 mode): Type-2 and Type-3 routes had no BGP Encapsulation extended community, so a receiver assumes MPLS. FRR ignores it; Arista, Junos and others use it. | #746 |
| 5 | RFC9136-RX (RMAC, ESI+GW, ESI, LABEL0) | 9136 §3.1, §3.2 | BOOTy | Medium to low: routes that must be treated as withdrawn or that need an overlay index BOOTy cannot resolve were installed through the BGP next hop. | #747 |
| 6 | RFC9136-GWIP | 9136 §3.2 | BOOTy | Medium to low: a Type-5 route with a gateway IP overlay index is installed through the gateway IP with the router MAC, without a recursive resolution through a Type-2 route (RFC: the router MAC is ignored, the route is not usable until the Type-2 exists). FRR fabrics send gateway IP 0 unless `advertise ipv4 unicast gateway-ip` is configured. | Open: needs a decision, because it changes a documented behaviour. Proposal: resolve through the L2-mode Type-2 table, else do not install. |
| 7 | RFC4271-COLLISION | 4271 §6.8 | GoBGP 3.37 | Low to medium: no connection collision detection; in OpenSent and OpenConfirm the new connection is always closed. Simultaneous opens converge only after a retry. | Fixed in GoBGP 4.10.0 (#699). |
| 8 | RFC4724-NEWCONN | 4724 §4.2 | GoBGP 3.37 and 4.10 | Low: a peer that restarts gracefully and connects again while BOOTy still has the old session is rejected until BOOTy's next KEEPALIVE draws an RST from the restarted peer (at most one keepalive interval: 3 s datacenter, 60 s traditional) or BFD ends the session; stale routes are kept meanwhile. | GoBGP fork (another agent). No sound BOOTy-side workaround. |
| 9 | RFC8365-RX-VNI | 8365 §5.1.3, 9136 §3.1 | BOOTy | Low to medium: the VNI in received Type-2 and Type-5 routes is ignored; BOOTy encapsulates with its own VNI. Breaks only with a fabric that uses another VNI for the same VRF or BD. | Open: proposal: reject a route with another VNI and log it (RFC 9135 §5.4 rule), after checking the "Type-5 with gateway" mode on CAPRF fabrics. |
| 10 | RFC7432-RD | 7432 §7.9 | BOOTy | Low: RD `ASN:VNI` (type 0 or 2) on every host and the same RD for the MAC-VRF and the IP-VRF; RECOMMENDED is type 1 `router-id:n`, unique per VRF. | #710 (`<router-id>:<n>` like FRR). |
| 11 | RFC7432-ESI-RX | 7432 §8.4, §9.2.2, §14, §17.3 | BOOTy | Low (L2 mode with EVPN multihoming in the fabric): a Type-2 route with a non-zero ESI is installed alone; no aliasing, no mass withdraw. | #767 (aliasing and mass withdraw through kernel FDB nexthop groups, like zebra). |
| 12 | RFC7432-MOBILITY-RX | 7432 §15 | BOOTy | Low: the FDB entry followed the last Type-2 update, not the highest MAC Mobility sequence number. | #748 |
| 13 | RFC8950-ENHE-CHECK | 8950 §4 | GoBGP 3.37 and 4.10 | Low: GoBGP sends IPv4 routes with an IPv6 next hop without checking the peer's Extended Next Hop capability. FRR peers always advertise it on interface sessions. | Fork branch `fix/enhe-require-peer-capability` (withdrawal instead; test `TestPostFilterpathExtendedNexthop`). |
| 14 | RFC5880-REMOTEDISCR, RFC5880-DEMAND | 5880 §6.8.1, §6.8.7, §6.8.18 | BOOTy | Low: stale remote discriminator kept in Down; peer Demand mode ignored. | #749 |
| 15 | RFC2131-EXPIRE | 2131 §4.4.5, §3.2 | BOOTy | Low: the address of an expired or refused lease stayed on the NIC. | #751 |
| 16 | LLDP-SHUTDOWN, LLDP-RX-ORDER | 802.1AB | BOOTy | Low: no shutdown LLDPDU; LLDPDUs with misordered mandatory TLVs accepted, LLDPDUs without End TLV dropped. | #754 |
| 17 | RFC4861-FINAL-RA | 4861 §6.2.5 | BOOTy | Low: no final RA with lifetime 0. | #752 |
| 18 | RFC9135-ARP-RX | 9135 §5.2, §6.2 | BOOTy | Low (L2 mode): the IP of a received Type-2 route is not installed as a neighbor entry; ARP resolves through flooding. | #766 (NOARP extern_learn neighbor entries, like zebra). |
| 19 | RFC9136-NH-REACH | 9136 §3.2 | BOOTy | Low: a Type-5 route is installed without a check that the VTEP is reachable; in Type-5-only mode BOOTy installs its own /32 to the VTEP. | Open, by design for a host with one uplink path. |
| 20 | RFC5880-ADMINDOWN | 5880 §6.8.16 | BOOTy | Low: at shutdown one AdminDown packet instead of AdminDown for a detection time. A lost packet makes the peer wait one detection time. | Open; see #723 (stop BGP and BFD before kexec). |
| 21 | RFC5880-CONGESTION | 5880 §7 | BOOTy | Low: no congestion control for multihop sessions (a few sessions per host). | Open. |
| 22 | RFC4861-HOST-RA | 4861 §6.2.1, §6.2.2, §6.2.4 | BOOTy | Low: BOOTy sends RAs with IPv6 forwarding off (a host), at a fixed 10 s interval, without joining all-routers. Needed for FRR BGP unnumbered; the legacy FRR ramdisk does the same. | By design, documented. |
| 23 | RFC2131-RENEW, RFC2131-ARP, RFC2131-BACKOFF | 2131 §4.1, §4.3.2, §4.4.1 | go-dhclient | Low: RENEWING requests are broadcast with server identifier and requested IP; no ARP probe and no DHCPDECLINE; fixed 1 s retransmission. Servers accept the requests. | Open (library). |
| 24 | RFC8365-PMSI-RX, RFC7432-PMSI-ID, RFC8365-ENCAP-RX | 8365 §6, §9; 7432 §11.2; 6514 §5 | BOOTy | Low: received Type-3 tunnel type not checked; the ingress replication destination is the originating router IP, not the PMSI Tunnel Identifier; the received BGP Encapsulation community is ignored, so a route without VXLAN encapsulation still gets VXLAN FDB entries. | Fix PRs for RFC7432-PMSI-ID and RFC8365-ENCAP-RX (see the PR table). |
| 25 | RFC4271-NH-RESOLVE | 4271 §9.1.2.1 | GoBGP | Low: no next hop tracking in the decision process; BOOTy's FIB writer skips unresolvable next hops instead. | Open. |
| 26 | RFC8950-ENHE-EVPN | 8950 §4 | GoBGP | Very low: the Extended Next Hop capability also lists L2VPN EVPN. | Documented in `docs/networking.md`. |
| 27 | RFC7432-MOBILITY-GOBGP | 7432 §15 | GoBGP | Very low: GoBGP withdraws its local Type-2 route on a remote higher sequence only when the ESIs differ, so not for two single-homed (ESI 0) PEs. BOOTy's MAC is its own bridge MAC and does not move. | Open. |
| 28 | LLDP-TTL | 802.1AB | BOOTy | Very low: TTL 120 instead of txTTL = 4 x 30 + 1 = 121; matches systemd-networkd. | Kept for networkd parity. |

### Open networking PRs at the time of the audit

See [Open PRs and their effect on the findings](#open-prs-and-their-effect-on-the-findings)
at the end.

## BGP

### RFC 4271: BGP-4

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 4271-1 | 4.2 | OPEN Version MUST be 4; Unsupported Version Number otherwise | GoBGP | GOBGP: COMPLIANT | `gobgp@v3.37.0/pkg/packet/bgp/validate.go:317` |
| 4271-2 | 4.2, 6.2 | Hold Time MUST be 0 or at least 3; Unacceptable Hold Time otherwise | GoBGP, BOOTy | GOBGP: COMPLIANT | receive: `validate.go:352`; send: BOOTy rejects 1 and 2 (`pkg/network/gobgp/config.go:468`, `:448`), tests `TestScenarioBGPTimers`, `TestValidateSessions` |
| 4271-3 | 4.2 | BGP Identifier MUST be an IPv4 address of the speaker, the same for every peer | BOOTy | COMPLIANT | router ID = underlay IPv4, validated in `config.go:399`; one router ID for all peers (`underlay.go:486`) |
| 4271-4 | 4.4, 10 | KEEPALIVE not more often than once per second; RECOMMENDED one third of the hold time | BOOTy, GoBGP | COMPLIANT | BOOTy rejects a keepalive above hold/3 (`config.go:472`). GoBGP keeps the configured keepalive and uses hold/3 only when the negotiated hold time is below the configured one (`gobgp@v3.37.0/pkg/server/fsm.go:1390` to `:1394`) |
| 4271-5 | 4.2 | Hold timer MUST be the smaller of the configured and received hold time | GoBGP | GOBGP: COMPLIANT | `fsm.go:1377` |
| 4271-6 | 4.3 | UPDATE: withdrawn routes, path attributes, NLRI encoding | GoBGP | GOBGP: COMPLIANT | GoBGP encoder; lab `TestProdTplLeafSessions`, `TestProdTplRRSessions` |
| 4271-7 | 4.5, 6 | NOTIFICATION codes and subcodes for each error | GoBGP | GOBGP: COMPLIANT | `validate.go:316` (OPEN), `bgp.go` MessageError codes |
| 4271-8 | 5.1.1 | ORIGIN MUST be generated by the originating speaker | BOOTy | COMPLIANT | ORIGIN IGP on the underlay route (`underlay.go:972`) and every EVPN route (`overlay.go:1699`, `:2028`, `:2092`); `TestRFCWireType5`, `TestRFCWireType3`, `TestRFCWireType2` check ORIGIN IGP on the wire |
| 4271-9 | 5.1.2 | AS_PATH: originator sends empty AS_PATH to iBGP and its AS to eBGP; prepend own AS to eBGP | GoBGP | GOBGP: COMPLIANT | BOOTy adds an empty AS_PATH (`underlay.go:987`); GoBGP prepends for eBGP (`internal/pkg/table/path.go:256`) and keeps it empty for iBGP (`path.go:279`); lab `TestProdTplLocalASReplaceASPath` |
| 4271-10 | 5.1.3 | NEXT_HOP to eBGP MUST be an address of the sending speaker on the shared link; iBGP keeps the next hop of an external route | GoBGP, BOOTy | GOBGP: COMPLIANT | next hop 0.0.0.0 for the underlay route is replaced by the session address (`path.go:248`); lab `TestGoBGPUnnumberedUnderlayRoute`, `TestProdTplRFC5549Routes` |
| 4271-11 | 5.1.4 | MED received from a neighboring AS MUST NOT be propagated to other ASes | GoBGP | GOBGP: COMPLIANT | `path.go:261` (removed for non-local paths to eBGP). BOOTy is no transit in practice |
| 4271-12 | 5.1.5 | LOCAL_PREF MUST be sent to iBGP peers and MUST NOT be sent to eBGP peers | GoBGP | GOBGP: COMPLIANT | added for iBGP (`path.go:286`), removed for eBGP (`gobgp@v3.37.0/pkg/server/server.go:821`, `postFilterpath`) |
| 4271-13 | 5.1.6, 5.1.7 | ATOMIC_AGGREGATE, AGGREGATOR | GoBGP | N/A | BOOTy does not aggregate |
| 4271-14 | 6.2 | OPEN errors: Bad Peer AS, Bad BGP Identifier (RFC 6286 rules) | GoBGP | GOBGP: COMPLIANT | `validate.go:341` to `:349`. Unnumbered peers take any peer AS (remote AS from OPEN, FRR `remote-as external` style) |
| 4271-15 | 6.3 | UPDATE errors: malformed attribute list, missing well-known attribute, invalid ORIGIN, invalid NEXT_HOP, AS loop | GoBGP | GOBGP: COMPLIANT | `validate.go:12` to `:80`, revised by RFC 7606 (see below) |
| 4271-16 | 6.5 | Hold timer expiry MUST send NOTIFICATION Hold Timer Expired | GoBGP | GOBGP: COMPLIANT | `fsm.go:1490`, `:1615` |
| 4271-17 | 6.8 | Connection collision detection MUST compare BGP Identifiers and close one connection | GoBGP | GOBGP: DEVIATION | RFC4271-COLLISION: GoBGP 3.37.0 closes every new connection in OpenSent, OpenConfirm and Established (`fsm.go:445`, `:1295`, `:1560`, `:1880`). Impact low to medium. Fixed in 4.10.0 (`resolveCollision`, #699) |
| 4271-18 | 8 | FSM: ConnectRetryTimer, IdleHoldTimer, states and events | GoBGP, BOOTy | GOBGP: COMPLIANT | connect retry and idle hold from BOOTy (`families.go:19`, idle hold 1 s like FRR `families.go:16`). #745 changes the restart after a session end |
| 4271-19 | 9 | Adj-RIB-In / Loc-RIB / Adj-RIB-Out processing | GoBGP | GOBGP: COMPLIANT | GoBGP RIB |
| 4271-20 | 9.1.2.1 | A route with an unresolvable NEXT_HOP SHOULD be excluded from the decision process | GoBGP, BOOTy | GOBGP: DEVIATION | RFC4271-NH-RESOLVE: GoBGP has no next hop tracking. BOOTy's FIB writer resolves each next hop and skips unresolvable ones (`pkg/network/gobgp/fib.go:297`), but a resolvable second-best path is not used then. Impact low |
| 4271-21 | 9.1.2.2 | Decision process tie breaking | GoBGP, BOOTy | GOBGP: COMPLIANT | GoBGP best path; BOOTy's FIB multipath uses the FRR rules (`fibselect.go:89`), tests `fibselect_test.go` |
| 4271-22 | 9.1.3 | Only the best route is advertised (without add-path send) | GoBGP | GOBGP: COMPLIANT | GoBGP default |
| 4271-23 | 9.2 | Routes with own AS in AS_PATH: loop detection | GoBGP | GOBGP: COMPLIANT | `gobgp@v3.37.0/pkg/server/peer.go:610` with `allowas-in` (`families.go:41`); lab `TestProdTplLocalASLoopCheck` |
| 4271-24 | 9.2.1.1 | MinRouteAdvertisementIntervalTimer | GoBGP | GOBGP: COMPLIANT | 0 in GoBGP and in FRR for these peers (documented in `docs/networking.md`) |
| 4271-25 | 9.3 | Routes BOOTy originates | BOOTy | COMPLIANT | underlay /32 (`underlay.go:958`), EVPN routes (`overlay.go:672`, `:720`, `:755`) |
| 4271-26 | 10 | ListenPort 179 | BOOTy | COMPLIANT | `config.go:241` |
| 4271-27 | 5 | Each attribute at most once per UPDATE | GoBGP | GOBGP: COMPLIANT | BOOTy builds one of each; GoBGP checks duplicates on receipt (`validate.go:29`) |
| 4271-28 | 8.1.1 | Optional session attributes (DelayOpen, DampPeerOscillations) | GoBGP | GOBGP: COMPLIANT | optional; not used |
| 4271-29 | 6.7 | Cease NOTIFICATION when a session is closed | GoBGP, BOOTy | COMPLIANT | GoBGP sends Cease on StopBgp/ResetPeer; BFD down resets with Cease subcode 4 (documented) |
| 4271-30 | 3 | Routes withdrawn when a session ends | GoBGP | GOBGP: COMPLIANT | GoBGP drops the peer's routes (`server.go:1574`) unless graceful restart applies |

### RFC 4760: Multiprotocol extensions

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 4760-1 | 3 | Next hop encoding MUST identify the network layer protocol (by length) | GoBGP | GOBGP: COMPLIANT | `bgp.go` `PathAttributeMpReachNLRI.DecodeFromBytes` |
| 4760-2 | 3 | Reserved octet MUST be 0, SHOULD be ignored | GoBGP | GOBGP: COMPLIANT | GoBGP encoder writes 0 |
| 4760-3 | 3 | MP_REACH_NLRI UPDATE MUST carry ORIGIN and AS_PATH, and LOCAL_PREF in iBGP | BOOTy, GoBGP | COMPLIANT | ORIGIN from BOOTy (`overlay.go:1699`), AS_PATH and LOCAL_PREF by GoBGP (`path.go:279`, `:286`) |
| 4760-4 | 3 | UPDATE with only MP NLRI SHOULD NOT carry NEXT_HOP; receiver SHOULD ignore it | BOOTy, GoBGP | COMPLIANT | EVPN paths carry only MP_REACH_NLRI (`overlay.go:1707`); `TestRFCWireType5`, `TestRFCWireType3`, `TestRFCWireType2` check that no NEXT_HOP attribute is present |
| 4760-5 | 3 | Same prefix not in more than one of NLRI, MP_REACH, MP_UNREACH | GoBGP | GOBGP: COMPLIANT | GoBGP encoder |
| 4760-6 | 7 | Incorrect MP_REACH/MP_UNREACH: delete the routes of the AFI/SAFI, SHOULD ignore further ones, SHOULD terminate | GoBGP | GOBGP: COMPLIANT | AFI/SAFI disable (`fsm.go:891`), refined by RFC 7606 §7.11 |
| 4760-7 | 8 | Capability advertisement for each AFI/SAFI; both sides MUST advertise for a bi-directional exchange | GoBGP, BOOTy | GOBGP: COMPLIANT | `fsm.go:763`; BOOTy sets the families per peer (`families.go:88` to `:132`, `:201`); lab `TestGoBGPDualEVPNOnNumberedOnly`, `TestProdTplOpenCapabilities` |
| 4760-8 | 8 | Unadvertised families not sent | GoBGP | GOBGP: COMPLIANT | `validate.go:97` rejects families not in the session |
| 4760-9 | 3 | MP_REACH next hop for EVPN: the VTEP address | BOOTy | COMPLIANT | `overlay.go:1707`; `TestRFCWireType5`, `TestRFCWireType3`, `TestRFCWireType2` |

### RFC 6793: 4-octet AS numbers

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 6793-1 | 4.1 | Speaker SHALL advertise the 4-octet AS capability with its AS | GoBGP | GOBGP: COMPLIANT | `fsm.go:765`; lab `TestProdTplOpenCapabilities` (`4byteAs`) |
| 6793-2 | 4.1 | Use the capability AS instead of My AS | GoBGP | GOBGP: COMPLIANT | `validate.go:330` |
| 6793-3 | 4.1 | 4-octet AS_PATH and AGGREGATOR between NEW speakers; no AS4_PATH/AS4_AGGREGATOR between NEW speakers; discard them if received | GoBGP | GOBGP: COMPLIANT | `internal/pkg/table/message.go:83` |
| 6793-4 | 4.2.1 | AS_TRANS in My AS for an AS above 65535 | GoBGP | GOBGP: COMPLIANT | `fsm.go:831` |
| 6793-5 | 4.2.2, 4.2.3 | OLD speaker interworking (AS4_PATH merge, confed segments excluded, malformed AS4 attributes discarded and logged) | GoBGP | GOBGP: COMPLIANT | `message.go:83` to `:212` |
| 6793-6 | 6 | Malformed AS4_PATH/AS4_AGGREGATOR from OLD speaker discarded, logged | GoBGP | GOBGP: COMPLIANT | `message.go:141` to `:164` |
| 6793-7 | 4.1 | No confed segments in AS4_PATH | GoBGP | GOBGP: COMPLIANT | `message.go:141` |
| 6793-8 | n/a | 4-octet ASN in BOOTy's RD and RT encodings (RFC 5668 4-octet AS specific type) | BOOTy | COMPLIANT | RD type 2 and RT type 0x02 for AS above 65535 (`overlay.go:1641`, `:1963`); `TestBuildRouteDistinguisher`, `TestBuildType3PathAttrs4ByteASN` |
| 6793-9 | n/a | No silent truncation of a 4-octet ASN RD/RT value | BOOTy | COMPLIANT | VNI above 65535 with 4-octet ASN is rejected (`config.go:352`) |
| 6793-10 | n/a | 4-octet local AS and remote AS for sessions | BOOTy | COMPLIANT | `families.go:31` to `:176` |

### RFC 7911: ADD-PATH

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 7911-1 | 2 | Path Identifier unique per (prefix, neighbor); re-advertiser generates its own | GoBGP | GOBGP: COMPLIANT | GoBGP send side; BOOTy does not send multiple paths |
| 7911-2 | 3 | NLRI extended with a 4-octet Path Identifier when negotiated | GoBGP | GOBGP: COMPLIANT | `bgp.go` `decodePathIdentifier` |
| 7911-3 | 4 | One ADD-PATH capability for all AFI/SAFIs; unknown Send/Receive values ignored | GoBGP | GOBGP: COMPLIANT | `fsm.go:731` |
| 7911-4 | 5 | Send multiple paths only if both sides negotiated (2/3 and 1/3) | GoBGP, BOOTy | GOBGP: COMPLIANT | BOOTy configures receive only (`families.go:195`); lab `TestProdTplOpenCapabilities` (`addPath`) |
| 7911-5 | 5 | Withdraw of an unknown Path Identifier SHOULD be ignored | GoBGP | GOBGP: COMPLIANT | GoBGP adj-in lookup by (prefix, id) |
| 7911-6 | 5 | SHOULD include the best route when advertising several | GoBGP | N/A | BOOTy does not send add-path |
| 7911-7 | n/a | Received paths installed with ECMP | BOOTy | COMPLIANT | FIB writer takes equal-cost paths of a destination (`fibselect.go:96`), `TestSelectRoutes*` |

### RFC 8950 (and RFC 5549): IPv4 NLRI with an IPv6 next hop

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 8950-1 | 3 | Receiver MUST use the next hop length to find the protocol | GoBGP | GOBGP: COMPLIANT | `bgp.go` `PathAttributeMpReachNLRI.DecodeFromBytes` (16 or 32 octets) |
| 8950-2 | 4 | Advertise the Extended Next Hop Encoding capability (code 5) | GoBGP | GOBGP: COMPLIANT | `fsm.go:803`; lab `TestProdTplRRSessionEchoAndENHE` (#710) |
| 8950-3 | 4 | MUST only advertise IPv4 NLRI with an IPv6 next hop if the peer advertised the capability for the AFI/SAFI | GoBGP | GOBGP: DEVIATION | RFC8950-ENHE-CHECK: GoBGP never checks the peer's capability (`grep` finds no use of `BGP_CAP_EXTENDED_NEXTHOP` outside the OPEN builder, 3.37.0 and 4.10.0). Impact low: FRR interface peers always advertise it. Fix: fork branch `fix/enhe-require-peer-capability` |
| 8950-4 | 4 | Capability tuples for IPv4 NLRI only | GoBGP | GOBGP: DEVIATION | RFC8950-ENHE-EVPN: GoBGP also lists L2VPN EVPN (`fsm.go:810`). FRR logs one warning per OPEN. Documented in `docs/networking.md` |
| 8950-5 | 5 | Over an IPvx session, own next hop SHOULD be IPvx | GoBGP | COMPLIANT | unnumbered sessions run over IPv6 link-local and advertise IPv6 next hops (`underlay.go:977`); numbered IPv4 sessions use IPv4 |
| 8950-6 | 5 | Next hop passed along unchanged keeps its encoding | GoBGP | N/A | BOOTy is no route reflector |
| 8950-7 | 3 | IPv4 route with an IPv6 (link-local) next hop installed in the kernel | BOOTy | COMPLIANT | `RTA_VIA` in `fib.go:286`; lab `TestProdTplRFC5549Routes`, `TestProdTplFIBRoutes` |
| 8950-8 | 3 | Link-local next hop bound to its interface | BOOTy | COMPLIANT | `fibselect.go` `fibNexthop.Dev` from the peer interface (`fib.go:270`) |

### RFC 4724: Graceful restart (helper)

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 4724-1 | 3 | Only one GR capability; receiver uses the last | GoBGP | GOBGP: COMPLIANT | `fsm.go:797`, `:1400` |
| 4724-2 | 3 | Reserved bits zero | GoBGP | GOBGP: COMPLIANT | GoBGP encoder |
| 4724-3 | 3, 4.2 | Restart State bit MUST NOT be set unless restarted | GoBGP, BOOTy | GOBGP: COMPLIANT | helper only (`families.go:188`), `restarting` false |
| 4724-4 | 4 | End-of-RIB MUST be sent after the initial update of each family (when GR is negotiated) | GoBGP | GOBGP: COMPLIANT | `server.go:1127` |
| 4724-5 | 4.1 | Restarting Speaker procedures (keep forwarding state, defer selection, selection deferral timer) | BOOTy | N/A | BOOTy is helper only and keeps no forwarding state; it sends no family tuples, so peers do not keep its routes (`families.go:184`) |
| 4724-6 | 4.2 | Receiving Speaker MUST retain the routes of a restarting peer and mark them stale | GoBGP | GOBGP: COMPLIANT | `server.go:1568`, `internal/pkg/table/adj.go:196`; lab `TestProdTplRRRestart` |
| 4724-7 | 4.2 | Previously stale routes MUST be deleted on consecutive restarts; stale routes deleted after Restart Time | GoBGP | GOBGP: COMPLIANT | GoBGP restart timer |
| 4724-8 | 4.2 | Stale routes replaced by updates; removed at End-of-RIB | GoBGP | GOBGP: COMPLIANT | GoBGP EOR handling (`server.go:1708` to `:1939`) |
| 4724-9 | 4.2 | No difference between stale and other routes in forwarding | BOOTy | COMPLIANT | FIB writer installs best paths regardless of the stale flag (`fib.go:177`) |
| 4724-10 | 4.2 | A new connection from a restarting peer MUST close the old session and keep the new one | GoBGP | GOBGP: DEVIATION | RFC4724-NEWCONN: GoBGP closes the new connection in Established (`fsm.go:1880`; also 4.10.0 `fsm.go:2107`). The old session ends when BOOTy's next KEEPALIVE draws an RST from the restarted peer (at most one keepalive interval: 3 s datacenter, 60 s traditional) or when BFD ends it; GoBGP then takes the graceful path and keeps the routes as stale meanwhile. FRR closes the old session at once without NOTIFICATION (`bgp_network.c` `bgp_accept`, NSF wait). Impact low. A BOOTy-side workaround would need log scraping and a socket destroy; the fix belongs in the GoBGP fork |
| 4724-11 | 5 | Second connection tracked until OPEN | GoBGP | GOBGP: COMPLIANT | covered by RFC4271-COLLISION for 3.37 |
| 4724-12 | n/a | RFC 8538 N bit (Notification support) | GoBGP | N/A | not in RFC scope of this audit; #744 sets the N bit like FRR |

### RFC 7313: Enhanced route refresh

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 7313-1 | 4 | SHOULD advertise the capability when supported | GoBGP | N/A | GoBGP does not support it and does not advertise it (documented in `docs/networking.md`) |
| 7313-2 | 4, 5 | BoRR/EoRR procedures, subtype checks | GoBGP | N/A | only with the capability; a peer MUST NOT send BoRR/EoRR without it |

### RFC 2918: Route refresh

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 2918-1 | 3 | Advertise the Route Refresh capability to request refreshes | GoBGP | GOBGP: COMPLIANT | `fsm.go:754`; lab `TestProdTplOpenCapabilities` (`routeRefresh`) |
| 2918-2 | 4 | On ROUTE-REFRESH, re-advertise the Adj-RIB-Out of the AFI/SAFI | GoBGP | GOBGP: COMPLIANT | `server.go:1218` |
| 2918-3 | 4 | Ignore a request for an AFI/SAFI not advertised (should) | GoBGP | GOBGP: COMPLIANT | `server.go:1227` |
| 2918-4 | 4 | Soft reconfiguration of policies without session reset | BOOTy | COMPLIANT | `routemap.go:340` soft reset after installing policies |

### RFC 8654: Extended message

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 8654-1 | 4, 5 | Capability, 65535-octet messages | GoBGP | N/A | not supported, not advertised (documented) |
| 8654-2 | 5 | MUST NOT accept or send messages above 4096 octets without the capability | GoBGP | GOBGP: COMPLIANT | GoBGP enforces 4096 |

### RFC 9234: BGP roles

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 9234-1 | 4 | Roles SHOULD be configured on eBGP sessions; capability when configured | BOOTy | N/A | data center fabric sessions, no provider/customer relation; GoBGP 3.37 has no roles |
| 9234-2 | 5 | OTC attribute procedures | GoBGP | N/A | only with roles |

### RFC 5492: Capabilities

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 5492-1 | 3 | Unknown capabilities MUST be ignored, no Unsupported Capability NOTIFICATION | GoBGP | GOBGP: COMPLIANT | unknown codes decode as `CapUnknown` |
| 5492-2 | 4 | Accept multiple instances and multiple Capabilities parameters | GoBGP | GOBGP: COMPLIANT | `bgp.go` OPEN decoder |
| 5492-3 | 4 | Capabilities in one optional parameter (SHOULD) | GoBGP | GOBGP: COMPLIANT | `fsm.go:827` |
| 5492-4 | 3 | Unsupported Capability data lists the capabilities | GoBGP | GOBGP: COMPLIANT | GoBGP does not require capabilities of peers |
| 5492-5 | 4 | No duplicate capability instances (SHOULD NOT) | GoBGP | GOBGP: COMPLIANT | `fsm.go:751` builds one of each |

### RFC 7606: Revised error handling for UPDATE messages

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 7606-1 | 2, 3 | Treat-as-withdraw instead of session reset for attribute errors | GoBGP | GOBGP: COMPLIANT | on by default (`gobgp@v3.37.0/pkg/config/oc/default.go:114`), handled in `fsm.go:914` (`handlingError`) |
| 7606-2 | 3 (c), (g) | Duplicate attributes: discard all but the first; duplicate MP_REACH/MP_UNREACH: session reset | GoBGP | GOBGP: COMPLIANT | `validate.go:29` to `:46` |
| 7606-3 | 3 (d) | Missing well-known mandatory attribute: treat-as-withdraw | GoBGP | GOBGP: COMPLIANT | `validate.go:52` to `:72` |
| 7606-4 | 7.x | Per-attribute handling (ORIGIN, AS_PATH, NEXT_HOP, MED, LOCAL_PREF, communities: treat-as-withdraw; ATOMIC_AGGREGATE, AGGREGATOR: attribute discard; MP_REACH/MP_UNREACH: AFI/SAFI disable) | GoBGP | GOBGP: COMPLIANT | `bgp.go:15278` |
| 7606-5 | 5.3 | A malformed NLRI in MP_REACH_NLRI ends in session reset or AFI/SAFI disable | GoBGP | GOBGP: DEVIATION | RFC7606-EVPN-PANIC: GoBGP 3.37.0 panics in the EVPN route type decoders for short route types 1 to 4 (`bgp.go:2768` and others; 19 cases in `TestEVPNDecodeTruncatedNLRIDoesNotPanic`, a fuzz run finds it in 3 s). `ParseBGPBody` decodes every MP_REACH_NLRI before any check of the negotiated families, so ANY BGP peer can trigger it, also on an IPv4-only session. BOOTy is PID 1: the kernel panics, and without `panic=` on the command line the host hangs unless the watchdog is armed (#765 makes it reboot). Impact high. Fixed in 4.10.0 (#699, test #755); 3.37.0 backport: fork branch `fix/v3-evpn-decode-bounds` |
| 7606-6 | 5.4 | Routes with unrecognized NLRI types of a typed family (EVPN) MUST be discarded | GoBGP | GOBGP: DEVIATION | RFC7606-TYPED-NLRI: an unknown EVPN route type returns a MessageError with AFI/SAFI disable (`bgp.go:3365`, handling 3 measured), in 3.37.0 and 4.10.0. Impact medium to high, fabric dependent. Fix: fork branch `fix/evpn-discard-unknown-route-types` |
| 7606-7 | n/a | BOOTy's own route processing does not fail on bad input | BOOTy | COMPLIANT | `processRouteUpdate` skips undecodable NLRIs and attributes (`overlay.go:828`), `TestProcessRouteUpdateGuardAndL2Branches`, `TestRouterMACInCommunitiesIgnoresUnmarshalFailures` |
| 7606-8 | 8 | Errors SHOULD be logged | GoBGP, BOOTy | COMPLIANT | GoBGP logs "treated as withdraw" (`fsm.go` `handlingError`); BOOTy logs skipped routes |

## BFD

BOOTy implements BFD itself (`pkg/network/bfd`): asynchronous mode, no
Demand mode, no Echo function, no authentication.

### RFC 5880: BFD

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 5880-1 | 4.1 | Version 1; Length; field layout | BOOTy | COMPLIANT | `packet.go:74`; `TestPacketRoundTripAndChecks`, `TestRFC5880PacketFields` |
| 5880-2 | 4.1 | Multipoint bit MUST be zero on transmit and receipt | BOOTy | COMPLIANT | never set (`packet.go:78`), discarded on receipt (`packet.go:143`) |
| 5880-3 | 4.2 to 4.4, 6.7 | Authentication sections and procedures | BOOTy | N/A | not implemented; packets with the A bit are discarded (`packet.go:149`). See 5880-39 |
| 5880-4 | 5 | Echo packets SHOULD be authenticated | BOOTy | N/A | no Echo function |
| 5880-5 | 6.1 | Active role MUST send packets without having received any; at least one side Active | BOOTy | COMPLIANT | the session loop sends from the start (`manager_linux.go:209`), `TestManagerAgainstFakeBFDD` |
| 5880-6 | 6.1 | Passive role MUST NOT send before receiving | BOOTy | N/A | BOOTy is always Active |
| 5880-7 | 6.3, 6.8.1 | Local discriminator nonzero, unique, SHOULD be random | BOOTy | COMPLIANT | `manager_linux.go:198` |
| 5880-8 | 6.4 | SHOULD advertise the lowest Required Min RX/Echo RX it can | BOOTy | COMPLIANT | configured value (profile); Echo RX 0 without echo. #710 advertises 50 ms Echo RX like FRR |
| 5880-9 | 6.5 | Poll and Final never both set; Poll on the periodic packets, no extra packets | BOOTy | COMPLIANT | `session.go:185`; `TestSessionPollFinal` |
| 5880-10 | 6.6, 6.8.7 | Demand bit only with Demand mode, both Up | BOOTy | COMPLIANT | never set (`packet.go:78`) |
| 5880-11 | 6.6 | Demand mode procedures (Poll on change) | BOOTy | N/A | BOOTy does not use Demand mode |
| 5880-12 | 6.8.1 | Session state preserved for a detection time after the last packet | BOOTy | COMPLIANT | sessions persist until Close/Replace |
| 5880-13 | 6.8.1 | SessionState and RemoteSessionState initialized Down; LocalDiag 0 | BOOTy | COMPLIANT | `session.go:52` |
| 5880-14 | 6.8.1 | RemoteDiscr initialized 0, MUST be set to 0 after a detection time without packets | BOOTy | DEVIATION | RFC5880-REMOTEDISCR: zeroed only when an Init/Up session times out (`session.go:153`); a Down session kept a stale value. Impact low. Fix #749 |
| 5880-15 | 6.8.1, 6.8.3 | DesiredMinTxInterval at least 1 s while not Up | BOOTy | COMPLIANT | `session.go:18`, `:54`, `:130`; `TestSessionPollFinal`, `TestRFC5880PacketFields` |
| 5880-16 | 6.8.1 | RemoteMinRxInterval initialized to 1 | BOOTy | COMPLIANT | `session.go:55` |
| 5880-17 | 6.8.1 | DetectMult nonzero | BOOTy | COMPLIANT | `manager_linux.go:103` |
| 5880-18 | 6.8.3 | Change of DesiredMinTx or RequiredMinRx MUST start a Poll Sequence | BOOTy | COMPLIANT | `session.go:116`; `TestSessionPollFinal` |
| 5880-19 | 6.8.3 | Increased DesiredMinTx in Up: old interval until the Poll Sequence ends | BOOTy | COMPLIANT | BOOTy only lowers it at Up (`session.go:119`) |
| 5880-20 | 6.8.3 | Reduced RequiredMinRx in Up: previous value for the detection time until the Poll Sequence ends | BOOTy | COMPLIANT | `curRx` (`session.go:41`, `:81`); `TestSessionDetectionTime` |
| 5880-21 | 6.8.3 | Reduced remote Required Min RX: new interval at once, next packet as soon as practicable | BOOTy | COMPLIANT | `rearm` (`manager_linux.go:229`); `TestManagerFastTxAfterUp` |
| 5880-22 | 6.8.3 | Echo active: RequiredMinRx at least 1 s (SHOULD) | BOOTy | N/A | no Echo |
| 5880-23 | 6.8.3 | Other timer changes take effect at once | BOOTy | COMPLIANT | `TxInterval`/`DetectionTime` computed per packet |
| 5880-24 | 6.8.4 | Detection time = remote DetectMult x max(RequiredMinRx, remote DesiredMinTx); Init/Up to Down with diag 1 | BOOTy | COMPLIANT | `session.go:137`, `:143`; `TestSessionDetectionTime` |
| 5880-25 | 6.8.4, 6.8.5 | Demand mode and Echo detection | BOOTy | N/A | not used |
| 5880-26 | 6.8.6 | Discard: version, length (24/26, payload), DetectMult 0, Multipoint, My Discriminator 0, Your Discriminator 0 in Init/Up, A bit without auth | BOOTy | COMPLIANT | `packet.go:110` to `:152`; `TestPacketRoundTripAndChecks` |
| 5880-27 | 6.8.6 | Nonzero Your Discriminator selects the session; no session: discard | BOOTy | COMPLIANT | `manager_linux.go:349`; `TestSessionIgnoresOtherDiscriminator` |
| 5880-28 | 6.8.6 | Zero Your Discriminator: select by source, interface | BOOTy | COMPLIANT | per device listener and source address (`manager_linux.go:352`) |
| 5880-29 | 6.8.6 | Store RemoteDiscr, RemoteState, RemoteDemandMode, RemoteMinRx | BOOTy | DEVIATION | RFC5880-DEMAND: the D bit (bfd.RemoteDemandMode) was not kept (`session.go:76`). Fix #749 |
| 5880-30 | 6.8.6 | Required Min Echo RX 0: echo transmission MUST cease | BOOTy | COMPLIANT | BOOTy never sends echo |
| 5880-31 | 6.8.6 | Final received during a Poll Sequence ends it | BOOTy | COMPLIANT | `session.go:81` |
| 5880-32 | 6.8.6 | State machine of section 6.2 (AdminDown, Down, Init, Up transitions, diag 3) | BOOTy | COMPLIANT | `session.go:98`; `TestSessionTransitions` |
| 5880-33 | 6.8.6, 6.8.7 | Remote Demand mode active: MUST cease periodic transmission | BOOTy | DEVIATION | RFC5880-DEMAND: BOOTy kept sending. Impact low (FRR bfdd does not use Demand). Fix #749 (`TestSessionNoPeriodicPacketsInRemoteDemandMode`) |
| 5880-34 | 6.8.7 | Interval = max(DesiredMinTx, RemoteMinRx), jitter 0 to 25 %, 10 to 25 % with DetectMult 1 | BOOTy | COMPLIANT | `session.go:159`, `:200`; `TestJitter` |
| 5880-35 | 6.8.7 | No periodic packets when RemoteMinRx is 0 | BOOTy | COMPLIANT | `session.go:160`; `TestSessionNoPacketsWhenRemoteRxIsZero` |
| 5880-36 | 6.8.7 | Poll received: Final as soon as practicable, without Poll | BOOTy | COMPLIANT | `manager_linux.go:271`, `session.go:185`; `TestSessionPollFinal` |
| 5880-37 | 6.8.7 | Packet contents: Diag, State, P, F, C (0 for a control plane implementation), A 0, D 0, M 0, DetectMult, Length, discriminators, intervals, Echo RX 0 without echo | BOOTy | COMPLIANT | `session.go:171`; `TestRFC5880PacketFields` |
| 5880-38 | 6.8.7 | SHOULD send a packet between periodic packets when the contents change | BOOTy | COMPLIANT | `manager_linux.go:271` sends at once on a state change |
| 5880-39 | 9, 6.7 | Authentication over multiple hops SHOULD be used; implementations supporting authentication MUST support both SHA1 types | BOOTy | NOT IMPLEMENTED | no authentication (like the legacy FRR profile). Needed only for multihop sessions over untrusted paths |
| 5880-40 | 6.8.15 | Forwarding plane reset: diag 4, Down | BOOTy | N/A | BOOTy keeps no independent forwarding plane |
| 5880-41 | 6.8.16 | Entering AdminDown: SHOULD send packets for a detection time | BOOTy | DEVIATION | RFC5880-ADMINDOWN: one AdminDown packet at Close (`manager_linux.go:238`). Impact low. Open; see #723 |
| 5880-42 | 6.8.18 | After a detection time without packets, RemoteMinRx SHOULD return to 1 | BOOTy | DEVIATION | RFC5880-REMOTEDISCR: the old value stayed (`session.go:143`). Fix #749 (`TestSessionForgetsRemoteAfterDetectionTime`) |
| 5880-43 | 7 | Multihop: congestion control MUST be implemented | BOOTy | DEVIATION | RFC5880-CONGESTION: none. Impact low (a few sessions per host). Open |
| 5880-44 | 9 | Single hop: TTL 255 on transmit, checked on receipt | BOOTy | COMPLIANT | see 5881-9, 5881-10 |

### RFC 5881: BFD for IPv4 and IPv6 (single hop)

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 5881-1 | 2 | Separate session per network-layer path and per protocol | BOOTy | COMPLIANT | one session per BGP neighbor and family (`gobgp/bfd.go`) |
| 5881-2 | 2 | Echo: no ingress filtering on the interface | BOOTy | COMPLIANT | BOOTy loops back peer echo packets through forwarding with rp_filter off (`docs/networking.md`); relevant with #710 |
| 5881-3 | 2, 4 | Echo transmission rules | BOOTy | N/A | BOOTy sends no echo |
| 5881-4 | 3 | Both sides Active; Your Discriminator 0 packets associated by remote system, interface and protocol | BOOTy | COMPLIANT | `manager_linux.go:346` |
| 5881-5 | 4 | Destination port 3784 | BOOTy | COMPLIANT | `manager_linux.go:27` |
| 5881-6 | 4 | Source port 49152 to 65535, one per session | BOOTy | COMPLIANT | `manager_linux.go:418`; `TestRFC5881SourcePortAndTTL` |
| 5881-7 | 4 | Source port SHOULD be unique per system | BOOTy | COMPLIANT | one bound socket per session; two sessions bound to different devices could share a port number (rare) |
| 5881-8 | 4 | Demultiplexing by the BFD mechanisms, not by port | BOOTy | COMPLIANT | by discriminator (`manager_linux.go:349`) |
| 5881-9 | 5 | All packets sent with TTL/Hop Limit 255 | BOOTy | COMPLIANT | `manager_linux.go:438`; `TestRFC5881SourcePortAndTTL`, `TestManagerSingleHopTTL` |
| 5881-10 | 5 | Received packets with TTL other than 255 discarded | BOOTy | COMPLIANT | `manager_linux.go:326`; `TestManagerSingleHopTTL` |
| 5881-11 | 6 | Packets only over the one-hop path (bound to the interface) | BOOTy | COMPLIANT | `SO_BINDTODEVICE` (`manager_linux.go:453`) |
| 5881-12 | 6 | Multiaccess: source and destination on the subnet | Kernel | COMPLIANT | kernel source selection on the bound interface |
| 5881-13 | 6 | Point-to-point links: no session identification by source address | BOOTy | N/A | the fabric links are Ethernet (multiaccess rules apply) |
| 5881-14 | 6 | Destination stays the configured address when the source changes | BOOTy | COMPLIANT | `manager_linux.go:121`; a changed link-local peer is a new session (`Replace`, `TestManagerReplace`) |
| 5881-15 | 7 | Authentication SHOULD be used | BOOTy | DEVIATION | no authentication (5880-39); the legacy FRR profile has none either. Impact low on point-to-point fabric links |
| 5881-16 | 4 | IPv6 link-local sessions carry the zone | BOOTy | COMPLIANT | `manager_linux.go:121`; lab `TestProdTplBFDSessions` |

### RFC 5883: BFD for multihop paths

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 5883-1 | 5 | Destination port 4784 | BOOTy | COMPLIANT | `manager_linux.go:28` |
| 5883-2 | 3 | Echo MUST NOT be used over multiple hops | BOOTy | COMPLIANT | no echo |
| 5883-3 | 4.3 | Unidirectional links: Active sender, Passive receiver | BOOTy | N/A | not used |
| 5883-4 | 6 | Cryptographic authentication SHOULD be used over multihop paths | BOOTy | DEVIATION | none; TTL at least 254 is checked instead (`manager_linux.go:326`, like FRR `minimum-ttl`). Impact low |

## EVPN

BOOTy is a single-homed VTEP with one VNI (`provision_vni`) and three
modes: Type-5-only, Type-5 with gateway, and L2 (Type-2, Type-3 and
Type-5). The routes it originates are built in `overlay.go` and encoded
by GoBGP; `rfc_wire_test.go` checks them on the wire. The routes it
receives come from GoBGP's best paths (`watchRoutes`, `overlay.go:801`).

### RFC 7432: BGP MPLS-based EVPN

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 7432-1 | 5 | ESI values and types (unique non-reserved ESI per ES) | BOOTy | N/A | single-homed, ESI 0 in every route (`overlay.go:1674`, `:2073`); `TestRFCWireType5`, `TestRFCWireType2` |
| 7432-2 | 6.1 | VLAN-based service: Ethernet Tag MUST be 0 | BOOTy | COMPLIANT | `overlay.go:1678`, `:2015`, `:2077`; `TestRFCWireType5`, `TestRFCWireType3`, `TestRFCWireType2` |
| 7432-3 | 6.2, 6.3 | VLAN bundle and VLAN-aware bundle services | BOOTy | N/A | one VNI per EVI |
| 7432-4 | 7 | Route types 1 to 4 encoding | GoBGP | GOBGP: COMPLIANT | GoBGP encoders; BOOTy originates types 2, 3 and 5 only |
| 7432-5 | 7, 7606 §5.4 | Unknown route types | GoBGP | GOBGP: DEVIATION | see 7606-6 (RFC7606-TYPED-NLRI) |
| 7432-6 | 7.2, 9.2.1 | MAC length 48, MAC 6 octets (802.1Q), IP 4 or 16 octets | BOOTy, GoBGP | COMPLIANT | `TestRFCWireType2` |
| 7432-7 | 7.5 | ESI Label extended community | BOOTy | N/A | multihoming only |
| 7432-8 | 7.6 | ES-Import route target; RT constraint for it | BOOTy | N/A | multihoming only |
| 7432-9 | 7.7 | MAC Mobility extended community, sequence number | GoBGP | GOBGP: COMPLIANT | GoBGP adds a sequence number one higher when BOOTy originates a MAC another PE advertised (`gobgp@v3.37.0/pkg/server/server.go:2275`); BOOTy originates without one (first advertisement) |
| 7432-10 | 7.8 | Default Gateway extended community | BOOTy | N/A | BOOTy is no default gateway of the EVI |
| 7432-11 | 7.9 | RD MUST be the RD of the MAC-VRF, assigned per MAC-VRF, unique across MAC-VRFs on a PE | BOOTy | COMPLIANT | one RD per VNI (`overlay.go:1641`); one MAC-VRF |
| 7432-12 | 7.9 | Type 1 RD RECOMMENDED | BOOTy | DEVIATION | RFC7432-RD: `ASN:VNI` (type 0, or type 2 above 65535), the same on every host and for the MAC-VRF and the IP-VRF. Route reflectors treat it as opaque, so nothing breaks. Fix #710 (`<router-id>:<n>` like FRR, separate RD for an L2VNI) |
| 7432-13 | 7.10.1 | RT Global Administrator MUST be the AS of the PE | BOOTy | N/A | the rule applies to RTs auto-derived from a VLAN ID; BOOTy's default RT is `ASN:VNI` (`overlay.go:1963`) or the configured RT (see 8365-4) |
| 7432-14 | 7.10.1 | RT auto-derivation from the VLAN ID | BOOTy | N/A | VXLAN: see 8365-4 |
| 7432-15 | 8.1 to 8.3, 8.5 | Ethernet Segment route, A-D per ES/EVI, split horizon, DF election | BOOTy | N/A | no multihoming |
| 7432-16 | 8.4, 14 | A-D per EVI route MUST NOT be used before the A-D per ES routes; aliasing and backup paths (SHOULD) | BOOTy | NOT IMPLEMENTED | RFC7432-ESI-RX: BOOTy ignores Type-1 routes. See 7432-21 |
| 7432-17 | 8.6 | Single-homing PEs SHOULD support some multihoming procedures to interoperate (receive side) | BOOTy | DEVIATION | RFC7432-ESI-RX. Impact low (L2 mode with EVPN multihoming in the fabric). Open: needs Type-1/Type-4 processing |
| 7432-18 | 9.1 | Local data plane learning MUST be supported | Kernel | COMPLIANT | bridge port learning (default); VXLAN remote learning is off (`overlay.go:506`), control plane learning instead |
| 7432-19 | 9.2.1 | RD per 7.9, MPLS Label1 downstream assigned (VNI per RFC 8365), next hop the PE, at least one RT | BOOTy | COMPLIANT | `overlay.go:2070`, `:2091`; `TestRFCWireType2`, `TestBuildType2PathAttrs` |
| 7432-20 | 9.2.2 | Reserved ESI (0 or MAX): forwarding state from the MAC/IP route alone | BOOTy | COMPLIANT | `handleType2Route` (`overlay.go:1487`); `TestHandleType2RouteInstallsFDB` |
| 7432-21 | 9.2.2 | Non-reserved ESI: forwarding state only with the A-D per ES routes | BOOTy | DEVIATION | RFC7432-ESI-RX: installed from the Type-2 route alone |
| 7432-22 | 10 | IP Address Length 32 or 128 | BOOTy | COMPLIANT | `TestRFCWireType2` |
| 7432-23 | 10 | One MAC/IP route per IP of a MAC; withdraw on dissociation | BOOTy | COMPLIANT | one IP (provision IP); routes end with the BGP session (no GR tuples, `families.go:184`) |
| 7432-24 | 10 | MAC/IP withdraw MUST NOT remove the MAC entry while the MAC-only route lives | BOOTy | DEVIATION | RFC7432-MACIP-WITHDRAW: every withdraw removed the FDB entry (`overlay.go:1533`). Impact medium in L2 mode with FRR (MAC falls back to flooding). Fix #748 (`TestHandleType2RouteBestPathPerMAC`) |
| 7432-25 | 10 | ARP proxy (SHOULD) | BOOTy | N/A | BOOTy is the end host of its BD; the kernel VXLAN `proxy` flag follows netplan |
| 7432-26 | 10.1 | Default gateway procedures | BOOTy | N/A | see 7432-10 |
| 7432-27 | 11 | Each PE MUST advertise an IMET route | BOOTy | COMPLIANT | L2 mode (`overlay.go:720`); `TestAdvertiseType3PublishesIMETWithIngressReplication`, `TestOverlaySetupAdvertisesType2And3OnlyWithL2`. Type-5 modes have no BD to flood |
| 7432-28 | 11.1 | RD per 7.9; originating router IP common to all EVIs; next hop the PE; RTs per 7.10 | BOOTy | COMPLIANT | `overlay.go:2012`, `:2027`; `TestRFCWireType3` |
| 7432-29 | 11.2 | PMSI Tunnel attribute MUST be present | BOOTy | COMPLIANT | `overlay.go:2054`; `TestRFCWireType3` |
| 7432-30 | 11.2 | Ingress replication: tunnel type IR, tunnel identifier a routable address of the PE, downstream assigned label (the VNI) | BOOTy | COMPLIANT | `TestRFCWireType3` |
| 7432-31 | 11.2 | Leaf Information Required MUST be 0 and MUST be ignored on receipt | BOOTy | COMPLIANT | flags 0 (`overlay.go:2055`); BOOTy does not read the PMSI flags (`overlay.go:1554`) |
| 7432-32 | 11.2 | P-multicast trees, aggregation | BOOTy | N/A | ingress replication only |
| 7432-33 | 12, 13.1 | BUM traffic replicated to each remote PE (ingress replication) | Kernel | COMPLIANT | BUM FDB entries per Type-3 VTEP (`overlay.go:1575`) and to the gateway (`overlay.go:613`); `TestHandleType3RouteAppendsBUM`, lab `TestGoBGPUnnumberedGatewayFDB` |
| 7432-46 | 11.2 | Ingress replication: BUM traffic goes to the routable address in the PMSI Tunnel Identifier | BOOTy | DEVIATION | RFC7432-PMSI-ID: BOOTy floods to the originating router IP of the IMET route (`overlay.go:1555`) and ignores the PMSI attribute. FRR 10.7.1 does the same (zebra gets the IMET originator as remote VTEP, `bgp_zebra_send_remote_vtep`), and in VXLAN fabrics both addresses are the VTEP. Kept like FRR; documented choice |
| 7432-47 | 11.2 | Use the PMSI tunnel type of the IMET route | BOOTy | DEVIATION | RFC8365-PMSI-RX (8365-15): BOOTy adds a head-end replication entry for every IMET route. FRR installs one only for ingress replication (PIM-SM aside) and disables flooding for other or missing tunnel types (`evpn_zebra_install`). Fix PR: like FRR |
| 7432-34 | 12, 13 | ESI label, P2MP LSP and split horizon procedures | BOOTy | N/A | VXLAN, single-homed |
| 7432-35 | 14.1 | Load balancing through A-D routes | BOOTy | N/A | see 7432-16 |
| 7432-36 | 15 | First advertisement without MAC Mobility community | BOOTy | COMPLIANT | `overlay.go:2091` |
| 7432-37 | 15 | A moved MAC: sequence one higher than the received one | GoBGP | GOBGP: COMPLIANT | `server.go:2187`; BOOTy's MAC is its own bridge MAC |
| 7432-38 | 15 | Receiver: the higher sequence number wins; equal: the lowest PE IP | BOOTy | DEVIATION | RFC7432-MOBILITY-RX: the last update won (`overlay.go:1543`). Impact low. Fix #748 |
| 7432-39 | 15 | Sequence number wraparound MUST be handled | BOOTy | DEVIATION | RFC7432-MOBILITY-RX: the sequence number was not used at all. Fix #748 (RFC 1982 serial arithmetic, `TestHandleType2RouteBestPathPerMAC`) |
| 7432-40 | 15 | "A PE receiving a MAC/IP Advertisement route for a MAC address with a different Ethernet segment identifier and a higher sequence number than that which it had previously advertised withdraws its MAC/IP Advertisement route." | GoBGP | GOBGP: DEVIATION | RFC7432-MOBILITY-GOBGP, an interpretation: GoBGP (`internal/pkg/table/table_manager.go:238`) applies it only when the ESI values differ, so two single-homed PEs (both ESI 0) never trigger it. The same section says that for single-homed sites "there is no need for ESI comparison", so reading "different Ethernet segment" as "different ESI value" is GoBGP's choice. Impact very low (BOOTy's MAC is its own bridge MAC and does not move) |
| 7432-41 | 15.1 | Duplicate MAC detection (M, N configurable), alert | BOOTy | N/A | BOOTy learns no MACs locally |
| 7432-42 | 15.2 | Sticky MAC: alert on local learning | BOOTy | N/A | BOOTy learns no MACs locally and sends no sticky MACs |
| 7432-43 | 17.3 | Withdraw on MAC age out and on A-D withdrawal (mass withdraw SHOULD) | BOOTy | N/A | static local MAC; no Type-1 processing (7432-16) |
| 7432-44 | 18 | Control word | BOOTy | N/A | MPLS only |
| 7432-45 | 9.2.2, 10 | Withdraw without next hop still removes the forwarding state | BOOTy | COMPLIANT | stored VTEP (`overlay.go:1496`); `TestHandleType2RouteWithdraw`; Type-5: `TestHandleType5RouteWithdrawUsesStoredRouterMAC` |

### RFC 8365: EVPN network virtualization overlay (VXLAN)

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 8365-1 | 5.1.1 | Globally unique VNIs in a DC | BOOTy | COMPLIANT | one configured VNI, range 1 to 16777215 checked (`config.go:344`) |
| 8365-2 | 5.1.2 | Option 1: one BD per EVI, RD and RT per VNI | BOOTy | COMPLIANT | `overlay.go:1641`, `:1963` |
| 8365-3 | 5.1.2 | Option 2: VLAN-aware bundle | BOOTy | N/A | |
| 8365-4 | 5.1.2.1 | RT auto-derivation (A bit, type, D-ID, VNI) | BOOTy | COMPLIANT | auto-derivation is optional ("can be"); BOOTy uses `ASN:VNI` like FRR. Both ends must use the same rule |
| 8365-5 | 5.1.3 | VLAN-based service: Ethernet Tag MUST be 0 in MAC/IP, A-D per EVI and IMET routes | BOOTy | COMPLIANT | `TestRFCWireType2`, `TestRFCWireType3` |
| 8365-6 | 5.1.3 | VLAN-aware bundle Ethernet Tag rules | BOOTy | N/A | |
| 8365-7 | 5.1.3 | VNI carried in the MPLS label field (24 bits) | GoBGP, BOOTy | GOBGP: COMPLIANT | `gobgp@v3.37.0/pkg/packet/bgp/bgp.go:2641` (no shift); `TestRFCWireType5`, `TestRFCWireType3`, `TestRFCWireType2` |
| 8365-8 | 5.1.3 | BGP Encapsulation extended community with all EVPN routes | BOOTy | DEVIATION | RFC8365-ENCAP: only on Type-5 (`overlay.go:1724`), not on Type-2 and Type-3 (`overlay.go:2027`, `:2091`). Impact medium in L2 mode with receivers that check it. Fix #746 (`TestEVPNRoutesCarryVXLANEncapsulation`) |
| 8365-9 | 5.1.3 | Next hop MUST be the IPv4 or IPv6 address of the NVE | BOOTy | COMPLIANT | router ID (VTEP) in every route; `TestRFCWireType*` |
| 8365-10 | 5.1.3, 6 | Receiver uses the VNI of the route (and the advertised encapsulation) | BOOTy | DEVIATION | RFC8365-RX-VNI: the label of received Type-2 and Type-5 routes is ignored; the FDB entries use the VXLAN device VNI (`overlay.go:1455`, `:1524`). Impact only with different VNIs for the same BD/VRF. Open |
| 8365-11 | 6 | Advertised encapsulations use the same EVPN procedures; never VXLAN and MPLS together | BOOTy | COMPLIANT | VXLAN only |
| 8365-19 | 6 | An ingress NVE sends to an egress NVE only with an encapsulation that both support; without the BGP Encapsulation community the receiver assumes MPLS or a configured encapsulation | BOOTy | DEVIATION | RFC8365-ENCAP-RX: BOOTy ignores the received community, so a route that advertises only another encapsulation (for example MPLS) still gets VXLAN state. FRR 10.7.1 ignores it too (it only keeps the community, `bgp_evpn.c` `bgp_evpn_filter_ecommunity`) and treats a route without it as VXLAN. Fix PR: drop a route whose encapsulation communities do not include VXLAN; keep FRR's handling of a route without the community |
| 8365-12 | 5.2 | MPLS over GRE | BOOTy | N/A | |
| 8365-13 | 8 | Multihoming, local bias, split horizon | BOOTy | N/A | single-homed |
| 8365-14 | 9 | Each PE MUST advertise an IMET route for its tunnel type (ingress replication) | BOOTy | COMPLIANT | L2 mode; see 7432-27 |
| 8365-15 | 9 | Receiver uses the PMSI tunnel type of the IMET route | BOOTy | DEVIATION | RFC8365-PMSI-RX: any Type-3 adds a unicast flood entry, also for multicast tunnel types (`overlay.go:1554`). Impact low (all-VXLAN fabrics use ingress replication). Open |
| 8365-20 | 7 | Single-homing NVE in a hypervisor (BOOTy's role): no multihoming procedures needed | BOOTy | COMPLIANT | BOOTy originates no Type-1 or Type-4 routes and uses ESI 0 (`TestRFCWireType2`) |
| 8365-21 | 7.1 | RD unique per EVI or per NVE, recommended at all times | BOOTy | DEVIATION | see 7432-12 (RFC7432-RD): `ASN:VNI` is the same on every host. Fix #710 |
| 8365-22 | 7.1 | Routes and attributes of a hypervisor NVE: MAC/IP, IMET, MAC Mobility, Default Gateway | BOOTy | COMPLIANT | MAC/IP and IMET in L2 mode (`TestRFCWireType2`, `TestRFCWireType3`); MAC Mobility by GoBGP (7432-37); Default Gateway not needed (7432-10) |
| 8365-23 | 7.1, 7.2 | A single-homing ingress NVE should receive and process A-D per ES and per EVI routes (fast convergence, aliasing, backup path, RFC 7432 sections 8.2 and 8.4) | BOOTy | NOT IMPLEMENTED | RFC7432-ESI-RX at the audited tree; implemented by #767 (aliasing and mass withdraw through kernel FDB nexthop groups, like zebra) |
| 8365-24 | 7.2 | Procedures 1 to 5: local learning, advertising local MACs, remote learning per RFC 7432 section 9.2, IMET discovery, MAC mobility per section 15 | BOOTy | DEVIATION | 1 and 2: the bridge MAC is the only local MAC and is advertised (`TestAdvertiseType2PublishesBridgeMACAndIP`); 3: see 7432-24 (#748); 4: COMPLIANT (7432-27, 7432-33); 5: see 7432-38 (#748) |
| 8365-16 | 10.2.2 | ASBR functionality with multihoming NVEs (A-D withdrawal handling at an ASBR) | BOOTy | N/A | BOOTy is no ASBR; the receive side of A-D routes is 7432-16 |
| 8365-17 | 5.1.3 | Route keys and withdraw processing for VXLAN routes | BOOTy | DEVIATION | Type-5 key per RFC 9136 (`overlay.go:1061`) is right; Type-2 routes were tracked by MAC only (`overlay.go:1487`), see RFC7432-MACIP-WITHDRAW. Fix #748 |
| 8365-18 | 5.1.3, RFC 9012 | Tunnel type 8 (VXLAN) in the encapsulation community | BOOTy | COMPLIANT | `overlay.go:2002`; `TestRFCWireType5` |

### RFC 9135: Integrated routing and bridging in EVPN

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 9135-1 | 4 | Asymmetric IRB: Ethernet NVO tunnel | BOOTy | COMPLIANT | VXLAN with inner Ethernet |
| 9135-2 | 4 | Asymmetric IRB: ARP entries for remote hosts, BTs for all subnets | BOOTy, Kernel | COMPLIANT | one subnet; the kernel resolves remote hosts by ARP through the BUM entries |
| 9135-3 | 4.1 | IRB/anycast MAC rules | BOOTy | N/A | BOOTy is no IRB gateway; its bridge MAC is per host (`docs/networking.md`, Router MAC) |
| 9135-4 | 5.1, 5.3, 5.4 | Symmetric IRB origination (two RTs, Label2, subnet RT-5) | BOOTy | N/A | BOOTy originates asymmetric MAC/IP routes (L2) and RT-5 host routes |
| 9135-5 | 5.2, 6.2 | Asymmetric-only receiver: ignore Label2, install the MAC in the MAC-VRF | BOOTy | COMPLIANT | `overlay.go:1487` uses only the MAC |
| 9135-6 | 5.2, 6.2 | Asymmetric-only receiver: install the (IP, MAC) association in the ARP/ND table | BOOTy | DEVIATION | RFC9135-ARP-RX: not installed; ARP resolves by flooding. Impact low. Open: install `extern_learn` neighbor entries like zebra |
| 9135-7 | 6.1 | Asymmetric MAC/IP route: length 37/49, Label2 not included, MAC-VRF RT, set per RFC 7432 and RFC 8365 (which adds the BGP Encapsulation community) | BOOTy | DEVIATION | length, labels and RT are right (`TestRFCWireType2`); the encapsulation community was missing, see 8365-8 (RFC8365-ENCAP). Fix #746 |
| 9135-8 | 8.1 | Advertiser attaches a single Router's MAC community | BOOTy | COMPLIANT | `overlay.go:1730`; `TestRFCWireType5` |
| 9135-9 | 8.1 | Receiver processes the first Router's MAC community | BOOTy | COMPLIANT | `overlay.go:1892` returns the first |
| 9135-10 | 9.1.1 | Router's MAC community MUST be added when the Ethernet NVO tunnel is used (symmetric IRB RT-2) | BOOTy | N/A | BOOTy originates asymmetric RT-2 only; the RT-5 rule is 9136-17 |
| 9135-11 | 9.1.1 | Malformed RT-2 label and RT combinations of a symmetric IRB receiver MUST be treated as withdrawn | BOOTy | N/A | BOOTy is an asymmetric-only receiver: section 6.2 tells it to ignore Label2 (9135-5) |
| 9135-12 | 5.4 | Label2 VNI mismatch | BOOTy | N/A | symmetric only |
| 9135-13 | 4 | Inter-subnet traffic through the gateway | BOOTy | COMPLIANT | provision gateway route and BUM entry (`overlay.go:602`); lab `TestGoBGPUnnumberedGatewayRoute`, `TestGoBGPUnnumberedVXLANPingGateway` |

### RFC 9136: IP prefix advertisement in EVPN (Type-5)

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 9136-1 | 3 | NVEs of a tenant on different BDs MUST support RT-5 | BOOTy | COMPLIANT | every mode originates and receives RT-5 |
| 9136-2 | 3.1 | Length 34 or 58; prefix and gateway of the same family | BOOTy, GoBGP | COMPLIANT | `TestRFCWireType5`; IPv6 prefixes get gateway `::` (`l3vni.go:134`) |
| 9136-3 | 3.1 | RD and Ethernet Tag per RFC 7432/8365 (RD unique per VRF) | BOOTy | DEVIATION | see 7432-12 (fixed by #710) |
| 9136-4 | 3.1 | ESI MUST be zero when it is no overlay index | BOOTy | COMPLIANT | `TestRFCWireType5` |
| 9136-5 | 3.1 | GW IP MUST be zero when it is no overlay index | BOOTy | COMPLIANT | `overlay.go:2004`, `l3vni.go:134`; `TestRFCWireType5` |
| 9136-6 | 3.1 | Prefix length at most 32/128 | BOOTy | COMPLIANT | originated from parsed prefixes; received lengths above are skipped (`overlay.go:1601`) |
| 9136-7 | 3.1 | Label: the VNI; SHOULD be 0 with recursive resolution | BOOTy | COMPLIANT | label = VNI (interface-less model, no overlay index); `TestRFCWireType5` |
| 9136-8 | 3.1 | Received label 0: MUST contain an overlay index and be resolved, else treat-as-withdraw | BOOTy | DEVIATION | RFC9136-LABEL0: installed through the next hop. Fix #747 |
| 9136-9 | 3.1 | Route key: RD, Ethernet Tag, prefix length, prefix | BOOTy | COMPLIANT | `overlay.go:1061`; `TestHandleType5RouteKeepsRouteWhileAnotherGatewayAdvertisesIt` |
| 9136-10 | 3.2 | ESI overlay index: usable only after an RT-1 resolves it | BOOTy | DEVIATION | RFC9136-ESI: installed through the next hop. Fix #747 (not usable, BOOTy has no RT-1 resolution) |
| 9136-11 | 3.2 | GW IP overlay index: usable only after an RT-2 with that IP resolves it; the Router's MAC is ignored | BOOTy | DEVIATION | RFC9136-GWIP: installed on-link through the gateway with the Router's MAC (`overlay.go:1101`, `:1193`). Impact medium to low. Open, needs a decision |
| 9136-12 | 3.2 | MAC overlay index (label 0 with Router's MAC) | BOOTy | N/A | optional ("support ... is OPTIONAL"); with #747 such routes are not used |
| 9136-13 | 3.2 | No IGP/BGP route to the BGP next hop: MUST NOT install | BOOTy | DEVIATION | RFC9136-NH-REACH: not checked; Type-5-only mode installs a /32 to the VTEP itself (`overlay.go:1175`). Impact low, by design. Open |
| 9136-14 | 3.2 | ESI and GW IP both non-zero: SHOULD treat as withdraw | BOOTy | DEVIATION | RFC9136-ESIGW. Fix #747 |
| 9136-15 | 3.2 | All of ESI, GW IP, MAC and label zero: SHOULD treat as withdraw | BOOTy | DEVIATION | RFC9136-LABEL0: installed through the next hop. Fix #747 (label 0 without GW IP is treated as withdrawn) |
| 9136-16 | 3.2 | Invalid Router's MAC (multicast, broadcast, malformed): MUST treat as withdraw | BOOTy | DEVIATION | RFC9136-RMAC: `parseRouterMAC` checked only the length (`overlay.go:1750`), so a multicast or broadcast router MAC was installed as the gateway neighbor and FDB entry; a malformed one installed the route without neighbor (`overlay.go:858`). Fix #747 (withdraw for a multicast or broadcast router MAC also with a gateway IP, like FRR; a zero router MAC counts as absent, like FRR) |
| 9136-17 | 4.4.1 | Interface-less IP-VRF-to-IP-VRF: GW IP 0, ESI 0, Router's MAC, label = L3 VNI | BOOTy | COMPLIANT | `TestRFCWireType5`; lab `TestProdTplType5Origination`, `TestType5RouteHasEncapAndRouterMAC` |
| 9136-18 | 4.4.1 | Ingress: inner destination MAC = Router's MAC, VNI of the route | BOOTy, Kernel | COMPLIANT | neighbor gateway -> Router's MAC and FDB Router's MAC -> VTEP (`overlay.go:1193`); lab `TestType5VM0ImportsVM1`, `TestType5RemoteVMDataPlaneMatchers`. VNI: see 8365-10 |
| 9136-19 | 3 | Withdraw removes the route; another path of the prefix takes over | BOOTy | COMPLIANT | `overlay.go:978`; `TestHandleType5RouteWithdrawDeletesOnlinkGatewayRoute`, `TestHandleType5RouteKeepsRouteWhileAnotherGatewayAdvertisesIt`, `TestHandleType5RouteFallsBackToPathWhoseInstallFailed` |
| 9136-20 | 3 | Own routes reflected back are not installed | BOOTy | COMPLIANT | `overlay.go:895`; `TestHandleType5RouteSelfRoute` |
| 9136-21 | 3.1 | GoBGP decodes the GW IP and prefix by NLRI length | GoBGP | GOBGP: COMPLIANT | `bgp.go:3127`, `:3165` |

### RFC 6514: PMSI Tunnel attribute (as used by RFC 7432)

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 6514-1 | 5 | Attribute layout: flags, tunnel type, label, tunnel identifier | GoBGP | GOBGP: COMPLIANT | `bgp.go:14153`; `TestRFCWireType3` |
| 6514-2 | 5 | Ingress replication: tunnel identifier = unicast address of the originator | BOOTy | COMPLIANT | router ID (`overlay.go:2058`); `TestRFCWireType3` |
| 6514-3 | 5 | Leaf Information Required 0 for ingress replication | BOOTy | COMPLIANT | `TestRFCWireType3` |
| 6514-4 | 5 | Label field carries the VNI for VXLAN (RFC 8365 §5.1.3) | BOOTy | COMPLIANT | `TestRFCWireType3` |
| 6514-5 | 5 | Receiver: the Tunnel Identifier names the P-tunnel | BOOTy | DEVIATION | see 7432-46: BOOTy uses the originating router IP, like FRR |

### RFC 7209: Requirements for EVPN

RFC 7209 states requirements on a solution, not on one implementation.
The rows map them to what BOOTy implements.

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 7209-1 | 4 (R1 to R6) | All-active and single-active multihoming, load balancing | BOOTy | N/A | single-homed host VTEP |
| 7209-2 | 5 (R7) | No MAC learning against MP2MP LSPs | BOOTy | N/A | VXLAN |
| 7209-3 | 6 (R8a) | Auto-discovery of member PEs | BOOTy | COMPLIANT | Type-3 IMET in L2 mode |
| 7209-4 | 6 (R8b to R8e) | Redundancy group discovery, DF election, VLAN aware auto-provisioning | BOOTy | N/A | |
| 7209-5 | 6 (R8f) | Default values for unconfigured parameters | BOOTy | COMPLIANT | `config.go:240`, FRR profile defaults |
| 7209-6 | 7 (R9a) | Port, VLAN and VLAN bundle services | BOOTy | NOT IMPLEMENTED | VLAN-based service only; enough for the provisioning network |
| 7209-7 | 8 (R10) | Convergence independent of the number of MACs | BOOTy | N/A | one MAC per host |
| 7209-8 | 9 (R11) | Flooding control | BOOTy | COMPLIANT | VXLAN learning off, control plane FDB; BUM only to Type-3 VTEPs or the gateway |
| 7209-9 | 10 (R12) | Flexible topologies, E-Tree, inter-AS | BOOTy | N/A | |
| 7209-10 | 11 (R13, R14) | Duplicate MAC detection, sticky MAC | BOOTy | N/A | see 7432-41, 7432-42 |

### RFC 8584: DF election framework

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 8584-1 | all | DF election FSM, DF Election extended community, HRW | BOOTy | N/A | no multihoming: BOOTy originates no ES routes and processes none |

### RFC 9161: Proxy ARP/ND in EVPN

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 9161-1 | 3 | Proxy ARP/ND function (learning, reply, maintenance, duplicate IP detection) | BOOTy | N/A | a PE function for attached CEs; BOOTy is the end host. The kernel VXLAN `proxy` flag is only set from netplan (`devices.go:44`) and has no EVPN-learned entries (see 9135-6) |

### RFC 9014: Interconnect solution for EVPN overlay networks

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 9014-1 | all | Gateway procedures (I-ESI, DF, separate sessions, VNI translation) | BOOTy | N/A | BOOTy is no DCI gateway |

### RFC 9251: IGMP and MLD proxies for EVPN

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 9251-1 | 4 to 9 | SMET and synch routes, proxy querier | BOOTy | N/A | no multicast group handling |
| 9251-2 | 9.2.1 | A PE without support MUST NOT indicate IGMP proxy support | BOOTy | COMPLIANT | BOOTy sends no Multicast Flags extended community |
| 9251-3 | 9.1 | Receiver handling of SMET routes from other PEs | GoBGP | GOBGP: DEVIATION | a received Type-6 route disables EVPN on the session; see 7606-6 |

### RFC 7348: VXLAN (data plane in the kernel)

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 7348-1 | 5 | UDP destination port 4789 | BOOTy, Kernel | COMPLIANT | `overlay.go:32`, `:505`; lab `TestGoBGPUnnumberedVXLANInterface` |
| 7348-2 | 5 | 24-bit VNI, I flag set, reserved bits zero | Kernel | COMPLIANT | Linux vxlan driver |
| 7348-3 | 5 | Outer UDP source port from a hash of the inner frame (SHOULD); UDP checksum 0 for IPv4 (SHOULD) | Kernel | COMPLIANT | kernel defaults (source port range, `udpcsum` off) |
| 7348-4 | 4.3 | VTEPs MUST NOT fragment VXLAN packets | BOOTy, Kernel | COMPLIANT | inner MTU = NIC MTU - 50 (`overlay.go:515`), so outer packets fit the NIC; lab `TestType5JumboMTU` |
| 7348-5 | 4.1 | Remote learning by the data plane | BOOTy | COMPLIANT | off on purpose (`overlay.go:506`); EVPN control plane instead (RFC 8365) |
| 7348-6 | 4.2 | BUM to a multicast group or by head-end replication | Kernel | COMPLIANT | head-end replication through the all-zero FDB entries |
| 7348-7 | 6 | IPv6 outer header | BOOTy | N/A | IPv4 VTEP only (`config.go:403`) |

## LLDP: IEEE 802.1AB

BOOTy sends LLDP like systemd-networkd `EmitLLDP=yes` (`tx.go`) and
receives it on every physical NIC (`receive.go`). Clause references
are to IEEE 802.1AB-2016; the standard is not freely available, so each
row states the rule it checks.

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| LLDP-1 | 7.1, 7.5 | Destination nearest bridge 01-80-C2-00-00-0E, EtherType 88-CC | BOOTy | COMPLIANT | `tx.go:34`, `:121`; `TestFrameLikeNetworkd`, lab `TestProdTplLLDPLikeNetworkd` |
| LLDP-2 | 8.2 | Mandatory TLVs Chassis ID, Port ID, TTL first, in this order | BOOTy, Library | COMPLIANT | gopacket encoder (`gopacket@v1.1.19/layers/lldp.go:769`); `TestFrameLikeNetworkd` |
| LLDP-3 | 8.5.2 | Chassis ID subtype and value (locally assigned machine ID, or MAC) | BOOTy | COMPLIANT | `tx.go:107`; `TestFrameLikeNetworkd`, `TestMachineIDPerBoot` |
| LLDP-4 | 8.5.3 | Port ID subtype interface name | BOOTy | COMPLIANT | `tx.go:124` |
| LLDP-5 | 8.5.4, 9.2.5 | TTL = txTTL = msgTxInterval x msgTxHold + 1 (since 802.1AB-2009) | BOOTy | DEVIATION | LLDP-TTL: 120 instead of 121, same as networkd (`tx.go:30`). Impact very low. Kept for parity |
| LLDP-6 | 8.5.8 | System capabilities: enabled a subset of supported | BOOTy | COMPLIANT | supported station/bridge/router, enabled station (`tx.go:43`); `TestFrameLikeNetworkd` |
| LLDP-7 | 8.4 | Optional TLVs: system name and description within 255 octets | BOOTy | COMPLIANT | `tx.go:134` |
| LLDP-8 | 9.2.5 | msgTxInterval 30 s, fast start (txFastInit 4 frames at msgFastTx 1 s) | BOOTy | COMPLIANT | `tx.go:27` to `:30`, `tx.go:155` |
| LLDP-9 | 9.2.7 | Shutdown LLDPDU (TTL 0, mandatory TLVs) when transmission stops | BOOTy | DEVIATION | LLDP-SHUTDOWN: not sent; the switch keeps BOOTy for 120 s after the handoff. Fix #754 (networkd does not send one either) |
| LLDP-10 | 9.2.7.7 | Receive: drop LLDPDUs whose first three TLVs are not Chassis ID, Port ID, TTL | BOOTy | DEVIATION | LLDP-RX-ORDER: gopacket accepts any order; a missing TTL TLV read as TTL 0 (shutdown). Fix #754 |
| LLDP-11 | 8.2 | End of LLDPDU TLV optional (2016) | BOOTy | DEVIATION | gopacket drops LLDPDUs without it. Fix #754 |
| LLDP-12 | 9.2.7.7 | Neighbor information aged out after TTL; TTL 0 removes it at once | BOOTy | COMPLIANT | `receive.go:102` to `:134`; `TestReceiverRecord` |
| LLDP-13 | 9.2.7.7 | Truncated or malformed TLVs dropped | BOOTy, Library | COMPLIANT | gopacket decoder checks lengths (`lldp.go:801`); `TestParseLLDPInvalidPayload` |
| LLDP-14 | 7.1 | Multicast group membership for receive | BOOTy | COMPLIANT | `lldp.go:77`; `TestOpenRXGroupsAndOwnFrames` |
| LLDP-15 | 9.1 | Own frames are not neighbors | BOOTy | COMPLIANT | `lldp.go:119`; `TestOwnFrame` |
| LLDP-16 | 9.2.7.7 | Several neighbors per port (tooManyNeighbors handling) | BOOTy | N/A | one neighbor per NIC by design (one switch port per NIC), documented |
| LLDP-17 | 8.6 | Organizationally specific TLVs | BOOTy | N/A | none sent like networkd; the cable check PRs (#729, #734) add some |
| LLDP-18 | 9.2.2 | adminStatus per port (txOnly, rxOnly, txAndRx) | BOOTy | COMPLIANT | `LLDP_EMIT` (send) and `LLDP_RECEIVE` (receive) per NIC set |
| LLDP-19 | 9.2.5 | Untrusted TLV strings sanitized | BOOTy | COMPLIANT | `lldp.go:227`; `TestSanitizeLLDP` |
| LLDP-20 | 9.2.4 | reinitDelay before restart of transmission | BOOTy | N/A | transmission starts once per run |

## IPv6 neighbor discovery and address configuration

The kernel does neighbor discovery and SLAAC. BOOTy itself sends Router
Advertisements on the unnumbered BGP NICs (`underlay.go:1015`), so FRR's
zebra learns the link-local address of the server, and ICMPv6 echo
requests to find the switch.

### RFC 4861: Neighbor Discovery (BOOTy's Router Advertisements)

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 4861-1 | 4.2, 6.1.2 | RA format; Hop Limit 255 | BOOTy | COMPLIANT | `underlay.go:1031`, `:1055`; `TestRouterAdvertisementPacket` (#752); lab: FRR drops other hop limits and the unnumbered sessions establish (`TestGoBGPUnnumberedBGPEstablished`, `TestProdTplLeafSessions`) |
| 4861-2 | 4.6.1 | Source Link-Layer Address option | BOOTy | COMPLIANT | `underlay.go:1064` |
| 4861-3 | 6.2.1 | AdvSendAdvertisements FALSE by default | BOOTy | COMPLIANT | RAs only in unnumbered and dual mode (`underlay.go:1074`) |
| 4861-4 | 6.2.1 | MaxRtrAdvInterval 4 to 1800 s; AdvDefaultLifetime 0 or MaxRtrAdvInterval to 9000 s | BOOTy | COMPLIANT | 10 s, 1800 s (`underlay.go:47`, `:60`) |
| 4861-5 | 6.2.1, 6.2.4 | MinRtrAdvInterval at most 0.75 x Max; random interval between Min and Max | BOOTy | DEVIATION | RFC4861-HOST-RA: fixed 10 s (`underlay.go:1093`). Impact low |
| 4861-6 | 6.2.2 | A router MUST join all-routers on an advertising interface | BOOTy | DEVIATION | RFC4861-HOST-RA: the kernel joins ff02::2 only with forwarding on; IPv6 forwarding is off by default. BOOTy answers no Router Solicitations, so nothing depends on it. Impact low |
| 4861-7 | 6.2.4 | A host MUST NOT send RAs | BOOTy | DEVIATION | RFC4861-HOST-RA: with IPv6 forwarding off BOOTy is a host and still sends RAs, because FRR BGP unnumbered needs them. The legacy FRR ramdisk does the same. By design, documented |
| 4861-8 | 6.2.5 | Final RAs with Router Lifetime 0 when advertising stops (SHOULD) | BOOTy | DEVIATION | RFC4861-FINAL-RA. Fix #752 |
| 4861-9 | 6.2.6 | RAs to all-nodes at most one per MIN_DELAY_BETWEEN_RAS (3 s); not more often than MinRtrAdvInterval | BOOTy | COMPLIANT | one RA per NIC per 10 s; the NDP trigger can add one RA at peer discovery (`underlay.go:1164`), before the periodic RAs start |
| 4861-10 | 6.2.6 | Router Solicitation processing | BOOTy | N/A | BOOTy answers no solicitations (zebra needs only the periodic RAs) |
| 4861-11 | 4.4, 7 | Neighbor solicitation and advertisement, NUD, DAD | Kernel | COMPLIANT | Linux; link-local discovery reads the kernel neighbor table (`underlay.go:1233`) |
| 4861-12 | 6.3 | Host processing of received RAs | Kernel | COMPLIANT | `accept_ra` 2 and `accept_ra_defrtr` 1 (`gobgp/helpers.go:42`), netplan `accept-ra: false` per NIC (`underlay.go:1271`) |
| 4861-13 | 8 | Redirects | Kernel | N/A | |
| 4861-14 | 4.2 | Router flag of Neighbor Advertisements consistent with the RAs | Kernel | COMPLIANT | the kernel sets the R flag from forwarding; with forwarding off it is 0 (`helpers.go:15` comment), which matches 4861-7 for a host |

### RFC 4862: IPv6 stateless address autoconfiguration

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 4862-1 | 5 | Link-local address generation and DAD | Kernel | COMPLIANT | Linux; netplan `link-local: []` sets `addr_gen_mode` none (`devices.go:50`) |
| 4862-2 | 5.5 | Global addresses from Prefix Information | Kernel | COMPLIANT | Linux, by `accept_ra` |

## DHCP

### RFC 2131: DHCPv4 client

BOOTy uses `github.com/digineo/go-dhclient v1.0.2` for the protocol and
applies the leases itself (`pkg/network/dhcp.go`).

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 2131-1 | 4.1, 4.4.1 | DISCOVER/OFFER/REQUEST/ACK exchange, xid, broadcast from 0.0.0.0 | Library | COMPLIANT | `go-dhclient@v1.0.2/client.go:205` to `:300`; `TestDHCPMode_*` |
| 2131-2 | 4.4.1 | Client SHOULD check the offered address (ARP) and MUST send DHCPDECLINE if in use | Library | DEVIATION | RFC2131-ARP: no ARP probe and no DECLINE. Impact low. Open (library) |
| 2131-3 | 4.3.2, 4.4.5 | RENEWING: unicast REQUEST with ciaddr, without server identifier and requested IP | Library | DEVIATION | RFC2131-RENEW: broadcast with both options (`client.go:232`). Servers accept it. Impact low. Open (library) |
| 2131-4 | 4.4.5 | T1 and T2 defaults 0.5 and 0.875 of the lease | Library | COMPLIANT | `client.go:255` to `:259` |
| 2131-5 | 4.4.5 | Lease expiry before renewal or rebind: MUST stop using the address, back to INIT | BOOTy | DEVIATION | RFC2131-EXPIRE: no `OnExpire` callback; the address stayed. Fix #751 (`TestOnExpireRemovesLeaseState`) |
| 2131-6 | 3.2 | DHCPNAK: stop using the address, restart | BOOTy | DEVIATION | covered by #751 (go-dhclient calls `OnExpire` for a NAK) |
| 2131-7 | 4.4.5 | A renewed lease updates address and router | BOOTy | COMPLIANT | `dhcp.go` `applyRenewal`; `TestOnBoundAppliesRenewalOfWinningNIC` |
| 2131-8 | 3.3 | Lease without router option drops the default route | BOOTy | COMPLIANT | `applyRenewal`; tests in `dhcp_test.go` |
| 2131-9 | 4.1 | Retransmission with randomized exponential backoff (SHOULD) | Library | DEVIATION | RFC2131-BACKOFF: fixed 1 s retry delay (`client.go:162`) inside BOOTy's 15 s probe. Impact very low for a bounded probe. Open (library) |
| 2131-10 | 4.4.6 | DHCPRELEASE on shutdown (MAY) | BOOTy | N/A | the OS takes over the NIC after the handoff |
| 2131-11 | 4.4.1 | Parameter request list, hostname option | BOOTy | COMPLIANT | `dhcp.go` `newDHCPClient`; `TestNewDHCPClientRequestsDefaultParams` |
| 2131-12 | 3.1 | Client identifier, chaddr | Library | N/A | chaddr only (allowed) |

### RFC 8415: DHCPv6

| ID | § | Requirement | Owner | Status | Evidence, impact, fix |
|---|---|---|---|---|---|
| 8415-1 | all | DHCPv6 client | BOOTy | N/A | not implemented; the provisioning network is IPv4 or EVPN |

## Deviations

The ranked list is in the [Summary](#deviations-ranked-by-impact). Fix
PRs (all drafts, based on `main` at the audited tree unless noted):

| PR or branch | Findings |
|---|---|
| #746 `fix/evpn-l2-encap-community` | RFC8365-ENCAP |
| #747 `fix/type5-rfc9136-validation` | RFC9136-RMAC, RFC9136-ESIGW, RFC9136-ESI, RFC9136-LABEL0 |
| #748 `fix/type2-mac-mobility-withdraw` | RFC7432-MACIP-WITHDRAW, RFC7432-MOBILITY-RX |
| #749 `fix/bfd-remote-discr-reset` | RFC5880-REMOTEDISCR, RFC5880-DEMAND |
| #751 `fix/dhcp-lease-expiry` | RFC2131-EXPIRE |
| #752 `fix/ra-final-zero-lifetime` | RFC4861-FINAL-RA |
| #754 `fix/lldp-shutdown-and-rx-order` | LLDP-SHUTDOWN, LLDP-RX-ORDER |
| #755 `test/gobgp-v4-evpn-decode-bounds` (stacked on #699) | RFC7606-EVPN-PANIC (regression test) |
| fork `MaxRink/gobgp` `fix/v3-evpn-decode-bounds` (on v3.37.0) | RFC7606-EVPN-PANIC (backport of upstream ac5a8894) |
| fork `MaxRink/gobgp` `fix/evpn-discard-unknown-route-types` (on v3.37.0) | RFC7606-TYPED-NLRI |
| fork `MaxRink/gobgp` `fix/enhe-require-peer-capability` (on v3.37.0) | RFC8950-ENHE-CHECK |
| #765 `fix/kernel-panic-reboot` | defence in depth for RFC7606-EVPN-PANIC (`panic=10`) |
| #766 `fix/evpn-type2-neighbors` (stacked on #748) | RFC9135-ARP-RX |
| #767 `fix/evpn-esi-aliasing` (stacked on #748) | RFC7432-ESI-RX |
| #768 `test/bfd-demand-guards` (stacked on #749) | RFC5880-DEMAND (more test cases) |
| #769 `test/lldp-short-lldpdu` (stacked on #754) | LLDP-RX-ORDER (length guard tests, duplicate mandatory TLVs) |
| #770 `fix/ra-lifetime-like-zebra` (stacked on #752) | RA router lifetime 30 s like zebra |

The next release moves to GoBGP v4 with a fork pin (`MaxRink/gobgp`
`v4.10.0-booty.1`); the GoBGP fork fixes of this audit (EVPN decode bounds,
unknown EVPN route types, the Extended Next Hop check, the graceful
restart reconnect) apply to that fork. The v3.37.0 branches above remain
for reference.

The fork branches are not filed upstream. Using one means a `replace`
in go.mod, which `docs/networking.md` currently rules out ("BOOTy uses
the released GoBGP without a fork"); the GoBGP v4 move (#699) fixes
RFC7606-EVPN-PANIC and RFC4271-COLLISION without one.

Open deviations without a PR: RFC9136-NH-REACH, RFC5880-ADMINDOWN,
RFC5880-CONGESTION, RFC4861-HOST-RA, RFC2131-RENEW, RFC2131-ARP,
RFC2131-BACKOFF, RFC4271-NH-RESOLVE, RFC8950-ENHE-EVPN,
RFC7432-MOBILITY-GOBGP, RFC7432-PMSI-ID, LLDP-TTL. Each needs a GoBGP or
library change, or is kept for FRR or networkd parity (the row says which).
RFC4724-NEWCONN goes to the GoBGP fork. RFC9136-GWIP, RFC8365-RX-VNI,
RFC8365-ENCAP-RX and RFC8365-PMSI-RX get fix PRs that follow FRR.

## Open PRs and their effect on the findings

Checked on 2026-10-18 after the audit, with `main` at `5bd139b7`
(#741 was merged after the audited tree).

| PR | Effect on the findings |
|---|---|
| #699 GoBGP v4.10.0 | Fixes RFC7606-EVPN-PANIC and RFC4271-COLLISION. Does not fix RFC7606-TYPED-NLRI, RFC8950-ENHE-CHECK, RFC8950-ENHE-EVPN, RFC4724-NEWCONN. The GoBGP line references in this document are for 3.37.0. |
| #710 fabric parity | Fixes RFC7432-RD (and 9136-3): RD `<router-id>:<n>`, a separate RD for an L2VNI. MED 0 on originated routes (allowed by RFC 4271 §5.1.4). BFD Required Min Echo RX 50 ms: BOOTy then must loop back peer echo packets, which the kernel does with forwarding on and rp_filter off (5881-2). |
| #741 Type-5 connected parity (merged after the audited tree) | Type-5 kernel routes get protocol bgp and metric 20 in both families; a withdraw is processed before the blackhole check (9136-19 stays COMPLIANT, more complete). No change to the RFC 9136 findings. |
| #743 Type-5 FIB owner | Type-5 routes for prefixes the underlay FIB owns are not installed. No RFC finding changes. |
| #744 GR N bit | Sets the RFC 8538 N bit like FRR (4724-12). |
| #745 restart after peer reset | FSM idle hold after a session end (4271-18); no finding changes. |
| #723 stop BGP and BFD before kexec | Ends the sessions with Cease and BFD AdminDown before the handoff; relevant for RFC5880-ADMINDOWN (still one packet) and for the final RA of #752. |
| #729, #734, #735, #702, #708 (LLDP and cable check) | Add LLDP TLVs (port description, management address, 802.1/802.3/MED) and an LLDP identity; #729 touches `parseLLDP` next to #754. No new deviation seen in their descriptions. |
| #750 DHCP on every NIC like networkd | Replaces the winner flag with per-NIC state and keeps a client on every NIC. It sets no `OnExpire` either, so RFC2131-EXPIRE stays; #751 must be rebased onto it if #750 merges first. |
| #753 DNS and MTU of the lease (stacked on #750) | Applies lease options (RFC 2132); no finding changes. |
| #756 LLDP for netplan match blocks | More NICs send LLDP; LLDP-SHUTDOWN and LLDP-TTL apply to them too. |
| #759, #760 (lldpd build, LLDP receive start) | No protocol change. |
| #761 no BGP reset in OpenConfirm on BFD down | BFD to BGP coupling (RFC 5882, out of scope); no finding changes. |
| #736 (combined test build) | Test only. |
