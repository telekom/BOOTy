# RFC compliance of the BOOTy networking stack

This document records an RFC compliance audit of the networking code of
BOOTy: the GoBGP stack (`pkg/network/gobgp`), BOOTy's own BFD
(`pkg/network/bfd`), LLDP (`pkg/network/lldp`), the Router Advertisements
of the unnumbered peering, the DHCP client mode and the VXLAN data plane
that BOOTy configures in the kernel.

| Item | Value |
|---|---|
| Audited tree | `main` at `f1e0e72e081ec228ffdc5b383cb72c7972b1373c` (refresh); the first audit read `03abb09f60556a28e4f781690a25d4b4f8466e09` |
| GoBGP | `github.com/osrg/gobgp/v4 v4.10.0`, replaced by the fork tag `github.com/MaxRink/gobgp/v4 v4.10.0-booty.10` (go.mod); source read at that tag. The first audit read `v3.37.0`. The tags booty.11 and booty.12 are in [#831] (open) |
| Other libraries | `digineo/go-dhclient v1.0.2`, `google/gopacket v1.1.19`, `vishvananda/netlink v1.3.1` |
| Open PRs | read at their heads on 2026-10-19, also the DHCP stack [#750] to [#825] |
| Kernel | Linux VXLAN, bridge, neighbor and IPv6 ND/SLAAC code (not audited, owner noted) |
| Date | 2026-10-19 (refresh); first audit 2026-10-18 |

## Method

For each RFC, the normative sentences (MUST, MUST NOT, SHALL, SHOULD,
SHOULD NOT, REQUIRED, RECOMMENDED) were extracted from the RFC text and
checked against the role of BOOTy: a host-side BGP speaker (eBGP to the
leaves, iBGP to route reflectors), a VXLAN VTEP with one VNI that has no
local Ethernet segment, and a DHCP client. Requirements for other roles
(route reflectors, PEs with a local Ethernet segment, interconnect
gateways, MPLS data planes) are grouped in one row per section and marked
not applicable. Requirements of the same section with the same owner and
the same result are grouped in one row; the row names each of them.

The refresh read every row again at the audited tree: the file and line
references moved to `main` and to the GoBGP fork tag (matched by the text
of the cited line, the rest looked up by hand), rows fixed by merged PRs
are COMPLIANT and name the PR, rows fixed only in open PRs stay DEVIATION
and say "fixed in #NNN (open)", and new rows cover the GoBGP fork changes
(booty.1 to booty.12) and the DHCPv4, DHCPv6 and Router Advertisement
stack of the open PRs [#750] to [#825].

Each row has an owner:

- **BOOTy**: code in this repository.
- **GoBGP**: the GoBGP library; checked in its source at the fork tag
  `v4.10.0-booty.10`, with the file and line of that tag.
- **Kernel**: Linux, configured by BOOTy.
- **Library**: another Go dependency (named in the row).

Status values:

| Status | Meaning |
|---|---|
| COMPLIANT | The requirement is met. Evidence: a file and line at the audited tree plus a test, or a lab test, or the source read. |
| DEVIATION | The requirement is not met. The row gives the impact and the fix: "fixed in #NNN (open)" for a fix in an open PR, else the proposal or the reason to keep it. |
| N/A | Not implemented and not needed for the role of BOOTy (or a requirement on a feature BOOTy does not use). |
| NOT IMPLEMENTED | Not implemented, and an implementation would be needed for full compliance. |
| PENDING | The code that the requirement is about exists only in an open PR. The row gives the result at the head of that PR (COMPLIANT or DEVIATION). |
| GOBGP: COMPLIANT / GOBGP: DEVIATION | Owned by GoBGP; verified in the GoBGP fork source. |

The last column, Verified, says how the status was checked at the audited
tree: **test** (a unit test named in the row), **lab** (a test of
`test/e2e/integration` against FRR in containerlab, build tag
`e2e_prodtpl`, `e2e_gobgp` or `e2e_gobgp_type5`), **GoBGP test** (a test
in the GoBGP fork tag), or **reading** (the source was read; no test pins
it). A test that exists only in an open PR does not count; for PENDING
rows, **PR test** means a test of that PR.

Evidence: file references are relative to the repository root at the
audited tree, `gobgp@v4.10.0-booty.10/...` to the GoBGP fork module. The bare GoBGP file names
`fsm.go`, `server.go`, `peer.go` (`pkg/server`), `bgp.go`, `validate.go`
(`pkg/packet/bgp`), `path.go`, `message.go`, `adj.go` and
`table_manager.go` (`internal/pkg/table`) are in the fork module as well.
New tests added by this audit:

- `pkg/network/gobgp/rfc_wire_test.go`: `TestRFCWireType5`,
  `TestRFCWireType3`, `TestRFCWireType2` encode the EVPN routes BOOTy
  originates with GoBGP and check every field on the wire, the VXLAN
  encapsulation community of [#746] included.
- `pkg/network/bfd/rfc_test.go`: `TestRFC5880PacketFields`,
  `TestRFC5881SourcePortAndTTL`.
- Fix PRs and the GoBGP fork add tests for each fixed deviation (see
  [Deviations fixed since the first audit](#deviations-fixed-since-the-first-audit)).

## Summary

Counts per RFC at the audited tree. GoBGP-owned rows are counted in the
GoBGP columns. "Fix open" counts the DEVIATION rows (BOOTy, kernel,
library and GoBGP) that an open PR fixes.

| RFC | Topic | COMPLIANT | DEVIATION | fix open | N/A | NOT IMPL. | PENDING | GOBGP: COMPLIANT | GOBGP: DEVIATION |
|---|---|---|---|---|---|---|---|---|---|
| 4271 | BGP-4 | 6 | 0 | 1 | 1 | 0 | 0 | 25 | 2 |
| 4760 | Multiprotocol BGP | 3 | 0 | 0 | 0 | 0 | 0 | 6 | 0 |
| 6793 | 4-octet AS | 3 | 0 | 0 | 0 | 0 | 0 | 7 | 0 |
| 7911 | ADD-PATH (and Paths-Limit draft) | 1 | 0 | 0 | 1 | 0 | 0 | 6 | 0 |
| 8950 / 5549 | IPv4 NLRI with IPv6 next hop | 3 | 0 | 0 | 1 | 0 | 0 | 4 | 0 |
| 4724 | Graceful restart | 1 | 0 | 0 | 2 | 0 | 0 | 9 | 0 |
| 7313 | Enhanced route refresh | 0 | 0 | 0 | 0 | 0 | 0 | 5 | 0 |
| 2918 | Route refresh | 1 | 0 | 0 | 0 | 0 | 0 | 3 | 0 |
| 8654 | Extended message | 0 | 0 | 0 | 0 | 0 | 0 | 2 | 0 |
| 9234 | BGP roles | 0 | 0 | 0 | 2 | 0 | 0 | 0 | 0 |
| 5492 | Capabilities | 0 | 0 | 0 | 0 | 0 | 0 | 5 | 0 |
| 7606 | UPDATE error handling | 2 | 0 | 0 | 0 | 0 | 0 | 7 | 0 |
| 9384 | Cease/BFD Down | 1 | 0 | 0 | 0 | 0 | 0 | 1 | 0 |
| 5880 | BFD | 34 | 2 | 0 | 7 | 1 | 0 | 0 | 0 |
| 5881 | BFD single hop | 13 | 1 | 0 | 2 | 0 | 0 | 0 | 0 |
| 5883 | BFD multihop | 2 | 1 | 0 | 1 | 0 | 0 | 0 | 0 |
| 7432 | EVPN | 25 | 2 | 1 | 15 | 0 | 0 | 4 | 1 |
| 8365 | EVPN NVO (VXLAN) | 15 | 3 | 3 | 5 | 0 | 0 | 1 | 0 |
| 9135 | EVPN IRB | 8 | 0 | 0 | 5 | 0 | 0 | 0 | 0 |
| 9136 | EVPN IP prefix (Type-5) | 12 | 7 | 6 | 1 | 0 | 0 | 1 | 0 |
| 9047 | EVPN ARP/ND flags | 0 | 1 | 1 | 0 | 0 | 0 | 1 | 0 |
| 6514 | PMSI Tunnel attribute | 3 | 1 | 0 | 0 | 0 | 0 | 1 | 0 |
| 7209 | EVPN requirements | 3 | 0 | 0 | 6 | 1 | 0 | 0 | 0 |
| 8584 | DF election | 0 | 0 | 0 | 1 | 0 | 0 | 0 | 0 |
| 9161 | Proxy ARP/ND | 0 | 0 | 0 | 1 | 0 | 0 | 0 | 0 |
| 9014 | EVPN interconnect | 0 | 0 | 0 | 1 | 0 | 0 | 0 | 0 |
| 9251 | IGMP/MLD proxy | 1 | 0 | 0 | 1 | 0 | 0 | 1 | 0 |
| 7348 | VXLAN | 6 | 0 | 0 | 1 | 0 | 0 | 0 | 0 |
| 802.1AB | LLDP | 17 | 1 | 0 | 2 | 0 | 0 | 0 | 0 |
| 4861 | IPv6 ND (RAs) | 9 | 3 | 0 | 2 | 0 | 2 | 0 | 0 |
| 4862 | IPv6 SLAAC | 2 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| 2131 | DHCPv4 client | 5 | 5 | 4 | 2 | 0 | 3 | 0 | 0 |
| 2132 / 3397 / 3442 / 4039 / 4361 | DHCPv4 options | 0 | 0 | 0 | 0 | 0 | 6 | 0 | 0 |
| 8415 | DHCPv6 | 0 | 0 | 0 | 0 | 1 | 8 | 0 | 0 |
| **Total** | | **176** | **27** | **16** | **60** | **3** | **19** | **89** | **3** |

Kernel- and library-owned rows count as COMPLIANT or DEVIATION; the owner
column names them. The first audit (`03abb09f`, GoBGP 3.37.0) counted 149
COMPLIANT, 47 DEVIATION, 67 N/A, 4 NOT IMPLEMENTED, 68 GOBGP: COMPLIANT and
10 GOBGP: DEVIATION.

### Open deviations ranked by impact

| Rank | ID | RFC | Owner | Impact | Fix |
|---|---|---|---|---|---|
| 1 | RFC9136-RX (RMAC, ESIGW, ESI, LABEL0) | 9136 §3.1, §3.2 | BOOTy | Medium to low: routes that must be treated as withdrawn, or that need an overlay index BOOTy cannot resolve (ESI, label 0 without gateway IP), are installed through the BGP next hop. | Fixed in [#747] (open); [#799] (open) adds a test. |
| 2 | RFC8365-RX-VNI | 8365 §5.1.3, 9136 §3.1 | BOOTy | Low to medium: the VNI of a received Type-5 route is ignored; BOOTy encapsulates with its own VNI. Breaks only with a fabric that uses another VNI for the same VRF. | Fixed in [#773] (open) for Type-5 routes in L3VNI mode, like zebra. Type-2 and Type-3 routes keep the local VNI, as FRR does. |
| 3 | RFC8365-ENCAP-RX | 8365 §6 | BOOTy | Low: a route that advertises only another encapsulation (for example MPLS) still gets VXLAN state. FRR 10.7.1 ignores the community too. | Fixed in [#771] (open). |
| 4 | RFC2131-EXPIRE | 2131 §4.4.5, §3.2 | BOOTy | Low: the address of an expired or refused (DHCPNAK) lease stays on the NIC. | Fixed in [#751] (open); the own client of [#790] (open) keeps it. |
| 5 | RFC2131-RENEW, RFC2131-BACKOFF | 2131 §4.3.2, §4.4.5, §4.1 | go-dhclient | Low: RENEWING requests are broadcast with server identifier and requested IP; fixed 1 s retransmission. Servers accept the requests. | Fixed in [#790] (open): BOOTy's own DHCPv4 client. |
| 6 | RFC7432-RD | 7432 §7.9 | BOOTy | Low: RD `ASN:VNI` (type 0 or 2) on every host and the same RD for the MAC-VRF and the IP-VRF; RECOMMENDED is type 1 `router-id:n`, unique per VRF. | Fixed in [#710] (open): `<router-id>:<n>` like FRR. |
| 7 | RFC9047-FLAGS | 9047 §3.2 | BOOTy | Low: the IPv6 neighbor entries of Type-2 routes do not get the Router flag of the ARP/ND extended community. | Fixed in [#798] (open): `NTF_ROUTER` like zebra. |
| 8 | RFC4271-COLLISION-DIALED | 4271 §6.8, §8.2.2 | GoBGP fork booty.10 | Low, only with `BGP_CONNECT_AT_ONCE` (off by default): after a carrier loss GoBGP drops its own dialed connection when the leaf's OPEN arrives on the other one; the leaf keeps the dialed one, both are gone, and the session waits 5 s in Idle. | Fixed in [#831] (open): fork booty.11 and booty.12. |
| 9 | RFC2131-ARP | 2131 §4.4.1 | go-dhclient | Low: no ARP probe of the offered address and no DHCPDECLINE. | Open; the own client of [#790] (open) does not probe either. |
| 10 | RFC9136-NH-REACH | 9136 §3.2 | BOOTy | Low: a Type-5 route is installed without a check that the VTEP is reachable; in Type-5-only mode BOOTy installs its own /32 to the VTEP. | Open, by design for a host with one uplink path. |
| 11 | RFC5880-ADMINDOWN | 5880 §6.8.16 | BOOTy | Low: at shutdown one AdminDown packet instead of AdminDown for a detection time. A lost packet makes the peer wait one detection time. | Open; [#723] (merged) stops BFD before kexec and reboot, still with one packet. |
| 12 | RFC5880-CONGESTION | 5880 §7 | BOOTy | Low: no congestion control for multihop sessions (a few sessions per host). | Open. |
| 13 | RFC4861-HOST-RA | 4861 §6.2.1, §6.2.2, §6.2.4 | BOOTy | Low: BOOTy sends RAs with IPv6 forwarding off (a host), at a fixed 10 s interval, without joining all-routers. Needed for FRR BGP unnumbered; the legacy FRR ramdisk does the same. | By design, documented. |
| 14 | RFC4271-NH-RESOLVE | 4271 §9.1.2.1 | GoBGP | Low: no next hop tracking in the decision process; BOOTy's FIB writer skips unresolvable next hops instead. | Open. |
| 15 | RFC7432-PMSI-ID | 7432 §11.2, 6514 §5 | BOOTy | Very low: the ingress replication destination is the originating router IP, not the PMSI Tunnel Identifier; in VXLAN fabrics both are the VTEP. | Kept like FRR, documented. |
| 16 | RFC7432-MOBILITY-GOBGP | 7432 §15 | GoBGP | Very low: GoBGP withdraws its local Type-2 route on a remote higher sequence only when the ESIs differ. BOOTy's MAC is its own bridge MAC and does not move. | Open. |
| 17 | LLDP-TTL | 802.1AB | BOOTy | Very low: TTL 120 instead of txTTL = 4 x 30 + 1 = 121; matches systemd-networkd. | Kept for networkd parity. |

The rows without authentication (5880-39, 5881-15, 5883-4) are not
ranked: the legacy FRR profile has none either. Two deviations exist only
in open PRs (PENDING rows): the DHCPv6 client of [#795] sends no DECLINE
(8415-8), and the first Router Solicitation after a carrier return has no
random delay (4861-16).

### Deviations fixed since the first audit

| ID | RFC | Fixed by | Test |
|---|---|---|---|
| RFC7606-EVPN-PANIC | 7606 §5.3 | GoBGP 4.10.0 ([#699]) | `TestEVPNDecodeTruncatedNLRIReturnsError` |
| RFC7606-TYPED-NLRI | 7606 §5.4 | fork booty.1 ([#699]) | `Test_MpReachNLRIDiscardsUnknownEVPNRouteType`, `Test_DiscardedEVPNWithdrawIsNotEndOfRib` (GoBGP) |
| RFC4271-COLLISION | 4271 §6.8 | GoBGP 4.10.0 ([#699]); fork booty.1, booty.5, booty.9, booty.10 ([#699], [#808]) | `TestFSMHandlerOpenconfirm_CollisionDominantKeepsOutgoing`, `TestCollisionResolvedByGoBGPKeepsSurvivingConnection`, `TestCollisionLoserClosedOnlyAfterItsOpen` (GoBGP); `TestCollisionAfterCarrierLoss`; lab `TestProdTplOneDropPerFault` |
| RFC4724-NEWCONN | 4724 §4.2 | fork booty.1 ([#699]) | `TestNewConnectionFromRestartingPeer` (GoBGP) |
| RFC8950-ENHE-CHECK | 8950 §4 | fork booty.1 ([#699]) | `TestPostFilterpathExtendedNexthop` (GoBGP) |
| RFC8950-ENHE-EVPN | 8950 §3, §4 | fork booty.1 ([#699]) | `TestCapabilitiesExtendedNexthopIPv4Only` (GoBGP) |
| RFC8365-ENCAP | 8365 §5.1.3 | [#746] | `TestEVPNRoutesCarryVXLANEncapsulation`, `TestRFCWireType2`, `TestRFCWireType3` |
| RFC7432-MACIP-WITHDRAW, RFC7432-MOBILITY-RX | 7432 §10, §15 | [#748] | `TestHandleType2RouteBestPathPerMAC` |
| RFC7432-ESI-RX | 7432 §8.4, §9.2.2, §14, §17.3 | [#767] | `TestType2ESIAliasingAndMassWithdraw`, `TestESINetlinkAliasing` |
| RFC8365-PMSI-RX | 8365 §9 | [#772] (documented in [#786]) | `TestProcessRouteUpdateType3TunnelType` |
| RFC9136-GWIP | 9136 §3.2 | [#775] | `TestType5GatewayIPResolvedByType2`, `TestType5GatewayIPResolutionKeepsTheActivePath` |
| RFC9135-ARP-RX | 9135 §5.2, §6.2 | [#766], [#784] | `TestHandleType2RouteInstallsNeighbor`, `TestType2NeighborKeptByTheOtherRD`, `TestType2AndType5ShareTheBridgeNeighbor` |
| RFC5880-REMOTEDISCR, RFC5880-DEMAND | 5880 §6.8.1, §6.8.7, §6.8.18 | [#749], [#768] | `TestSessionForgetsRemoteAfterDetectionTime`, `TestSessionNoPeriodicPacketsInRemoteDemandMode` |
| LLDP-SHUTDOWN, LLDP-RX-ORDER, LLDP-END | 802.1AB | [#754], [#769], [#792] | `TestShutdownFrame`, `TestEmitEndsWithShutdownFrame`, `TestStopNetworkOnceStopsLLDPFirst`, `TestParseLLDPMandatoryTLVs` |
| RFC4861-FINAL-RA | 4861 §6.2.5 | [#752] | `TestPeriodicRAEndsWithZeroLifetime` |

### Open networking PRs

See [Open PRs and their effect on the findings](#open-prs-and-their-effect-on-the-findings)
at the end.

## BGP

### RFC 4271: BGP-4

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 4271-1 | 4.2 | OPEN Version MUST be 4; Unsupported Version Number otherwise | GoBGP | GOBGP: COMPLIANT | `gobgp@v4.10.0-booty.10/pkg/packet/bgp/validate.go:284` | reading |
| 4271-2 | 4.2, 6.2 | Hold Time MUST be 0 or at least 3; Unacceptable Hold Time otherwise | GoBGP, BOOTy | GOBGP: COMPLIANT | receive: `validate.go:332`; send: BOOTy rejects 1 and 2 (`pkg/network/gobgp/config.go:480`, `:460`), tests `TestScenarioBGPTimers`, `TestValidateSessions` | test |
| 4271-3 | 4.2 | BGP Identifier MUST be an IPv4 address of the speaker, the same for every peer | BOOTy | COMPLIANT | router ID = underlay IPv4, validated in `config.go:411`; one router ID for all peers (`underlay.go:510`) | reading |
| 4271-4 | 4.4, 10 | KEEPALIVE not more often than once per second; RECOMMENDED one third of the hold time | BOOTy, GoBGP | COMPLIANT | BOOTy rejects a keepalive above hold/3 (`config.go:484`). GoBGP keeps the configured keepalive and uses hold/3 only when the negotiated hold time is below the configured one (`gobgp@v4.10.0-booty.10/pkg/server/fsm.go:746` to `:750`) | reading |
| 4271-5 | 4.2 | Hold timer MUST be the smaller of the configured and received hold time | GoBGP | GOBGP: COMPLIANT | `fsm.go:733` | reading |
| 4271-6 | 4.3 | UPDATE: withdrawn routes, path attributes, NLRI encoding | GoBGP | GOBGP: COMPLIANT | GoBGP encoder; lab `TestProdTplLeafSessions`, `TestProdTplRRSessions` | lab |
| 4271-7 | 4.5, 6 | NOTIFICATION codes and subcodes for each error | GoBGP | GOBGP: COMPLIANT | `validate.go:283` (OPEN), `bgp.go` MessageError codes | reading |
| 4271-8 | 5.1.1 | ORIGIN MUST be generated by the originating speaker | BOOTy | COMPLIANT | ORIGIN IGP on the underlay route (`underlay.go:1022`) and every EVPN route (`overlay.go:2249`, `originIGP`); `TestRFCWireType5`, `TestRFCWireType3`, `TestRFCWireType2` check ORIGIN IGP on the wire | test |
| 4271-9 | 5.1.2 | AS_PATH: originator sends empty AS_PATH to iBGP and its AS to eBGP; prepend own AS to eBGP | GoBGP | GOBGP: COMPLIANT | BOOTy adds an empty AS_PATH (`underlay.go:1024`); GoBGP prepends for eBGP (`internal/pkg/table/path.go:339`) and keeps it empty for iBGP (`path.go:361`); lab `TestProdTplLocalASReplaceASPath` | lab |
| 4271-10 | 5.1.3 | NEXT_HOP to eBGP MUST be an address of the sending speaker on the shared link; iBGP keeps the next hop of an external route | GoBGP, BOOTy | GOBGP: COMPLIANT | next hop 0.0.0.0 for the underlay route is replaced by the session address (`path.go:331`); lab `TestGoBGPUnnumberedUnderlayRoute`, `TestProdTplRFC5549Routes` | lab |
| 4271-11 | 5.1.4 | MED received from a neighboring AS MUST NOT be propagated to other ASes | GoBGP | GOBGP: COMPLIANT | `path.go:344` (removed for non-local paths to eBGP). BOOTy is no transit in practice | reading |
| 4271-12 | 5.1.5 | LOCAL_PREF MUST be sent to iBGP peers and MUST NOT be sent to eBGP peers | GoBGP | GOBGP: COMPLIANT | added for iBGP (`path.go:368`), removed for eBGP (`gobgp@v4.10.0-booty.10/pkg/server/server.go:741`, `postFilterpath`) | reading |
| 4271-13 | 5.1.6, 5.1.7 | ATOMIC_AGGREGATE, AGGREGATOR | GoBGP | N/A | BOOTy does not aggregate | reading |
| 4271-14 | 6.2 | OPEN errors: Bad Peer AS, Bad BGP Identifier (RFC 6286 rules) | GoBGP | GOBGP: COMPLIANT | `validate.go:308` to `:329`. Unnumbered peers take any peer AS (remote AS from OPEN, FRR `remote-as external` style) | reading |
| 4271-15 | 6.3 | UPDATE errors: malformed attribute list, missing well-known attribute, invalid ORIGIN, invalid NEXT_HOP, AS loop | GoBGP | GOBGP: COMPLIANT | `validate.go:12` to `:81`, revised by RFC 7606 (see below) | reading |
| 4271-16 | 6.5 | Hold timer expiry MUST send NOTIFICATION Hold Timer Expired | GoBGP | GOBGP: COMPLIANT | `fsm.go:1869` (OpenSent), `:2041` (OpenConfirm), `:2448` (Established); `TestFSMHandlerOpensent_HoldTimerExpired` | GoBGP test |
| 4271-17 | 6.8 | Connection collision detection MUST compare BGP Identifiers and close one connection | GoBGP | GOBGP: COMPLIANT | RFC4271-COLLISION at the first audit: GoBGP 3.37.0 closed every new connection. GoBGP 4.10.0 compares the BGP Identifiers (`fsm.go:513`, `resolveCollision`, [#699]). The fork fixes the cases where GoBGP lost both connections: OpenConfirm takes the outgoing connection (booty.1, [osrg/gobgp#3741](https://github.com/osrg/gobgp/issues/3741)); GoBGP keeps the surviving connection when it resolves a collision itself (booty.5); it closes the losing incoming connection with Cease/Connection Collision Resolution only after the peer's OPEN arrives on it (booty.9, booty.10, `fsm.go:962`). GoBGP tests `TestFSMHandlerOpenconfirm_CollisionDominantKeepsOutgoing`, `TestFSMHandlerOpenconfirm_CollisionNotDominantKeepsIncoming`, `TestCollisionResolvedByGoBGPKeepsSurvivingConnection`, `TestCollisionSimultaneousOpenFRRTiming`, `TestCollisionBothConnectionsDropped`, `TestCollisionLoserClosedOnlyAfterItsOpen`, `TestCollisionLoserAnswers`; BOOTy `TestCollisionAfterCarrierLoss`; lab `TestProdTplOneDropPerFault`. See 4271-31 for the case that booty.10 still loses | test, lab, GoBGP test |
| 4271-18 | 8 | FSM: ConnectRetryTimer, IdleHoldTimer, states and events | GoBGP, BOOTy | GOBGP: COMPLIANT | connect retry and idle hold from BOOTy (`families.go:32`, idle hold after reset 1 s like FRR, `families.go:29`). [#745] (merged) restarts a peer at once after a session end. Idle hold: 4271-33; start: 4271-34 | reading |
| 4271-19 | 9 | Adj-RIB-In / Loc-RIB / Adj-RIB-Out processing | GoBGP | GOBGP: COMPLIANT | GoBGP RIB | reading |
| 4271-20 | 9.1.2.1 | A route with an unresolvable NEXT_HOP SHOULD be excluded from the decision process | GoBGP, BOOTy | GOBGP: DEVIATION | RFC4271-NH-RESOLVE: GoBGP has no next hop tracking. BOOTy's FIB writer resolves each next hop and skips unresolvable ones (`pkg/network/gobgp/fib.go:303`), but a resolvable second-best path is not used then. Impact low | reading |
| 4271-21 | 9.1.2.2 | Decision process tie breaking | GoBGP, BOOTy | GOBGP: COMPLIANT | GoBGP best path; BOOTy's FIB multipath uses the FRR rules (`fibselect.go:94`), tests `fibselect_test.go` | reading |
| 4271-22 | 9.1.3 | Only the best route is advertised (without add-path send) | GoBGP | GOBGP: COMPLIANT | GoBGP default | reading |
| 4271-23 | 9.2 | Routes with own AS in AS_PATH: loop detection | GoBGP | GOBGP: COMPLIANT | `gobgp@v4.10.0-booty.10/pkg/server/peer.go:767` with `allowas-in` (`families.go:55`); lab `TestProdTplLocalASLoopCheck` | lab |
| 4271-24 | 9.2.1.1 | MinRouteAdvertisementIntervalTimer | GoBGP | GOBGP: COMPLIANT | 0 in GoBGP and in FRR for these peers (documented in `docs/networking.md`) | reading |
| 4271-25 | 9.3 | Routes BOOTy originates | BOOTy | COMPLIANT | underlay /32 (`underlay.go:1007`), EVPN routes (`overlay.go:717`, `:760`, `:787`) | reading |
| 4271-26 | 10 | ListenPort 179 | BOOTy | COMPLIANT | `config.go:253` | reading |
| 4271-27 | 5 | Each attribute at most once per UPDATE | GoBGP | GOBGP: COMPLIANT | BOOTy builds one of each; GoBGP checks duplicates on receipt (`validate.go:29`) | reading |
| 4271-28 | 8.1.1 | Optional session attributes (DelayOpen, DampPeerOscillations) | GoBGP | GOBGP: COMPLIANT | optional; not used | reading |
| 4271-29 | 6.7 | Cease NOTIFICATION when a session is closed | GoBGP, BOOTy | COMPLIANT | GoBGP sends Cease on StopBgp and ResetPeer; a BFD Down reset sends Cease/BFD Down (subcode 10, see 9384-1) | reading |
| 4271-30 | 3 | Routes withdrawn when a session ends | GoBGP | GOBGP: COMPLIANT | GoBGP drops the peer's routes (`server.go:1808`) unless graceful restart applies | reading |
| 4271-31 | 6.8, 8.2.2 | Collision: a connection that GoBGP dialed before the peer's OPEN arrived on the other connection takes part in the collision resolution; no ConnectRetry in OpenConfirm | GoBGP | GOBGP: DEVIATION, fixed in [#831] (open) | RFC4271-COLLISION-DIALED: booty.10 stops the outgoing connection manager when OpenSent gets the peer's OPEN on the incoming connection, also when its own connection is dialed but has no OPEN yet. The leaf, with the lower BGP Identifier, keeps that connection and closes the other one with Cease/Connection Collision Resolution; both are gone and the FSM waits 5 s in Idle. Only with `BGP_CONNECT_AT_ONCE` (off by default, `docs/networking.md`). Fork booty.11 keeps a dialed connection (`TestFSMHandlerOpensent_DialedOutgoingConnKeptForCollision`); booty.12 does not connect again in OpenConfirm (`TestConnectLoopNoRetryInOpenConfirm`) | reading |
| 4271-32 | 8.2.2 | OpenSent: a NOTIFICATION (Event 25) is answered with the FSM Error NOTIFICATION; a NOTIFICATION with a version error (Event 24) gets no answer; OpenConfirm answers no NOTIFICATION | GoBGP | GOBGP: COMPLIANT | fork booty.8 answers a NOTIFICATION in OpenSent with FSM Error 5/1 and goes on with the outgoing connection instead of 5 s Idle (`fsm.go:1708`); booty.10 sends no answer to a version error (`fsm.go:932`, `isVersionError`). GoBGP tests `TestNotificationInsteadOfOpen`, `TestOpenSentVersionErrorGetsNoAnswer`; lab `TestProdTplCarrierLossPastBFD`. The tag booty.7 does not answer a NOTIFICATION in OpenSent and must not be used (`docs/networking.md`) | lab, GoBGP test |
| 4271-33 | 8.1.1, 8.2.2 | IdleHoldTimer (optional session attribute): the time in Idle before a new start | GoBGP, BOOTy | GOBGP: COMPLIANT | GoBGP holds a peer 5 s in Idle after a session end and IdleHoldTimeAfterReset after ResetPeer; BOOTy sets 1 s (`families.go:29`). The fork applies it also when ResetPeer finds no session (booty.1, [osrg/gobgp#3740](https://github.com/osrg/gobgp/issues/3740)) and for BFD Down (booty.4, `fsm.go:838`), and a reset shortens a running idle hold timer, at most to 5 s (booty.6, `fsm.go:1010`). GoBGP tests `TestResetPeerWhileAdminDownAppliesIdleHoldTimeAfterReset`, `TestResetPeerShortensRunningIdleHoldTimer`; BOOTy `TestPeersIdleHoldAfterReset` | test, GoBGP test |
| 4271-34 | 8.2.2 | Idle, ManualStart/AutomaticStart: start the ConnectRetryTimer and initiate a TCP connection | GoBGP, BOOTy | GOBGP: COMPLIANT | GoBGP waits a random 1.5 to 2 s before the first connect after Idle ([osrg/gobgp#3736](https://github.com/osrg/gobgp/issues/3736)); the RFC sets no delay, FRR connects at once. Fork option `ConnectAtOnce` (booty.3, default off, `fsm.go:1193`); BOOTy sets it with `BGP_CONNECT_AT_ONCE`, off by default because of 4271-31 (`families.go:39`). GoBGP test `TestFirstConnectAtOnce`; BOOTy `TestPeersIdleHoldAfterReset` checks the option on every peer | test, GoBGP test |

### RFC 4760: Multiprotocol extensions

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 4760-1 | 3 | Next hop encoding MUST identify the network layer protocol (by length) | GoBGP | GOBGP: COMPLIANT | `bgp.go` `PathAttributeMpReachNLRI.DecodeFromBytes` | reading |
| 4760-2 | 3 | Reserved octet MUST be 0, SHOULD be ignored | GoBGP | GOBGP: COMPLIANT | GoBGP encoder writes 0 | reading |
| 4760-3 | 3 | MP_REACH_NLRI UPDATE MUST carry ORIGIN and AS_PATH, and LOCAL_PREF in iBGP | BOOTy, GoBGP | COMPLIANT | ORIGIN from BOOTy (`overlay.go:2249`), AS_PATH and LOCAL_PREF by GoBGP (`path.go:361`, `:368`); `TestRFCWireType5`, `TestRFCWireType3`, `TestRFCWireType2` | test |
| 4760-4 | 3 | UPDATE with only MP NLRI SHOULD NOT carry NEXT_HOP; receiver SHOULD ignore it | BOOTy, GoBGP | COMPLIANT | EVPN paths carry only MP_REACH_NLRI (`overlay.go:2257`); `TestRFCWireType5`, `TestRFCWireType3`, `TestRFCWireType2` check that no NEXT_HOP attribute is present | test |
| 4760-5 | 3 | Same prefix not in more than one of NLRI, MP_REACH, MP_UNREACH | GoBGP | GOBGP: COMPLIANT | GoBGP encoder | reading |
| 4760-6 | 7 | Incorrect MP_REACH/MP_UNREACH: delete the routes of the AFI/SAFI, SHOULD ignore further ones, SHOULD terminate | GoBGP | GOBGP: COMPLIANT | AFI/SAFI disable (`fsm.go:1510`, `:1602`), refined by RFC 7606 §7.11 | reading |
| 4760-7 | 8 | Capability advertisement for each AFI/SAFI; both sides MUST advertise for a bi-directional exchange | GoBGP, BOOTy | GOBGP: COMPLIANT | `fsm.go:1357`; BOOTy sets the families per peer (`families.go:102` to `:146`, `:221`); lab `TestGoBGPDualEVPNOnNumberedOnly`, `TestProdTplOpenCapabilities` | lab |
| 4760-8 | 8 | Unadvertised families not sent | GoBGP | GOBGP: COMPLIANT | `validate.go:95` rejects families not in the session | reading |
| 4760-9 | 3 | MP_REACH next hop for EVPN: the VTEP address | BOOTy | COMPLIANT | `overlay.go:2257`; `TestRFCWireType5`, `TestRFCWireType3`, `TestRFCWireType2` | test |

### RFC 6793: 4-octet AS numbers

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 6793-1 | 4.1 | Speaker SHALL advertise the 4-octet AS capability with its AS | GoBGP | GOBGP: COMPLIANT | `fsm.go:1359`; lab `TestProdTplOpenCapabilities` (`4byteAs`) | lab |
| 6793-2 | 4.1 | Use the capability AS instead of My AS | GoBGP | GOBGP: COMPLIANT | `validate.go:297` | reading |
| 6793-3 | 4.1 | 4-octet AS_PATH and AGGREGATOR between NEW speakers; no AS4_PATH/AS4_AGGREGATOR between NEW speakers; discard them if received | GoBGP | GOBGP: COMPLIANT | `internal/pkg/table/message.go:96` | reading |
| 6793-4 | 4.2.1 | AS_TRANS in My AS for an AS above 65535 | GoBGP | GOBGP: COMPLIANT | `fsm.go:1443` | reading |
| 6793-5 | 4.2.2, 4.2.3 | OLD speaker interworking (AS4_PATH merge, confed segments excluded, malformed AS4 attributes discarded and logged) | GoBGP | GOBGP: COMPLIANT | `message.go:96` to `:239` | reading |
| 6793-6 | 6 | Malformed AS4_PATH/AS4_AGGREGATOR from OLD speaker discarded, logged | GoBGP | GOBGP: COMPLIANT | `message.go:159` to `:181` | reading |
| 6793-7 | 4.1 | No confed segments in AS4_PATH | GoBGP | GOBGP: COMPLIANT | `message.go:159` | reading |
| 6793-8 | n/a | 4-octet ASN in BOOTy's RD and RT encodings (RFC 5668 4-octet AS specific type) | BOOTy | COMPLIANT | RD type 2 and RT type 0x02 for AS above 65535 (`overlay.go:2180`, `:2464`); `TestBuildRouteDistinguisher`, `TestBuildType3PathAttrs4ByteASN` | test |
| 6793-9 | n/a | No silent truncation of a 4-octet ASN RD/RT value | BOOTy | COMPLIANT | VNI above 65535 with 4-octet ASN is rejected (`config.go:364`) | reading |
| 6793-10 | n/a | 4-octet local AS and remote AS for sessions | BOOTy | COMPLIANT | `families.go:45` to `:191` | reading |

### RFC 7911: ADD-PATH

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 7911-1 | 2 | Path Identifier unique per (prefix, neighbor); re-advertiser generates its own | GoBGP | GOBGP: COMPLIANT | GoBGP send side; BOOTy does not send multiple paths | reading |
| 7911-2 | 3 | NLRI extended with a 4-octet Path Identifier when negotiated | GoBGP | GOBGP: COMPLIANT | `bgp.go` `decodePathIdentifier` | reading |
| 7911-3 | 4 | One ADD-PATH capability for all AFI/SAFIs; unknown Send/Receive values ignored | GoBGP | GOBGP: COMPLIANT | `fsm.go:1316` | reading |
| 7911-4 | 5 | Send multiple paths only if both sides negotiated (2/3 and 1/3) | GoBGP, BOOTy | GOBGP: COMPLIANT | BOOTy configures receive only (`families.go:215`); lab `TestProdTplOpenCapabilities` (`addPath`) | lab |
| 7911-5 | 5 | Withdraw of an unknown Path Identifier SHOULD be ignored | GoBGP | GOBGP: COMPLIANT | GoBGP adj-in lookup by (prefix, id) | reading |
| 7911-6 | 5 | SHOULD include the best route when advertising several | GoBGP | N/A | BOOTy does not send add-path | reading |
| 7911-7 | n/a | Received paths installed with ECMP | BOOTy | COMPLIANT | FIB writer takes equal-cost paths of a destination (`fibselect.go:101`), `TestSelectRoutes*` | test |
| 7911-8 | draft | Paths-Limit capability (draft-abraitis-idr-addpath-paths-limit, not an RFC): the most paths per prefix a receiver wants, 0 for no limit | GoBGP | GOBGP: COMPLIANT | fork booty.3 sends limit 0 for the families that receive with ADD-PATH, like FRR (`fsm.go:1425`). Received limits are not applied to ADD-PATH send, which BOOTy does not use. GoBGP tests `TestCapPathsLimit`, `TestCapabilitiesPathsLimit` | GoBGP test |

### RFC 8950 (and RFC 5549): IPv4 NLRI with an IPv6 next hop

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 8950-1 | 3 | Receiver MUST use the next hop length to find the protocol | GoBGP | GOBGP: COMPLIANT | `bgp.go` `PathAttributeMpReachNLRI.DecodeFromBytes` (16 or 32 octets) | reading |
| 8950-2 | 4 | Advertise the Extended Next Hop Encoding capability (code 5) | GoBGP | GOBGP: COMPLIANT | `fsm.go:1402`; lab `TestProdTplRRSessionEchoAndENHE` in [#710] (open) | reading |
| 8950-3 | 4 | MUST only advertise IPv4 NLRI with an IPv6 next hop if the peer advertised the capability for the AFI/SAFI | GoBGP | GOBGP: COMPLIANT | RFC8950-ENHE-CHECK at the first audit (GoBGP 3.37.0 and 4.10.0 never checked the peer's capability). Fork booty.1 turns such a path into a withdrawal (`server.go:729`, `postFilterpath`); GoBGP test `TestPostFilterpathExtendedNexthop` | GoBGP test |
| 8950-4 | 3, 4 | Capability tuples for IPv4 NLRI only | GoBGP | GOBGP: COMPLIANT | RFC8950-ENHE-EVPN at the first audit (GoBGP also listed L2VPN EVPN, and FRR logged a warning per OPEN). Fork booty.1 sends tuples only for the IPv4 families of RFC 8950 (`fsm.go:1410`); GoBGP test `TestCapabilitiesExtendedNexthopIPv4Only` | GoBGP test |
| 8950-5 | 5 | Over an IPvx session, own next hop SHOULD be IPvx | GoBGP | COMPLIANT | unnumbered sessions run over IPv6 link-local and advertise IPv6 next hops (`underlay.go:1013`); numbered IPv4 sessions use IPv4 | reading |
| 8950-6 | 5 | Next hop passed along unchanged keeps its encoding | GoBGP | N/A | BOOTy is no route reflector | reading |
| 8950-7 | 3 | IPv4 route with an IPv6 (link-local) next hop installed in the kernel | BOOTy | COMPLIANT | `RTA_VIA` in `fib.go:292`; lab `TestProdTplRFC5549Routes`, `TestProdTplFIBRoutes` | lab |
| 8950-8 | 3 | Link-local next hop bound to its interface | BOOTy | COMPLIANT | `fibselect.go` `fibNexthop.Dev` from the peer interface (`fib.go:276`) | reading |

### RFC 4724: Graceful restart (helper)

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 4724-1 | 3 | Only one GR capability; receiver uses the last | GoBGP | GOBGP: COMPLIANT | `fsm.go:1396`, `:756` | reading |
| 4724-2 | 3 | Reserved bits zero | GoBGP | GOBGP: COMPLIANT | GoBGP encoder | reading |
| 4724-3 | 3, 4.2 | Restart State bit MUST NOT be set unless restarted | GoBGP, BOOTy | GOBGP: COMPLIANT | helper only (`families.go:207`), `restarting` false | reading |
| 4724-4 | 4 | End-of-RIB MUST be sent after the initial update of each family (when GR is negotiated) | GoBGP | GOBGP: COMPLIANT | `server.go:1154` | reading |
| 4724-5 | 4.1 | Restarting Speaker procedures (keep forwarding state, defer selection, selection deferral timer) | BOOTy | N/A | BOOTy is helper only and keeps no forwarding state; it sends no family tuples, so peers do not keep its routes (`families.go:199`) | reading |
| 4724-6 | 4.2 | Receiving Speaker MUST retain the routes of a restarting peer and mark them stale | GoBGP | GOBGP: COMPLIANT | `server.go:1801`, `internal/pkg/table/adj.go:298`; lab `TestProdTplRRRestart` | lab |
| 4724-7 | 4.2 | Previously stale routes MUST be deleted on consecutive restarts; stale routes deleted after Restart Time | GoBGP | GOBGP: COMPLIANT | GoBGP restart timer | reading |
| 4724-8 | 4.2 | Stale routes replaced by updates; removed at End-of-RIB | GoBGP | GOBGP: COMPLIANT | GoBGP EOR handling (`server.go:1975` to `:2211`) | reading |
| 4724-9 | 4.2 | No difference between stale and other routes in forwarding | BOOTy | COMPLIANT | FIB writer installs best paths regardless of the stale flag (`fib.go:181`) | reading |
| 4724-10 | 4.2 | A new connection from a restarting peer MUST close the old session and keep the new one | GoBGP | GOBGP: COMPLIANT | RFC4724-NEWCONN at the first audit: GoBGP 3.37.0 and 4.10.0 closed the new connection in Established, so the old session lasted until BOOTy's next KEEPALIVE drew an RST or BFD ended it. Fork booty.1 ends the old session with the graceful restart reason, keeps its routes as stale and takes the new connection (`fsm.go:2404`); GoBGP test `TestNewConnectionFromRestartingPeer` | GoBGP test |
| 4724-11 | 5 | Second connection tracked until OPEN | GoBGP | GOBGP: COMPLIANT | see 4271-17 (collision handling of the fork) | reading |
| 4724-12 | n/a | RFC 8538 N bit (Notification support) | GoBGP | N/A | not in the RFC scope of this audit; [#744] (merged) sets the N bit like FRR | reading |

### RFC 7313: Enhanced route refresh

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 7313-1 | 4 | SHOULD advertise the capability when supported | GoBGP | GOBGP: COMPLIANT | fork booty.3 sends the Enhanced Route Refresh capability next to Route Refresh, like FRR (`fsm.go:1340`); GoBGP tests `Test_EnhancedRouteRefreshCapability`, `TestEnhancedRouteRefresh` | GoBGP test |
| 7313-2 | 4 | Sender: when both sides advertised it, the routes of a refresh go between BoRR and EoRR | GoBGP | GOBGP: COMPLIANT | fork booty.3: the refresh goes out between BoRR and EoRR; a queued EoRR goes out after the UPDATEs queued before it (`server.go:1261`); GoBGP test `TestEnhancedRouteRefresh` | GoBGP test |
| 7313-3 | 4 | Receiver: BoRR marks the routes of the family stale, EoRR removes those not sent again; a local bound for a missing EoRR | GoBGP | GOBGP: COMPLIANT | fork booty.3 (`server.go:1280`); the bound is the graceful restart stale-routes-time, else 360 s like FRR's stalepath-time (`server.go:1325`); GoBGP tests `TestEnhancedRouteRefresh`, `TestEnhancedRouteRefreshNoEoRR` | GoBGP test |
| 7313-4 | 4 | A BoRR from a graceful restart peer before its End-of-RIB for the family MUST be ignored | GoBGP | GOBGP: COMPLIANT | `server.go:1290`; GoBGP test `TestEnhancedRouteRefreshBoRRBeforeEoR` | GoBGP test |
| 7313-5 | 5 | Unknown ROUTE-REFRESH subtypes ignored; BoRR and EoRR without the negotiated capability ignored | GoBGP | GOBGP: COMPLIANT | `server.go:1305` | reading |

### RFC 2918: Route refresh

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 2918-1 | 3 | Advertise the Route Refresh capability to request refreshes | GoBGP | GOBGP: COMPLIANT | `fsm.go:1339`; lab `TestProdTplOpenCapabilities` (`routeRefresh`) | lab |
| 2918-2 | 4 | On ROUTE-REFRESH, re-advertise the Adj-RIB-Out of the AFI/SAFI | GoBGP | GOBGP: COMPLIANT | `server.go:1261` | reading |
| 2918-3 | 4 | Ignore a request for an AFI/SAFI not advertised (should) | GoBGP | GOBGP: COMPLIANT | `server.go:1267` | reading |
| 2918-4 | 4 | Soft reconfiguration of policies without session reset | BOOTy | COMPLIANT | `routemap.go:339` soft reset after installing policies | reading |

### RFC 8654: Extended message

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 8654-1 | 3, 4 | Capability; messages up to 65535 octets only after both sides advertised it | GoBGP | GOBGP: COMPLIANT | GoBGP 4 sends the capability in every OPEN (`fsm.go:1354`) and raises the limit only when the peer sent it too (`fsm.go:731`, `:1543`); GoBGP tests `TestExtendedMessage_AdvertisedUnconditionally`, `TestIsExtendedMessageSerialization`. Not used by BOOTy's routes | GoBGP test |
| 8654-2 | 5 | MUST NOT accept or send messages above 4096 octets without the capability | GoBGP | GOBGP: COMPLIANT | `bgp.go:17520` (`BGP_MAX_MESSAGE_LENGTH`), `fsm.go:1543` | reading |

### RFC 9234: BGP roles

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 9234-1 | 4 | Roles SHOULD be configured on eBGP sessions; capability when configured | BOOTy | N/A | data center fabric sessions, no provider/customer relation; BOOTy configures no roles | reading |
| 9234-2 | 5 | OTC attribute procedures | GoBGP | N/A | only with roles | reading |

### RFC 5492: Capabilities

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 5492-1 | 3 | Unknown capabilities MUST be ignored, no Unsupported Capability NOTIFICATION | GoBGP | GOBGP: COMPLIANT | unknown codes decode as `CapUnknown` | reading |
| 5492-2 | 4 | Accept multiple instances and multiple Capabilities parameters | GoBGP | GOBGP: COMPLIANT | `bgp.go` OPEN decoder | reading |
| 5492-3 | 4 | Capabilities in one optional parameter (SHOULD) | GoBGP | GOBGP: COMPLIANT | `fsm.go:1439` | reading |
| 5492-4 | 3 | Unsupported Capability data lists the capabilities | GoBGP | GOBGP: COMPLIANT | GoBGP does not require capabilities of peers | reading |
| 5492-5 | 4 | No duplicate capability instances (SHOULD NOT) | GoBGP | GOBGP: COMPLIANT | `fsm.go:1336` builds one of each | reading |

### RFC 7606: Revised error handling for UPDATE messages

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 7606-1 | 2, 3 | Treat-as-withdraw instead of session reset for attribute errors | GoBGP | GOBGP: COMPLIANT | on by default (`gobgp@v4.10.0-booty.10/pkg/config/oc/default.go:120`), handled in `fsm.go:1495` (`handlingError`) | reading |
| 7606-2 | 3 (c), (g) | Duplicate attributes: discard all but the first; duplicate MP_REACH/MP_UNREACH: session reset | GoBGP | GOBGP: COMPLIANT | `validate.go:29` to `:46` | reading |
| 7606-3 | 3 (d) | Missing well-known mandatory attribute: treat-as-withdraw | GoBGP | GOBGP: COMPLIANT | `validate.go:52` to `:72` | reading |
| 7606-4 | 7.x | Per-attribute handling (ORIGIN, AS_PATH, NEXT_HOP, MED, LOCAL_PREF, communities: treat-as-withdraw; ATOMIC_AGGREGATE, AGGREGATOR: attribute discard; MP_REACH/MP_UNREACH: AFI/SAFI disable) | GoBGP | GOBGP: COMPLIANT | `bgp.go:17702` | reading |
| 7606-5 | 5.3 | A malformed NLRI in MP_REACH_NLRI ends in session reset or AFI/SAFI disable | GoBGP | GOBGP: COMPLIANT | RFC7606-EVPN-PANIC at the first audit: GoBGP 3.37.0 panicked in the EVPN route type decoders for short route types 1 to 4, before any check of the negotiated families, so any BGP peer could end BOOTy (PID 1). GoBGP 4.10.0 ([#699]) returns a MessageError (`bgp.go:2870` and the other decoders); `TestEVPNDecodeTruncatedNLRIReturnsError` pins it. Defence in depth: [#765] (open) reboots after a kernel panic (`panic=10`) | test |
| 7606-6 | 5.4 | Routes with unrecognized NLRI types of a typed family (EVPN) MUST be discarded | GoBGP | GOBGP: COMPLIANT | RFC7606-TYPED-NLRI at the first audit (an unknown EVPN route type disabled L2VPN EVPN on the session, in 3.37.0 and 4.10.0). Fork booty.1 skips the NLRI by its length (`bgp.go:3485`); GoBGP tests `Test_MpReachNLRIDiscardsUnknownEVPNRouteType`, `Test_DiscardedEVPNWithdrawIsNotEndOfRib` | GoBGP test |
| 7606-7 | n/a | BOOTy's own route processing does not fail on bad input | BOOTy | COMPLIANT | `processRouteUpdate` skips undecodable NLRIs and attributes (`overlay.go:848`), `TestProcessRouteUpdateGuardAndL2Branches`, `TestRouterMACInCommunitiesIgnoresUnmarshalFailures` | test |
| 7606-8 | 8 | Errors SHOULD be logged | GoBGP, BOOTy | COMPLIANT | GoBGP logs "treated as withdraw" (`fsm.go` `handlingError`); BOOTy logs skipped routes | reading |
| 7606-9 | 7.14 | An unrecognized Extended Community type or sub-type MUST NOT be treated as an error | GoBGP | GOBGP: COMPLIANT | GoBGP 4.10.0 treated an unknown EVPN sub-type as malformed (treat-as-withdraw): FRR's MAC/IP routes with the ARP/ND community (sub-type 0x08) were withdrawn. Fork booty.2 keeps it as `UnknownExtended` (`bgp.go:14909`) and decodes the ARP/ND community (9047-1); GoBGP test `Test_UnknownEvpnExtended_RoundTrip` | GoBGP test |

### RFC 9384: Cease NOTIFICATION subcode for BFD

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 9384-1 | 4 | A speaker that ends a session because BFD went down sends Cease with subcode 10 (BFD Down) | BOOTy, GoBGP | COMPLIANT | BOOTy resets the peer with `ResetPeerRequest.BfdDown` (`gobgp/bfd.go:309`), a fork feature (booty.3, booty.4); before, GoBGP sent Cease/Administrative Reset (subcode 4). Like FRR (`bgp_bfd.c`). GoBGP tests `TestResetPeerBfdDown`, `TestCeaseBFDDown`; BOOTy `TestResetPeerReestablishes` | test, GoBGP test |
| 9384-2 | 4, 5 | BFD Down is a Hard Reset (RFC 8538) only where RFC 8538 requires one; the subcode has no impact on the FSM | GoBGP | GOBGP: COMPLIANT | fork booty.3 keeps BFD Down a normal Cease, so a graceful restart peer keeps its routes as stale (`bgp.go:17438`, `ShouldHardReset`); FRR treats it as a Hard Reset. GoBGP test `TestCeaseBFDDown` | GoBGP test |

## BFD

BOOTy implements BFD itself (`pkg/network/bfd`): asynchronous mode, no
Demand mode, no Echo function, no authentication.

### RFC 5880: BFD

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 5880-1 | 4.1 | Version 1; Length; field layout | BOOTy | COMPLIANT | `packet.go:74`; `TestPacketRoundTripAndChecks`, `TestRFC5880PacketFields` | test |
| 5880-2 | 4.1 | Multipoint bit MUST be zero on transmit and receipt | BOOTy | COMPLIANT | never set (`packet.go:78`), discarded on receipt (`packet.go:143`) | reading |
| 5880-3 | 4.2 to 4.4, 6.7 | Authentication sections and procedures | BOOTy | N/A | not implemented; packets with the A bit are discarded (`packet.go:149`). See 5880-39 | reading |
| 5880-4 | 5 | Echo packets SHOULD be authenticated | BOOTy | N/A | no Echo function | reading |
| 5880-5 | 6.1 | Active role MUST send packets without having received any; at least one side Active | BOOTy | COMPLIANT | the session loop sends from the start (`manager_linux.go:209`), `TestManagerAgainstFakeBFDD` | test |
| 5880-6 | 6.1 | Passive role MUST NOT send before receiving | BOOTy | N/A | BOOTy is always Active | reading |
| 5880-7 | 6.3, 6.8.1 | Local discriminator nonzero, unique, SHOULD be random | BOOTy | COMPLIANT | `manager_linux.go:198` | reading |
| 5880-8 | 6.4 | SHOULD advertise the lowest Required Min RX/Echo RX it can | BOOTy | COMPLIANT | configured value (profile); Echo RX 0 without echo. [#710] (open) advertises 50 ms Echo RX like FRR | reading |
| 5880-9 | 6.5 | Poll and Final never both set; Poll on the periodic packets, no extra packets | BOOTy | COMPLIANT | `session.go:201`; `TestSessionPollFinal` | test |
| 5880-10 | 6.6, 6.8.7 | Demand bit only with Demand mode, both Up | BOOTy | COMPLIANT | never set (`packet.go:78`) | reading |
| 5880-11 | 6.6 | Demand mode procedures (Poll on change) | BOOTy | N/A | BOOTy does not use Demand mode | reading |
| 5880-12 | 6.8.1 | Session state preserved for a detection time after the last packet | BOOTy | COMPLIANT | sessions persist until Close/Replace | reading |
| 5880-13 | 6.8.1 | SessionState and RemoteSessionState initialized Down; LocalDiag 0 | BOOTy | COMPLIANT | `session.go:59` | reading |
| 5880-14 | 6.8.1 | RemoteDiscr initialized 0, MUST be set to 0 after a detection time without packets | BOOTy | COMPLIANT | RFC5880-REMOTEDISCR at the first audit (a Down session kept a stale value). [#749]: every state forgets the remote session after a detection time (`session.go:154`, `CheckTimeout`); `TestSessionForgetsRemoteAfterDetectionTime` | test |
| 5880-15 | 6.8.1, 6.8.3 | DesiredMinTxInterval at least 1 s while not Up | BOOTy | COMPLIANT | `session.go:18`, `:61`, `:138`; `TestSessionPollFinal`, `TestRFC5880PacketFields` | test |
| 5880-16 | 6.8.1 | RemoteMinRxInterval initialized to 1 | BOOTy | COMPLIANT | `session.go:62` | reading |
| 5880-17 | 6.8.1 | DetectMult nonzero | BOOTy | COMPLIANT | `manager_linux.go:103` | reading |
| 5880-18 | 6.8.3 | Change of DesiredMinTx or RequiredMinRx MUST start a Poll Sequence | BOOTy | COMPLIANT | `session.go:124`; `TestSessionPollFinal` | test |
| 5880-19 | 6.8.3 | Increased DesiredMinTx in Up: old interval until the Poll Sequence ends | BOOTy | COMPLIANT | BOOTy only lowers it at Up (`session.go:127`) | reading |
| 5880-20 | 6.8.3 | Reduced RequiredMinRx in Up: previous value for the detection time until the Poll Sequence ends | BOOTy | COMPLIANT | `curRx` (`session.go:48`, `:89`); `TestSessionDetectionTime` | test |
| 5880-21 | 6.8.3 | Reduced remote Required Min RX: new interval at once, next packet as soon as practicable | BOOTy | COMPLIANT | `rearm` (`manager_linux.go:229`); `TestManagerFastTxAfterUp` | test |
| 5880-22 | 6.8.3 | Echo active: RequiredMinRx at least 1 s (SHOULD) | BOOTy | N/A | no Echo | reading |
| 5880-23 | 6.8.3 | Other timer changes take effect at once | BOOTy | COMPLIANT | `TxInterval`/`DetectionTime` computed per packet | reading |
| 5880-24 | 6.8.4 | Detection time = remote DetectMult x max(RequiredMinRx, remote DesiredMinTx); Init/Up to Down with diag 1 | BOOTy | COMPLIANT | `session.go:145`, `:154`; `TestSessionDetectionTime` | test |
| 5880-25 | 6.8.4, 6.8.5 | Demand mode and Echo detection | BOOTy | N/A | not used | reading |
| 5880-26 | 6.8.6 | Discard: version, length (24/26, payload), DetectMult 0, Multipoint, My Discriminator 0, Your Discriminator 0 in Init/Up, A bit without auth | BOOTy | COMPLIANT | `packet.go:110` to `:152`; `TestPacketRoundTripAndChecks` | test |
| 5880-27 | 6.8.6 | Nonzero Your Discriminator selects the session; no session: discard | BOOTy | COMPLIANT | `manager_linux.go:349`; `TestSessionIgnoresOtherDiscriminator` | test |
| 5880-28 | 6.8.6 | Zero Your Discriminator: select by source, interface | BOOTy | COMPLIANT | per device listener and source address (`manager_linux.go:352`) | reading |
| 5880-29 | 6.8.6 | Store RemoteDiscr, RemoteState, RemoteDemandMode, RemoteMinRx | BOOTy | COMPLIANT | RFC5880-DEMAND at the first audit (the D bit was not kept). [#749] keeps it (`session.go:88`); `TestSessionNoPeriodicPacketsInRemoteDemandMode` | test |
| 5880-30 | 6.8.6 | Required Min Echo RX 0: echo transmission MUST cease | BOOTy | COMPLIANT | BOOTy never sends echo | reading |
| 5880-31 | 6.8.6 | Final received during a Poll Sequence ends it | BOOTy | COMPLIANT | `session.go:89` | reading |
| 5880-32 | 6.8.6 | State machine of section 6.2 (AdminDown, Down, Init, Up transitions, diag 3) | BOOTy | COMPLIANT | `session.go:106`; `TestSessionTransitions` | test |
| 5880-33 | 6.8.6, 6.8.7 | Remote Demand mode active (both sides Up): MUST cease periodic transmission | BOOTy | COMPLIANT | RFC5880-DEMAND at the first audit. [#749] stops the periodic packets while the peer's D bit is set and both sides are Up and no Poll runs (`session.go:176`); [#768] pins the Up condition; `TestSessionNoPeriodicPacketsInRemoteDemandMode` | test |
| 5880-34 | 6.8.7 | Interval = max(DesiredMinTx, RemoteMinRx), jitter 0 to 25 %, 10 to 25 % with DetectMult 1 | BOOTy | COMPLIANT | `session.go:172`, `:216`; `TestJitter` | test |
| 5880-35 | 6.8.7 | No periodic packets when RemoteMinRx is 0 | BOOTy | COMPLIANT | `session.go:173`; `TestSessionNoPacketsWhenRemoteRxIsZero` | test |
| 5880-36 | 6.8.7 | Poll received: Final as soon as practicable, without Poll | BOOTy | COMPLIANT | `manager_linux.go:271`, `session.go:201`; `TestSessionPollFinal` | test |
| 5880-37 | 6.8.7 | Packet contents: Diag, State, P, F, C (0 for a control plane implementation), A 0, D 0, M 0, DetectMult, Length, discriminators, intervals, Echo RX 0 without echo | BOOTy | COMPLIANT | `session.go:187`; `TestRFC5880PacketFields` | test |
| 5880-38 | 6.8.7 | SHOULD send a packet between periodic packets when the contents change | BOOTy | COMPLIANT | `manager_linux.go:271` sends at once on a state change | reading |
| 5880-39 | 9, 6.7 | Authentication over multiple hops SHOULD be used; implementations supporting authentication MUST support both SHA1 types | BOOTy | NOT IMPLEMENTED | no authentication (like the legacy FRR profile). Needed only for multihop sessions over untrusted paths | reading |
| 5880-40 | 6.8.15 | Forwarding plane reset: diag 4, Down | BOOTy | N/A | BOOTy keeps no independent forwarding plane | reading |
| 5880-41 | 6.8.16 | Entering AdminDown: SHOULD send packets for a detection time | BOOTy | DEVIATION | RFC5880-ADMINDOWN: one AdminDown packet at Close (`manager_linux.go:240`). [#723] (merged) stops BGP and BFD before kexec and reboot, with the same single packet. Impact low. Open | reading |
| 5880-42 | 6.8.18 | After a detection time without packets, RemoteMinRx SHOULD return to 1 | BOOTy | COMPLIANT | RFC5880-REMOTEDISCR at the first audit. [#749] (`session.go:154`); `TestSessionForgetsRemoteAfterDetectionTime` | test |
| 5880-43 | 7 | Multihop: congestion control MUST be implemented | BOOTy | DEVIATION | RFC5880-CONGESTION: none. Impact low (a few sessions per host). Open | reading |
| 5880-44 | 9 | Single hop: TTL 255 on transmit, checked on receipt | BOOTy | COMPLIANT | see 5881-9, 5881-10 | reading |

### RFC 5881: BFD for IPv4 and IPv6 (single hop)

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 5881-1 | 2 | Separate session per network-layer path and per protocol | BOOTy | COMPLIANT | one session per BGP neighbor and family (`gobgp/bfd.go`) | reading |
| 5881-2 | 2 | Echo: no ingress filtering on the interface | BOOTy | COMPLIANT | BOOTy loops back peer echo packets through forwarding with rp_filter off (`docs/networking.md`); relevant with [#710] (open) | reading |
| 5881-3 | 2, 4 | Echo transmission rules | BOOTy | N/A | BOOTy sends no echo | reading |
| 5881-4 | 3 | Both sides Active; Your Discriminator 0 packets associated by remote system, interface and protocol | BOOTy | COMPLIANT | `manager_linux.go:346` | reading |
| 5881-5 | 4 | Destination port 3784 | BOOTy | COMPLIANT | `manager_linux.go:27` | reading |
| 5881-6 | 4 | Source port 49152 to 65535, one per session | BOOTy | COMPLIANT | `manager_linux.go:418`; `TestRFC5881SourcePortAndTTL` | test |
| 5881-7 | 4 | Source port SHOULD be unique per system | BOOTy | COMPLIANT | one bound socket per session; two sessions bound to different devices could share a port number (rare) | reading |
| 5881-8 | 4 | Demultiplexing by the BFD mechanisms, not by port | BOOTy | COMPLIANT | by discriminator (`manager_linux.go:349`) | reading |
| 5881-9 | 5 | All packets sent with TTL/Hop Limit 255 | BOOTy | COMPLIANT | `manager_linux.go:438`; `TestRFC5881SourcePortAndTTL`, `TestManagerSingleHopTTL` | test |
| 5881-10 | 5 | Received packets with TTL other than 255 discarded | BOOTy | COMPLIANT | `manager_linux.go:326`; `TestManagerSingleHopTTL` | test |
| 5881-11 | 6 | Packets only over the one-hop path (bound to the interface) | BOOTy | COMPLIANT | `SO_BINDTODEVICE` (`manager_linux.go:453`) | reading |
| 5881-12 | 6 | Multiaccess: source and destination on the subnet | Kernel | COMPLIANT | kernel source selection on the bound interface | reading |
| 5881-13 | 6 | Point-to-point links: no session identification by source address | BOOTy | N/A | the fabric links are Ethernet (multiaccess rules apply) | reading |
| 5881-14 | 6 | Destination stays the configured address when the source changes | BOOTy | COMPLIANT | `manager_linux.go:121`; a changed link-local peer is a new session (`Replace`, `TestManagerReplace`) | test |
| 5881-15 | 7 | Authentication SHOULD be used | BOOTy | DEVIATION | no authentication (5880-39); the legacy FRR profile has none either. Impact low on point-to-point fabric links | reading |
| 5881-16 | 4 | IPv6 link-local sessions carry the zone | BOOTy | COMPLIANT | `manager_linux.go:121`; lab `TestProdTplBFDSessions` | lab |

### RFC 5883: BFD for multihop paths

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 5883-1 | 5 | Destination port 4784 | BOOTy | COMPLIANT | `manager_linux.go:28` | reading |
| 5883-2 | 3 | Echo MUST NOT be used over multiple hops | BOOTy | COMPLIANT | no echo | reading |
| 5883-3 | 4.3 | Unidirectional links: Active sender, Passive receiver | BOOTy | N/A | not used | reading |
| 5883-4 | 6 | Cryptographic authentication SHOULD be used over multihop paths | BOOTy | DEVIATION | none; TTL at least 254 is checked instead (`manager_linux.go:326`, like FRR `minimum-ttl`). Impact low | reading |

## EVPN

BOOTy is a VTEP without a local Ethernet segment, with one VNI
(`provision_vni`) and three modes: Type-5-only, Type-5 with gateway, and L2 (Type-2, Type-3 and
Type-5). The routes it originates are built in `overlay.go` and encoded
by GoBGP; `rfc_wire_test.go` checks them on the wire. The routes it
receives come from GoBGP's best paths (`watchRoutes`). Since [#767]
BOOTy processes the EAD (Type-1) routes of remote Ethernet segments.

### RFC 7432: BGP MPLS-based EVPN

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 7432-1 | 5 | ESI values and types (unique non-reserved ESI per ES) | BOOTy | N/A | single-homed, ESI 0 in every route (`overlay.go:2206`, `:2545`); `TestRFCWireType5`, `TestRFCWireType2` | test |
| 7432-2 | 6.1 | VLAN-based service: Ethernet Tag MUST be 0 | BOOTy | COMPLIANT | `overlay.go:2210`, `:2516`, `:2549`; `TestRFCWireType5`, `TestRFCWireType3`, `TestRFCWireType2` | test |
| 7432-3 | 6.2, 6.3 | VLAN bundle and VLAN-aware bundle services | BOOTy | N/A | one VNI per EVI | reading |
| 7432-4 | 7 | Route types 1 to 4 encoding | GoBGP | GOBGP: COMPLIANT | GoBGP encoders; BOOTy originates types 2, 3 and 5 only | reading |
| 7432-5 | 7, 7606 §5.4 | Unknown route types | GoBGP | GOBGP: COMPLIANT | discarded since fork booty.1, see 7606-6 | reading |
| 7432-6 | 7.2, 9.2.1 | MAC length 48, MAC 6 octets (802.1Q), IP 4 or 16 octets | BOOTy, GoBGP | COMPLIANT | `TestRFCWireType2` | test |
| 7432-7 | 7.5 | ESI Label extended community | BOOTy | N/A | multihoming only | reading |
| 7432-8 | 7.6 | ES-Import route target; RT constraint for it | BOOTy | N/A | multihoming only | reading |
| 7432-9 | 7.7 | MAC Mobility extended community, sequence number | GoBGP | GOBGP: COMPLIANT | GoBGP adds a sequence number one higher when BOOTy originates a MAC another PE advertised (`gobgp@v4.10.0-booty.10/pkg/server/server.go:2567`); BOOTy originates without one (first advertisement) | reading |
| 7432-10 | 7.8 | Default Gateway extended community | BOOTy | N/A | BOOTy is no default gateway of the EVI | reading |
| 7432-11 | 7.9 | RD MUST be the RD of the MAC-VRF, assigned per MAC-VRF, unique across MAC-VRFs on a PE | BOOTy | COMPLIANT | one RD per VNI (`overlay.go:2180`); one MAC-VRF | reading |
| 7432-12 | 7.9 | Type 1 RD RECOMMENDED | BOOTy | DEVIATION, fixed in [#710] (open) | RFC7432-RD: `ASN:VNI` (type 0, or type 2 above 65535, `overlay.go:2180`), the same on every host and for the MAC-VRF and the IP-VRF. Route reflectors treat it as opaque, so nothing breaks. [#710]: `<router-id>:<n>` like FRR, a separate RD for an L2VNI | reading |
| 7432-13 | 7.10.1 | RT Global Administrator MUST be the AS of the PE | BOOTy | N/A | the rule applies to RTs auto-derived from a VLAN ID; BOOTy's default RT is `ASN:VNI` (`overlay.go:2464`) or the configured RT (see 8365-4) | reading |
| 7432-14 | 7.10.1 | RT auto-derivation from the VLAN ID | BOOTy | N/A | VXLAN: see 8365-4 | reading |
| 7432-15 | 8.1 to 8.3, 8.5 | Ethernet Segment route, A-D per ES/EVI, split horizon, DF election | BOOTy | N/A | no multihoming | reading |
| 7432-16 | 8.4, 14 | A-D per EVI route MUST NOT be used before the A-D per ES routes; aliasing and backup paths (SHOULD) | BOOTy | COMPLIANT | RFC7432-ESI-RX at the first audit (Type-1 routes ignored). [#767]: a VTEP is active on an ES only with both its EAD-per-ES and its EAD-per-EVI route (`esi.go:142`, `activeVTEPs`), like FRR; aliasing through a kernel FDB nexthop group (`esi.go:168`); `TestType2ESIAliasingAndMassWithdraw`, `TestESINetlinkAliasing` | test |
| 7432-17 | 8.6 | Single-homing PEs SHOULD support some multihoming procedures to interoperate (receive side) | BOOTy | COMPLIANT | [#767], see 7432-16, 7432-21, 7432-43; `TestType2ESIAliasingAndMassWithdraw` | test |
| 7432-18 | 9.1 | Local data plane learning MUST be supported | Kernel | COMPLIANT | bridge port learning (default); VXLAN remote learning is off (`overlay.go:551`), control plane learning instead | reading |
| 7432-19 | 9.2.1 | RD per 7.9, MPLS Label1 downstream assigned (VNI per RFC 8365), next hop the PE, at least one RT | BOOTy | COMPLIANT | `overlay.go:2542`, `:2559`; `TestRFCWireType2`, `TestBuildType2PathAttrs` | test |
| 7432-20 | 9.2.2 | Reserved ESI (0 or MAX): forwarding state from the MAC/IP route alone | BOOTy | COMPLIANT | `handleType2Route` (`overlay.go:1695`); `TestHandleType2RouteInstallsFDB` | test |
| 7432-21 | 9.2.2 | Non-reserved ESI: forwarding state only with the A-D per ES routes | BOOTy | COMPLIANT | RFC7432-ESI-RX at the first audit. [#767]: the MAC is installed only through the active VTEPs of its ES and not while the ES has none (`esi.go:168`, `syncES`); `TestType2ESIAliasingAndMassWithdraw` | test |
| 7432-22 | 10 | IP Address Length 32 or 128 | BOOTy | COMPLIANT | `TestRFCWireType2` | test |
| 7432-23 | 10 | One MAC/IP route per IP of a MAC; withdraw on dissociation | BOOTy | COMPLIANT | one IP (provision IP); routes end with the BGP session (no GR tuples, `families.go:199`) | reading |
| 7432-24 | 10 | MAC/IP withdraw MUST NOT remove the MAC entry while the MAC-only route lives | BOOTy | COMPLIANT | RFC7432-MACIP-WITHDRAW at the first audit. [#748] keeps every live route of a MAC by route key and points the FDB entry at the best one (`overlay.go:1927`, `:1946`, `:1960`); `TestHandleType2RouteBestPathPerMAC` | test |
| 7432-25 | 10 | ARP proxy (SHOULD) | BOOTy | N/A | BOOTy is the end host of its BD; the kernel VXLAN `proxy` flag follows netplan | reading |
| 7432-26 | 10.1 | Default gateway procedures | BOOTy | N/A | see 7432-10 | reading |
| 7432-27 | 11 | Each PE MUST advertise an IMET route | BOOTy | COMPLIANT | L2 mode (`overlay.go:760`); `TestAdvertiseType3PublishesIMETWithIngressReplication`, `TestOverlaySetupAdvertisesType2And3OnlyWithL2`. Type-5 modes have no BD to flood | test |
| 7432-28 | 11.1 | RD per 7.9; originating router IP common to all EVIs; next hop the PE; RTs per 7.10 | BOOTy | COMPLIANT | `overlay.go:2513`, `:2524`; `TestRFCWireType3` | test |
| 7432-29 | 11.2 | PMSI Tunnel attribute MUST be present | BOOTy | COMPLIANT | `overlay.go:2529`; `TestRFCWireType3` | test |
| 7432-30 | 11.2 | Ingress replication: tunnel type IR, tunnel identifier a routable address of the PE, downstream assigned label (the VNI) | BOOTy | COMPLIANT | `TestRFCWireType3` | test |
| 7432-31 | 11.2 | Leaf Information Required MUST be 0 and MUST be ignored on receipt | BOOTy | COMPLIANT | flags 0 (`overlay.go:2530`); BOOTy reads only the tunnel type of a received PMSI attribute (`overlay.go:2087`); `TestRFCWireType3` | test |
| 7432-32 | 11.2 | P-multicast trees, aggregation | BOOTy | N/A | ingress replication only | reading |
| 7432-33 | 12, 13.1 | BUM traffic replicated to each remote PE (ingress replication) | Kernel | COMPLIANT | BUM FDB entries per Type-3 VTEP (`overlay.go:2120`) and to the gateway (`overlay.go:658`); `TestHandleType3RouteAppendsBUM`, lab `TestGoBGPUnnumberedGatewayFDB` | test, lab |
| 7432-46 | 11.2 | Ingress replication: BUM traffic goes to the routable address in the PMSI Tunnel Identifier | BOOTy | DEVIATION | RFC7432-PMSI-ID: BOOTy floods to the originating router IP of the IMET route (`overlay.go:2100`) and ignores the PMSI attribute. FRR 10.7.1 does the same (zebra gets the IMET originator as remote VTEP, `bgp_zebra_send_remote_vtep`), and in VXLAN fabrics both addresses are the VTEP. Kept like FRR; documented choice | reading |
| 7432-47 | 11.2 | Use the PMSI tunnel type of the IMET route | BOOTy | COMPLIANT | RFC8365-PMSI-RX at the first audit. [#772]: a head-end replication entry only for tunnel type ingress replication, like FRR (`overlay.go:2087`, `ingressReplication`); an update to another tunnel type removes it, where FRR keeps it ([#786] documents the difference); `TestProcessRouteUpdateType3TunnelType` | test |
| 7432-34 | 12, 13 | ESI label, P2MP LSP and split horizon procedures | BOOTy | N/A | VXLAN, single-homed | reading |
| 7432-35 | 14.1 | Load balancing through A-D routes (aliasing), single-active primary | BOOTy | COMPLIANT | [#767]: all-active ES through the FDB nexthop group of its active VTEPs; a single-active ES uses only the primary PE, the VTEP of the MAC route (`esi.go:131`); `TestType2ESIAliasingAndMassWithdraw` | test |
| 7432-36 | 15 | First advertisement without MAC Mobility community | BOOTy | COMPLIANT | `overlay.go:2559` | reading |
| 7432-37 | 15 | A moved MAC: sequence one higher than the received one | GoBGP | GOBGP: COMPLIANT | `server.go:2479`; BOOTy's MAC is its own bridge MAC | reading |
| 7432-38 | 15 | Receiver: the higher sequence number wins; equal: the lowest PE IP | BOOTy | COMPLIANT | RFC7432-MOBILITY-RX at the first audit (the last update won). [#748]: sticky first like FRR, then the higher sequence number, then the lowest VTEP (`overlay.go:1946`, `bestType2Path`); `TestHandleType2RouteBestPathPerMAC` | test |
| 7432-39 | 15 | Sequence number wraparound MUST be handled | BOOTy | COMPLIANT | [#748]: RFC 1982 serial arithmetic (`overlay.go:1671`); `TestHandleType2RouteBestPathPerMAC` | test |
| 7432-40 | 15 | "A PE receiving a MAC/IP Advertisement route for a MAC address with a different Ethernet segment identifier and a higher sequence number than that which it had previously advertised withdraws its MAC/IP Advertisement route." | GoBGP | GOBGP: DEVIATION | RFC7432-MOBILITY-GOBGP, an interpretation: GoBGP (`internal/pkg/table/table_manager.go:322`) applies it only when the ESI values differ, so two single-homed PEs (both ESI 0) never trigger it. The same section says that for single-homed sites "there is no need for ESI comparison", so reading "different Ethernet segment" as "different ESI value" is GoBGP's choice. Impact very low (BOOTy's MAC is its own bridge MAC and does not move) | reading |
| 7432-41 | 15.1 | Duplicate MAC detection (M, N configurable), alert | BOOTy | N/A | BOOTy learns no MACs locally | reading |
| 7432-42 | 15.2 | Sticky MAC: alert on local learning | BOOTy | N/A | BOOTy learns no MACs locally and sends no sticky MACs | reading |
| 7432-43 | 17.3 | Withdraw on MAC age out and on A-D withdrawal (mass withdraw SHOULD) | BOOTy | COMPLIANT | [#767]: an EAD withdraw takes its VTEP out of the group, so the MACs of the ES move at once (`esi.go:168`); BOOTy's own MAC is static and does not age out; `TestType2ESIAliasingAndMassWithdraw` | test |
| 7432-44 | 18 | Control word | BOOTy | N/A | MPLS only | reading |
| 7432-45 | 9.2.2, 10 | Withdraw without next hop still removes the forwarding state | BOOTy | COMPLIANT | Type-2: the withdraw removes the route by its key (`overlay.go:1755`); `TestHandleType2RouteWithdraw`; Type-5: `TestHandleType5RouteWithdrawUsesStoredRouterMAC` | test |

### RFC 8365: EVPN network virtualization overlay (VXLAN)

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 8365-1 | 5.1.1 | Globally unique VNIs in a DC | BOOTy | COMPLIANT | one configured VNI, range 1 to 16777215 checked (`config.go:356`) | reading |
| 8365-2 | 5.1.2 | Option 1: one BD per EVI, RD and RT per VNI | BOOTy | COMPLIANT | `overlay.go:2180`, `:2464` | reading |
| 8365-3 | 5.1.2 | Option 2: VLAN-aware bundle | BOOTy | N/A | | reading |
| 8365-4 | 5.1.2.1 | RT auto-derivation (A bit, type, D-ID, VNI) | BOOTy | COMPLIANT | auto-derivation is optional ("can be"); BOOTy uses `ASN:VNI` like FRR. Both ends must use the same rule | reading |
| 8365-5 | 5.1.3 | VLAN-based service: Ethernet Tag MUST be 0 in MAC/IP, A-D per EVI and IMET routes | BOOTy | COMPLIANT | `TestRFCWireType2`, `TestRFCWireType3` | test |
| 8365-6 | 5.1.3 | VLAN-aware bundle Ethernet Tag rules | BOOTy | N/A | | reading |
| 8365-7 | 5.1.3 | VNI carried in the MPLS label field (24 bits) | GoBGP, BOOTy | GOBGP: COMPLIANT | `gobgp@v4.10.0-booty.10/pkg/packet/bgp/bgp.go:2737` (no shift); `TestRFCWireType5`, `TestRFCWireType3`, `TestRFCWireType2` | test |
| 8365-8 | 5.1.3 | BGP Encapsulation extended community with all EVPN routes | BOOTy | COMPLIANT | RFC8365-ENCAP at the first audit (only on Type-5). [#746] adds it to Type-2 and Type-3 routes (`overlay.go:2501`, `vxlanEncapCommunity`); `TestEVPNRoutesCarryVXLANEncapsulation`, `TestRFCWireType2`, `TestRFCWireType3`, `TestRFCWireType5` | test |
| 8365-9 | 5.1.3 | Next hop MUST be the IPv4 or IPv6 address of the NVE | BOOTy | COMPLIANT | router ID (VTEP) in every route; `TestRFCWireType*` | test |
| 8365-10 | 5.1.3, 6 | Receiver uses the VNI of the route (and the advertised encapsulation) | BOOTy | DEVIATION, fixed in [#773] (open) | RFC8365-RX-VNI: the label of received Type-2 and Type-5 routes is ignored; the FDB entries use the VXLAN device VNI (`overlay.go:2120`). Impact only with different VNIs for the same BD/VRF. [#773] treats a Type-5 route with another VNI in L3VNI mode as withdrawn, like zebra; Type-2 and Type-3 keep the local VNI, as FRR does | reading |
| 8365-11 | 6 | Advertised encapsulations use the same EVPN procedures; never VXLAN and MPLS together | BOOTy | COMPLIANT | VXLAN only | reading |
| 8365-19 | 6 | An ingress NVE sends to an egress NVE only with an encapsulation that both support; without the BGP Encapsulation community the receiver assumes MPLS or a configured encapsulation | BOOTy | DEVIATION, fixed in [#771] (open) | RFC8365-ENCAP-RX: BOOTy ignores the received community, so a route that advertises only another encapsulation (for example MPLS) still gets VXLAN state. FRR 10.7.1 ignores it too (`bgp_evpn.c` `bgp_evpn_filter_ecommunity`) and treats a route without it as VXLAN. [#771] drops a route whose encapsulation communities do not include VXLAN and keeps FRR's handling of a route without the community | reading |
| 8365-12 | 5.2 | MPLS over GRE | BOOTy | N/A | | reading |
| 8365-13 | 8 | Multihoming, local bias, split horizon | BOOTy | N/A | single-homed | reading |
| 8365-14 | 9 | Each PE MUST advertise an IMET route for its tunnel type (ingress replication) | BOOTy | COMPLIANT | L2 mode; see 7432-27 | reading |
| 8365-15 | 9 | Receiver uses the PMSI tunnel type of the IMET route | BOOTy | COMPLIANT | [#772], see 7432-47; `TestProcessRouteUpdateType3TunnelType` | test |
| 8365-20 | 7 | Single-homing NVE in a hypervisor (BOOTy's role): no multihoming procedures needed | BOOTy | COMPLIANT | BOOTy originates no Type-1 or Type-4 routes and uses ESI 0 (`TestRFCWireType2`) | test |
| 8365-21 | 7.1 | RD unique per EVI or per NVE, recommended at all times | BOOTy | DEVIATION, fixed in [#710] (open) | see 7432-12 (RFC7432-RD): `ASN:VNI` is the same on every host | reading |
| 8365-22 | 7.1 | Routes and attributes of a hypervisor NVE: MAC/IP, IMET, MAC Mobility, Default Gateway | BOOTy | COMPLIANT | MAC/IP and IMET in L2 mode (`TestRFCWireType2`, `TestRFCWireType3`); MAC Mobility by GoBGP (7432-37); Default Gateway not needed (7432-10) | test |
| 8365-23 | 7.1, 7.2 | A single-homing ingress NVE should receive and process A-D per ES and per EVI routes (fast convergence, aliasing, backup path, RFC 7432 sections 8.2 and 8.4) | BOOTy | COMPLIANT | RFC7432-ESI-RX at the first audit. [#767], see 7432-16; `TestType2ESIAliasingAndMassWithdraw`, `TestESINetlinkAliasing` | test |
| 8365-24 | 7.2 | Procedures 1 to 5: local learning, advertising local MACs, remote learning per RFC 7432 section 9.2, IMET discovery, MAC mobility per section 15 | BOOTy | COMPLIANT | 1 and 2: the bridge MAC is the only local MAC and is advertised (`TestAdvertiseType2PublishesBridgeMACAndIP`); 3: see 7432-24 ([#748]); 4: 7432-27, 7432-33; 5: see 7432-38 ([#748]) | test |
| 8365-16 | 10.2.2 | ASBR functionality with multihoming NVEs (A-D withdrawal handling at an ASBR) | BOOTy | N/A | BOOTy is no ASBR; the receive side of A-D routes is 7432-16 | reading |
| 8365-17 | 5.1.3 | Route keys and withdraw processing for VXLAN routes | BOOTy | COMPLIANT | Type-5 key per RFC 9136 (`overlay.go:1190`); Type-2 routes by route key since [#748] (`overlay.go:1927`); `TestHandleType2RouteBestPathPerMAC`, `TestHandleType5RouteKeepsRouteWhileAnotherGatewayAdvertisesIt` | test |
| 8365-18 | 5.1.3, RFC 9012 | Tunnel type 8 (VXLAN) in the encapsulation community | BOOTy | COMPLIANT | `overlay.go:2496`; `TestRFCWireType5` | test |

### RFC 9135: Integrated routing and bridging in EVPN

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 9135-1 | 4 | Asymmetric IRB: Ethernet NVO tunnel | BOOTy | COMPLIANT | VXLAN with inner Ethernet | reading |
| 9135-2 | 4 | Asymmetric IRB: ARP entries for remote hosts, BTs for all subnets | BOOTy, Kernel | COMPLIANT | one subnet; the kernel resolves remote hosts by ARP through the BUM entries | reading |
| 9135-3 | 4.1 | IRB/anycast MAC rules | BOOTy | N/A | BOOTy is no IRB gateway; its bridge MAC is per host (`docs/networking.md`, Router MAC) | reading |
| 9135-4 | 5.1, 5.3, 5.4 | Symmetric IRB origination (two RTs, Label2, subnet RT-5) | BOOTy | N/A | BOOTy originates asymmetric MAC/IP routes (L2) and RT-5 host routes | reading |
| 9135-5 | 5.2, 6.2 | Asymmetric-only receiver: ignore Label2, install the MAC in the MAC-VRF | BOOTy | COMPLIANT | `overlay.go:1695` uses only the MAC | reading |
| 9135-6 | 5.2, 6.2 | Asymmetric-only receiver: install the (IP, MAC) association in the ARP/ND table | BOOTy | COMPLIANT | RFC9135-ARP-RX at the first audit. [#766] installs NOARP `extern_learn` neighbor entries on the bridge for the best route of each IP, like zebra (`overlay.go:1776`, `syncType2Neighbor`); [#784] shares the entry of a VTEP IP with the Type-5 gateway neighbor; `TestHandleType2RouteInstallsNeighbor`, `TestType2NeighborKeptByTheOtherRD`, `TestType2NeighborTiebreakOnMAC`, `TestType2AndType5ShareTheBridgeNeighbor` | test |
| 9135-7 | 6.1 | Asymmetric MAC/IP route: length 37/49, Label2 not included, MAC-VRF RT, set per RFC 7432 and RFC 8365 (which adds the BGP Encapsulation community) | BOOTy | COMPLIANT | `TestRFCWireType2` (length, labels, RT, encapsulation community of [#746]) | test |
| 9135-8 | 8.1 | Advertiser attaches a single Router's MAC community | BOOTy | COMPLIANT | `overlay.go:2237`; `TestRFCWireType5` | test |
| 9135-9 | 8.1 | Receiver processes the first Router's MAC community | BOOTy | COMPLIANT | `overlay.go:2396` returns the first | reading |
| 9135-10 | 9.1.1 | Router's MAC community MUST be added when the Ethernet NVO tunnel is used (symmetric IRB RT-2) | BOOTy | N/A | BOOTy originates asymmetric RT-2 only; the RT-5 rule is 9136-17 | reading |
| 9135-11 | 9.1.1 | Malformed RT-2 label and RT combinations of a symmetric IRB receiver MUST be treated as withdrawn | BOOTy | N/A | BOOTy is an asymmetric-only receiver: section 6.2 tells it to ignore Label2 (9135-5) | reading |
| 9135-12 | 5.4 | Label2 VNI mismatch | BOOTy | N/A | symmetric only | reading |
| 9135-13 | 4 | Inter-subnet traffic through the gateway | BOOTy | COMPLIANT | provision gateway route and BUM entry (`overlay.go:647`); lab `TestGoBGPUnnumberedGatewayRoute`, `TestGoBGPUnnumberedVXLANPingGateway` | lab |

### RFC 9136: IP prefix advertisement in EVPN (Type-5)

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 9136-1 | 3 | NVEs of a tenant on different BDs MUST support RT-5 | BOOTy | COMPLIANT | every mode originates and receives RT-5 | reading |
| 9136-2 | 3.1 | Length 34 or 58; prefix and gateway of the same family | BOOTy, GoBGP | COMPLIANT | `TestRFCWireType5`; IPv6 prefixes get gateway `::` (`l3vni.go:131`) | test |
| 9136-3 | 3.1 | RD and Ethernet Tag per RFC 7432/8365 (RD unique per VRF) | BOOTy | DEVIATION, fixed in [#710] (open) | see 7432-12 | reading |
| 9136-4 | 3.1 | ESI MUST be zero when it is no overlay index | BOOTy | COMPLIANT | `TestRFCWireType5` | test |
| 9136-5 | 3.1 | GW IP MUST be zero when it is no overlay index | BOOTy | COMPLIANT | `overlay.go:2505`, `l3vni.go:131`; `TestRFCWireType5` | test |
| 9136-6 | 3.1 | Prefix length at most 32/128 | BOOTy | COMPLIANT | originated from parsed prefixes; received lengths above are skipped (`overlay.go:2146`) | reading |
| 9136-7 | 3.1 | Label: the VNI; SHOULD be 0 with recursive resolution | BOOTy | COMPLIANT | label = VNI (interface-less model, no overlay index); `TestRFCWireType5` | test |
| 9136-8 | 3.1 | Received label 0: MUST contain an overlay index and be resolved, else treat-as-withdraw | BOOTy | DEVIATION, fixed in [#747] (open) | RFC9136-LABEL0: installed through the next hop. [#747] treats label 0 without an overlay index as withdrawn | reading |
| 9136-9 | 3.1 | Route key: RD, Ethernet Tag, prefix length, prefix | BOOTy | COMPLIANT | `overlay.go:1190`; `TestHandleType5RouteKeepsRouteWhileAnotherGatewayAdvertisesIt` | test |
| 9136-10 | 3.2 | ESI overlay index: usable only after an RT-1 resolves it | BOOTy | DEVIATION, fixed in [#747] (open) | RFC9136-ESI: installed through the next hop. [#747]: not usable, BOOTy has no RT-1 resolution of a Type-5 ESI | reading |
| 9136-11 | 3.2 | GW IP overlay index: usable only after an RT-2 with that IP resolves it; the Router's MAC is ignored | BOOTy | COMPLIANT | RFC9136-GWIP at the first audit. [#775]: like FRR, such a route is installed only with `enable-resolve-overlay-index` (`EVPN_RESOLVE_OVERLAY_INDEX`), in L2 mode, and only while a Type-2 route carries the gateway IP; the router MAC is ignored (`overlay.go:1127`, `resolveType5Gateway`). Behaviour change, documented in `docs/networking.md`. `TestType5GatewayIPResolvedByType2`, `TestType5GatewayIPResolutionKeepsTheActivePath`; [#799] (open) adds a test | test |
| 9136-12 | 3.2 | MAC overlay index (label 0 with Router's MAC) | BOOTy | N/A | optional ("support ... is OPTIONAL"); [#747] (open) does not use such routes | reading |
| 9136-13 | 3.2 | No IGP/BGP route to the BGP next hop: MUST NOT install | BOOTy | DEVIATION | RFC9136-NH-REACH: not checked; Type-5-only mode installs a /32 to the VTEP itself (`overlay.go:1323`). Impact low, by design. Open | reading |
| 9136-14 | 3.2 | ESI and GW IP both non-zero: SHOULD treat as withdraw | BOOTy | DEVIATION, fixed in [#747] (open) | RFC9136-ESIGW: installed. [#747] treats it as withdrawn | reading |
| 9136-15 | 3.2 | All of ESI, GW IP, MAC and label zero: SHOULD treat as withdraw | BOOTy | DEVIATION, fixed in [#747] (open) | RFC9136-LABEL0: installed through the next hop. [#747] treats label 0 without gateway IP as withdrawn | reading |
| 9136-16 | 3.2 | Invalid Router's MAC (multicast, broadcast, malformed): MUST treat as withdraw | BOOTy | DEVIATION, fixed in [#747] (open) | RFC9136-RMAC: `parseRouterMAC` checks only the length (`overlay.go:2270`), so a multicast or broadcast router MAC is installed as the gateway neighbor and FDB entry; a malformed one installs the route without neighbor (`overlay.go:873`). [#747]: withdraw for a multicast or broadcast router MAC, also with a gateway IP, like FRR; a zero router MAC counts as absent, like FRR | reading |
| 9136-17 | 4.4.1 | Interface-less IP-VRF-to-IP-VRF: GW IP 0, ESI 0, Router's MAC, label = L3 VNI | BOOTy | COMPLIANT | `TestRFCWireType5`; lab `TestProdTplType5Origination`, `TestType5RouteHasEncapAndRouterMAC` | test, lab |
| 9136-18 | 4.4.1 | Ingress: inner destination MAC = Router's MAC, VNI of the route | BOOTy, Kernel | COMPLIANT | neighbor gateway -> Router's MAC and FDB Router's MAC -> VTEP (`overlay.go:1341`); lab `TestType5VM0ImportsVM1`, `TestType5RemoteVMDataPlaneMatchers`. VNI: see 8365-10 ([#773], open) | lab |
| 9136-19 | 3 | Withdraw removes the route; another path of the prefix takes over | BOOTy | COMPLIANT | `overlay.go:1008`; `TestHandleType5RouteWithdrawDeletesOnlinkGatewayRoute`, `TestHandleType5RouteKeepsRouteWhileAnotherGatewayAdvertisesIt`, `TestHandleType5RouteFallsBackToPathWhoseInstallFailed` | test |
| 9136-20 | 3 | Own routes reflected back are not installed | BOOTy | COMPLIANT | `overlay.go:924`; `TestHandleType5RouteSelfRoute` | test |
| 9136-21 | 3.1 | GoBGP decodes the GW IP and prefix by NLRI length | GoBGP | GOBGP: COMPLIANT | `bgp.go:3249`, `:3290` | reading |

### RFC 9047: ARP/ND flags in EVPN

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 9047-1 | 2 | Receive the EVPN ARP/ND Extended Community (type 0x06, sub-type 0x08) with the R, O and I flags | GoBGP | GOBGP: COMPLIANT | fork booty.2 decodes it (`ArpNdExtended`); FRR sends it with the MAC/IP routes of IPv6 neighbors. GoBGP tests `Test_UpdateWithEvpnArpNdExtended`, `Test_ArpNdExtended` | GoBGP test |
| 9047-2 | 3.2 | An IPv6 MAC/IP route: MUST add the R and O flags to the ND entry; IPv4: MUST ignore them; I flag: SHOULD install an immutable binding | BOOTy | DEVIATION, fixed in [#798] (open) | RFC9047-FLAGS: the neighbor entries of [#766] (`overlay.go:1776`) carry no router flag. Impact low: a host sends no traffic to the remote IPv6 address as a router. [#798] sets `NTF_ROUTER` from the R flag like zebra. The I flag is not used, as in FRR | reading |

### RFC 6514: PMSI Tunnel attribute (as used by RFC 7432)

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 6514-1 | 5 | Attribute layout: flags, tunnel type, label, tunnel identifier | GoBGP | GOBGP: COMPLIANT | `bgp.go:16452`; `TestRFCWireType3` | test |
| 6514-2 | 5 | Ingress replication: tunnel identifier = unicast address of the originator | BOOTy | COMPLIANT | router ID (`overlay.go:2533`); `TestRFCWireType3` | test |
| 6514-3 | 5 | Leaf Information Required 0 for ingress replication | BOOTy | COMPLIANT | `TestRFCWireType3` | test |
| 6514-4 | 5 | Label field carries the VNI for VXLAN (RFC 8365 §5.1.3) | BOOTy | COMPLIANT | `TestRFCWireType3` | test |
| 6514-5 | 5 | Receiver: the Tunnel Identifier names the P-tunnel | BOOTy | DEVIATION | see 7432-46: BOOTy uses the originating router IP, like FRR. Kept | reading |

### RFC 7209: Requirements for EVPN

RFC 7209 states requirements on a solution, not on one implementation.
The rows map them to what BOOTy implements.

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 7209-1 | 4 (R1 to R6) | All-active and single-active multihoming, load balancing | BOOTy | N/A | single-homed host VTEP | reading |
| 7209-2 | 5 (R7) | No MAC learning against MP2MP LSPs | BOOTy | N/A | VXLAN | reading |
| 7209-3 | 6 (R8a) | Auto-discovery of member PEs | BOOTy | COMPLIANT | Type-3 IMET in L2 mode | reading |
| 7209-4 | 6 (R8b to R8e) | Redundancy group discovery, DF election, VLAN aware auto-provisioning | BOOTy | N/A | | reading |
| 7209-5 | 6 (R8f) | Default values for unconfigured parameters | BOOTy | COMPLIANT | `config.go:252`, FRR profile defaults | reading |
| 7209-6 | 7 (R9a) | Port, VLAN and VLAN bundle services | BOOTy | NOT IMPLEMENTED | VLAN-based service only; enough for the provisioning network | reading |
| 7209-7 | 8 (R10) | Convergence independent of the number of MACs | BOOTy | N/A | one MAC per host | reading |
| 7209-8 | 9 (R11) | Flooding control | BOOTy | COMPLIANT | VXLAN learning off, control plane FDB; BUM only to Type-3 VTEPs or the gateway | reading |
| 7209-9 | 10 (R12) | Flexible topologies, E-Tree, inter-AS | BOOTy | N/A | | reading |
| 7209-10 | 11 (R13, R14) | Duplicate MAC detection, sticky MAC | BOOTy | N/A | see 7432-41, 7432-42 | reading |

### RFC 8584: DF election framework

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 8584-1 | all | DF election FSM, DF Election extended community, HRW | BOOTy | N/A | no multihoming: BOOTy originates no ES routes and processes none | reading |

### RFC 9161: Proxy ARP/ND in EVPN

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 9161-1 | 3 | Proxy ARP/ND function (learning, reply, maintenance, duplicate IP detection) | BOOTy | N/A | a PE function for attached CEs; BOOTy is the end host. The kernel VXLAN `proxy` flag is only set from netplan (`devices.go:44`) and has no EVPN-learned entries (see 9135-6) | reading |

### RFC 9014: Interconnect solution for EVPN overlay networks

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 9014-1 | all | Gateway procedures (I-ESI, DF, separate sessions, VNI translation) | BOOTy | N/A | BOOTy is no DCI gateway | reading |

### RFC 9251: IGMP and MLD proxies for EVPN

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 9251-1 | 4 to 9 | SMET and synch routes, proxy querier | BOOTy | N/A | no multicast group handling | reading |
| 9251-2 | 9.2.1 | A PE without support MUST NOT indicate IGMP proxy support | BOOTy | COMPLIANT | BOOTy sends no Multicast Flags extended community | reading |
| 9251-3 | 9.1 | Receiver handling of SMET routes from other PEs | GoBGP | GOBGP: COMPLIANT | a received Type-6 route is discarded since fork booty.1 (7606-6); BOOTy does no multicast group handling | reading |

### RFC 7348: VXLAN (data plane in the kernel)

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 7348-1 | 5 | UDP destination port 4789 | BOOTy, Kernel | COMPLIANT | `overlay.go:33`, `:550`; lab `TestGoBGPUnnumberedVXLANInterface` | lab |
| 7348-2 | 5 | 24-bit VNI, I flag set, reserved bits zero | Kernel | COMPLIANT | Linux vxlan driver | reading |
| 7348-3 | 5 | Outer UDP source port from a hash of the inner frame (SHOULD); UDP checksum 0 for IPv4 (SHOULD) | Kernel | COMPLIANT | kernel defaults (source port range, `udpcsum` off) | reading |
| 7348-4 | 4.3 | VTEPs MUST NOT fragment VXLAN packets | BOOTy, Kernel | COMPLIANT | inner MTU = NIC MTU - 50 (`overlay.go:560`), so outer packets fit the NIC; lab `TestType5JumboMTU` | lab |
| 7348-5 | 4.1 | Remote learning by the data plane | BOOTy | COMPLIANT | off on purpose (`overlay.go:551`); EVPN control plane instead (RFC 8365) | reading |
| 7348-6 | 4.2 | BUM to a multicast group or by head-end replication | Kernel | COMPLIANT | head-end replication through the all-zero FDB entries | reading |
| 7348-7 | 6 | IPv6 outer header | BOOTy | N/A | IPv4 VTEP only (`config.go:415`) | reading |

## LLDP: IEEE 802.1AB

BOOTy sends LLDP like systemd-networkd `EmitLLDP=yes` (`tx.go`) and
receives it on every physical NIC (`receive.go`). Clause references
are to IEEE 802.1AB-2016; the standard is not freely available, so each
row states the rule it checks.

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| LLDP-1 | 7.1, 7.5 | Destination nearest bridge 01-80-C2-00-00-0E, EtherType 88-CC | BOOTy | COMPLIANT | `tx.go:34`, `:163`; `TestFrameLikeNetworkd`, lab `TestProdTplLLDPLikeNetworkd` | test, lab |
| LLDP-2 | 8.2 | Mandatory TLVs Chassis ID, Port ID, TTL first, in this order | BOOTy, Library | COMPLIANT | gopacket encoder (`gopacket@v1.1.19/layers/lldp.go:769`); `TestFrameLikeNetworkd` | test |
| LLDP-3 | 8.5.2 | Chassis ID subtype and value (locally assigned machine ID, or MAC) | BOOTy | COMPLIANT | `tx.go:153`; `TestFrameLikeNetworkd`, `TestMachineIDPerBoot` | test |
| LLDP-4 | 8.5.3 | Port ID subtype interface name | BOOTy | COMPLIANT | `tx.go:157` (parity level; the bm4x-cablecheck profile of [#702] can set other subtypes, `TestFrameBM4XPortIDFormats`); `TestFrameLikeNetworkd` | test |
| LLDP-5 | 8.5.4, 9.2.5 | TTL = txTTL = msgTxInterval x msgTxHold + 1 (since 802.1AB-2009) | BOOTy | DEVIATION | LLDP-TTL: 120 instead of 121, same as networkd (`tx.go:30`). Impact very low. Kept for parity | reading |
| LLDP-6 | 8.5.8 | System capabilities: enabled a subset of supported | BOOTy | COMPLIANT | supported station/bridge/router, enabled station (`tx.go:43`); `TestFrameLikeNetworkd` | test |
| LLDP-7 | 8.4 | Optional TLVs: system name and description within 255 octets | BOOTy | COMPLIANT | `tx.go:176` | reading |
| LLDP-8 | 9.2.5 | msgTxInterval 30 s, fast start (txFastInit 4 frames at msgFastTx 1 s) | BOOTy | COMPLIANT | `tx.go:27` to `:30`, `tx.go:225` | reading |
| LLDP-9 | 9.2.7 | Shutdown LLDPDU (TTL 0, mandatory TLVs) when transmission stops | BOOTy | COMPLIANT | LLDP-SHUTDOWN at the first audit. [#754] sends it when the sender stops (`tx.go:148`, `ShutdownFrame`); [#792] sends it for the every-NIC sender before the handoff, first in the network stop. networkd sends none. `TestShutdownFrame`, `TestEmitEndsWithShutdownFrame`, `TestStopNetworkOnceStopsLLDPFirst` | test |
| LLDP-10 | 9.2.7.7 | Receive: drop LLDPDUs whose first three TLVs are not Chassis ID, Port ID, TTL, or with a second instance of them | BOOTy | COMPLIANT | LLDP-RX-ORDER at the first audit. [#754] and [#769] (`lldp.go:188`, `mandatoryTLVsFirst`); `TestParseLLDPMandatoryTLVs` | test |
| LLDP-11 | 8.2 | End of LLDPDU TLV optional (2016) | BOOTy | COMPLIANT | [#754]: a payload without End TLV gets one before gopacket decodes it (`decode.go:53`); `TestParseLLDPMandatoryTLVs` ("in order without End") | test |
| LLDP-12 | 9.2.7.7 | Neighbor information aged out after TTL; TTL 0 removes it at once | BOOTy | COMPLIANT | `receive.go:137` to `:175`; `TestReceiverRecord` | test |
| LLDP-13 | 9.2.7.7 | Truncated or malformed TLVs dropped | BOOTy, Library | COMPLIANT | gopacket decoder checks lengths (`gopacket@v1.1.19/layers/lldp.go:801`); BOOTy skips organizationally specific TLVs that gopacket would read past their end and keeps a partial neighbor on a decoder panic (`decode.go:53`); `TestParseLLDPInvalidPayload`, `TestParseLLDPShortOrgTLVs`, `TestOrgTLVSafe` | test |
| LLDP-14 | 7.1 | Multicast group membership for receive | BOOTy | COMPLIANT | `lldp.go:78`; `TestOpenRXGroupsAndOwnFrames` | test |
| LLDP-15 | 9.1 | Own frames are not neighbors | BOOTy | COMPLIANT | `lldp.go:120`; `TestOwnFrame` | test |
| LLDP-16 | 9.2.7.7 | Several neighbors per port (tooManyNeighbors handling) | BOOTy | N/A | one neighbor per NIC by design (one switch port per NIC), documented | reading |
| LLDP-17 | 8.6 | Organizationally specific TLVs (OUI, subtype, information string) | BOOTy, Library | COMPLIANT | the parity level sends none, like networkd; the levels standard and full ([#729], [#734]) send 802.1, 802.3 and LLDP-MED TLVs, encoded by gopacket; `TestFrameStandard`, `TestFrameFull`, `TestFrameMEDCapabilities` | test |
| LLDP-18 | 9.2.2 | adminStatus per port (txOnly, rxOnly, txAndRx) | BOOTy | COMPLIANT | `LLDP_EMIT` (send) and `LLDP_RECEIVE` (receive) per NIC set | reading |
| LLDP-19 | 9.2.5 | Untrusted TLV strings sanitized | BOOTy | COMPLIANT | `lldp.go:221`; `TestSanitizeLLDP` | test |
| LLDP-20 | 9.2.4 | reinitDelay before restart of transmission | BOOTy | N/A | transmission starts once per run | reading |

## IPv6 neighbor discovery and address configuration

The kernel does neighbor discovery and SLAAC. BOOTy itself sends Router
Advertisements on the unnumbered BGP NICs (`underlay.go:1015`), so FRR's
zebra learns the link-local address of the server, and ICMPv6 echo
requests to find the switch.

### RFC 4861: Neighbor Discovery (BOOTy's Router Advertisements)

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 4861-1 | 4.2, 6.1.2 | RA format; Hop Limit 255 | BOOTy | COMPLIANT | `underlay.go:1062`, `:1081`; `TestRouterAdvertisementPacket` ([#752]); lab: FRR drops other hop limits and the unnumbered sessions establish (`TestGoBGPUnnumberedBGPEstablished`, `TestProdTplLeafSessions`) | test, lab |
| 4861-2 | 4.6.1 | Source Link-Layer Address option | BOOTy | COMPLIANT | `underlay.go:1090` | reading |
| 4861-3 | 6.2.1 | AdvSendAdvertisements FALSE by default | BOOTy | COMPLIANT | RAs only in unnumbered and dual mode (`underlay.go:1097`) | reading |
| 4861-4 | 6.2.1 | MaxRtrAdvInterval 4 to 1800 s; AdvDefaultLifetime 0 or MaxRtrAdvInterval to 9000 s | BOOTy | COMPLIANT | 10 s, 1800 s (`underlay.go:47`, `:58`). [#770] (open) uses zebra's lifetime, also in range | reading |
| 4861-5 | 6.2.1, 6.2.4 | MinRtrAdvInterval at most 0.75 x Max; random interval between Min and Max | BOOTy | DEVIATION | RFC4861-HOST-RA: fixed 10 s (`underlay.go:1116`). Impact low | reading |
| 4861-6 | 6.2.2 | A router MUST join all-routers on an advertising interface | BOOTy | DEVIATION | RFC4861-HOST-RA: the kernel joins ff02::2 only with forwarding on; IPv6 forwarding is off by default. BOOTy answers no Router Solicitations, so nothing depends on it. Impact low | reading |
| 4861-7 | 6.2.4 | A host MUST NOT send RAs | BOOTy | DEVIATION | RFC4861-HOST-RA: with IPv6 forwarding off BOOTy is a host and still sends RAs, because FRR BGP unnumbered needs them. The legacy FRR ramdisk does the same. By design, documented | reading |
| 4861-8 | 6.2.5 | Final RAs with Router Lifetime 0 when advertising stops (SHOULD) | BOOTy | COMPLIANT | RFC4861-FINAL-RA at the first audit. [#752] ends the periodic RAs with lifetime 0 (`underlay.go:1123`), also before the handoff (`underlay.go:254`); `TestPeriodicRAEndsWithZeroLifetime`, `TestTeardownStopsPeriodicRA` | test |
| 4861-9 | 6.2.6 | RAs to all-nodes at most one per MIN_DELAY_BETWEEN_RAS (3 s); not more often than MinRtrAdvInterval | BOOTy | COMPLIANT | one RA per NIC per 10 s; the NDP trigger can add one RA at peer discovery (`underlay.go:1193`), before the periodic RAs start | reading |
| 4861-10 | 6.2.6 | Router Solicitation processing | BOOTy | N/A | BOOTy answers no solicitations (zebra needs only the periodic RAs) | reading |
| 4861-11 | 4.4, 7 | Neighbor solicitation and advertisement, NUD, DAD | Kernel | COMPLIANT | Linux; link-local discovery reads the kernel neighbor table (`underlay.go:1262`) | reading |
| 4861-12 | 6.3 | Host processing of received RAs | Kernel | COMPLIANT | `accept_ra` 2 and `accept_ra_defrtr` 1 (`gobgp/helpers.go:47`), netplan `accept-ra: false` per NIC (`underlay.go:1300`) | reading |
| 4861-13 | 8 | Redirects | Kernel | N/A | | reading |
| 4861-14 | 4.2 | Router flag of Neighbor Advertisements consistent with the RAs | Kernel | COMPLIANT | the kernel sets the R flag from forwarding; with forwarding off it is 0 (`helpers.go:20` comment), which matches 4861-7 for a host | reading |
| 4861-15 | 6.1.2 | Host: a received RA is valid only with hop limit 255 and a link-local source | BOOTy | PENDING | [#795] (open) reads the M and O flags of RAs in DHCP mode to start DHCPv6, like networkd, and checks them first (`dhcp6_mode.go` `validRA`): COMPLIANT there, PR test `TestValidRA`. At the audited tree only the kernel reads RAs (4861-12) | PR test |
| 4861-16 | 6.3.7 | Host: up to MAX_RTR_SOLICITATIONS (3) Router Solicitations RTR_SOLICITATION_INTERVAL (4 s) apart; the first after a random delay of up to MAX_RTR_SOLICITATION_DELAY (1 s, SHOULD) | BOOTy | PENDING | [#795] (open) sends three solicitations 4 s apart with the source link-layer address when the carrier of a DHCP NIC returns (`dhcp6_mode.go` `solicitRA`), PR test `TestDHCPv6FollowsRAAndCarrier`. DEVIATION there: the first one has no random delay. Impact very low (one host per link) | PR test |

### RFC 4862: IPv6 stateless address autoconfiguration

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 4862-1 | 5 | Link-local address generation and DAD | Kernel | COMPLIANT | Linux; netplan `link-local: []` sets `addr_gen_mode` none (`devices.go:50`) | reading |
| 4862-2 | 5.5 | Global addresses from Prefix Information | Kernel | COMPLIANT | Linux, by `accept_ra` | reading |

## DHCP

### RFC 2131: DHCPv4 client

BOOTy uses `github.com/digineo/go-dhclient v1.0.2` for the protocol and
applies the leases itself (`pkg/network/dhcp.go`). The open stack from
[#750] to [#825] keeps DHCP on every NIC like networkd ([#750]), replaces
go-dhclient with BOOTy's own client ([#790]) and adds DHCPv6 ([#795]); the rows give the
result at the audited tree and in those PRs.

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 2131-1 | 4.1, 4.4.1 | DISCOVER/OFFER/REQUEST/ACK exchange, xid, broadcast from 0.0.0.0 | Library | COMPLIANT | `go-dhclient@v1.0.2/client.go:205` to `:300`; `TestDHCPMode_Setup`. [#790] (open) replaces go-dhclient (GPL-3.0) with BOOTy's own client (`pkg/network/dhcp4`), PR tests `TestDiscoverBackoffAndOptions`, `TestTransportOnVeth` | test |
| 2131-2 | 4.4.1 | Client SHOULD check the offered address (ARP) and MUST send DHCPDECLINE if in use | Library | DEVIATION | RFC2131-ARP: no ARP probe and no DECLINE. Impact low. Open: the own client of [#790] (open) does not probe either | reading |
| 2131-3 | 4.3.2, 4.4.5 | RENEWING: unicast REQUEST with ciaddr, without server identifier and requested IP; REBINDING: broadcast | Library | DEVIATION, fixed in [#790] (open) | RFC2131-RENEW: broadcast with both options (`client.go:232`). Servers accept it. Impact low. [#790]: unicast REQUEST to the server of the lease with ciaddr and neither option, broadcast when rebinding (`dhcp4/client.go` `renewState`, `rebindState`), PR tests `TestRenewUnicastRebindBroadcast`, `TestRenewWait` | reading |
| 2131-4 | 4.4.5 | T1 and T2 defaults 0.5 and 0.875 of the lease | Library | COMPLIANT | `client.go:255` to `:259`; [#790] (open) keeps them (PR test `TestLeaseTimes`) | reading |
| 2131-5 | 4.4.5 | Lease expiry before renewal or rebind: MUST stop using the address, back to INIT | BOOTy | DEVIATION, fixed in [#751] (open) | RFC2131-EXPIRE: no `OnExpire` callback (`dhcp.go:142`); the address stays. [#751] removes the lease state at expiry (PR test `TestOnExpireRemovesLeaseState`); the own client of [#790] calls `OnExpire` as well (PR test `TestExpiryAndNak`) | reading |
| 2131-6 | 3.2 | DHCPNAK: stop using the address, restart | BOOTy | DEVIATION, fixed in [#751] (open) | go-dhclient calls `OnExpire` for a NAK, which BOOTy does not set. [#790] (open) also takes a NAK only from the server of the lease, like networkd (PR test `TestNakFromOtherServerIgnored`) | reading |
| 2131-7 | 4.4.5 | A renewed lease updates address and router | BOOTy | COMPLIANT | `dhcp.go:271` `applyRenewal`; `TestOnBoundAppliesRenewalOfWinningNIC` | test |
| 2131-8 | 3.3 | Lease without router option drops the default route | BOOTy | COMPLIANT | `applyRenewal`; tests in `dhcp_test.go` | reading |
| 2131-9 | 4.1 | Retransmission with randomized exponential backoff (SHOULD) | Library | DEVIATION, fixed in [#790] (open) | RFC2131-BACKOFF: fixed 1 s retry delay (`client.go:162`) inside BOOTy's 15 s probe. Impact very low for a bounded probe. [#790]: 2, 4, 8, 16, 32, then 64 s, each +-1 s, like networkd (PR test `TestDiscoverBackoffAndOptions`) | reading |
| 2131-10 | 4.4.6 | DHCPRELEASE on shutdown (MAY) | BOOTy | N/A | the OS takes over the NIC after the handoff. [#790] (open) can release like networkd (`SendRelease`, PR test `TestRebootAndRelease`) | reading |
| 2131-11 | 4.4.1 | Parameter request list, hostname option | BOOTy | COMPLIANT | `dhcp.go:141` `newDHCPClient`; `TestNewDHCPClientRequestsDefaultParams`. [#776] (open) sends networkd's list and options | test |
| 2131-12 | 3.1 | Client identifier, chaddr | Library | N/A | chaddr only (allowed). [#776] (open) sends networkd's client identifier, see 4361-1 | reading |
| 2131-13 | 3.2, 4.4.2 | INIT-REBOOT: confirm the last address with a broadcast REQUEST (requested IP, no server identifier); NAK: back to INIT | BOOTy | PENDING | [#790] (open) `rebootState`, started by [#793] (open) when the carrier returns, like networkd: COMPLIANT there, PR tests `TestRebootTriesOnce`, `TestRebootEndsRenewal`, `TestCarrierLossKeepsLeaseAndReturnReboots` | PR test |
| 2131-14 | 4.1 | secs: the time since the client began the exchange | BOOTy | PENDING | [#790] (open) `secs`: COMPLIANT there, PR test `TestDiscoverBackoffAndOptions` | PR test |
| 2131-15 | 4.1 | Replies are matched by xid and chaddr | BOOTy | PENDING | [#790] (open) `wanted`: COMPLIANT there, PR test `TestIgnoresForeignReplies` | PR test |

### DHCPv4 options (RFC 2132, 3397, 3442, 4039, 4361)

The open PRs [#753], [#776], [#790], [#819] and [#820] apply the options of a lease
like systemd-networkd in the legacy ramdisk. At the audited tree BOOTy
applies only the address, the router and NTP.

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 2132-1 | RFC 2132 3.8, 5.1 | DNS servers (option 6) and interface MTU (option 26, at least 68) of the lease | BOOTy | PENDING | [#753] (open) writes the DNS servers of the lease and sets the MTU, clamped (PR tests `TestOnBoundWritesLeaseDNS`, `TestOnBoundSetsLeaseMTU`, `TestClampMTU`); [#819] (open) asks every server at once, like the legacy resolver (PR tests in `dns_fanout_test.go`). COMPLIANT there | PR test |
| 2132-2 | RFC 2132 5.8, 9.10 | Static routes (option 33, classful, no default route) and maximum message size (option 57, at least 576) | BOOTy | PENDING | [#776] (open) `staticRoutes`, [#790] (open) option 57 = NIC MTU - 28: COMPLIANT there, PR tests `TestLeaseRoutesTable`, `TestMalformedRouteAndSearchOptions` | PR test |
| 3442-1 | RFC 3442 | Classless static routes (option 121): MUST ignore the Router (3) and Static Routes (33) options when present; a malformed option is ignored | BOOTy | PENDING | [#776] (open) `leaseRouteSet`: COMPLIANT there, PR tests `TestOnBoundAppliesClasslessRoutes`, `TestMalformedRouteAndSearchOptions` | PR test |
| 3397-1 | RFC 3397 | Domain search (option 119) with compression pointers; a malformed option is ignored | BOOTy | PENDING | [#776] (open) `domainSearch`: COMPLIANT there, PR tests `TestLeaseSearchDomains`, `FuzzDomainSearch` | PR test |
| 4039-1 | RFC 4039 | Rapid commit: option 80 in DISCOVER; an ACK with rapid commit binds at once | BOOTy | PENDING | [#790] (open) `initState`, like networkd: COMPLIANT there, PR test `TestDiscoverBackoffAndOptions` | PR test |
| 4361-1 | RFC 4361 | Client identifier type 255: IAID and DUID | BOOTy | PENDING | [#776] (open) `dhcpClientID`: IAID of the NIC and a DUID-EN from the machine ID, like networkd: COMPLIANT there, PR test `TestClientIDWithoutMachineID` | PR test |

### RFC 8415: DHCPv6

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 8415-1 | all | DHCPv6 client | BOOTy | NOT IMPLEMENTED, added in [#795] (open) | the legacy ramdisk runs networkd with DHCPv6 started by RAs (`DHCPv6Client=yes`); BOOTy has none at the audited tree. [#795] adds BOOTy's own client (`pkg/network/dhcp6`), started by the M or O flag of an RA on every DHCP NIC | reading |
| 8415-2 | 15, 18.2.1 | Retransmission: IRT, MRT, RAND; the first SOLICIT timeout greater than IRT | BOOTy | PENDING | [#795] and [#825] (open) `next`: COMPLIANT there, PR tests `TestSolicitLikeNetworkd`, `TestFirstSolicitAboveIRT` | PR test |
| 8415-3 | 18.2.1, 18.2.9 | SOLICIT with rapid commit; collect ADVERTISEs for the first timeout and take the highest preference, 255 at once; ignore an ADVERTISE with NoAddrsAvail | BOOTy | PENDING | [#825] (open) `solicit`, like sd-dhcp6-client: COMPLIANT there, PR tests `TestAdvertisePreference`, `TestLateAdvertiseSameSocket`, `TestRequestRetriesAndAdvertiseStatus` | PR test |
| 8415-4 | 18.2.2 | REQUEST up to REQ_MAX_RC (10) times | BOOTy | PENDING | [#795] (open) `request`: COMPLIANT there, PR test `TestRequestRetriesAndAdvertiseStatus` | PR test |
| 8415-5 | 18.2.4, 18.2.5, 18.2.10.1 | RENEW at T1, REBIND at T2, stop using the address at the end of its valid lifetime; NoBinding in a REPLY to RENEW or REBIND: REQUEST | BOOTy | PENDING | [#795] and [#825] (open) `extend`, `phase`: COMPLIANT there, PR tests `TestRenewRebindExpire`, `TestRenewNeedsAddress`, `TestRenewNoBindingRequests` | PR test |
| 8415-6 | 18.2.6, 21.23 | INFORMATION-REQUEST (O flag); refresh after the Information Refresh Time, at least IRT_MINIMUM (600 s), default 86400 s | BOOTy | PENDING | [#795] (open) `information`, `infoRefresh`: COMPLIANT there, PR tests `TestInformationRequest`, `TestInfoRefresh`, `TestInformationWaitsRefreshTime` | PR test |
| 8415-7 | 18.2.7 | RELEASE of a bound address, wait for the REPLY | BOOTy | PENDING | [#795] (open) `release` at stop, like networkd: COMPLIANT there, PR test `TestAdvertiseRequestAndRelease` | PR test |
| 8415-8 | 18.2.10.1 | Duplicate address after the REPLY: send DECLINE (SHOULD) | BOOTy | PENDING | DEVIATION in [#795] (open): no DECLINE. Impact very low | reading |
| 8415-9 | 21.24, RFC 3646 | DNS servers (option 23) and domain list (option 24) used | BOOTy | PENDING | [#795] (open) adds them after the DHCPv4 servers, a link-local server with its zone: COMPLIANT there, PR tests `TestDomainList`, `TestDHCPv6LinkLocalDNSAndDomains` | PR test |

## Fix PRs and fork tags

State on 2026-10-19. Merged PRs are on `main` at the audited tree.

| PR or tag | State | Findings |
|---|---|---|
| [#699] GoBGP 4.10.0, fork tag booty.2 | merged | RFC7606-EVPN-PANIC, RFC4271-COLLISION (GoBGP 4.10.0); fork booty.1 and booty.2: RFC7606-TYPED-NLRI, RFC4724-NEWCONN, RFC8950-ENHE-CHECK, RFC8950-ENHE-EVPN, 7606-9, 9047-1, 4271-33 (osrg/gobgp#3740), 4271-17 (osrg/gobgp#3741) |
| [#808], [#811] fork tags booty.3 to booty.10 | merged | 9384-1, 9384-2, 7313-1 to 7313-5, 7911-8, 4271-34 (booty.3, booty.4); 4271-17 (booty.5, booty.9, booty.10); 4271-33 (booty.4, booty.6); 4271-32 (booty.8, booty.10) |
| [#831] fork tag booty.12 | open | 4271-31 (booty.11, booty.12) |
| [#746] | merged | RFC8365-ENCAP |
| [#747] | open | RFC9136-RMAC, RFC9136-ESIGW, RFC9136-ESI, RFC9136-LABEL0 |
| [#748] | merged | RFC7432-MACIP-WITHDRAW, RFC7432-MOBILITY-RX |
| [#749], [#768] | merged | RFC5880-REMOTEDISCR, RFC5880-DEMAND |
| [#752] | merged | RFC4861-FINAL-RA |
| [#754], [#769], [#792] | merged | LLDP-SHUTDOWN, LLDP-RX-ORDER, LLDP-11 |
| [#766], [#784] | merged | RFC9135-ARP-RX |
| [#767] | merged | RFC7432-ESI-RX |
| [#772], [#786] | merged | RFC8365-PMSI-RX |
| [#775] | merged | RFC9136-GWIP |
| [#771] (stacked on [#747]) | open | RFC8365-ENCAP-RX |
| [#773] (stacked on [#747]) | open | RFC8365-RX-VNI |
| [#799] (stacked on [#747]) | open | RFC9136-GWIP (test) |
| [#710] | open | RFC7432-RD (7432-12, 8365-21, 9136-3) |
| [#798] | open | RFC9047-FLAGS |
| [#765], [#787] (stacked on [#765]) | open | defence in depth for RFC7606-EVPN-PANIC (`panic=10`) |
| [#770] | open | RA router lifetime like zebra (4861-4) |
| [#750], [#751], [#753], [#776], [#788], [#790], [#793], [#795], [#819], [#820], [#825] (one stack) | open | RFC2131-EXPIRE ([#751]), RFC2131-RENEW and RFC2131-BACKOFF ([#790]), the PENDING rows of RFC 2131, the DHCPv4 options, RFC 8415 and 4861-15, 4861-16 |

The fork branches on GoBGP 3.37.0 of the first audit
(`fix/v3-evpn-decode-bounds`, `fix/evpn-discard-unknown-route-types`,
`fix/enhe-require-peer-capability`) are obsolete: BOOTy uses GoBGP 4 with
the fork tag, which contains these fixes. `docs/networking.md`
([GoBGP version and fork](networking.md#gobgp-version-and-fork)) lists
every change of the fork and the condition to drop the `replace`.

Open deviations without a PR: RFC2131-ARP, RFC9136-NH-REACH,
RFC5880-ADMINDOWN, RFC5880-CONGESTION, RFC4861-HOST-RA,
RFC4271-NH-RESOLVE, RFC7432-MOBILITY-GOBGP, RFC7432-PMSI-ID, LLDP-TTL.
Each needs a GoBGP or kernel-side change, or is kept for FRR or networkd
parity (the row says which).

## Open PRs and their effect on the findings

Checked on 2026-10-19 with `main` at `f1e0e72e`.

| PR | Effect on the findings |
|---|---|
| [#710] fabric parity | Fixes RFC7432-RD (and 8365-21, 9136-3): RD `<router-id>:<n>`, a separate RD for an L2VNI. MED 0 on originated routes (allowed by RFC 4271 §5.1.4). BFD Required Min Echo RX 50 ms: BOOTy then must loop back peer echo packets, which the kernel does with forwarding on and rp_filter off (5881-2). |
| [#743] Type-5 FIB owner | Type-5 routes for prefixes the underlay FIB owns are not installed. No finding changes. |
| [#747], [#771], [#773], [#799] (Type-5 and EVPN receive stack) | See the rows 9136-8, 9136-10, 9136-14 to 9136-16, 8365-19, 8365-10. |
| [#760] LLDP receive start | No protocol change. |
| [#765], [#787] kernel panic reboot | Defence in depth for a panic of PID 1; no finding changes. |
| [#770] RA lifetime like zebra | 4861-4 stays COMPLIANT. |
| [#798] NTF_ROUTER | Fixes RFC9047-FLAGS. |
| [#826] LLDP link watch | No protocol change. |
| [#831] fork tag booty.12 | Fixes RFC4271-COLLISION-DIALED (4271-31). |
| DHCP stack [#750] to [#825] | [#750] keeps a client on every NIC and applies every lease like networkd (no RFC finding); [#751] fixes RFC2131-EXPIRE; [#790] fixes RFC2131-RENEW and RFC2131-BACKOFF, not RFC2131-ARP; [#788] starts DHCP on NICs that appear later and [#820] routes by source address per NIC (no RFC finding); the other PRs are in the PENDING rows. |
| [#736] (combined test build) | Test only. |

[#699]: https://github.com/telekom/BOOTy/pull/699
[#702]: https://github.com/telekom/BOOTy/pull/702
[#710]: https://github.com/telekom/BOOTy/pull/710
[#723]: https://github.com/telekom/BOOTy/pull/723
[#729]: https://github.com/telekom/BOOTy/pull/729
[#734]: https://github.com/telekom/BOOTy/pull/734
[#736]: https://github.com/telekom/BOOTy/pull/736
[#743]: https://github.com/telekom/BOOTy/pull/743
[#744]: https://github.com/telekom/BOOTy/pull/744
[#745]: https://github.com/telekom/BOOTy/pull/745
[#746]: https://github.com/telekom/BOOTy/pull/746
[#747]: https://github.com/telekom/BOOTy/pull/747
[#748]: https://github.com/telekom/BOOTy/pull/748
[#749]: https://github.com/telekom/BOOTy/pull/749
[#750]: https://github.com/telekom/BOOTy/pull/750
[#751]: https://github.com/telekom/BOOTy/pull/751
[#752]: https://github.com/telekom/BOOTy/pull/752
[#753]: https://github.com/telekom/BOOTy/pull/753
[#754]: https://github.com/telekom/BOOTy/pull/754
[#760]: https://github.com/telekom/BOOTy/pull/760
[#765]: https://github.com/telekom/BOOTy/pull/765
[#766]: https://github.com/telekom/BOOTy/pull/766
[#767]: https://github.com/telekom/BOOTy/pull/767
[#768]: https://github.com/telekom/BOOTy/pull/768
[#769]: https://github.com/telekom/BOOTy/pull/769
[#770]: https://github.com/telekom/BOOTy/pull/770
[#771]: https://github.com/telekom/BOOTy/pull/771
[#772]: https://github.com/telekom/BOOTy/pull/772
[#773]: https://github.com/telekom/BOOTy/pull/773
[#775]: https://github.com/telekom/BOOTy/pull/775
[#776]: https://github.com/telekom/BOOTy/pull/776
[#784]: https://github.com/telekom/BOOTy/pull/784
[#786]: https://github.com/telekom/BOOTy/pull/786
[#787]: https://github.com/telekom/BOOTy/pull/787
[#788]: https://github.com/telekom/BOOTy/pull/788
[#790]: https://github.com/telekom/BOOTy/pull/790
[#792]: https://github.com/telekom/BOOTy/pull/792
[#793]: https://github.com/telekom/BOOTy/pull/793
[#795]: https://github.com/telekom/BOOTy/pull/795
[#798]: https://github.com/telekom/BOOTy/pull/798
[#799]: https://github.com/telekom/BOOTy/pull/799
[#808]: https://github.com/telekom/BOOTy/pull/808
[#811]: https://github.com/telekom/BOOTy/pull/811
[#819]: https://github.com/telekom/BOOTy/pull/819
[#820]: https://github.com/telekom/BOOTy/pull/820
[#825]: https://github.com/telekom/BOOTy/pull/825
[#826]: https://github.com/telekom/BOOTy/pull/826
[#831]: https://github.com/telekom/BOOTy/pull/831
