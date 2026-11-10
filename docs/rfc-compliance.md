# RFC compliance of the BOOTy networking stack

This document records an RFC compliance audit of the networking code of
BOOTy: the GoBGP stack (`pkg/network/gobgp`), BOOTy's own BFD
(`pkg/network/bfd`), LLDP (`pkg/network/lldp`), the Router Advertisements
of the unnumbered peering, the DHCP client mode and the VXLAN data plane
that BOOTy configures in the kernel.

| Item | Value |
|---|---|
| Audited tree | `main` at `5a47f1cbd1b3bbb6a01441bc64fe4d6c705758f8` (third refresh, with [#892] and [#908]); the second refresh read `5ac79521`, the first refresh `f1e0e72e`, the first audit `03abb09f` |
| GoBGP | `github.com/osrg/gobgp/v4 v4.11.0`, replaced by the fork tag `github.com/MaxRink/gobgp/v4 v4.11.0-booty.6` (go.mod): upstream v4.11.0 ([#874]) plus [#851], [#876] and [#884]; source read at that tag. The first refresh read `v4.10.0-booty.10`, the first audit `v3.37.0` |
| Other libraries | `gopacket/gopacket v1.7.4`, replaced by the fork `MaxRink/gopacket v1.7.4-booty.2`; `vishvananda/netlink v1.3.1`, replaced by the fork `MaxRink/netlink v1.3.1-booty.1` ([dependency-forks.md](dependency-forks.md)). `digineo/go-dhclient` is gone: [#790] added BOOTy's own DHCPv4 client (`pkg/network/dhcp4`) |
| Open PRs | none: every fix PR that the first refresh listed as open is merged |
| Kernel | Linux VXLAN, bridge, neighbor and IPv6 ND/SLAAC code (not audited, owner noted) |
| Date | 2026-11-09 (third refresh); second refresh 2026-11-07, first refresh 2026-10-19, first audit 2026-10-18 |

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

The second refresh read every row again at the audited tree. File and
line references moved to `main` and to the fork tag `v4.11.0-booty.6`:
where the cited line still exists, it was matched by its text and its
enclosing function; the rest were looked up by hand. Rows fixed by the
PRs that were open at the first refresh are now COMPLIANT and name the
PR, and the PENDING rows have a status at `main`. The fork tags of
the first refresh (`v4.10.0-booty.N`) are history: many of their changes
are upstream in v4.11.0, and the rows say which code provides a behaviour
now. [networking.md](networking.md#gobgp-version-and-fork) lists every
change of the current fork.

The third refresh checked every file and line reference again at the
audited tree (the GoBGP fork tag is unchanged) and corrected the ones
that had moved or pointed next to the cited code. It found two DHCPv4
rows that were marked COMPLIANT but were not: a DHCPRELEASE without the
server identifier (2131-10) and option 57 values below 576 for MTUs 576
to 603 (2132-2), both fixed by [#908]. [#892] fixed 8415-7. It added
deviations that the earlier refreshes missed: seven of very low impact
(4861-15: RA options of length zero; 8415-10: an address revoked with
valid lifetime 0; 8415-11: SOL_MAX_RT not applied; 8415-12: options 32
and 83 not requested; 8415-13: no delay before the first SOLICIT;
2131-16: no expiry check while SELECTING; 2132-3: option overload not
read), one of low impact
(2131-13: a DHCPNAK from another server is ignored in INIT-REBOOT and
REBINDING) and one in GoBGP (8654-1: extended messages received only
from a peer that advertised the capability). It also corrected the text
of 4271-4, 3442-1, 8654-2 and 7348-4.

Each row has an owner:

- **BOOTy**: code in this repository.
- **GoBGP**: the GoBGP library; checked in its source at the fork tag
  `v4.11.0-booty.6`, with the file and line of that tag.
- **Kernel**: Linux, configured by BOOTy.
- **Library**: another Go dependency (named in the row).

Status values:

| Status | Meaning |
|---|---|
| COMPLIANT | The requirement is met. Evidence: a file and line at the audited tree plus a test, or a lab test, or the source read. |
| DEVIATION | The requirement is not met. The row gives the impact and the fix, or the reason to keep it. |
| N/A | Not implemented and not needed for the role of BOOTy (or a requirement on a feature BOOTy does not use). |
| NOT IMPLEMENTED | Not implemented, and an implementation would be needed for full compliance. |
| GOBGP: COMPLIANT / GOBGP: DEVIATION | Owned by GoBGP; verified in the GoBGP fork source. |

The last column, Verified, says how the status was checked at the audited
tree: **test** (a unit test named in the row), **lab** (a test of
`test/e2e/integration` against FRR in containerlab, build tag
`e2e_prodtpl`, `e2e_gobgp` or `e2e_gobgp_type5`), **GoBGP test** (a test
in the GoBGP fork tag), or **reading** (the source was read; no test pins
it). Only tests on `main` and in the fork tag count.

Evidence: file references are relative to the repository root at the
audited tree, `gobgp@v4.11.0-booty.6/...` to the GoBGP fork module. The bare GoBGP file names
`fsm.go`, `server.go`, `peer.go` (`pkg/server`), `bgp.go`, `validate.go`
(`pkg/packet/bgp`), `path.go`, `message.go`, `adj.go` and
`table_manager.go` (`internal/pkg/table`) are in the fork module as well.
New tests added by this audit:

- `pkg/network/gobgp/rfc_wire_test.go`: `TestRFCWireType5`,
  `TestRFCWireType3`, `TestRFCWireType2` encode the EVPN routes BOOTy
  originates with GoBGP and check the NLRI bytes and every attribute BOOTy sets, the VXLAN
  encapsulation community of [#746] included, and the type 1 route
  distinguisher of [#710].
- `pkg/network/bfd/rfc_test.go`: `TestRFC5880PacketFields`,
  `TestRFC5881SourcePortAndTTL`.
- Fix PRs and the GoBGP fork add tests for each fixed deviation (see
  [Deviations fixed since the first audit](#deviations-fixed-since-the-first-audit)).

## Summary

Counts per RFC at the audited tree. GoBGP-owned rows are counted in the
GoBGP columns. No row is PENDING any more: every fix PR of the first
refresh is merged.

| RFC | Topic | COMPLIANT | DEVIATION | N/A | NOT IMPL. | GOBGP: COMPLIANT | GOBGP: DEVIATION |
|---|---|---|---|---|---|---|---|
| 4271 | BGP-4 | 6 | 0 | 1 | 0 | 26 | 1 |
| 4760 | Multiprotocol BGP | 3 | 0 | 0 | 0 | 6 | 0 |
| 6793 | 4-octet AS | 3 | 0 | 0 | 0 | 7 | 0 |
| 7911 | ADD-PATH (and Paths-Limit draft) | 1 | 0 | 1 | 0 | 6 | 0 |
| 8950 / 5549 | IPv4 NLRI with IPv6 next hop | 3 | 0 | 1 | 0 | 4 | 0 |
| 4724 | Graceful restart | 1 | 0 | 2 | 0 | 9 | 0 |
| 7313 | Enhanced route refresh | 0 | 0 | 0 | 0 | 5 | 0 |
| 2918 | Route refresh | 1 | 0 | 0 | 0 | 3 | 0 |
| 8654 | Extended message | 0 | 0 | 0 | 0 | 1 | 1 |
| 9234 | BGP roles | 0 | 0 | 2 | 0 | 0 | 0 |
| 5492 | Capabilities | 0 | 0 | 0 | 0 | 5 | 0 |
| 7606 | UPDATE error handling | 2 | 0 | 0 | 0 | 7 | 0 |
| 9384 | Cease/BFD Down | 1 | 0 | 0 | 0 | 1 | 0 |
| 5880 | BFD | 33 | 3 | 7 | 1 | 0 | 0 |
| 5881 | BFD single hop | 13 | 1 | 2 | 0 | 0 | 0 |
| 5883 | BFD multihop | 2 | 1 | 1 | 0 | 0 | 0 |
| 7432 | EVPN | 26 | 1 | 15 | 0 | 4 | 1 |
| 8365 | EVPN NVO (VXLAN) | 17 | 1 | 5 | 0 | 1 | 0 |
| 9135 | EVPN IRB | 8 | 0 | 5 | 0 | 0 | 0 |
| 9136 | EVPN IP prefix (Type-5) | 17 | 2 | 1 | 0 | 1 | 0 |
| 9047 | EVPN ARP/ND flags | 1 | 0 | 0 | 0 | 1 | 0 |
| 6514 | PMSI Tunnel attribute | 3 | 1 | 0 | 0 | 1 | 0 |
| 7209 | EVPN requirements | 3 | 0 | 6 | 1 | 0 | 0 |
| 8584 | DF election | 0 | 0 | 1 | 0 | 0 | 0 |
| 9161 | Proxy ARP/ND | 0 | 0 | 1 | 0 | 0 | 0 |
| 9014 | EVPN interconnect | 0 | 0 | 1 | 0 | 0 | 0 |
| 9251 | IGMP/MLD proxy | 1 | 0 | 1 | 0 | 1 | 0 |
| 7348 | VXLAN | 6 | 0 | 1 | 0 | 0 | 0 |
| 802.1AB | LLDP | 17 | 1 | 2 | 0 | 0 | 0 |
| 4861 | IPv6 ND (RAs) | 12 | 3 | 1 | 0 | 0 | 0 |
| 4862 | IPv6 SLAAC | 2 | 0 | 0 | 0 | 0 | 0 |
| 2131 | DHCPv4 client | 14 | 2 | 0 | 0 | 0 | 0 |
| 2132 / 3397 / 3442 / 4039 / 4361 | DHCPv4 options | 6 | 1 | 0 | 0 | 0 | 0 |
| 8415 | DHCPv6 | 13 | 2 | 0 | 0 | 0 | 0 |
| **Total** | | **215** | **19** | **57** | **2** | **89** | **3** |

Kernel- and library-owned rows count as COMPLIANT or DEVIATION; the owner
column names them. The second refresh (`5ac79521`) counted 209
COMPLIANT, 17 DEVIATION, 90 GOBGP: COMPLIANT and 2 GOBGP:
DEVIATION, the other columns as now. The first refresh (`f1e0e72e`, fork tag
`v4.10.0-booty.10`) counted 176 COMPLIANT, 27 DEVIATION (16 with an open
fix), 60 N/A, 3 NOT IMPLEMENTED, 19 PENDING, 89 GOBGP: COMPLIANT and 3
GOBGP: DEVIATION. The first audit (`03abb09f`, GoBGP 3.37.0) counted 149
COMPLIANT, 47 DEVIATION, 67 N/A, 4 NOT IMPLEMENTED, 68 GOBGP: COMPLIANT and
10 GOBGP: DEVIATION.

### Open deviations ranked by impact

| Rank | ID | RFC | Owner | Impact | Fix |
|---|---|---|---|---|---|
| 1 | RFC8365-RX-VNI | 8365 §5.1.3, 9136 §3.1 | BOOTy | Low: outside the Type-5-only (L3VNI) mode the VNI of a received Type-5 route is not checked, and Type-2 and Type-3 entries use the local VNI. Breaks only with a fabric that uses another VNI for the same VRF. | Partly fixed by [#773] for Type-5 routes in L3VNI mode, like zebra; the rest is kept like FRR. |
| 2 | RFC2131-ARP | 2131 §4.4.1 | BOOTy | Low: no ARP probe of the offered address and no DHCPDECLINE. | Open: an ARP probe (RFC 5227) before the bind and a DECLINE on a conflict. |
| 3 | RFC2131-NAK-SERVER | 2131 §4.4.2, §4.4.5 | BOOTy | Low: in INIT-REBOOT and REBINDING a DHCPNAK counts only from the server of the lease, like networkd; on a changed network the old address stays until a new lease or its expiry (2131-13). | Kept for networkd parity; fix: accept a NAK from any server in these states. |
| 4 | RFC9136-ESI | 9136 §3.2 | BOOTy | Low: a Type-5 route with an ESI overlay index and a label is installed through the BGP next hop, without RT-1 resolution. | Kept like FRR ([#747]). |
| 5 | RFC5880-ECHORX | 5880 §4.1, §6.8.7 | BOOTy | Low: since [#710] BFD advertises Required Min Echo RX 50 ms like FRR bfdd, but BOOTy loops back no echo packets; a peer with echo mode gets a failing echo session. The fabric does not enable echo mode. | Kept for FRR parity. |
| 6 | RFC9136-NH-REACH | 9136 §3.2 | BOOTy | Low: a Type-5 route is installed without a check that the VTEP is reachable; in Type-5-only mode BOOTy installs its own /32 to the VTEP. | Open, by design for a host with one uplink path. |
| 7 | RFC5880-ADMINDOWN | 5880 §6.8.16 | BOOTy | Low: at shutdown one AdminDown packet instead of AdminDown for a detection time. A lost packet makes the peer wait one detection time. | Open; [#723] stops BFD before kexec and reboot, still with one packet. |
| 8 | RFC5880-CONGESTION | 5880 §7 | BOOTy | Low: no congestion control for multihop sessions (a few sessions per host). | Open. |
| 9 | RFC4861-HOST-RA | 4861 §6.2.1, §6.2.2, §6.2.4, §6.2.6 | BOOTy | Low: BOOTy sends RAs with IPv6 forwarding off (a host) at a fixed 10 s interval, answers Router Solicitations, and since [#846] starts with fast RAs 1 s apart like zebra. Needed for FRR BGP unnumbered; the legacy FRR ramdisk does the same. | By design, documented. |
| 10 | RFC4271-NH-RESOLVE | 4271 §9.1.2.1 | GoBGP | Low: no next hop tracking in the decision process; BOOTy's FIB writer skips unresolvable next hops instead. | Open. |
| 11 | RFC7432-PMSI-ID | 7432 §11.2, 6514 §5 | BOOTy | Very low: the ingress replication destination is the originating router IP, not the PMSI Tunnel Identifier; in VXLAN fabrics both are the VTEP. | Kept like FRR, documented. |
| 12 | RFC7432-MOBILITY-GOBGP | 7432 §15 | GoBGP | Very low: GoBGP withdraws its local Type-2 route on a remote higher sequence only when the ESIs differ. BOOTy's MAC is its own bridge MAC and does not move. | Open. |
| 13 | RFC8654-RX | 8654 §4 | GoBGP | Very low: GoBGP accepts UPDATEs above 4096 octets only from a peer that advertised the extended message capability, although it advertises the capability itself. BOOTy's and the fabric's routes are far smaller. | Open (GoBGP). |
| 14 | RFC8415-DECLINE | 8415 §18.2.8 | BOOTy | Very low: a failed DAD sends no DECLINE (8415-8). | Open: DECLINE on a DAD failure. |
| 15 | RFC2132-OVERLOAD | 2132 §9.3 | BOOTy | Very low: an OFFER or ACK with option overload (52) is dropped instead of read (2132-3). | Open: read the options of `file` and `sname`. |
| 16 | RFC8415-ORO-SOL-MAX-RT | 8415 §18.2.2, §18.2.4, §18.2.5 | BOOTy | Very low: REQUEST, RENEW and REBIND do not ask for SOL_MAX_RT (8415-15); a server can still send it. | Kept for networkd parity. |
| 17 | LLDP-TTL | 802.1AB | BOOTy | Very low: TTL 120 instead of txTTL = 4 x 30 + 1 = 121; matches systemd-networkd. | Kept for networkd parity. |

The rows without authentication (5880-39, 5881-15, 5883-4) are not
ranked: the legacy FRR profile has none either.

### Deviations fixed since the first audit

| ID | RFC | Fixed by | Test |
|---|---|---|---|
| RFC7606-EVPN-PANIC | 7606 §5.3 | GoBGP 4 ([#699]) | `TestEVPNDecodeTruncatedNLRIReturnsError` (on `main` since [#699], proposed in the closed [#755]) |
| RFC7606-TYPED-NLRI | 7606 §5.4 | fork ([#699]), upstream v4.11.0 ([#874]) | `Test_EVPNUnknownRouteTypeIsDiscarded`, `TestProcessMessageEVPNUnknownRouteTypeOnly` (GoBGP) |
| RFC4271-COLLISION, RFC4271-COLLISION-DIALED | 4271 §6.8, §8.2.2 | GoBGP 4 and the fork ([#699], [#808], [#831], [#884]) | `TestResolveCollision`, `TestCollisionResolvedByGoBGPKeepsSurvivingConnection`, `TestCollisionLoserClosedOnlyAfterItsOpen`, `TestFSMHandlerOpensent_DialedOutgoingConnKeptForCollision` (GoBGP); `TestCollisionAfterCarrierLoss`; lab `TestProdTplOneDropPerFault` |
| RFC4724-NEWCONN | 4724 §4.2 | fork ([#699]), upstream v4.11.0 with the fork's F-bit gate ([#876]) | `TestFSMHandlerEstablished_NewConnection`, `TestNewConnectionAfterTuplesFromEarlierSessionIsCollision` (GoBGP) |
| RFC8950-ENHE-CHECK | 8950 §4 | fork ([#699]), upstream v4.11.0 | `TestPostFilterpathExtendedNexthop` (GoBGP) |
| RFC8950-ENHE-EVPN | 8950 §3, §4 | fork ([#699]), upstream v4.11.0 | `TestCapabilitiesExtendedNexthopIPv4Only` (GoBGP) |
| RFC8365-ENCAP | 8365 §5.1.3 | [#746] | `TestEVPNRoutesCarryVXLANEncapsulation`, `TestRFCWireType2`, `TestRFCWireType3` |
| RFC8365-ENCAP-RX | 8365 §6 | [#771] | `TestProcessRouteUpdateHonoursEncapsulation` |
| RFC9136-RMAC, RFC9136-ESIGW, RFC9136-LABEL0 | 9136 §3.1, §3.2 | [#747], [#799] | `TestProcessRouteUpdateType5TreatAsWithdraw`, `TestParseRouterMACRejectsNonUnicast`, `TestType5GatewayIPRouteConvertedWithdraw` |
| RFC7432-RD | 7432 §7.9 | [#710] | `TestEVPNRDs`, `TestRouteDistinguisher`, `TestRFCWireType5` |
| RFC9047-FLAGS | 9047 §3.2 | [#798] | `TestType2NeighborRouterFlag`, `TestType2NeighborRouterFollowsBestPath` |
| RFC7432-MACIP-WITHDRAW, RFC7432-MOBILITY-RX | 7432 §10, §15 | [#748] | `TestHandleType2RouteBestPathPerMAC` |
| RFC7432-ESI-RX | 7432 §8.4, §9.2.2, §14, §17.3 | [#767] | `TestType2ESIAliasingAndMassWithdraw`, `TestESINetlinkAliasing` |
| RFC8365-PMSI-RX | 8365 §9 | [#772] (documented in [#786]) | `TestProcessRouteUpdateType3TunnelType` |
| RFC9136-GWIP | 9136 §3.2 | [#775] | `TestType5GatewayIPResolvedByType2`, `TestType5GatewayIPResolutionKeepsTheActivePath` |
| RFC9135-ARP-RX | 9135 §5.2, §6.2 | [#766], [#784] | `TestHandleType2RouteInstallsNeighbor`, `TestType2NeighborKeptByTheOtherRD`, `TestType2AndType5ShareTheBridgeNeighbor` |
| RFC5880-REMOTEDISCR, RFC5880-DEMAND | 5880 §6.8.1, §6.8.7, §6.8.18 | [#749], [#768] | `TestSessionForgetsRemoteAfterDetectionTime`, `TestSessionNoPeriodicPacketsInRemoteDemandMode` |
| LLDP-SHUTDOWN, LLDP-RX-ORDER, LLDP-END | 802.1AB | [#754], [#769], [#792] | `TestShutdownFrame`, `TestEmitShutdownFrame`, `TestStopNetworkOnceStopsLLDPFirst`, `TestParseLLDPMandatoryTLVs` |
| RFC4861-FINAL-RA | 4861 §6.2.5 | [#752] | `TestPeriodicRAEndsWithZeroLifetime` |
| RFC4861-ALLROUTERS | 4861 §6.2.2 | [#846] | `TestListenRSJoinsAllRouters` |
| RFC2131-EXPIRE | 2131 §4.4.5, §3.2 | [#751], [#790] | see 2131-5, 2131-6 |
| RFC2131-RENEW, RFC2131-BACKOFF | 2131 §4.3.2, §4.4.5, §4.1 | [#790] | see 2131-3, 2131-9 |
| RFC2131-RELEASE-SID, RFC2132-MAXSIZE | 2131 Table 5, 2132 §9.10 | [#908] | `TestRebootAndRelease`, `TestMaxMessageSize` |
| RFC8415-RELEASE | 8415 §18.2.7 | [#892] | `TestDHCPv6DropsAddressBeforeRelease` |
| RFC4861-RA-OPTLEN | 4861 §4.6, §6.1.2 | [#915] | `TestValidRA` |
| RFC2131-EXPIRE-SELECTING | 2131 §4.4.5 | [#917] | `TestLeaseExpiresInSelectingAfterFailedReboot` |
| RFC8415-ZERO-LIFETIME | 8415 §18.2.10.1 | [#918] | `TestReplyWithZeroValidLifetime` |
| RFC8415-SOL-MAX-RT | 8415 §18.2.9, §21.24 | [#919] | `TestDecodeMaxRT`, `TestSolMaxRT` |
| RFC8415-INF-OPTIONS | 8415 §18.2.6, §21.25 | [#919] | `TestInfMaxRT` |
| RFC8415-SOL-DELAY | 8415 §18.2.1 | [#919] | `TestFirstSolicitDelay` |

## BGP

### RFC 4271: BGP-4

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 4271-1 | 4.2 | OPEN Version MUST be 4; Unsupported Version Number otherwise | GoBGP | GOBGP: COMPLIANT | `gobgp@v4.11.0-booty.6/pkg/packet/bgp/validate.go:377` | reading |
| 4271-2 | 4.2, 6.2 | Hold Time MUST be 0 or at least 3; Unacceptable Hold Time otherwise | GoBGP, BOOTy | GOBGP: COMPLIANT | receive: `validate.go:425`; send: BOOTy rejects 1 and 2, and also 0 (FRR `timers 0 0`, which GoBGP would replace with 30/90; a local choice the RFC allows) (`pkg/network/gobgp/config.go:518`, `:497`), tests `TestScenarioBGPTimers`, `TestValidateSessions` | test |
| 4271-3 | 4.2 | BGP Identifier MUST be an IPv4 address of the speaker, the same for every peer | BOOTy | COMPLIANT | router ID = underlay IPv4, validated in `config.go:448`; one router ID for all peers (`underlay.go:528`) | reading |
| 4271-4 | 4.4, 10 | KEEPALIVE MUST NOT be sent more often than once per second; "a reasonable maximum time between KEEPALIVE messages would be one third of the Hold Time" (no keyword) | BOOTy, GoBGP | COMPLIANT | A keepalive of 0 is rejected for the global timers and every session (`config.go:492`, `pkg/config/validate.go:1325`). A keepalive above hold/3 is rejected for `BGP_KEEPALIVE`/`BGP_HOLD` (`config.go:521`). A session takes any keepalive (`config.go:486` `validateSessions`), and since [#916] `buildSessionPeer` lowers one above hold/3 to hold/3, like the keepalive that FRR negotiates (`families.go:161` to `:164`, `:176`); `TestBuildSessionPeerKeepaliveCap`. GoBGP alone keeps it and uses hold/3 only when the negotiated hold time is below the configured one (`gobgp@v4.11.0-booty.6/pkg/server/fsm.go:927` to `:931`), so before [#916] a session with `timers 30 9` sent a keepalive every 30 s against a 9 s hold time and lost an idle session | test |
| 4271-5 | 4.2 | Hold timer MUST be the smaller of the configured and received hold time | GoBGP | GOBGP: COMPLIANT | `fsm.go:914` | reading |
| 4271-6 | 4.3 | UPDATE: withdrawn routes, path attributes, NLRI encoding | GoBGP | GOBGP: COMPLIANT | GoBGP encoder; lab `TestProdTplLeafSessions`, `TestProdTplRRSessions` | lab |
| 4271-7 | 4.5, 6 | NOTIFICATION codes and subcodes for each error | GoBGP | GOBGP: COMPLIANT | `validate.go:376` (OPEN), `bgp.go` MessageError codes | reading |
| 4271-8 | 5.1.1 | ORIGIN MUST be generated by the originating speaker | BOOTy | COMPLIANT | ORIGIN IGP on the underlay route (`underlay.go:1051`) and every EVPN route (`overlay.go:2497`, `originIGP`); `TestRFCWireType5`, `TestRFCWireType3`, `TestRFCWireType2` check ORIGIN IGP on the wire | test |
| 4271-9 | 5.1.2 | AS_PATH: originator sends empty AS_PATH to iBGP and its AS to eBGP; prepend own AS to eBGP | GoBGP | GOBGP: COMPLIANT | BOOTy adds an empty AS_PATH (`underlay.go:1053`); GoBGP prepends for eBGP (`internal/pkg/table/path.go:401`) and keeps it empty for iBGP (`path.go:423`); lab `TestProdTplLocalASReplaceASPath` | lab |
| 4271-10 | 5.1.3 | NEXT_HOP to eBGP MUST be an address of the sending speaker on the shared link; iBGP keeps the next hop of an external route | GoBGP, BOOTy | GOBGP: COMPLIANT | next hop 0.0.0.0 for the underlay route is replaced by the session address (`path.go:392`); lab `TestGoBGPUnnumberedUnderlayRoute`, `TestProdTplRFC5549Routes` | lab |
| 4271-11 | 5.1.4 | MED received from a neighboring AS MUST NOT be propagated to other ASes | GoBGP | GOBGP: COMPLIANT | `path.go:406` (removed for non-local paths to eBGP). BOOTy is no transit in practice | reading |
| 4271-12 | 5.1.5 | LOCAL_PREF MUST be sent to iBGP peers and MUST NOT be sent to eBGP peers | GoBGP | GOBGP: COMPLIANT | added for iBGP (`path.go:430`), removed for eBGP (`gobgp@v4.11.0-booty.6/pkg/server/server.go:902`, `postFilterpath`) | reading |
| 4271-13 | 5.1.6, 5.1.7 | ATOMIC_AGGREGATE, AGGREGATOR | GoBGP | N/A | BOOTy does not aggregate | reading |
| 4271-14 | 6.2 | OPEN errors: Bad Peer AS, Bad BGP Identifier (RFC 6286 rules) | GoBGP | GOBGP: COMPLIANT | `validate.go:401` to `:422`. Unnumbered peers take any peer AS (remote AS from OPEN, FRR `remote-as external` style) | reading |
| 4271-15 | 6.3 | UPDATE errors: malformed attribute list, missing well-known attribute, invalid ORIGIN, invalid NEXT_HOP, AS loop | GoBGP | GOBGP: COMPLIANT | `validate.go:11` to `:109`, revised by RFC 7606 (see below) | reading |
| 4271-16 | 6.5 | Hold timer expiry MUST send NOTIFICATION Hold Timer Expired | GoBGP | GOBGP: COMPLIANT | `fsm.go:2248` (OpenSent), `:2563` (OpenConfirm), `:3000` (Established); `TestFSMHandlerOpensent_HoldTimerExpired` | GoBGP test |
| 4271-17 | 6.8 | Connection collision detection MUST compare BGP Identifiers and close one connection | GoBGP | GOBGP: COMPLIANT | RFC4271-COLLISION at the first audit: GoBGP 3.37.0 closed every new connection. GoBGP 4 compares the BGP Identifiers and closes the losing connection with Cease/Connection Collision Resolution (`fsm.go:707`, `resolveCollision`). Upstream v4.11.0 resolves collisions in OpenSent and OpenConfirm and keeps the surviving connection ([osrg/gobgp#3741](https://github.com/osrg/gobgp/issues/3741), [osrg/gobgp#3751](https://github.com/osrg/gobgp/issues/3751)). The fork closes the losing incoming connection only after the peer's OPEN arrives on it (`fsm.go:1149`, `loserAnswer`, [osrg/gobgp#3863](https://github.com/osrg/gobgp/issues/3863)) and, since [#884], closes the peer's connection at once when GoBGP's OPEN is out on its own one, like FRR `bgp_collision_detect`. GoBGP tests `TestResolveCollision`, `TestFSMHandlerOpenconfirm_Collision`, `TestOpensentCollisionWithOurOpenSent`, `TestCollisionResolvedByGoBGPKeepsSurvivingConnection`, `TestCollisionSimultaneousOpenFRRTiming`, `TestCollisionBothConnectionsDropped`, `TestCollisionLoserClosedOnlyAfterItsOpen`, `TestCollisionLoserAnswers`; BOOTy `TestCollisionAfterCarrierLoss`; lab `TestProdTplOneDropPerFault`. See 4271-31 for the dialed connection ([#831]) | test, lab, GoBGP test |
| 4271-18 | 8 | FSM: ConnectRetryTimer, IdleHoldTimer, states and events | GoBGP, BOOTy | GOBGP: COMPLIANT | connect retry and idle hold from BOOTy (`families.go:32`, idle hold after reset 1 s like FRR, `families.go:29`). [#745] (merged) restarts a peer at once after a session end. Idle hold: 4271-33; start: 4271-34 | reading |
| 4271-19 | 9 | Adj-RIB-In / Loc-RIB / Adj-RIB-Out processing | GoBGP | GOBGP: COMPLIANT | GoBGP RIB | reading |
| 4271-20 | 9.1.2.1 | A route with an unresolvable NEXT_HOP SHOULD be excluded from the decision process | GoBGP, BOOTy | GOBGP: DEVIATION | RFC4271-NH-RESOLVE: GoBGP has no next hop tracking. BOOTy's FIB writer resolves each next hop and skips unresolvable ones (`pkg/network/gobgp/fib.go:349`), but a resolvable second-best path is not used then. Impact low | reading |
| 4271-21 | 9.1.2.2 | Decision process tie breaking | GoBGP, BOOTy | GOBGP: COMPLIANT | GoBGP best path; BOOTy's FIB multipath uses the FRR rules (`fibselect.go:156` `equalCost`, `:177` `better`), tests `fibselect_test.go` | reading |
| 4271-22 | 9.1.3 | Only the best route is advertised (without add-path send) | GoBGP | GOBGP: COMPLIANT | GoBGP default | reading |
| 4271-23 | 9.2 | Routes with own AS in AS_PATH: loop detection | GoBGP | GOBGP: COMPLIANT | `gobgp@v4.11.0-booty.6/pkg/server/peer.go:806` with `allowas-in` (`families.go:55`); lab `TestProdTplLocalASLoopCheck` | lab |
| 4271-24 | 9.2.1.1 | MinRouteAdvertisementIntervalTimer | GoBGP | GOBGP: COMPLIANT | 0 in GoBGP and in FRR for these peers (documented in `docs/networking.md`) | reading |
| 4271-25 | 9.3 | Routes BOOTy originates | BOOTy | COMPLIANT | underlay /32 (`underlay.go:1036`), EVPN routes (`overlay.go:730`, `:776`, `:806`) | reading |
| 4271-26 | 10 | ListenPort 179 | BOOTy | COMPLIANT | `config.go:277` | reading |
| 4271-27 | 5 | Each attribute at most once per UPDATE | GoBGP | GOBGP: COMPLIANT | BOOTy builds one of each; GoBGP checks duplicates on receipt (`validate.go:29`) | reading |
| 4271-28 | 8.1.1 | Optional session attributes (DelayOpen, DampPeerOscillations) | GoBGP | GOBGP: COMPLIANT | optional; not used | reading |
| 4271-29 | 6.7 | Cease NOTIFICATION when a session is closed | GoBGP, BOOTy | COMPLIANT | GoBGP sends Cease on StopBgp and ResetPeer; a BFD Down reset sends Cease/BFD Down (subcode 10, see 9384-1) | reading |
| 4271-30 | 3 | Routes withdrawn when a session ends | GoBGP | GOBGP: COMPLIANT | GoBGP drops the routes of every family that the graceful restart Helper does not keep as stale (`server.go:2317`) | reading |
| 4271-31 | 6.8, 8.2.2 | Collision: a connection that GoBGP dialed before the peer's OPEN arrived on the other connection takes part in the collision resolution; no ConnectRetry in OpenConfirm | GoBGP | GOBGP: COMPLIANT | RFC4271-COLLISION-DIALED at the refresh: v4.10.0-booty.10 stopped its dialed connection when the peer's OPEN arrived on the other one, so with `BGP_CONNECT_AT_ONCE` both connections could be gone and the FSM waited 5 s in Idle. Fixed in [#831] (merged): the fork keeps the dialed connection; upstream b9438d5e covers the time after the dial ([osrg/gobgp#3898](https://github.com/osrg/gobgp/issues/3898)). No connect retry in OpenConfirm and Established. GoBGP tests `TestFSMHandlerOpensent_DialedOutgoingConnKeptForCollision`, `TestConnectLoopWaitsInOpenConfirm`, `TestOutgoingConnManagerKeepsRunningInOpenConfirm` | GoBGP test |
| 4271-32 | 8.2.2 | OpenSent: a NOTIFICATION (Event 25) is answered with the FSM Error NOTIFICATION; a NOTIFICATION with a version error (Event 24) gets no answer; OpenConfirm answers no NOTIFICATION | GoBGP | GOBGP: COMPLIANT | a NOTIFICATION in OpenSent gets FSM Error 5/1 (`fsm.go:2096`), and the FSM goes on with the outgoing connection instead of 5 s Idle (upstream db3c9280); a NOTIFICATION with a version error gets no answer (`fsm.go:2088`, upstream b5630a0b). GoBGP tests `TestNotificationInsteadOfOpen`, `TestOpenSentVersionErrorGetsNoAnswer`; lab `TestProdTplCarrierLossPastBFD`. The old tag v4.10.0-booty.7 did not answer it (`docs/networking.md`) | lab, GoBGP test |
| 4271-33 | 8.1.1, 8.2.2 | IdleHoldTimer (optional session attribute): the time in Idle before a new start | GoBGP, BOOTy | GOBGP: COMPLIANT | GoBGP holds a peer 5 s in Idle after a session end and IdleHoldTimeAfterReset after ResetPeer; BOOTy sets 1 s (`families.go:29`). Upstream handles ResetPeer in every FSM state and ignores it for an administratively down peer (95c43b00, [osrg/gobgp#3740](https://github.com/osrg/gobgp/issues/3740)). The fork applies it to the BFD Down reset (`fsm.go:489`, `peerReset.bfdDown`), backs off like FRR `v_start`, and a reset never lengthens a running idle hold timer (`fsm.go:3099`). GoBGP tests `TestFSMHandlerIdle_ResetWhileAdminDown`, `TestResetPeerShortensRunningIdleHoldTimer`, `TestNextIdleHoldTime`; BOOTy `TestPeersIdleHoldAfterReset` | test, GoBGP test |
| 4271-34 | 8.2.2 | Idle, ManualStart/AutomaticStart: start the ConnectRetryTimer and initiate a TCP connection | GoBGP, BOOTy | GOBGP: COMPLIANT | Upstream GoBGP waits a random 0 to 1 s before the first connect after Idle (2be423ab; [osrg/gobgp#3736](https://github.com/osrg/gobgp/issues/3736)); the RFC sets no delay, FRR connects at once. Fork option `ConnectAtOnce` (default off, `fsm.go:1483`, [osrg/gobgp#3896](https://github.com/osrg/gobgp/issues/3896)); BOOTy sets it with `BGP_CONNECT_AT_ONCE`, off by default because of 4271-31 (`families.go:39`). GoBGP test `TestFirstConnectAtOnce`; BOOTy `TestPeersIdleHoldAfterReset` checks the option on every peer | test, GoBGP test |

### RFC 4760: Multiprotocol extensions

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 4760-1 | 3 | Next hop encoding MUST identify the network layer protocol (by length) | GoBGP | GOBGP: COMPLIANT | `bgp.go` `PathAttributeMpReachNLRI.DecodeFromBytes` | reading |
| 4760-2 | 3 | Reserved octet MUST be 0, SHOULD be ignored | GoBGP | GOBGP: COMPLIANT | GoBGP encoder writes 0 | reading |
| 4760-3 | 3 | MP_REACH_NLRI UPDATE MUST carry ORIGIN and AS_PATH, and LOCAL_PREF in iBGP | BOOTy, GoBGP | COMPLIANT | ORIGIN from BOOTy (`overlay.go:2497`), AS_PATH and LOCAL_PREF by GoBGP (`path.go:423`, `:430`); `TestRFCWireType5`, `TestRFCWireType3`, `TestRFCWireType2` | test |
| 4760-4 | 3 | UPDATE with only MP NLRI SHOULD NOT carry NEXT_HOP; receiver SHOULD ignore it | BOOTy, GoBGP | COMPLIANT | EVPN paths carry only MP_REACH_NLRI (`overlay.go:2505`); `TestRFCWireType5`, `TestRFCWireType3`, `TestRFCWireType2` check that no NEXT_HOP attribute is present | test |
| 4760-5 | 3 | Same prefix not in more than one of NLRI, MP_REACH, MP_UNREACH | GoBGP | GOBGP: COMPLIANT | GoBGP encoder | reading |
| 4760-6 | 7 | Incorrect MP_REACH/MP_UNREACH: delete the routes of the AFI/SAFI, SHOULD ignore further ones, SHOULD terminate | GoBGP | GOBGP: COMPLIANT | AFI/SAFI disable (`fsm.go:1894`, `:1986`), refined by RFC 7606 §7.11 | reading |
| 4760-7 | 8 | Capability advertisement for each AFI/SAFI; both sides MUST advertise for a bi-directional exchange | GoBGP, BOOTy | GOBGP: COMPLIANT | `fsm.go:1654`; BOOTy sets the families per peer (`families.go:102` to `:146`, `:228`); lab `TestGoBGPDualEVPNOnNumberedOnly`, `TestProdTplOpenCapabilities` | lab |
| 4760-8 | 8 | Unadvertised families not sent | GoBGP | GOBGP: COMPLIANT | `validate.go:124` rejects families not in the session | reading |
| 4760-9 | 3 | MP_REACH next hop for EVPN: the VTEP address | BOOTy | COMPLIANT | `overlay.go:2505`; `TestRFCWireType5`, `TestRFCWireType3`, `TestRFCWireType2` | test |

### RFC 6793: 4-octet AS numbers

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 6793-1 | 4.1 | Speaker SHALL advertise the 4-octet AS capability with its AS | GoBGP | GOBGP: COMPLIANT | `fsm.go:1656`; lab `TestProdTplOpenCapabilities` (`4byteAs`) | lab |
| 6793-2 | 4.1 | Use the capability AS instead of My AS | GoBGP | GOBGP: COMPLIANT | `validate.go:390` | reading |
| 6793-3 | 4.1 | 4-octet AS_PATH and AGGREGATOR between NEW speakers; no AS4_PATH/AS4_AGGREGATOR between NEW speakers; discard them if received | GoBGP | GOBGP: COMPLIANT | `internal/pkg/table/message.go:110` | reading |
| 6793-4 | 4.2.1 | AS_TRANS in My AS for an AS above 65535 | GoBGP | GOBGP: COMPLIANT | `fsm.go:1749` | reading |
| 6793-5 | 4.2.2, 4.2.3 | OLD speaker interworking (AS4_PATH merge, confed segments excluded, malformed AS4 attributes discarded and logged) | GoBGP | GOBGP: COMPLIANT | `message.go:110` to `:253` | reading |
| 6793-6 | 6 | Malformed AS4_PATH/AS4_AGGREGATOR from OLD speaker discarded, logged | GoBGP | GOBGP: COMPLIANT | `message.go:173` to `:195` | reading |
| 6793-7 | 4.1 | No confed segments in AS4_PATH | GoBGP | GOBGP: COMPLIANT | `message.go:173` | reading |
| 6793-8 | n/a | 4-octet ASN in BOOTy's RD and RT encodings (RFC 5668 4-octet AS specific type) | BOOTy | COMPLIANT | RD type 2 for an `ASN:NN` RD (`EVPN_RD` or `frr.conf` `rd`) with an AS above 65535 (`policy.go:185`); the default RD `<router-id>:<n>` is type 1 ([#710]). RT type 0x02 for an AS above 65535 (`overlay.go:2729`); `TestParseRouteDistinguisher`, `TestBuildRouteTarget`, `TestBuildType3PathAttrs4ByteASN` | test |
| 6793-9 | n/a | No silent truncation of a 4-octet ASN RD/RT value | BOOTy | COMPLIANT | VNI above 65535 with 4-octet ASN is rejected (`config.go:386`) | reading |
| 6793-10 | n/a | 4-octet local AS and remote AS for sessions | BOOTy | COMPLIANT | `families.go:45` to `:198` | reading |

### RFC 7911: ADD-PATH

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 7911-1 | 2 | Path Identifier unique per (prefix, neighbor); re-advertiser generates its own | GoBGP | GOBGP: COMPLIANT | GoBGP send side; BOOTy does not send multiple paths | reading |
| 7911-2 | 3 | NLRI extended with a 4-octet Path Identifier when negotiated | GoBGP | GOBGP: COMPLIANT | `bgp.go` `decodePathIdentifier` | reading |
| 7911-3 | 4 | One ADD-PATH capability for all AFI/SAFIs; unknown Send/Receive values ignored | GoBGP | GOBGP: COMPLIANT | `fsm.go:1613` | reading |
| 7911-4 | 5 | Send multiple paths only if both sides negotiated (2/3 and 1/3) | GoBGP, BOOTy | GOBGP: COMPLIANT | BOOTy configures receive only (`families.go:222`); lab `TestProdTplOpenCapabilities` (`addPath`) | lab |
| 7911-5 | 5 | Withdraw of an unknown Path Identifier SHOULD be ignored | GoBGP | GOBGP: COMPLIANT | GoBGP adj-in lookup by (prefix, id) | reading |
| 7911-6 | 5 | SHOULD include the best route when advertising several | GoBGP | N/A | BOOTy does not send add-path | reading |
| 7911-7 | n/a | Received paths installed with ECMP | BOOTy | COMPLIANT | FIB writer takes equal-cost paths of a destination (`fibselect.go:109`), `TestSelectRoutes*` | test |
| 7911-8 | draft | Paths-Limit capability (draft-abraitis-idr-addpath-paths-limit, not an RFC): the most paths per prefix a receiver wants, 0 for no limit | GoBGP | GOBGP: COMPLIANT | the fork sends limit 0 for the families that receive with ADD-PATH, like FRR (`fsm.go:1731`). Received limits are not applied to ADD-PATH send, which BOOTy does not use. GoBGP tests `TestCapPathsLimit`, `TestCapabilitiesPathsLimit` | GoBGP test |

### RFC 8950 (and RFC 5549): IPv4 NLRI with an IPv6 next hop

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 8950-1 | 3 | Receiver MUST use the next hop length to find the protocol | GoBGP | GOBGP: COMPLIANT | `bgp.go` `PathAttributeMpReachNLRI.DecodeFromBytes` (16 or 32 octets) | reading |
| 8950-2 | 4 | Advertise the Extended Next Hop Encoding capability (code 5) | GoBGP | GOBGP: COMPLIANT | `fsm.go:1699`; lab `TestProdTplOpenCapabilities` | reading |
| 8950-3 | 4 | MUST only advertise IPv4 NLRI with an IPv6 next hop if the peer advertised the capability for the AFI/SAFI | GoBGP | GOBGP: COMPLIANT | RFC8950-ENHE-CHECK at the first audit (GoBGP 3.37.0 and 4.10.0 never checked the peer's capability). Upstream v4.11.0 turns such a path into a withdrawal (`server.go:892`, `postFilterpath`; 2544643c, [osrg/gobgp#3743](https://github.com/osrg/gobgp/issues/3743)); GoBGP test `TestPostFilterpathExtendedNexthop` | GoBGP test |
| 8950-4 | 3, 4 | Capability tuples for IPv4 NLRI only | GoBGP | GOBGP: COMPLIANT | RFC8950-ENHE-EVPN at the first audit (GoBGP also listed L2VPN EVPN, and FRR logged a warning per OPEN). Upstream v4.11.0 sends tuples only for the IPv4 families of RFC 8950 (ee9c4b73, [osrg/gobgp#3744](https://github.com/osrg/gobgp/issues/3744), `fsm.go:1716`); GoBGP test `TestCapabilitiesExtendedNexthopIPv4Only` | GoBGP test |
| 8950-5 | 5 | Over an IPvx session, own next hop SHOULD be IPvx | GoBGP | COMPLIANT | unnumbered sessions run over IPv6 link-local and advertise IPv6 next hops (`underlay.go:1042`); numbered IPv4 sessions use IPv4 | reading |
| 8950-6 | 5 | Next hop passed along unchanged keeps its encoding | GoBGP | N/A | BOOTy is no route reflector | reading |
| 8950-7 | 3 | IPv4 route with an IPv6 (link-local) next hop installed in the kernel | BOOTy | COMPLIANT | `RTA_VIA` in `fib.go:338`; lab `TestProdTplRFC5549Routes`, `TestProdTplFIBRoutes` | lab |
| 8950-8 | 3 | Link-local next hop bound to its interface | BOOTy | COMPLIANT | `fibselect.go` `fibNexthop.Dev` from the peer interface (`fib.go:322`) | reading |

### RFC 4724: Graceful restart (helper)

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 4724-1 | 3 | Only one GR capability; receiver uses the last | GoBGP | GOBGP: COMPLIANT | `fsm.go:1693`, `:954` | reading |
| 4724-2 | 3 | Reserved bits zero | GoBGP | GOBGP: COMPLIANT | GoBGP encoder | reading |
| 4724-3 | 3, 4.2 | Restart State bit MUST NOT be set unless restarted | GoBGP, BOOTy | GOBGP: COMPLIANT | helper only (`families.go:214`), `restarting` false | reading |
| 4724-4 | 4 | End-of-RIB MUST be sent after the initial update of each family (when GR is negotiated) | GoBGP | GOBGP: COMPLIANT | `server.go:1330` | reading |
| 4724-5 | 4.1 | Restarting Speaker procedures (keep forwarding state, defer selection, selection deferral timer) | BOOTy | N/A | BOOTy is helper only and keeps no forwarding state; it sends no family tuples, so peers do not keep its routes (`families.go:206`) | reading |
| 4724-6 | 4.2 | Receiving Speaker MUST retain the routes of a restarting peer and mark them stale | GoBGP | GOBGP: COMPLIANT | `server.go:2309` (the graceful restart Helper, `internal/pkg/gr`), `internal/pkg/table/adj.go:310`; lab `TestProdTplRRRestart` | lab |
| 4724-7 | 4.2 | Previously stale routes MUST be deleted on consecutive restarts; stale routes deleted after Restart Time | GoBGP | GOBGP: COMPLIANT | GoBGP restart timer | reading |
| 4724-8 | 4.2 | Stale routes replaced by updates; removed at End-of-RIB | GoBGP | GOBGP: COMPLIANT | GoBGP EOR handling (`server.go:2386` to `:2536`) | reading |
| 4724-9 | 4.2 | No difference between stale and other routes in forwarding | BOOTy | COMPLIANT | FIB writer installs best paths regardless of the stale flag (`fib.go:190`) | reading |
| 4724-10 | 4.2 | A new connection from a restarting peer MUST close the old session and keep the new one | GoBGP | GOBGP: COMPLIANT | RFC4724-NEWCONN at the first audit: GoBGP 3.37.0 and 4.10.0 closed the new connection in Established, so the old session lasted until BOOTy's next KEEPALIVE drew an RST or BFD ended it. Upstream v4.11.0 ends the old session as a graceful restart and takes the new connection (3eef7645, [osrg/gobgp#3745](https://github.com/osrg/gobgp/issues/3745)); the fork does so only when the new OPEN says that the peer preserved forwarding state, so a late connection of a helper-only FRR leaf is a collision ([#876], [osrg/gobgp#3894](https://github.com/osrg/gobgp/issues/3894), `fsm.go:2938`). GoBGP tests `TestFSMHandlerEstablished_NewConnection`, `TestNewConnectionAfterTuplesFromEarlierSessionIsCollision` | GoBGP test |
| 4724-11 | 5 | Second connection tracked until OPEN | GoBGP | GOBGP: COMPLIANT | see 4271-17 (collision handling of the fork) | reading |
| 4724-12 | n/a | RFC 8538 N bit (Notification support) | GoBGP | N/A | not in the RFC scope of this audit; [#744] (merged) sets the N bit like FRR | reading |

### RFC 7313: Enhanced route refresh

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 7313-1 | 4 | SHOULD advertise the capability when supported | GoBGP | GOBGP: COMPLIANT | the fork sends the Enhanced Route Refresh capability next to Route Refresh, like FRR (`fsm.go:1637`); GoBGP tests `Test_EnhancedRouteRefreshCapability`, `TestEnhancedRouteRefresh` | GoBGP test |
| 7313-2 | 4 | Sender: when both sides advertised it, the routes of a refresh go between BoRR and EoRR | GoBGP | GOBGP: COMPLIANT | the fork: the refresh goes out between BoRR and EoRR; a queued EoRR goes out after the UPDATEs queued before it (`server.go:1432`); GoBGP test `TestEnhancedRouteRefresh` | GoBGP test |
| 7313-3 | 4 | Receiver: BoRR marks the routes of the family stale, EoRR removes those not sent again; a local bound for a missing EoRR | GoBGP | GOBGP: COMPLIANT | the fork (`server.go:1451`); the bound is the graceful restart stale-routes-time, else 360 s like FRR's stalepath-time (`server.go:1496`); GoBGP tests `TestEnhancedRouteRefresh`, `TestEnhancedRouteRefreshNoEoRR` | GoBGP test |
| 7313-4 | 4 | A BoRR from a graceful restart peer before its End-of-RIB for the family MUST be ignored | GoBGP | GOBGP: COMPLIANT | `server.go:1461`; GoBGP test `TestEnhancedRouteRefreshBoRRBeforeEoR` | GoBGP test |
| 7313-5 | 5 | Unknown ROUTE-REFRESH subtypes ignored; BoRR and EoRR without the negotiated capability ignored | GoBGP | GOBGP: COMPLIANT | `server.go:1476` | reading |

### RFC 2918: Route refresh

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 2918-1 | 3 | Advertise the Route Refresh capability to request refreshes | GoBGP | GOBGP: COMPLIANT | `fsm.go:1636`; lab `TestProdTplOpenCapabilities` (`routeRefresh`) | lab |
| 2918-2 | 4 | On ROUTE-REFRESH, re-advertise the Adj-RIB-Out of the AFI/SAFI | GoBGP | GOBGP: COMPLIANT | `server.go:1432` | reading |
| 2918-3 | 4 | Ignore a request for an AFI/SAFI not advertised (should) | GoBGP | GOBGP: COMPLIANT | `server.go:1438` | reading |
| 2918-4 | 4 | Soft reconfiguration of policies without session reset | BOOTy | COMPLIANT | `routemap.go:347` soft reset after installing policies | reading |

### RFC 8654: Extended message

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 8654-1 | 3, 4 | A speaker that advertises the capability MUST be able to receive messages up to 65535 octets (from any peer); it MAY send them only to a peer that advertised it | GoBGP | GOBGP: DEVIATION | RFC8654-RX: GoBGP 4 sends the capability in every OPEN (`fsm.go:1651`) and sends extended messages only to a peer that sent it too, as required. But it also raises its receive limit only when the peer sent the capability (`fsm.go:912`, `:1927`): an UPDATE above 4096 octets from a peer that did not advertise it, which section 4 allows, ends in Bad Message Length. GoBGP tests `TestExtendedMessage_AdvertisedUnconditionally`, `TestIsExtendedMessageSerialization`. Impact very low: BOOTy's routes and the fabric's EVPN routes are far below 4096 octets, and FRR advertises the capability. Fix in GoBGP: accept up to 65535 octets for UPDATE, NOTIFICATION and ROUTE-REFRESH whenever the capability was advertised | reading |
| 8654-2 | 5 | A speaker that has not advertised the capability MUST NOT accept extended messages; a NOTIFICATION to a peer that did not advertise it MUST NOT exceed 4096 octets | GoBGP | GOBGP: COMPLIANT | GoBGP always advertises the capability, so the first case does not arise; messages to a peer without it, NOTIFICATION included, are limited to 4096 octets (`bgp.go:17818` `BGP_MAX_MESSAGE_LENGTH`, `fsm.go:1927`) | reading |

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
| 5492-3 | 4 | Capabilities in one optional parameter (SHOULD) | GoBGP | GOBGP: COMPLIANT | `fsm.go:1745` | reading |
| 5492-4 | 3 | Unsupported Capability data lists the capabilities | GoBGP | GOBGP: COMPLIANT | GoBGP does not require capabilities of peers | reading |
| 5492-5 | 4 | No duplicate capability instances (SHOULD NOT) | GoBGP | GOBGP: COMPLIANT | `fsm.go:1633` builds one of each | reading |

### RFC 7606: Revised error handling for UPDATE messages

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 7606-1 | 2, 3 | Treat-as-withdraw instead of session reset for attribute errors | GoBGP | GOBGP: COMPLIANT | on by default (`gobgp@v4.11.0-booty.6/pkg/config/oc/default.go:125`), handled in `fsm.go:1879` (`handlingError`) | reading |
| 7606-2 | 3 (c), (g) | Duplicate attributes: discard all but the first; duplicate MP_REACH/MP_UNREACH: session reset | GoBGP | GOBGP: COMPLIANT | `validate.go:29` to `:72` | reading |
| 7606-3 | 3 (d) | Missing well-known mandatory attribute: treat-as-withdraw | GoBGP | GOBGP: COMPLIANT | `validate.go:84` to `:102` | reading |
| 7606-4 | 7.x | Per-attribute handling (ORIGIN, AS_PATH, NEXT_HOP, MED, LOCAL_PREF, communities: treat-as-withdraw; ATOMIC_AGGREGATE, AGGREGATOR: attribute discard; MP_REACH/MP_UNREACH: AFI/SAFI disable) | GoBGP | GOBGP: COMPLIANT | `bgp.go:18000` | reading |
| 7606-5 | 5.3 | A malformed NLRI in MP_REACH_NLRI ends in session reset or AFI/SAFI disable | GoBGP | GOBGP: COMPLIANT | RFC7606-EVPN-PANIC at the first audit: GoBGP 3.37.0 panicked in the EVPN route type decoders for short route types 1 to 4, before any check of the negotiated families, so any BGP peer could end BOOTy (PID 1). GoBGP 4.10.0 ([#699]) returns a MessageError (`bgp.go:2893` and the other decoders); `TestEVPNDecodeTruncatedNLRIReturnsError` pins it. Defence in depth: [#765] and [#787] (merged) reboot after a kernel panic (`panic=10`). The panic regression test proposed in [#755] (closed) is on `main` since [#699] as `pkg/network/gobgp/rfc7606_decode_test.go` | test |
| 7606-6 | 5.4 | Routes with unrecognized NLRI types of a typed family (EVPN) MUST be discarded | GoBGP | GOBGP: COMPLIANT | RFC7606-TYPED-NLRI at the first audit (an unknown EVPN route type disabled L2VPN EVPN on the session, in 3.37.0 and 4.10.0). Upstream v4.11.0 skips the NLRI by its length (`bgp.go:3582`; 97833030, [osrg/gobgp#3742](https://github.com/osrg/gobgp/issues/3742)); GoBGP tests `Test_EVPNUnknownRouteTypeIsDiscarded`, `TestProcessMessageEVPNUnknownRouteTypeOnly` | GoBGP test |
| 7606-7 | n/a | BOOTy's own route processing does not fail on bad input | BOOTy | COMPLIANT | `processRouteUpdate` skips undecodable NLRIs and attributes (`overlay.go:870`), `TestProcessRouteUpdateGuardAndL2Branches`, `TestRouterMACInCommunitiesIgnoresUnmarshalFailures` | test |
| 7606-8 | 8 | Errors SHOULD be logged | GoBGP, BOOTy | COMPLIANT | GoBGP logs "treated as withdraw" (`fsm.go` `handlingError`); BOOTy logs skipped routes | reading |
| 7606-9 | 7.14 | An unrecognized Extended Community type or sub-type MUST NOT be treated as an error | GoBGP | GOBGP: COMPLIANT | GoBGP 4.10.0 treated an unknown EVPN sub-type as malformed (treat-as-withdraw): FRR's MAC/IP routes with the ARP/ND community (sub-type 0x08) were withdrawn. Upstream v4.11.0 keeps it as `UnknownExtended` (`bgp.go:15164`, f1fcae18); the fork also decodes the ARP/ND community (9047-1; 40650d96, on upstream master after v4.11.0, [osrg/gobgp#3746](https://github.com/osrg/gobgp/issues/3746)); GoBGP test `Test_UnknownEvpnExtended_RoundTrip` | GoBGP test |

### RFC 9384: Cease NOTIFICATION subcode for BFD

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 9384-1 | 3 | A speaker that ends a session because BFD went down SHOULD send Cease with subcode 10 (BFD Down) | BOOTy, GoBGP | COMPLIANT | BOOTy resets the peer with `ResetPeerRequest.BfdDown` (`gobgp/bfd.go:420`), a fork feature ([osrg/gobgp#3895](https://github.com/osrg/gobgp/issues/3895), `fsm.go:489`); upstream sends BFD Down only from its own BFD (7d11a2c2), which BOOTy does not use. Like FRR (`bgp_bfd.c`). GoBGP tests `TestResetPeerBfdDown`, `TestResetAfterBfdDownKeepsBfdDown`; BOOTy `TestResetPeerReestablishes` | test, GoBGP test |
| 9384-2 | 4 | When RFC 8538 requires a Hard Reset and BFD ended the session, BFD Down SHOULD be in the data of the Hard Reset | GoBGP | GOBGP: COMPLIANT | With the N bit exchanged, GoBGP sends Cease/Hard Reset with the Cease/BFD Down code in its data (`fsm.go:1080`, `sendEstablishedNotification`; upstream 95e1ed8b, 6bd78b2a, 625456d7). A received BFD Down counts as a Hard Reset (`bgp.go:17735`, `ShouldHardReset`), like FRR. Changed since the refresh: v4.10.0-booty.10 kept BFD Down a normal Cease, so a graceful restart peer kept the routes. GoBGP test `TestFSMHandlerEstablish_HardResetReceived` | GoBGP test, reading |

## BFD

BOOTy implements BFD itself (`pkg/network/bfd`): asynchronous mode, no
Demand mode, no Echo function, no authentication. Like FRR bfdd it
advertises Required Min Echo RX 50 ms ([#710]), but it loops back no echo
packets itself: only kernel IP forwarding returns them (5880-8). Since
[#852] the receive sockets are open before the first RA and BGP
connection; a packet for no session is dropped.

### RFC 5880: BFD

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 5880-1 | 4.1 | Version 1; Length; field layout | BOOTy | COMPLIANT | `packet.go:74`; `TestPacketRoundTripAndChecks`, `TestRFC5880PacketFields` | test |
| 5880-2 | 4.1 | Multipoint bit MUST be zero on transmit and receipt | BOOTy | COMPLIANT | never set (`packet.go:78`), discarded on receipt (`packet.go:143`) | reading |
| 5880-3 | 4.2 to 4.4, 6.7 | Authentication sections and procedures | BOOTy | N/A | not implemented; packets with the A bit are discarded (`packet.go:149`). See 5880-39 | reading |
| 5880-4 | 5 | Echo packets SHOULD be authenticated | BOOTy | N/A | no Echo function | reading |
| 5880-5 | 6.1 | Active role MUST send packets without having received any; at least one side Active | BOOTy | COMPLIANT | the session loop sends from the start (`manager_linux.go:245`, timer 0), `TestManagerAgainstFakeBFDD` | test |
| 5880-6 | 6.1 | Passive role MUST NOT send before receiving | BOOTy | N/A | BOOTy is always Active | reading |
| 5880-7 | 6.3, 6.8.1 | Local discriminator nonzero, unique, SHOULD be random | BOOTy | COMPLIANT | `manager_linux.go:228` | reading |
| 5880-8 | 4.1, 6.4, 6.8.7 | SHOULD advertise the lowest Required Min RX/Echo RX it can; Echo RX 0 when the system will not loop echo packets back | BOOTy | DEVIATION | RFC5880-ECHORX: [#710] advertises Required Min Echo RX 50 ms like FRR bfdd (`session.go:22`, `:204`; `TestRFC5880PacketFields`), but BOOTy loops back no echo packets: only kernel forwarding returns them, which is off for IPv6 by default and for IPv4 with `IP_FORWARDING=false` (bm4x template; `gobgp/helpers.go:28`). A peer with `echo-mode` then gets a failing echo session (`docs/networking.md`, "FRR and GoBGP compared", BFD row). Impact low: the fabric enables no echo mode; FRR bfdd has the same IPv4 exposure. Fix: advertise 0 without forwarding, or loop echo back. Kept for FRR parity | test |
| 5880-9 | 6.5 | Poll and Final never both set; Poll on the periodic packets, no extra packets | BOOTy | COMPLIANT | `session.go:210`; `TestSessionPollFinal` | test |
| 5880-10 | 6.6, 6.8.7 | Demand bit only with Demand mode, both Up | BOOTy | COMPLIANT | never set (`packet.go:78`) | reading |
| 5880-11 | 6.6 | Demand mode procedures (Poll on change) | BOOTy | N/A | BOOTy does not use Demand mode | reading |
| 5880-12 | 6.8.1 | Session state preserved for a detection time after the last packet | BOOTy | COMPLIANT | sessions persist until Close/Replace | reading |
| 5880-13 | 6.8.1 | SessionState and RemoteSessionState initialized Down; LocalDiag 0 | BOOTy | COMPLIANT | `session.go:67` | reading |
| 5880-14 | 6.8.1 | RemoteDiscr initialized 0, MUST be set to 0 after a detection time without packets | BOOTy | COMPLIANT | RFC5880-REMOTEDISCR at the first audit (a Down session kept a stale value). [#749]: every state forgets the remote session after a detection time (`session.go:162`, `CheckTimeout`); `TestSessionForgetsRemoteAfterDetectionTime` | test |
| 5880-15 | 6.8.1, 6.8.3 | DesiredMinTxInterval at least 1 s while not Up | BOOTy | COMPLIANT | `session.go:26`, `:69`, `:146`; `TestSessionPollFinal`, `TestRFC5880PacketFields` | test |
| 5880-16 | 6.8.1 | RemoteMinRxInterval initialized to 1 | BOOTy | COMPLIANT | `session.go:70` | reading |
| 5880-17 | 6.8.1 | DetectMult nonzero | BOOTy | COMPLIANT | `manager_linux.go:103` | reading |
| 5880-18 | 6.8.3 | Change of DesiredMinTx or RequiredMinRx MUST start a Poll Sequence | BOOTy | COMPLIANT | `session.go:132`; `TestSessionPollFinal` | test |
| 5880-19 | 6.8.3 | Increased DesiredMinTx in Up: old interval until the Poll Sequence ends | BOOTy | COMPLIANT | BOOTy only lowers it at Up (`session.go:135`) | reading |
| 5880-20 | 6.8.3 | Reduced RequiredMinRx in Up: previous value for the detection time until the Poll Sequence ends | BOOTy | COMPLIANT | `curRx` (`session.go:56`, `:97`); `TestSessionDetectionTime` | test |
| 5880-21 | 6.8.3 | Reduced remote Required Min RX: new interval at once, next packet as soon as practicable | BOOTy | COMPLIANT | `rearm` (`manager_linux.go:259`); `TestManagerFastTxAfterUp` | test |
| 5880-22 | 6.8.3 | Echo active: RequiredMinRx at least 1 s (SHOULD) | BOOTy | N/A | no Echo | reading |
| 5880-23 | 6.8.3 | Other timer changes take effect at once | BOOTy | COMPLIANT | `TxInterval`/`DetectionTime` computed per packet | reading |
| 5880-24 | 6.8.4 | Detection time = remote DetectMult x max(RequiredMinRx, remote DesiredMinTx); Init/Up to Down with diag 1 | BOOTy | COMPLIANT | `session.go:153`, `:162`; `TestSessionDetectionTime` | test |
| 5880-25 | 6.8.4, 6.8.5 | Demand mode and Echo detection | BOOTy | N/A | not used | reading |
| 5880-26 | 6.8.6 | Discard: version, length (24/26, payload), DetectMult 0, Multipoint, My Discriminator 0, Your Discriminator 0 in Init/Up, A bit without auth | BOOTy | COMPLIANT | `packet.go:110` to `:152`; `TestPacketRoundTripAndChecks` | test |
| 5880-27 | 6.8.6 | Nonzero Your Discriminator selects the session; no session: discard | BOOTy | COMPLIANT | `manager_linux.go:379`; `TestSessionIgnoresOtherDiscriminator`. [#852] opens the receive socket before the session (`manager_linux.go:143`, `Listen`); a packet for no session is dropped without answer or ICMP (`:365`); `TestManagerListenDropsUnknownSessions`, `TestManagerListenThenAddSharesSocket` | test |
| 5880-28 | 6.8.6 | Zero Your Discriminator: select by source, interface | BOOTy | COMPLIANT | per device listener and source address (`manager_linux.go:382`) | reading |
| 5880-29 | 6.8.6 | Store RemoteDiscr, RemoteState, RemoteDemandMode, RemoteMinRx | BOOTy | COMPLIANT | RFC5880-DEMAND at the first audit (the D bit was not kept). [#749] keeps it (`session.go:96`); `TestSessionNoPeriodicPacketsInRemoteDemandMode` | test |
| 5880-30 | 6.8.6 | Required Min Echo RX 0: echo transmission MUST cease | BOOTy | COMPLIANT | BOOTy never sends echo | reading |
| 5880-31 | 6.8.6 | Final received during a Poll Sequence ends it | BOOTy | COMPLIANT | `session.go:97` | reading |
| 5880-32 | 6.8.6 | State machine of section 6.2 (AdminDown, Down, Init, Up transitions, diag 3) | BOOTy | COMPLIANT | `session.go:114`; `TestSessionTransitions` | test |
| 5880-33 | 6.8.6, 6.8.7 | Remote Demand mode active (both sides Up): MUST cease periodic transmission | BOOTy | COMPLIANT | RFC5880-DEMAND at the first audit. [#749] stops the periodic packets while the peer's D bit is set and both sides are Up and no Poll runs (`session.go:184`); [#768] pins the Up condition; `TestSessionNoPeriodicPacketsInRemoteDemandMode` | test |
| 5880-34 | 6.8.7 | Interval = max(DesiredMinTx, RemoteMinRx), jitter 0 to 25 %, 10 to 25 % with DetectMult 1 | BOOTy | COMPLIANT | `session.go:180`, `:225`; `TestJitter` | test |
| 5880-35 | 6.8.7 | No periodic packets when RemoteMinRx is 0 | BOOTy | COMPLIANT | `session.go:181`; `TestSessionNoPacketsWhenRemoteRxIsZero` | test |
| 5880-36 | 6.8.7 | Poll received: Final as soon as practicable, without Poll | BOOTy | COMPLIANT | `manager_linux.go:301`, `session.go:210`; `TestSessionPollFinal` | test |
| 5880-37 | 6.8.7 | Packet contents: Diag, State, P, F, C (0 for a control plane implementation), A 0, D 0, M 0, DetectMult, Length, discriminators, intervals; Echo RX see 5880-8 | BOOTy | COMPLIANT | `session.go:195`; `TestRFC5880PacketFields`. Echo RX is 50 ms since [#710], not 0 (5880-8) | test |
| 5880-38 | 6.8.7 | SHOULD send a packet between periodic packets when the contents change | BOOTy | COMPLIANT | `manager_linux.go:301` sends at once on a state change | reading |
| 5880-39 | 9, 6.7 | Authentication over multiple hops SHOULD be used; implementations supporting authentication MUST support both SHA1 types | BOOTy | NOT IMPLEMENTED | no authentication (like the legacy FRR profile). Needed only for multihop sessions over untrusted paths | reading |
| 5880-40 | 6.8.15 | Forwarding plane reset: diag 4, Down | BOOTy | N/A | BOOTy keeps no independent forwarding plane | reading |
| 5880-41 | 6.8.16 | Entering AdminDown: SHOULD send packets for a detection time | BOOTy | DEVIATION | RFC5880-ADMINDOWN: one AdminDown packet at Close (`manager_linux.go:270`). [#723] (merged) stops BGP and BFD before kexec and reboot, with the same single packet. Impact low. Open | reading |
| 5880-42 | 6.8.18 | After a detection time without packets, RemoteMinRx SHOULD return to 1 | BOOTy | COMPLIANT | RFC5880-REMOTEDISCR at the first audit. [#749] (`session.go:162`); `TestSessionForgetsRemoteAfterDetectionTime` | test |
| 5880-43 | 7 | Multihop: congestion control MUST be implemented | BOOTy | DEVIATION | RFC5880-CONGESTION: none. Impact low (a few sessions per host). Open | reading |
| 5880-44 | 9 | Single hop: TTL 255 on transmit, checked on receipt | BOOTy | COMPLIANT | see 5881-9, 5881-10 | reading |

### RFC 5881: BFD for IPv4 and IPv6 (single hop)

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 5881-1 | 2 | Separate session per network-layer path and per protocol | BOOTy | COMPLIANT | one session per BGP neighbor and family (`gobgp/bfd.go`) | reading |
| 5881-2 | 2 | Echo: no ingress filtering on the interface | BOOTy | COMPLIANT | `rp_filter` 0 for `all` and `default` (`gobgp/helpers.go:32`, `frr/manager.go:629`). Peer echo packets come back only through kernel forwarding (IPv4 with `ip_forward` on; IPv6 forwarding is off by default); BOOTy does not loop them back itself. Relevant since [#710] advertises Echo RX 50 ms (5880-8) | reading |
| 5881-3 | 2, 4 | Echo transmission rules | BOOTy | N/A | BOOTy sends no echo | reading |
| 5881-4 | 3 | Both sides Active; Your Discriminator 0 packets associated by remote system, interface and protocol | BOOTy | COMPLIANT | `manager_linux.go:376` | reading |
| 5881-5 | 4 | Destination port 3784 | BOOTy | COMPLIANT | `manager_linux.go:27` | reading |
| 5881-6 | 4 | Source port 49152 to 65535, one per session | BOOTy | COMPLIANT | `manager_linux.go:448`; `TestRFC5881SourcePortAndTTL` | test |
| 5881-7 | 4 | Source port SHOULD be unique per system | BOOTy | COMPLIANT | one bound socket per session; two sessions bound to different devices could share a port number (rare) | reading |
| 5881-8 | 4 | Demultiplexing by the BFD mechanisms, not by port | BOOTy | COMPLIANT | by discriminator (`manager_linux.go:379`) | reading |
| 5881-9 | 5 | All packets sent with TTL/Hop Limit 255 | BOOTy | COMPLIANT | `manager_linux.go:468`; `TestRFC5881SourcePortAndTTL` (IPv4), `TestManagerSingleHopTTL`; the IPv6 Hop Limit (`SetHopLimit`) is checked by reading only | test |
| 5881-10 | 5 | Received packets with TTL other than 255 discarded | BOOTy | COMPLIANT | `manager_linux.go:356`; `TestManagerSingleHopTTL` | test |
| 5881-11 | 6 | Packets only over the one-hop path (bound to the interface) | BOOTy | COMPLIANT | `SO_BINDTODEVICE` (`manager_linux.go:488`) on the send and receive sockets | reading |
| 5881-12 | 6 | Multiaccess: source and destination on the subnet | Kernel | COMPLIANT | kernel source selection on the bound interface | reading |
| 5881-13 | 6 | Point-to-point links: no session identification by source address | BOOTy | N/A | the fabric links are Ethernet (multiaccess rules apply) | reading |
| 5881-14 | 6 | Destination stays the configured address when the source changes | BOOTy | COMPLIANT | `manager_linux.go:118`; a changed link-local peer is a new session (`Replace`, `TestManagerReplace`) | test |
| 5881-15 | 7 | Authentication SHOULD be used | BOOTy | DEVIATION | no authentication (5880-39); the legacy FRR profile has none either. Impact low on point-to-point fabric links | reading |
| 5881-16 | 4 | IPv6 link-local sessions carry the zone | BOOTy | COMPLIANT | `manager_linux.go:118`; lab `TestProdTplBFDSessions` | lab |

### RFC 5883: BFD for multihop paths

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 5883-1 | 5 | Destination port 4784 | BOOTy | COMPLIANT | `manager_linux.go:28` | reading |
| 5883-2 | 3 | Echo MUST NOT be used over multiple hops | BOOTy | COMPLIANT | no echo | reading |
| 5883-3 | 4.3 | Unidirectional links: Active sender, Passive receiver | BOOTy | N/A | not used | reading |
| 5883-4 | 6 | Cryptographic authentication SHOULD be used over multihop paths | BOOTy | DEVIATION | none; TTL at least 254 is checked instead (`manager_linux.go:356`, like FRR `minimum-ttl`). Impact low | reading |

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
| 7432-1 | 5 | ESI values and types (unique non-reserved ESI per ES) | BOOTy | N/A | single-homed, ESI 0 in every route (`overlay.go:2454`, `:2810`); `TestRFCWireType5`, `TestRFCWireType2` | test |
| 7432-2 | 6.1 | VLAN-based service: Ethernet Tag MUST be 0 | BOOTy | COMPLIANT | `overlay.go:2458`, `:2781`, `:2814`; `TestRFCWireType5`, `TestRFCWireType3`, `TestRFCWireType2` | test |
| 7432-3 | 6.2, 6.3 | VLAN bundle and VLAN-aware bundle services | BOOTy | N/A | one VNI per EVI | reading |
| 7432-4 | 7 | Route types 1 to 4 encoding | GoBGP | GOBGP: COMPLIANT | GoBGP encoders; BOOTy originates types 2, 3 and 5 only | reading |
| 7432-5 | 7, 7606 §5.4 | Unknown route types | GoBGP | GOBGP: COMPLIANT | discarded since fork booty.1, see 7606-6 | reading |
| 7432-6 | 7.2, 9.2.1 | MAC length 48, MAC 6 octets (802.1Q), IP 4 or 16 octets | BOOTy, GoBGP | COMPLIANT | `TestRFCWireType2` | test |
| 7432-7 | 7.5 | ESI Label extended community | BOOTy | N/A | multihoming only | reading |
| 7432-8 | 7.6 | ES-Import route target; RT constraint for it | BOOTy | N/A | multihoming only | reading |
| 7432-9 | 7.7 | MAC Mobility extended community, sequence number | GoBGP | GOBGP: COMPLIANT | GoBGP adds a sequence number one higher when BOOTy originates a MAC another PE advertised (`gobgp@v4.11.0-booty.6/pkg/server/server.go:2898`); BOOTy originates without one (first advertisement) | reading |
| 7432-10 | 7.8 | Default Gateway extended community | BOOTy | N/A | BOOTy is no default gateway of the EVI | reading |
| 7432-11 | 7.9 | RD MUST be the RD of the MAC-VRF, assigned per MAC-VRF, unique across MAC-VRFs on a PE | BOOTy | COMPLIANT | one MAC-VRF; since [#710] its Type-2 and Type-3 routes carry the L2VNI RD, `<router-id>:<n>` with the id after the instances, which differs from the RD of the Type-5 routes (`config.go:165`, `overlay.go:777`, `:2437`). An explicit `EVPN_RD` or `frr.conf` `rd` applies to both; `TestEVPNRDs` | test |
| 7432-12 | 7.9 | Type 1 RD RECOMMENDED | BOOTy | COMPLIANT | RFC7432-RD at the first refresh (`ASN:VNI`, the same on every host). [#710]: FRR's automatic RD `<router-id>:<n>`, type 1, per host (`config.go:165`, `overlay.go:2437`, `policy.go:185`); `EVPN_RD` or the `frr.conf` `rd` may set another type. `TestEVPNRDs`, `TestRouteDistinguisher`, `TestRFCWireType5`, `TestRFCWireType3`, `TestRFCWireType2` (type 1 RD on the wire) | test |
| 7432-13 | 7.10.1 | RT Global Administrator MUST be the AS of the PE | BOOTy | N/A | the rule applies to RTs auto-derived from a VLAN ID; BOOTy's default RT is `ASN:VNI` (`overlay.go:2729`) or the configured RT (see 8365-4) | reading |
| 7432-14 | 7.10.1 | RT auto-derivation from the VLAN ID | BOOTy | N/A | VXLAN: see 8365-4 | reading |
| 7432-15 | 8.1 to 8.3, 8.5 | Ethernet Segment route, A-D per ES/EVI, split horizon, DF election | BOOTy | N/A | no multihoming | reading |
| 7432-16 | 8.4, 14 | A-D per EVI route MUST NOT be used before the A-D per ES routes; aliasing and backup paths (SHOULD) | BOOTy | COMPLIANT | RFC7432-ESI-RX at the first audit (Type-1 routes ignored). [#767]: a VTEP is active on an ES only with both its EAD-per-ES and its EAD-per-EVI route (`esi.go:142`, `activeVTEPs`), like FRR; aliasing through a kernel FDB nexthop group (`esi.go:168`); `TestType2ESIAliasingAndMassWithdraw`, `TestESINetlinkAliasing` | test |
| 7432-17 | 8.6 | Single-homing PEs SHOULD support some multihoming procedures to interoperate (receive side) | BOOTy | COMPLIANT | [#767], see 7432-16, 7432-21, 7432-43; `TestType2ESIAliasingAndMassWithdraw` | test |
| 7432-18 | 9.1 | Local data plane learning MUST be supported | Kernel | COMPLIANT | bridge port learning (default); VXLAN remote learning is off (`overlay.go:564`), control plane learning instead | reading |
| 7432-19 | 9.2.1 | RD per 7.9, MPLS Label1 downstream assigned (VNI per RFC 8365), next hop the PE, at least one RT | BOOTy | COMPLIANT | `overlay.go:2807`, `:2824`; `TestRFCWireType2`, `TestBuildType2PathAttrs` | test |
| 7432-20 | 9.2.2 | Reserved ESI (0 or MAX): forwarding state from the MAC/IP route alone | BOOTy | COMPLIANT | `handleType2Route` (`overlay.go:1922`); `TestHandleType2RouteInstallsFDB` | test |
| 7432-21 | 9.2.2 | Non-reserved ESI: forwarding state only with the A-D per ES routes | BOOTy | COMPLIANT | RFC7432-ESI-RX at the first audit. [#767]: the MAC is installed only through the active VTEPs of its ES and not while the ES has none (`esi.go:168`, `syncES`); `TestType2ESIAliasingAndMassWithdraw` | test |
| 7432-22 | 10 | IP Address Length 32 or 128 | BOOTy | COMPLIANT | `TestRFCWireType2` | test |
| 7432-23 | 10 | One MAC/IP route per IP of a MAC; withdraw on dissociation | BOOTy | COMPLIANT | one IP (provision IP); routes end with the BGP session (no GR tuples, `families.go:206`) | reading |
| 7432-24 | 10 | MAC/IP withdraw MUST NOT remove the MAC entry while the MAC-only route lives | BOOTy | COMPLIANT | RFC7432-MACIP-WITHDRAW at the first audit. [#748] keeps every live route of a MAC by route key and points the FDB entry at the best one (`overlay.go:2169`, `:2188`, `:2202`); `TestHandleType2RouteBestPathPerMAC` | test |
| 7432-25 | 10 | ARP proxy (SHOULD) | BOOTy | N/A | BOOTy is the end host of its BD; the kernel VXLAN `proxy` flag follows netplan | reading |
| 7432-26 | 10.1 | Default gateway procedures | BOOTy | N/A | see 7432-10 | reading |
| 7432-27 | 11 | Each PE MUST advertise an IMET route | BOOTy | COMPLIANT | L2 mode (`overlay.go:776`); `TestAdvertiseType3PublishesIMETWithIngressReplication`, `TestOverlaySetupAdvertisesType2And3OnlyWithL2`. Type-5 modes have no BD to flood | test |
| 7432-28 | 11.1 | RD per 7.9; originating router IP common to all EVIs; next hop the PE; RTs per 7.10 | BOOTy | COMPLIANT | `overlay.go:2778`, `:2789`; `TestRFCWireType3` | test |
| 7432-29 | 11.2 | PMSI Tunnel attribute MUST be present | BOOTy | COMPLIANT | `overlay.go:2794`; `TestRFCWireType3` | test |
| 7432-30 | 11.2 | Ingress replication: tunnel type IR, tunnel identifier a routable address of the PE, downstream assigned label (the VNI) | BOOTy | COMPLIANT | `TestRFCWireType3` | test |
| 7432-31 | 11.2 | Leaf Information Required MUST be 0 and MUST be ignored on receipt | BOOTy | COMPLIANT | flags 0 (`overlay.go:2795`); BOOTy reads only the tunnel type of a received PMSI attribute (`overlay.go:2342`); `TestRFCWireType3` | test |
| 7432-32 | 11.2 | P-multicast trees, aggregation | BOOTy | N/A | ingress replication only | reading |
| 7432-33 | 12, 13.1 | BUM traffic replicated to each remote PE (ingress replication) | Kernel | COMPLIANT | BUM FDB entries per Type-3 VTEP (`overlay.go:2375`) and to the gateway (`overlay.go:671`); `TestHandleType3RouteAppendsBUM`, lab `TestGoBGPUnnumberedGatewayFDB` | test, lab |
| 7432-46 | 11.2 | Ingress replication: BUM traffic goes to the routable address in the PMSI Tunnel Identifier | BOOTy | DEVIATION | RFC7432-PMSI-ID: BOOTy floods to the originating router IP of the IMET route (`overlay.go:2355`) and ignores the PMSI attribute. FRR 10.7.1 does the same (zebra gets the IMET originator as remote VTEP, `bgp_zebra_send_remote_vtep`), and in VXLAN fabrics both addresses are the VTEP. Kept like FRR; documented choice | reading |
| 7432-47 | 11.2 | Use the PMSI tunnel type of the IMET route | BOOTy | COMPLIANT | RFC8365-PMSI-RX at the first audit. [#772]: a head-end replication entry only for tunnel type ingress replication, like FRR (`overlay.go:2342`, `ingressReplication`); an update to another tunnel type removes it, where FRR keeps it ([#786] documents the difference); `TestProcessRouteUpdateType3TunnelType` | test |
| 7432-34 | 12, 13 | ESI label, P2MP LSP and split horizon procedures | BOOTy | N/A | VXLAN, single-homed | reading |
| 7432-35 | 14.1 | Load balancing through A-D routes (aliasing), single-active primary | BOOTy | COMPLIANT | [#767]: all-active ES through the FDB nexthop group of its active VTEPs; a single-active ES uses only the primary PE, the VTEP of the MAC route (`esi.go:131`); `TestType2ESIAliasingAndMassWithdraw` | test |
| 7432-36 | 15 | First advertisement without MAC Mobility community | BOOTy | COMPLIANT | `overlay.go:2824` | reading |
| 7432-37 | 15 | A moved MAC: sequence one higher than the received one | GoBGP | GOBGP: COMPLIANT | `server.go:2810`; BOOTy's MAC is its own bridge MAC | reading |
| 7432-38 | 15 | Receiver: the higher sequence number wins; equal: the lowest PE IP | BOOTy | COMPLIANT | RFC7432-MOBILITY-RX at the first audit (the last update won). [#748]: sticky first like FRR, then the higher sequence number, then the lowest VTEP (`overlay.go:2188`, `bestType2Path`); `TestHandleType2RouteBestPathPerMAC` | test |
| 7432-39 | 15 | Sequence number wraparound MUST be handled | BOOTy | COMPLIANT | [#748]: RFC 1982 serial arithmetic (`overlay.go:1898`); `TestHandleType2RouteBestPathPerMAC` | test |
| 7432-40 | 15 | "A PE receiving a MAC/IP Advertisement route for a MAC address with a different Ethernet segment identifier and a higher sequence number than that which it had previously advertised withdraws its MAC/IP Advertisement route." | GoBGP | GOBGP: DEVIATION | RFC7432-MOBILITY-GOBGP, an interpretation: GoBGP (`internal/pkg/table/table_manager.go:355`) applies it only when the ESI values differ, so two single-homed PEs (both ESI 0) never trigger it. The same section says that for single-homed sites "there is no need for ESI comparison", so reading "different Ethernet segment" as "different ESI value" is GoBGP's choice. Impact very low (BOOTy's MAC is its own bridge MAC and does not move) | reading |
| 7432-41 | 15.1 | Duplicate MAC detection (M, N configurable), alert | BOOTy | N/A | BOOTy learns no MACs locally | reading |
| 7432-42 | 15.2 | Sticky MAC: alert on local learning | BOOTy | N/A | BOOTy learns no MACs locally and sends no sticky MACs | reading |
| 7432-43 | 17.3 | Withdraw on MAC age out and on A-D withdrawal (mass withdraw SHOULD) | BOOTy | COMPLIANT | [#767]: an EAD withdraw takes its VTEP out of the group, so the MACs of the ES move at once (`esi.go:168`); BOOTy's own MAC is static and does not age out; `TestType2ESIAliasingAndMassWithdraw` | test |
| 7432-44 | 18 | Control word | BOOTy | N/A | MPLS only | reading |
| 7432-45 | 9.2.2, 10 | Withdraw without next hop still removes the forwarding state | BOOTy | COMPLIANT | Type-2: the withdraw removes the route by its key (`overlay.go:1982`); `TestHandleType2RouteWithdraw`; Type-5: `TestHandleType5RouteWithdrawUsesStoredRouterMAC` | test |

### RFC 8365: EVPN network virtualization overlay (VXLAN)

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 8365-1 | 5.1.1 | Globally unique VNIs in a DC | BOOTy | COMPLIANT | one configured VNI, range 1 to 16777215 checked (`config.go:378`) | reading |
| 8365-2 | 5.1.2 | Option 1: one BD per EVI, RD and RT per VNI | BOOTy | COMPLIANT | one VNI; RD per VNI since [#710] (`config.go:165`, `overlay.go:2437`), RT `ASN:VNI` or configured (`overlay.go:2729`) | reading |
| 8365-3 | 5.1.2 | Option 2: VLAN-aware bundle | BOOTy | N/A | | reading |
| 8365-4 | 5.1.2.1 | RT auto-derivation (A bit, type, D-ID, VNI) | BOOTy | COMPLIANT | auto-derivation is optional ("can be"); BOOTy uses `ASN:VNI` like FRR. Both ends must use the same rule | reading |
| 8365-5 | 5.1.3 | VLAN-based service: Ethernet Tag MUST be 0 in MAC/IP, A-D per EVI and IMET routes | BOOTy | COMPLIANT | `TestRFCWireType2`, `TestRFCWireType3` | test |
| 8365-6 | 5.1.3 | VLAN-aware bundle Ethernet Tag rules | BOOTy | N/A | | reading |
| 8365-7 | 5.1.3 | VNI carried in the MPLS label field (24 bits) | GoBGP, BOOTy | GOBGP: COMPLIANT | `gobgp@v4.11.0-booty.6/pkg/packet/bgp/bgp.go:2760` (no shift); `TestRFCWireType5`, `TestRFCWireType3`, `TestRFCWireType2` | test |
| 8365-8 | 5.1.3 | BGP Encapsulation extended community with all EVPN routes | BOOTy | COMPLIANT | RFC8365-ENCAP at the first audit (only on Type-5). [#746] adds it to Type-2 and Type-3 routes (`overlay.go:2766`, `vxlanEncapCommunity`); `TestEVPNRoutesCarryVXLANEncapsulation`, `TestRFCWireType2`, `TestRFCWireType3`, `TestRFCWireType5` | test |
| 8365-9 | 5.1.3 | Next hop MUST be the IPv4 or IPv6 address of the NVE | BOOTy | COMPLIANT | router ID (VTEP) in every route; `TestRFCWireType*` | test |
| 8365-10 | 5.1.3, 6 | Receiver uses the VNI of the route (and the advertised encapsulation) | BOOTy | DEVIATION | RFC8365-RX-VNI, partly fixed by [#773]: in Type-5-only (L3VNI) mode a Type-5 route without gateway IP whose VNI is not the provision VNI is treated as withdrawn, like zebra (`overlay.go:1008`, `:1020` `type5LocalVNI`); `TestProcessRouteUpdateType5L3VNI`. In the other modes the Type-5 label is not checked, and Type-2 and Type-3 FDB entries use the VXLAN device VNI (`overlay.go:2375`), as FRR does. Impact only with different VNIs for the same BD/VRF. Kept like FRR | test |
| 8365-11 | 6 | Advertised encapsulations use the same EVPN procedures; never VXLAN and MPLS together | BOOTy | COMPLIANT | VXLAN only | reading |
| 8365-19 | 6 | An ingress NVE sends to an egress NVE only with an encapsulation that both support; without the BGP Encapsulation community the receiver assumes MPLS or a configured encapsulation | BOOTy | COMPLIANT | RFC8365-ENCAP-RX at the first refresh. [#771]: an update whose encapsulation communities do not include VXLAN (tunnel type 8) is treated as a withdraw of its route key, for Type-2, Type-3 and Type-5 (`overlay.go:934`, `notVXLAN`); a route without the community counts as VXLAN, the configured encapsulation, as in FRR 10.7.1. `TestProcessRouteUpdateHonoursEncapsulation` | test |
| 8365-12 | 5.2 | MPLS over GRE | BOOTy | N/A | | reading |
| 8365-13 | 8 | Multihoming, local bias, split horizon | BOOTy | N/A | single-homed | reading |
| 8365-14 | 9 | Each PE MUST advertise an IMET route for its tunnel type (ingress replication) | BOOTy | COMPLIANT | L2 mode; see 7432-27 | reading |
| 8365-15 | 9 | Receiver uses the PMSI tunnel type of the IMET route | BOOTy | COMPLIANT | [#772], see 7432-47; `TestProcessRouteUpdateType3TunnelType` | test |
| 8365-20 | 7 | Single-homing NVE in a hypervisor (BOOTy's role): no multihoming procedures needed | BOOTy | COMPLIANT | BOOTy originates no Type-1 or Type-4 routes and uses ESI 0 (`TestRFCWireType2`) | test |
| 8365-21 | 7.1 | RD unique per EVI or per NVE, recommended at all times | BOOTy | COMPLIANT | RFC7432-RD at the first refresh. [#710]: `<router-id>:<n>` differs per host and between the IP-VRF and the MAC-VRF, see 7432-11, 7432-12; `TestEVPNRDs` | test |
| 8365-22 | 7.1 | Routes and attributes of a hypervisor NVE: MAC/IP, IMET, MAC Mobility, Default Gateway | BOOTy | COMPLIANT | MAC/IP and IMET in L2 mode (`TestRFCWireType2`, `TestRFCWireType3`); MAC Mobility by GoBGP (7432-37); Default Gateway not needed (7432-10) | test |
| 8365-23 | 7.1, 7.2 | A single-homing ingress NVE should receive and process A-D per ES and per EVI routes (fast convergence, aliasing, backup path, RFC 7432 sections 8.2 and 8.4) | BOOTy | COMPLIANT | RFC7432-ESI-RX at the first audit. [#767], see 7432-16; `TestType2ESIAliasingAndMassWithdraw`, `TestESINetlinkAliasing` | test |
| 8365-24 | 7.2 | Procedures 1 to 5: local learning, advertising local MACs, remote learning per RFC 7432 section 9.2, IMET discovery, MAC mobility per section 15 | BOOTy | COMPLIANT | 1 and 2: the bridge MAC is the only local MAC and is advertised (`TestAdvertiseType2PublishesBridgeMACAndIP`); 3: see 7432-24 ([#748]); 4: 7432-27, 7432-33; 5: see 7432-38 ([#748]) | test |
| 8365-16 | 10.2.2 | ASBR functionality with multihoming NVEs (A-D withdrawal handling at an ASBR) | BOOTy | N/A | BOOTy is no ASBR; the receive side of A-D routes is 7432-16 | reading |
| 8365-17 | 5.1.3 | Route keys and withdraw processing for VXLAN routes | BOOTy | COMPLIANT | Type-5 key per RFC 9136 (`overlay.go:1409`); Type-2 routes by route key since [#748] (`overlay.go:2169`); `TestHandleType2RouteBestPathPerMAC`, `TestHandleType5RouteKeepsRouteWhileAnotherGatewayAdvertisesIt` | test |
| 8365-18 | 5.1.3, RFC 9012 | Tunnel type 8 (VXLAN) in the encapsulation community | BOOTy | COMPLIANT | `overlay.go:2761`; `TestRFCWireType5` | test |

### RFC 9135: Integrated routing and bridging in EVPN

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 9135-1 | 4 | Asymmetric IRB: Ethernet NVO tunnel | BOOTy | COMPLIANT | VXLAN with inner Ethernet | reading |
| 9135-2 | 4 | Asymmetric IRB: ARP entries for remote hosts, BTs for all subnets | BOOTy, Kernel | COMPLIANT | one subnet; the kernel resolves remote hosts by ARP through the BUM entries | reading |
| 9135-3 | 4.1 | IRB/anycast MAC rules | BOOTy | N/A | BOOTy is no IRB gateway; its bridge MAC is per host (`docs/networking.md`, Router MAC) | reading |
| 9135-4 | 5.1, 5.3, 5.4 | Symmetric IRB origination (two RTs, Label2, subnet RT-5) | BOOTy | N/A | BOOTy originates asymmetric MAC/IP routes (L2) and RT-5 host routes | reading |
| 9135-5 | 5.2, 6.2 | Asymmetric-only receiver: ignore Label2, install the MAC in the MAC-VRF | BOOTy | COMPLIANT | `overlay.go:1922` uses only the MAC | reading |
| 9135-6 | 5.2, 6.2 | Asymmetric-only receiver: install the (IP, MAC) association in the ARP/ND table | BOOTy | COMPLIANT | RFC9135-ARP-RX at the first audit. [#766] installs NOARP `extern_learn` neighbor entries on the bridge for the best route of each IP, like zebra (`overlay.go:2008`, `syncType2Neighbor`); [#784] shares the entry of a VTEP IP with the Type-5 gateway neighbor; `TestHandleType2RouteInstallsNeighbor`, `TestType2NeighborKeptByTheOtherRD`, `TestType2NeighborTiebreakOnMAC`, `TestType2AndType5ShareTheBridgeNeighbor` | test |
| 9135-7 | 6.1 | Asymmetric MAC/IP route: length 37/49, Label2 not included, MAC-VRF RT, set per RFC 7432 and RFC 8365 (which adds the BGP Encapsulation community) | BOOTy | COMPLIANT | `TestRFCWireType2` (length, labels, RT, encapsulation community of [#746]) | test |
| 9135-8 | 8.1 | Advertiser attaches a single Router's MAC community | BOOTy | COMPLIANT | `overlay.go:2485`; `TestRFCWireType5` | test |
| 9135-9 | 8.1 | Receiver processes the first Router's MAC community | BOOTy | COMPLIANT | `overlay.go:2658` returns the first | reading |
| 9135-10 | 9.1.1 | Router's MAC community MUST be added when the Ethernet NVO tunnel is used (symmetric IRB RT-2) | BOOTy | N/A | BOOTy originates asymmetric RT-2 only; the RT-5 rule is 9136-17 | reading |
| 9135-11 | 9.1.1 | Malformed RT-2 label and RT combinations of a symmetric IRB receiver MUST be treated as withdrawn | BOOTy | N/A | BOOTy is an asymmetric-only receiver: section 6.2 tells it to ignore Label2 (9135-5) | reading |
| 9135-12 | 5.4 | Label2 VNI mismatch | BOOTy | N/A | symmetric only | reading |
| 9135-13 | 4 | Inter-subnet traffic through the gateway | BOOTy | COMPLIANT | provision gateway route and BUM entry (`overlay.go:660`); lab `TestGoBGPUnnumberedGatewayRoute`, `TestGoBGPUnnumberedVXLANPingGateway` | lab |

### RFC 9136: IP prefix advertisement in EVPN (Type-5)

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 9136-1 | 3 | NVEs of a tenant on different BDs MUST support RT-5 | BOOTy | COMPLIANT | every mode originates and receives RT-5 | reading |
| 9136-2 | 3.1 | Length 34 or 58; prefix and gateway of the same family | BOOTy, GoBGP | COMPLIANT | `TestRFCWireType5`; IPv6 prefixes get gateway `::` (`l3vni.go:134`) | test |
| 9136-3 | 3.1 | RD and Ethernet Tag per RFC 7432/8365 (RD unique per VRF) | BOOTy | COMPLIANT | RFC7432-RD at the first refresh. [#710]: the Type-5 routes carry the L3VNI RD `<router-id>:<n>` (`overlay.go:736`, `:2437`), see 7432-12; Ethernet Tag 0 (`overlay.go:2458`). `TestEVPNRDs`, `TestRFCWireType5` | test |
| 9136-4 | 3.1 | ESI MUST be zero when it is no overlay index | BOOTy | COMPLIANT | `TestRFCWireType5` | test |
| 9136-5 | 3.1 | GW IP MUST be zero when it is no overlay index | BOOTy | COMPLIANT | `overlay.go:2770`, `l3vni.go:134`; `TestRFCWireType5` | test |
| 9136-6 | 3.1 | Prefix length at most 32/128 | BOOTy | COMPLIANT | originated from parsed prefixes; received lengths above are skipped (`overlay.go:2401`) | reading |
| 9136-7 | 3.1 | Label: the VNI; SHOULD be 0 with recursive resolution | BOOTy | COMPLIANT | label = VNI (interface-less model, no overlay index); `TestRFCWireType5` | test |
| 9136-8 | 3.1 | Received label 0: MUST contain an overlay index and be resolved, else treat-as-withdraw | BOOTy | COMPLIANT | RFC9136-LABEL0 at the first refresh. [#747]: label 0 without gateway IP is treated as withdrawn (`overlay.go:1006`, `type5Unusable`); with a gateway IP the route needs a Type-2 resolution (9136-11). `TestProcessRouteUpdateType5TreatAsWithdraw` | test |
| 9136-9 | 3.1 | Route key: RD, Ethernet Tag, prefix length, prefix | BOOTy | COMPLIANT | `overlay.go:1409`; `TestHandleType5RouteKeepsRouteWhileAnotherGatewayAdvertisesIt` | test |
| 9136-10 | 3.2 | ESI overlay index: usable only after an RT-1 resolves it | BOOTy | DEVIATION | RFC9136-ESI: a route with ESI, no gateway IP and a label is installed through the BGP next hop, like FRR `process_type5_route`; BOOTy has no RT-1 resolution of a Type-5 ESI. [#747] kept FRR's handling on purpose (`overlay.go:996` comment); `TestProcessRouteUpdateType5TreatAsWithdraw` (valid case). Impact low: a label still names the IP-VRF. Kept like FRR | test |
| 9136-11 | 3.2 | GW IP overlay index: usable only after an RT-2 with that IP resolves it; the Router's MAC is ignored | BOOTy | COMPLIANT | RFC9136-GWIP at the first audit. [#775]: like FRR, such a route is installed only with `enable-resolve-overlay-index` (`EVPN_RESOLVE_OVERLAY_INDEX`), in L2 mode, and only while a Type-2 route carries the gateway IP; the router MAC is ignored (`overlay.go:1339`, `resolveType5Gateway`). Behaviour change, documented in `docs/networking.md`. `TestType5GatewayIPResolvedByType2`, `TestType5GatewayIPResolutionKeepsTheActivePath`, `TestType5GatewayIPRouteConvertedWithdraw` ([#799]) | test |
| 9136-12 | 3.2 | MAC overlay index (label 0 with Router's MAC) | BOOTy | N/A | optional ("support ... is OPTIONAL"); since [#747] such a route (label 0, no gateway IP) is treated as withdrawn (`overlay.go:1006`) | reading |
| 9136-13 | 3.2 | No IGP/BGP route to the BGP next hop: MUST NOT install | BOOTy | DEVIATION | RFC9136-NH-REACH: not checked; Type-5-only mode installs a /32 to the VTEP itself (`overlay.go:1542`). Impact low, by design. Open | reading |
| 9136-14 | 3.2 | ESI and GW IP both non-zero: SHOULD treat as withdraw | BOOTy | COMPLIANT | RFC9136-ESIGW at the first refresh. [#747] treats it as withdrawn, also for an ESI of type 1 with a zero value, like FRR (`overlay.go:1004`); `TestProcessRouteUpdateType5TreatAsWithdraw` | test |
| 9136-15 | 3.2 | All of ESI, GW IP, MAC and label zero: SHOULD treat as withdraw | BOOTy | COMPLIANT | RFC9136-LABEL0 at the first refresh. [#747]: label 0 without gateway IP is treated as withdrawn (`overlay.go:1006`); `TestProcessRouteUpdateType5TreatAsWithdraw` | test |
| 9136-16 | 3.2 | Invalid Router's MAC (multicast, broadcast, malformed): MUST treat as withdraw | BOOTy | COMPLIANT | RFC9136-RMAC at the first refresh. [#747]: `parseRouterMAC` rejects a multicast, broadcast or malformed MAC (`overlay.go:2528`), and the update is treated as withdrawn, also with a gateway IP, like FRR (`overlay.go:1002`); a zero router MAC counts as absent, like FRR (`overlay.go:2658`). `TestProcessRouteUpdateType5TreatAsWithdraw`, `TestParseRouterMACRejectsNonUnicast` | test |
| 9136-17 | 4.4.1 | Interface-less IP-VRF-to-IP-VRF: GW IP 0, ESI 0, Router's MAC, label = L3 VNI | BOOTy | COMPLIANT | `TestRFCWireType5`; lab `TestProdTplType5Origination`, `TestType5RouteHasEncapAndRouterMAC` | test, lab |
| 9136-18 | 4.4.1 | Ingress: inner destination MAC = Router's MAC, VNI of the route | BOOTy, Kernel | COMPLIANT | neighbor gateway -> Router's MAC and FDB Router's MAC -> VTEP (`overlay.go:1560`); lab `TestType5VM0ImportsVM1`, `TestType5RemoteVMDataPlaneMatchers`. VNI: see 8365-10 ([#773]) | lab |
| 9136-19 | 3 | Withdraw removes the route; another path of the prefix takes over | BOOTy | COMPLIANT | `overlay.go:1209` (`withdrawType5Path`); `TestHandleType5RouteWithdrawDeletesOnlinkGatewayRoute`, `TestHandleType5RouteKeepsRouteWhileAnotherGatewayAdvertisesIt`, `TestHandleType5RouteFallsBackToPathWhoseInstallFailed` | test |
| 9136-20 | 3 | Own routes reflected back are not installed | BOOTy | COMPLIANT | `overlay.go:1058`; `TestHandleType5RouteSelfRoute` | test |
| 9136-21 | 3.1 | GoBGP decodes the GW IP and prefix by NLRI length | GoBGP | GOBGP: COMPLIANT | `bgp.go:3289`, `:3330` | reading |

### RFC 9047: ARP/ND flags in EVPN

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 9047-1 | 2 | Receive the EVPN ARP/ND Extended Community (type 0x06, sub-type 0x08) with the R, O and I flags | GoBGP | GOBGP: COMPLIANT | fork booty.2 decodes it (`ArpNdExtended`); FRR sends it with the MAC/IP routes of IPv6 neighbors. GoBGP tests `Test_UpdateWithEvpnArpNdExtended`, `Test_ArpNdExtended` | GoBGP test |
| 9047-2 | 3.2 | An IPv6 MAC/IP route: MUST add the R and O flags to the ND entry; IPv4: MUST ignore them; I flag: SHOULD install an immutable binding | BOOTy | COMPLIANT | RFC9047-FLAGS at the first refresh. [#798]: an IPv6 neighbor entry gets `NTF_ROUTER` from the R flag of the best route, like zebra; IPv4 entries never get it (`overlay.go:2067`, `:2073`, `:2325` `arpNdRouter`). The kernel has no O flag; the NOARP `extern_learn` entry replaces what the kernel had. The I flag is not used, as in FRR. `TestType2NeighborRouterFlag`, `TestType2NeighborRouterFollowsBestPath` | test |

### RFC 6514: PMSI Tunnel attribute (as used by RFC 7432)

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 6514-1 | 5 | Attribute layout: flags, tunnel type, label, tunnel identifier | GoBGP | GOBGP: COMPLIANT | `bgp.go:16718`; `TestRFCWireType3` | test |
| 6514-2 | 5 | Ingress replication: tunnel identifier = unicast address of the originator | BOOTy | COMPLIANT | router ID (`overlay.go:2798`); `TestRFCWireType3` | test |
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
| 7209-5 | 6 (R8f) | Default values for unconfigured parameters | BOOTy | COMPLIANT | `config.go:276`, FRR profile defaults | reading |
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
| 7348-1 | 5 | UDP destination port 4789 | BOOTy, Kernel | COMPLIANT | `overlay.go:35`, `:563`; lab `TestGoBGPUnnumberedVXLANInterface` | lab |
| 7348-2 | 5 | 24-bit VNI, I flag set, reserved bits zero | Kernel | COMPLIANT | Linux vxlan driver | reading |
| 7348-3 | 5 | Outer UDP source port from a hash of the inner frame (SHOULD); UDP checksum 0 for IPv4 (SHOULD) | Kernel | COMPLIANT | kernel defaults (source port range, `udpcsum` off) | reading |
| 7348-4 | 4.3 | VTEPs MUST NOT fragment VXLAN packets | BOOTy, Kernel | COMPLIANT | With the default MTU only: the VXLAN device MTU is the NIC MTU - 50 (`overlay.go:573` `vxlanMTU`), so the outer packets fit the NIC and the kernel does not fragment them; `TestVXLANDeviceMTU`. An explicit `network.overlay.vxlanMTU` (the netplan tunnel MTU) is used unchanged (`devices.go:25` `vxlanDeviceMTU`, validated only as 576 to 65535): a value above the NIC MTU - 50 makes the kernel fragment outer packets, as with networkd and the same netplan; keeping it below is the operator's job. No test checks fragmentation on the wire; lab `TestType5JumboMTU` checks only the interface MTUs of the fabric | test |
| 7348-5 | 4.1 | Remote learning by the data plane | BOOTy | COMPLIANT | off on purpose (`overlay.go:564`); EVPN control plane instead (RFC 8365) | reading |
| 7348-6 | 4.2 | BUM to a multicast group or by head-end replication | Kernel | COMPLIANT | head-end replication through the all-zero FDB entries | reading |
| 7348-7 | 6 | IPv6 outer header | BOOTy | N/A | IPv4 VTEP only (`config.go:452`) | reading |

## LLDP: IEEE 802.1AB

BOOTy sends LLDP like systemd-networkd 249 `EmitLLDP=yes` (`tx.go`), with
its carrier-driven transmit timer (`carrier.go`, [#845]), and with
`LLDP_RECEIVE` (on by default, [#698]) receives it on every physical NIC
(`receive.go`). It decodes LLDPDUs with the gopacket fork
`MaxRink/gopacket v1.7.4-booty.2` (`docs/dependency-forks.md`), which
checks the mandatory TLVs and the TLV lengths; `booty lldp neighbors` and
`booty lldp status` ([#735], `cli.go`) replace lldpcli, and the ramdisk has
no lldpd ([#812]). Clause references are to IEEE 802.1AB-2016; the
standard is not freely available, so each row states the rule it checks.

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| LLDP-1 | 7.1, 7.5 | Destination nearest bridge 01-80-C2-00-00-0E, EtherType 88-CC | BOOTy | COMPLIANT | `tx.go:38`, `:165`; `TestFrameLikeNetworkd`, lab `TestProdTplLLDPLikeNetworkd` | test, lab |
| LLDP-2 | 8.2 | Mandatory TLVs Chassis ID, Port ID, TTL first, in this order | BOOTy, Library | COMPLIANT | gopacket encoder (`gopacket@v1.7.4-booty.2/layers/lldp.go:769`, `SerializeTo`); `TestFrameLikeNetworkd`, `TestFrameNetworkdGolden` | test |
| LLDP-3 | 8.5.2 | Chassis ID subtype and value (locally assigned machine ID, or MAC) | BOOTy | COMPLIANT | `tx.go:155`; `TestFrameLikeNetworkd`, `TestMachineIDPerBoot` | test |
| LLDP-4 | 8.5.3 | Port ID subtype interface name | BOOTy | COMPLIANT | `tx.go:159` (parity level; the bm4x-cablecheck profile of [#702] can set other subtypes, `TestFrameBM4XPortIDFormats`); `TestFrameLikeNetworkd` | test |
| LLDP-5 | 8.5.4, 9.2.5 | TTL = txTTL = msgTxInterval x msgTxHold + 1 (since 802.1AB-2009) | BOOTy | DEVIATION | LLDP-TTL: 120 instead of 121, same as networkd (`tx.go:34`). Impact very low. Kept for parity | reading |
| LLDP-6 | 8.5.8 | System capabilities: enabled a subset of supported | BOOTy | COMPLIANT | supported station/bridge/router, enabled station (`tx.go:47`); `TestFrameLikeNetworkd` | test |
| LLDP-7 | 8.4 | Optional TLVs: system name and description within 255 octets | BOOTy | COMPLIANT | `tx.go:178` | reading |
| LLDP-8 | 9.2.5 | msgTxInterval 30 s, fast start (txFastInit 4 frames at msgFastTx 1 s) | BOOTy | COMPLIANT | `tx.go:30` to `:34`; networkd 249 timer (`carrier.go:32`, `:46`): 4 fast frames 0.8 s plus jitter of 0 to 400 ms apart, then 29.8 s plus jitter (1 s and 30 s on average). [#845] restarts the fast start on every carrier gain (`tx.go:278`) and stops on carrier loss. `TestEmitNetworkdTiming`, `TestTxTimerStartWhileRunning`, `TestEmitFollowsVethCarrier` (veth, root) | test |
| LLDP-9 | 9.2.7 | Shutdown LLDPDU (TTL 0, mandatory TLVs) when transmission stops | BOOTy | COMPLIANT | LLDP-SHUTDOWN at the first audit. [#754] sends it when the sender stops (`tx.go:150`, `ShutdownFrame`; `tx.go:258`), only on a NIC with carrier ([#845], `tx.go:261`); [#792] sends it for the every-NIC sender before the handoff, first in the network stop. networkd sends none. `TestShutdownFrame`, `TestEmitShutdownFrame`, `TestStopNetworkOnceStopsLLDPFirst` | test |
| LLDP-10 | 9.2.7.7 | Receive: drop LLDPDUs whose first three TLVs are not Chassis ID, Port ID, TTL, or with a second instance of them | BOOTy, Library | COMPLIANT | LLDP-RX-ORDER at the first audit. [#754] and [#769] checked it in BOOTy; since [#833] the gopacket fork does (`gopacket@v1.7.4-booty.2/layers/lldp.go:836`, `:841`; fork `TestLLDPMandatoryTLVs`); `TestParseLLDPMandatoryTLVs` | test |
| LLDP-11 | 8.2 | End of LLDPDU TLV optional (2016) | Library | COMPLIANT | since [#833] the gopacket fork decodes an LLDPDU without End TLV, also with one byte of padding (`gopacket@v1.7.4-booty.2/layers/lldp.go:806`; fork `TestLLDPOnePaddingByteWithoutEnd`); `TestParseLLDPMandatoryTLVs` ("in order without End", "one padding byte") | test |
| LLDP-12 | 9.2.7.7 | Neighbor information aged out after TTL; TTL 0 removes it at once | BOOTy | COMPLIANT | `receive.go:184` to `:222`; `TestReceiverRecord` | test |
| LLDP-13 | 9.2.7.7 | Truncated or malformed TLVs dropped | BOOTy, Library | COMPLIANT | the gopacket fork checks the TLV lengths (`gopacket@v1.7.4-booty.2/layers/lldp.go:801`) and, since [#833], returns an error instead of reading past the end for short 802.1, 802.3, LLDP-MED and management address TLVs (`lldp.go:919`, `:977`, `:1049`; fork `TestLLDPDecodeShortTLVs`, `TestLLDPDecodeMgmtAddressLength`); BOOTy no longer filters these TLVs itself, and still keeps a partial neighbor on a decoder panic (`decode.go:52`) and drops the frame on a panic in the receive (`receive.go:170`); `TestParseLLDPInvalidPayload`, `TestParseLLDPShortOrgTLVs`, `TestMgmtAddressesShort`, `FuzzParseLLDP` | test |
| LLDP-14 | 7.1 | Multicast group membership for receive | BOOTy | COMPLIANT | `lldp.go:77`; `TestOpenRXGroupsAndOwnFrames` | test |
| LLDP-15 | 9.1 | Own frames are not neighbors | BOOTy | COMPLIANT | `lldp.go:119`; `TestOwnFrame` | test |
| LLDP-16 | 9.2.7.7 | Several neighbors per port (tooManyNeighbors handling) | BOOTy | N/A | one neighbor per NIC by design (one switch port per NIC), documented | reading |
| LLDP-17 | 8.6 | Organizationally specific TLVs (OUI, subtype, information string) | BOOTy, Library | COMPLIANT | the parity level sends none, like networkd; the levels standard and full ([#729], [#734]) send 802.1, 802.3 and LLDP-MED TLVs, encoded by gopacket; `TestFrameStandard`, `TestFrameFull`, `TestFrameMEDCapabilities` | test |
| LLDP-18 | 9.2.2 | adminStatus per port (txOnly, rxOnly, txAndRx) | BOOTy | COMPLIANT | `LLDP_EMIT` selects the sending NICs (`pkg/caprf/client.go:1940`); `LLDP_RECEIVE` turns receive on or off for all physical NICs, on by default (`main.go:1184`). Receive is not selectable per NIC | reading |
| LLDP-19 | 9.2.5 | Untrusted TLV strings sanitized | BOOTy | COMPLIANT | `lldp.go:187`; `TestSanitizeLLDP` | test |
| LLDP-20 | 9.2.4 | reinitDelay before restart of transmission | BOOTy | N/A | adminStatus is fixed for the run; a carrier gain restarts the transmit timer at once like networkd ([#845], `tx.go:278`), without reinitDelay | reading |

## IPv6 neighbor discovery and address configuration

The kernel does neighbor discovery and SLAAC. BOOTy itself sends Router
Advertisements on the unnumbered BGP NICs (`underlay.go:1079` `sendRA`,
`gobgp/ra.go:116` `startPeriodicRA`), joins the all-routers group there and
answers Router Solicitations like zebra ([#846]), so FRR's zebra learns the
link-local address of the server. BOOTy finds the switch in the kernel
neighbor table and sends no echo request (`underlay.go:1147`). In DHCP mode it
also reads the RAs of every DHCP NIC to start DHCPv6 ([#795]).

### RFC 4861: Neighbor Discovery (BOOTy's Router Advertisements)

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 4861-1 | 4.2, 6.1.2 | RA format; Hop Limit 255 | BOOTy | COMPLIANT | `underlay.go:1096`, `:1127`; `TestRouterAdvertisementPacket` ([#752]); lab: FRR drops other hop limits and the unnumbered sessions establish (`TestGoBGPUnnumberedBGPEstablished`, `TestProdTplLeafSessions`) | test, lab |
| 4861-2 | 4.6.1 | Source Link-Layer Address option | BOOTy | COMPLIANT | `underlay.go:1136` | reading |
| 4861-3 | 6.2.1 | AdvSendAdvertisements FALSE by default | BOOTy | COMPLIANT | RAs only on the NICs of unnumbered and dual mode or of session interfaces (`underlay.go:213` `startRAs`, `:601` `needsNICs`) | reading |
| 4861-4 | 6.2.1 | MaxRtrAdvInterval 4 to 1800 s; AdvDefaultLifetime 0 or MaxRtrAdvInterval to 9000 s | BOOTy | COMPLIANT | 10 s and, since [#770], zebra's lifetime 30 s (`underlay.go:47`, `:64`); `TestRouterAdvertisementLifetimeLikeZebra` | test |
| 4861-5 | 6.2.1, 6.2.4 | MinRtrAdvInterval at most 0.75 x Max; random interval between Min and Max | BOOTy | DEVIATION | RFC4861-HOST-RA: fixed 10 s after each regular RA (`gobgp/ra.go:93`), no random interval. Impact low | reading |
| 4861-6 | 6.2.2 | A router MUST join all-routers on an advertising interface | BOOTy | COMPLIANT | DEVIATION at the first audit. [#846] joins ff02::2 on every RA NIC (`gobgp/ra.go:205` `listenRS`) and leaves it after the final RA (`:131`), like zebra `if_join_all_router`; `TestListenRSJoinsAllRouters` (build tag `linux_e2e`) | test |
| 4861-7 | 6.2.4, 6.2.6 | A host MUST NOT send RAs (and MUST discard Router Solicitations) | BOOTy | DEVIATION | RFC4861-HOST-RA: with IPv6 forwarding off BOOTy is a host and still sends RAs, because FRR BGP unnumbered needs them; since [#846] it also answers Router Solicitations, like zebra. The legacy FRR ramdisk does the same. By design, documented | reading |
| 4861-8 | 6.2.5 | Final RAs with Router Lifetime 0 when advertising stops (SHOULD) | BOOTy | COMPLIANT | RFC4861-FINAL-RA at the first audit. [#752] ends the RAs of each NIC with lifetime 0 (`gobgp/ra.go:174`); `stopPeriodicRA` waits for it (`:146`), also before the handoff (`underlay.go:276`) and when the setup fails (`:184`); `TestPeriodicRAEndsWithZeroLifetime`, `TestTeardownStopsPeriodicRA` | test |
| 4861-9 | 6.2.4, 6.2.6 | RAs to all-nodes at most one per MIN_DELAY_BETWEEN_RAS (3 s); unsolicited RAs not more often than MinRtrAdvInterval | BOOTy | DEVIATION | [#846] starts with 5 RAs 1 s apart and sends 4 more 1 s apart when a NIC gets carrier back (`gobgp/ra.go:28` to `:36`, `:84`), like zebra RTADV_FAST_REXMIT_PERIOD. Answers to solicitations keep MIN_DELAY_BETWEEN_RAS (`:100` `solicited`). Impact very low: the leaf's zebra does the same, one host per link. Fix only by dropping zebra parity; keep. `TestRAFastStartLikeZebra`, `TestRACarrierUpRestartsFastStart`, `TestRAScheduleSolicited` | test |
| 4861-10 | 6.1.1, 6.2.6 | Router Solicitation processing | BOOTy | COMPLIANT | N/A at the first audit. [#846] checks each RS (hop limit 255, code 0, option lengths, no source link-layer option from ::; `gobgp/ra.go:231` `validRS`) and answers after a random 0 to 500 ms delay (MAX_RA_DELAY_TIME), at least MIN_DELAY_BETWEEN_RAS after the last RA, or with an RA already due earlier (`:48`, `:100`); `TestValidRS`, `TestRASolicitationAnswer`, `TestRASolicitationWaitsRandomDelay`, `TestRAScheduleSolicited`. The neighbor cache update from an RS is the kernel's (not verified with forwarding off) | test |
| 4861-11 | 4.4, 7 | Neighbor solicitation and advertisement, NUD, DAD | Kernel | COMPLIANT | Linux; link-local discovery reads the kernel neighbor table (`underlay.go:1241`) | reading |
| 4861-12 | 6.3 | Host processing of received RAs | Kernel | COMPLIANT | `accept_ra` 2 and `accept_ra_defrtr` 1 (`gobgp/helpers.go:47`), netplan `accept-ra: false` per NIC (`underlay.go:1279`) | reading |
| 4861-13 | 8 | Redirects | Kernel | N/A | | reading |
| 4861-14 | 4.2 | Router flag of Neighbor Advertisements consistent with the RAs | Kernel | COMPLIANT | the kernel sets the R flag from forwarding; with forwarding off it is 0 (`helpers.go:20` comment), which matches 4861-7 for a host | reading |
| 4861-15 | 4.6, 6.1.2 | Host: a received RA MUST be silently discarded unless it has hop limit 255, a link-local source, a valid checksum, code 0, at least 16 octets and only options with a length greater than zero | BOOTy | COMPLIANT | RFC4861-RA-OPTLEN at the third refresh: [#795] reads the M and O flags of RAs in DHCP mode to start DHCPv6, like networkd, but did not walk the options, so an RA with a zero-length option, which the kernel drops, still started DHCPv6. Since [#915] `validRA` (`dhcp6_mode.go:145`, used at `:134`) checks hop limit 255, the link-local source, code 0, at least 16 octets and the option lengths like `validRS` (`gobgp/ra.go:231`); the kernel checks the checksum. `TestValidRA` | test |
| 4861-16 | 6.3.7 | Host: up to MAX_RTR_SOLICITATIONS (3) Router Solicitations RTR_SOLICITATION_INTERVAL (4 s) apart; the first after a random delay of up to MAX_RTR_SOLICITATION_DELAY (1 s, SHOULD) | BOOTy | COMPLIANT | [#795] sends three solicitations 4 s apart with the source link-layer address when the carrier of a DHCP NIC returns (`dhcp6_mode.go:322` `solicitRA`, `:58`, `sendRS` `:85`; started at `dhcp.go:395`), the first after a random 0.5 to 1 s, like sd-ndisc (`dhcp6_mode.go:63`); the DEVIATION of the PR (no random delay) is gone. `TestDHCPv6FollowsRAAndCarrier` | test |

### RFC 4862: IPv6 stateless address autoconfiguration

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 4862-1 | 5 | Link-local address generation and DAD | Kernel | COMPLIANT | Linux; netplan `link-local: []` sets `addr_gen_mode` none (`devices.go:50`) | reading |
| 4862-2 | 5.5 | Global addresses from Prefix Information | Kernel | COMPLIANT | Linux, by `accept_ra` | reading |

## DHCP

### RFC 2131: DHCPv4 client

BOOTy runs its own DHCPv4 client, one per NIC, on every NIC like networkd
(`pkg/network/dhcp4`, [#750], [#790]; go-dhclient is removed), and applies
the leases itself (`pkg/network/dhcp.go`). The messages are encoded and
decoded with `github.com/insomniacslk/dhcp` ([#835]); BOOTy owns the states,
timers and checks.

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 2131-1 | 4.1, 4.4.1 | DISCOVER/OFFER/REQUEST/ACK exchange, xid, broadcast from 0.0.0.0 | BOOTy | COMPLIANT | [#790] `dhcp4/client.go:197` `initState`, `:218` `selecting` (broadcast, server identifier and requested IP); `TestDiscoverBackoffAndOptions`, `TestSelectingIgnoresOtherTypesAndRetriesFiveTimes`, `TestTransportOnVeth` (build tag `linux_e2e`), `TestDHCPMode_Setup` | test |
| 2131-2 | 4.4.1 | Client SHOULD check the offered address (ARP) and MUST send DHCPDECLINE if in use | BOOTy | DEVIATION | RFC2131-ARP: the own client of [#790] binds an ACK at once (`dhcp4/client.go:227`, `:341` `bind`); no ARP probe and no DHCPDECLINE anywhere in `pkg/network`. Impact low. Fix: an ARP probe (RFC 5227) before `bind` and a DECLINE on a conflict | reading |
| 2131-3 | 4.3.2, 4.4.5 | RENEWING: unicast REQUEST with ciaddr, without server identifier and requested IP; REBINDING: broadcast | BOOTy | COMPLIANT | RFC2131-RENEW at the first audit (go-dhclient). [#790]: unicast from the lease address to the server of the lease with ciaddr and neither option, broadcast when rebinding (`dhcp4/client.go:292` `renewState`, `:300` `rebindState`, `:564` `request`); `TestRenewUnicastRebindBroadcast`, `TestRenewWait` | test |
| 2131-4 | 4.4.5 | T1 and T2 defaults 0.5 and 0.875 of the lease | BOOTy | COMPLIANT | `dhcp4/message.go:302` (`leaseTimes.set`, also for a T1 or T2 past the lease, like networkd); `TestLeaseTimes` | test |
| 2131-5 | 4.4.5 | Lease expiry before renewal or rebind: MUST stop using the address, back to INIT | BOOTy | COMPLIANT | RFC2131-EXPIRE at the first audit. The client ends the lease at expiry (`dhcp4/client.go:318`, `:391` `expire`, also in INIT and SELECTING after failed INIT-REBOOTs, `:378` `leaseTimeout`); [#751] removes address, routes, DNS, NTP and MTU (`dhcp.go:544` `onExpire`, `:554` `endLease`); `TestExpiryAndNak`, `TestLeaseExpiresInInitAfterFailedReboot`, `TestLeaseExpiresInSelectingAfterFailedReboot`, `TestOnExpireRemovesLeaseState` | test |
| 2131-6 | 3.2 | DHCPNAK: stop using the address, restart | BOOTy | COMPLIANT | Fixed by [#751] and [#790]: a NAK ends the lease (`dhcp4/client.go:260`, `:334`) and BOOTy removes it (`dhcp.go:544`); a NAK counts only from the server of the offer or lease, like networkd (`dhcp4/client.go:458`; in INIT-REBOOT and REBINDING this is a deviation, see 2131-13); `TestExpiryAndNak`, `TestNakFromOtherServerIgnored`, `TestNakFromOtherServerIgnoredInRequesting`, `TestNakFromOtherServerIgnoredInRebooting` | test |
| 2131-7 | 4.4.5 | A renewed lease updates address and router | BOOTy | COMPLIANT | `dhcp.go:597` `apply` for every ACK, `:735` `applyRoutes`; `TestOnBoundAppliesRenewal`, `TestOnBoundAppliesEveryNIC` | test |
| 2131-8 | 3.3 | Lease without router option drops the default route | BOOTy | COMPLIANT | `dhcp_options.go:113` `leaseRouteSet` (no default route without a non-zero router), `dhcp.go:735` removes the routes the new lease lacks; `TestLeaseRoutesTable`, `TestOnBoundRenewalDropsRouteAndRestoresMask` | test |
| 2131-9 | 4.1 | Retransmission with randomized exponential backoff (SHOULD) | BOOTy | COMPLIANT | RFC2131-BACKOFF at the first audit (go-dhclient, fixed 1 s). [#790]: 2, 4, 8, 16, 32, then 64 s, each +-1 s, like networkd (`dhcp4/client.go:76` `timeout`); `TestDiscoverBackoffAndOptions` | test |
| 2131-10 | 4.4.6, Table 5 | DHCPRELEASE on shutdown (MAY); a DHCPRELEASE MUST carry the server identifier (54) | BOOTy | COMPLIANT | [#790] releases a bound lease at stop, unicast with ciaddr, like networkd SendRelease=yes (`dhcp4/client.go:518` `release`, `dhcp.go:456`); [#908] adds the server identifier of the lease, which the release sent without before, to the client identifier; `TestRebootAndRelease`, `TestGolden` | test |
| 2131-11 | 4.4.1 | Parameter request list, hostname option | BOOTy | COMPLIANT | [#776] networkd's list and options 61, 55, 57, 12 (`dhcp_options.go:27` `networkdParams`, `dhcp.go:446` `newDHCPClient`, `dhcp4/client.go:579`); `TestNewDHCPClientSendsNetworkdOptions`, `TestDiscoverBackoffAndOptions` | test |
| 2131-12 | 3.1 | Client identifier, chaddr | BOOTy | COMPLIANT | N/A at the first audit (chaddr only). [#776] sends networkd's client identifier in every message with chaddr (`dhcp_options.go:43`, `dhcp4/client.go:582`), see 4361-1; `TestNewDHCPClientSendsNetworkdOptions` | test |
| 2131-13 | 3.2, 4.4.2, 4.4.5 | INIT-REBOOT: confirm the last address with a broadcast REQUEST (requested IP, no server identifier); NAK: back to INIT. INIT-REBOOT and REBINDING ask any server, so a DHCPNAK from any server ends the lease | BOOTy | DEVIATION | RFC2131-NAK-SERVER: [#790] `dhcp4/client.go:240` `rebootState`, started by [#793] when the carrier returns, like networkd (`dhcp.go:400`); `TestRebootTriesOnce`, `TestRebootEndsRenewal`, `TestCarrierLossKeepsLeaseAndReturnReboots`. But `rebootState` and `extend` (also REBINDING) pass the server of the lease to `wanted` (`dhcp4/client.go:251`, `:325`, `:458`), which ignores a NAK from another server, like networkd; `TestNakFromOtherServerIgnoredInRebooting`. On a changed network the client then keeps the old address until a new lease or its expiry instead of restarting at once. Impact low. Fix: accept a NAK from any server in INIT-REBOOT and REBINDING (kept for networkd parity so far) | test |
| 2131-14 | 4.1 | secs: the time since the client began the exchange | BOOTy | COMPLIANT | [#790] `dhcp4/client.go:512` `secs`, set on each send (`:419`); no test checks the value | reading |
| 2131-15 | 4.1 | Replies are matched by xid and chaddr | BOOTy | COMPLIANT | [#790] `dhcp4/client.go:454` `wanted`; `TestIgnoresForeignReplies` | test |
| 2131-16 | 4.4.5 | Lease expiry also while SELECTING after a failed INIT-REBOOT: MUST stop using the address | BOOTy | COMPLIANT | RFC2131-EXPIRE-SELECTING at the third refresh: INIT checked the kept lease's expiry before each DISCOVER, but `selecting` sent up to five REQUESTs without that check, so with an OFFER just before the expiry and no ACK the expired address and routes stayed up to about a minute. Since [#917] both bound their timeouts by the lease and expire it on time (`dhcp4/client.go:378` `leaseTimeout`, used at `:202` and `:221`); `TestLeaseExpiresInSelectingAfterFailedReboot` | test |

### DHCPv4 options (RFC 2132, 3397, 3442, 4039, 4361)

BOOTy applies the options of a lease like systemd-networkd in the legacy
ramdisk ([#753], [#776], [#790], [#819], [#820]).

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 2132-1 | RFC 2132 3.8, 5.1 | DNS servers (option 6) and interface MTU (option 26, at least 68) of the lease | BOOTy | COMPLIANT | [#753] writes the DNS servers of the lease and sets the MTU, clamped to 576 (1280 with IPv6) and the NIC maximum, like networkd (`dhcp.go:626` `leaseDNS`, `:639` `applyMTU`, `:887` `clampMTU`); [#819] asks every server at once, like the legacy resolver (`dns_fanout.go:102`); `TestOnBoundWritesLeaseDNS`, `TestOnBoundSetsLeaseMTU`, `TestClampMTU`, `TestDNSFanout` | test |
| 2132-2 | RFC 2132 5.8, 9.10 | Static routes (option 33, classful, no default route) and maximum message size (option 57, at least 576) | BOOTy | COMPLIANT | [#776] `dhcp_options.go:76` `staticRoutes`; [#790] option 57 = NIC MTU - 28; [#908] leaves it out when that is below 576 (an MTU below 604; before, MTUs 576 to 603 sent 548 to 575) (`dhcp4/client.go:591`); `TestLeaseRoutesTable`, `TestMalformedRouteAndSearchOptions`, `TestMaxMessageSize` | test |
| 2132-3 | RFC 2132 9.3, RFC 2131 4.1 | Option overload (52): with it, the client reads further options from `file` and `sname` after the options field | BOOTy | DEVIATION | RFC2132-OVERLOAD: the reply decoder reads only the options field (`dhcp4/message.go:106`) and `usable` drops every OFFER and ACK with option 52 (`dhcp4/message.go:286`); `TestDecodeReplyChecks` pins this. A server that overloads its replies gives no lease. Impact very low: servers overload only replies too large for the options field, which BOOTy's parameter list does not produce. Fix: read the options of `file` and `sname` as option 52 says | test |
| 3442-1 | RFC 3442 | Classless static routes (option 121): MUST install its routes; with option 121 present MUST ignore the Router (3) and Static Routes (33) options; MUST zero the host bits of each destination. The RFC says nothing on a malformed option | BOOTy | COMPLIANT | [#776] `dhcp_options.go:113` `leaseRouteSet`, `:95` `classlessRoutes` (destinations masked, `:102`). [#835] cuts a malformed option 121 after its last whole route, like networkd `lease_parse_classless_routes` (`dhcp4/message.go:181` `classless`, from `prepare` `:157`): those routes are installed and options 3 and 33 ignored. Only a 121 without a whole route is ignored (`message.go:151`), and BOOTy then uses option 33 and the router option, as without 121. `TestLeaseRoutesTable`, `TestOnBoundAppliesClasslessRoutes`, `TestOptionsKeepLastValidCopy`, `TestDecodeReplyRepeatedOptions`, `FuzzClasslessRoutes` | test |
| 3397-1 | RFC 3397 | Domain search (option 119) with compression pointers; a malformed option is ignored | BOOTy | COMPLIANT | [#776] `dhcp4/message.go:249` `SearchDomains`, `dhcp_options.go:230` `leaseSearchDomains`; `TestLeaseSearchDomains`, `FuzzDomainSearch`, `TestMalformedRouteAndSearchOptions` | test |
| 4039-1 | RFC 4039 | Rapid commit: option 80 in DISCOVER; an ACK with rapid commit binds at once | BOOTy | COMPLIANT | [#790] `dhcp4/client.go:557`, `:208`, like networkd; `TestDiscoverBackoffAndOptions` | test |
| 4361-1 | RFC 4361 | Client identifier type 255: IAID and DUID | BOOTy | COMPLIANT | [#776] `dhcp_options.go:43` `dhcpClientID`: IAID of the NIC and a DUID-EN from the machine ID, like networkd; DHCPv6 uses the same IAID and DUID (`dhcp6_mode.go:212`); `TestClientIDWithoutMachineID`, `TestNewDHCPClientSendsNetworkdOptions` | test |

### RFC 8415: DHCPv6

| ID | § | Requirement | Owner | Status | Evidence, impact, fix | Verified |
|---|---|---|---|---|---|---|
| 8415-1 | all | DHCPv6 client | BOOTy | COMPLIANT | NOT IMPLEMENTED at the first audit. [#795] adds BOOTy's own client (`pkg/network/dhcp6`), started by the M or O flag of an RA on every DHCP NIC, like networkd `DHCPv6Client=yes` (`dhcp6_mode.go:179` `onRA`); rows 8415-2 to 8415-15 | test |
| 8415-2 | 15, 18.2.1 | Retransmission: IRT, MRT, RAND; the first SOLICIT timeout greater than IRT | BOOTy | COMPLIANT | [#795], [#825] `dhcp6/client.go:97` `next`, `:76` to `:79`; the first-timeout rule is a flag of SOLICIT since [#919] (it also matched INFORMATION-REQUEST, whose parameters are the same); the MRT of the server: 8415-11, 8415-12; the delay before the first SOLICIT: 8415-13; `TestSolicitLikeNetworkd`, `TestFirstSolicitAboveIRT`, `TestInfMaxRT` | test |
| 8415-3 | 18.2.1, 18.2.9 | SOLICIT with rapid commit; collect ADVERTISEs for the first timeout and take the highest preference, 255 at once; ignore an ADVERTISE with NoAddrsAvail | BOOTy | COMPLIANT | [#825] `dhcp6/client.go:192` `solicit`, like sd-dhcp6-client; an ADVERTISE without a usable address is skipped (`dhcp6/message.go:208`, `:221`); `TestAdvertisePreference`, `TestLateAdvertiseSameSocket`, `TestRequestRetriesAndAdvertiseStatus` | test |
| 8415-4 | 18.2.2 | REQUEST up to REQ_MAX_RC (10) times | BOOTy | COMPLIANT | [#795] `dhcp6/client.go:245` `request`; since [#918] the retries also stop on a REPLY that gives the address valid lifetime 0 (`:267`, 8415-10); `TestRequestRetriesAndAdvertiseStatus`, `TestRequestReplyWithZeroValidLifetime` | test |
| 8415-5 | 18.2.4, 18.2.5, 18.2.10.1 | RENEW at T1, REBIND at T2, stop using the address at the end of its valid lifetime; NoBinding in a REPLY to RENEW or REBIND: REQUEST | BOOTy | COMPLIANT | [#795], [#825] `dhcp6/client.go:276` `extend`, `:309` `phase`; the address goes at expiry (`dhcp6_mode.go:262` `onExpire6`); `TestRenewRebindExpire`, `TestRenewNeedsAddress`, `TestRenewNoBindingRequests`, `TestReplyWithZeroValidLifetime` | test |
| 8415-6 | 18.2.6, 21.23 | INFORMATION-REQUEST (O flag); refresh after the Information Refresh Time, at least IRT_MINIMUM (600 s), default 86400 s | BOOTy | COMPLIANT | [#795] `dhcp6/client.go:359` `information`, `:349` `infoRefresh`, `dhcp6/message.go:201`; `TestInformationRequest`, `TestInfoRefresh`, `TestInformationWaitsRefreshTime`; options 32 and 83 are requested since [#919] (8415-12) | test |
| 8415-7 | 18.2.7 | RELEASE of a bound address, wait for the REPLY; the address MUST be removed from the interface before the RELEASE | BOOTy | COMPLIANT | [#795] releases at stop and waits up to REL_TIMEOUT (1 s) for the REPLY, without retransmission (the RFC says SHOULD retransmit one or more times but MAY terminate early, e.g. on power down; BOOTy stops before the handoff) (`dhcp6/client.go:420` `release`); `TestAdvertiseRequestAndRelease`. Fixed by [#892]: `stop6` removes the address and DNS servers first (`dhcp6_mode.go:301` `dropAddr6`), then stops the client, which sends the RELEASE (`:312` `c.Stop()`); late callbacks of the stopping client are ignored; `TestDHCPv6DropsAddressBeforeRelease`, `TestDHCPv6IgnoresLeaseWhileStopping` | test |
| 8415-8 | 18.2.10.1, 18.2.8 | DAD on each received address before use; an address found in use is declined with a DECLINE (no keyword: "the client sends a Decline message") | BOOTy | DEVIATION | [#795] adds the address with kernel DAD (`dhcp6_mode.go:240`) but sends no DECLINE when DAD fails (no DECLINE in `pkg/network/dhcp6`). Impact very low | reading |
| 8415-9 | 21.24, RFC 3646 | DNS servers (option 23) and domain list (option 24) used | BOOTy | COMPLIANT | [#795] adds them after the DHCPv4 servers, a link-local server with its zone (`dhcp6_mode.go:229` `onBound6`, `:253`; `dhcp.go:666` `writeResolvers`); `TestDomainList`, `TestDHCPv6LinkLocalDNSAndDomains` | test |
| 8415-10 | 18.2.10.1 | REPLY to RENEW or REBIND: discard the leases with a valid lifetime of 0 (no keyword: "Discard any leases from the IA ... that have a valid lifetime of 0") | BOOTy | COMPLIANT | RFC8415-ZERO-LIFETIME at the third refresh: an address with valid lifetime 0 was only skipped (`dhcp6/message.go:235`), so a REPLY whose IA_NA had only such addresses counted as no answer and the address stayed until its old valid lifetime ended. Since [#918] the decoder records them, with the IA and address checks of `ia` (`dhcp6/message.go:274` `revoked`), and a REPLY that gives the bound address valid lifetime 0 and no other address ends the lease at once (`dhcp6/client.go:338` in `phase`; `onExpire6` removes the address, `dhcp6_mode.go:262`); a REQUEST after NoBinding stops on it too (`dhcp6/client.go:267`). An IA with a new address and the old one at 0 binds the new one (`onBound6` replaces the address, `dhcp6_mode.go:243`). `TestReplyWithZeroValidLifetime`, `TestRequestReplyWithZeroValidLifetime`, `TestDecodeRevoked` | test |
| 8415-11 | 18.2.9, 21.24 | The client MUST include SOL_MAX_RT in the Option Request of a SOLICIT and MUST process a SOL_MAX_RT option (60 to 86400 s) in an ADVERTISE, also one that it otherwise ignores | BOOTy | COMPLIANT | RFC8415-SOL-MAX-RT at the third refresh: [#795] requested option 82 but did not decode it, so SOLICIT kept the default maximum of 3600 s. Since [#919] the SOLICIT still requests it (`dhcp6/client.go:405`); `decode` reads options 82 and 83 before the networkd checks (`dhcp6/message.go:181`, `:258` `maxRT`, other values ignored), `exchange` keeps them from every ADVERTISE or REPLY of the transaction, also one without addresses or with a failure status (18.2.10; only from an ADVERTISE or REPLY of the transaction; `dhcp6/client.go:501`, `:514`), and `solicit` uses SOL_MAX_RT as its MRT (`:200`). `TestDecodeMaxRT`, `TestSolMaxRT` | test |
| 8415-12 | 18.2.6, 21.25 | INFORMATION-REQUEST: the Option Request MUST ask for INF_MAX_RT (83) and the Information Refresh Time (32); the client MUST process INF_MAX_RT | BOOTy | COMPLIANT | RFC8415-INF-OPTIONS at the third refresh: the Option Request had only 23, 31, 56 and 103 and option 83 was not decoded. Since [#919] it is 23, 31, 32, 56, 83, 103 (`dhcp6/client.go:369`); option 32 is applied (`dhcp6/message.go:201`), and INF_MAX_RT, read like SOL_MAX_RT (8415-11), is the MRT of the INFORMATION-REQUEST (`dhcp6/client.go:366`). `TestInfMaxRT`, `TestDecodeMaxRT`, `TestGolden` | test |
| 8415-13 | 18.2.1 | The first SOLICIT on an interface SHOULD be delayed by a random time of 0 to SOL_MAX_DELAY (1 s) | BOOTy | COMPLIANT | RFC8415-SOL-DELAY at the third refresh: `solicit` sent the first SOLICIT at once, so hosts that start DHCPv6 on the same unsolicited RA could send together. Since [#919] the first SOLICIT of a client waits a random 0 to 1 s, which Stop ends (`dhcp6/client.go:232` `delayFirst`, called at `:193`); the elapsed time starts after it. A client starts with each RA that starts DHCPv6 and after the carrier returns. `TestFirstSolicitDelay`, `TestStopDuringSolicitDelay`. The INFORMATION-REQUEST: 8415-14 | test | test |
| 8415-14 | 18.2.6 | The first INFORMATION-REQUEST on an interface MUST be delayed by a random time of 0 to INF_MAX_DELAY (1 s) | BOOTy | COMPLIANT | Found in the review of [#919]: the first INFORMATION-REQUEST went out at once. Since [#919] it waits like the first SOLICIT (8415-13; `dhcp6/client.go:232` `delayFirst`, called at `:360`); the one after the refresh time does not. `TestFirstInformationRequestDelay` | test |
| 8415-15 | 18.2.2, 18.2.4, 18.2.5, 21.24 | REQUEST, RENEW and REBIND: the Option Request MUST ask for SOL_MAX_RT (82) | BOOTy | DEVIATION | RFC8415-ORO-SOL-MAX-RT, found in the review of [#919]: only the SOLICIT asks for 82; REQUEST, RENEW and REBIND send the Option Request 23, 31, 56, 103 of networkd 255 as captured (`dhcp6/client.go:411` `withTail`, `TestRenewRebindExpire`, `TestGolden`). A SOL_MAX_RT that a server sends in these REPLYs is still applied (8415-11). Impact very low. Kept for networkd parity; fix: add 82 to `withTail` | test |

## Fix PRs and fork tags

State on 2026-11-09. Every PR in this table is merged and on `main` at the
audited tree, except [#755] (closed; its test is on `main`).

| PR or tag | Findings |
|---|---|
| [#699] GoBGP 4.10.0 | RFC7606-EVPN-PANIC, RFC4271-COLLISION, and the first fork tags for RFC7606-TYPED-NLRI, RFC4724-NEWCONN, RFC8950-ENHE-CHECK, RFC8950-ENHE-EVPN, 7606-9, 9047-1 |
| [#808], [#811], [#831] fork tags `v4.10.0-booty.3` to `booty.16` | 9384-1, 7313-1 to 7313-5, 7911-8, 4271-17, 4271-31 to 4271-34 |
| [#874] fork on upstream v4.11.0, [#851], [#876], [#884] (`v4.11.0-booty.6`) | upstream now has RFC7606-TYPED-NLRI, RFC8950-ENHE-CHECK, RFC8950-ENHE-EVPN, RFC4724-NEWCONN and 9384-2 (Hard Reset with BFD Down); the fork keeps 4271-17, 4271-31, 4271-33, 4271-34, 4724-10 (F-bit gate, [#876]), 7313, 7911-8, 9384-1 |
| [#755] (closed) | panic regression test of RFC7606-EVPN-PANIC, on `main` since [#699] as `pkg/network/gobgp/rfc7606_decode_test.go` |
| [#710] | RFC7432-RD (7432-12, 8365-21, 9136-3); MED 0; BFD Required Min Echo RX 50 ms |
| [#746] | RFC8365-ENCAP |
| [#747], [#799] | RFC9136-RMAC, RFC9136-ESIGW, RFC9136-ESI, RFC9136-LABEL0 |
| [#748] | RFC7432-MACIP-WITHDRAW, RFC7432-MOBILITY-RX |
| [#749], [#768] | RFC5880-REMOTEDISCR, RFC5880-DEMAND |
| [#752], [#770] | RFC4861-FINAL-RA, 4861-4 |
| [#754], [#769], [#792] | LLDP-SHUTDOWN, LLDP-RX-ORDER, LLDP-11 |
| [#698], [#735] | LLDP receive and the `booty lldp` diagnosis |
| [#766], [#784] | RFC9135-ARP-RX |
| [#767] | RFC7432-ESI-RX |
| [#771] | RFC8365-ENCAP-RX |
| [#772], [#786] | RFC8365-PMSI-RX |
| [#773] | RFC8365-RX-VNI |
| [#775] | RFC9136-GWIP |
| [#798] | RFC9047-FLAGS |
| [#765], [#787] | defence in depth for RFC7606-EVPN-PANIC (`panic=10`) |
| [#750], [#751], [#753], [#776], [#788], [#790], [#793], [#795], [#819], [#820], [#825] | RFC2131-EXPIRE ([#751]), RFC2131-RENEW and RFC2131-BACKOFF ([#790]), the former PENDING rows of RFC 2131, the DHCPv4 options, RFC 8415 and 4861-15 (in part, see RFC4861-RA-OPTLEN), 4861-16 |
| [#892] | RFC8415-RELEASE |
| [#908] | RFC2131-RELEASE-SID, RFC2132-MAXSIZE (found by the third refresh of this audit) |
| [#915] | RFC4861-RA-OPTLEN |
| [#916] | 4271-4: the session keepalive is capped at hold/3 (no keyword in the RFC) |
| [#917] | RFC2131-EXPIRE-SELECTING |
| [#918] | RFC8415-ZERO-LIFETIME |
| [#919] | RFC8415-SOL-MAX-RT, RFC8415-INF-OPTIONS, RFC8415-SOL-DELAY; in its review 8415-14 (fixed) and RFC8415-ORO-SOL-MAX-RT (8415-15, kept) |

### Fork branches on GoBGP 3.37.0

The first audit made fork branches on GoBGP 3.37.0 in `MaxRink/gobgp`
(`fix/v3-evpn-decode-bounds`, `fix/evpn-discard-unknown-route-types`,
`fix/enhe-require-peer-capability`, `fix/v3-evpn-nd-extended-community`,
tags `v3.37.0-booty.1` and `booty.2`). They are obsolete: BOOTy uses
GoBGP 4, and upstream v4.11.0 has the same fixes.

Two of the findings were GoBGP bugs that other users of GoBGP also have.
Each already has an upstream issue, closed by a fix in v4.11.0:

- an EVPN NLRI of an unknown route type disabled the L2VPN EVPN family for
  the whole session (RFC 7606 section 5.4; [osrg/gobgp#3742](https://github.com/osrg/gobgp/issues/3742),
  97833030);
- IPv4 routes with an IPv6 next hop went to peers that did not advertise
  the Extended Next Hop Encoding capability (RFC 8950 section 4;
  [osrg/gobgp#3743](https://github.com/osrg/gobgp/issues/3743), 2544643c).

Upstream has no v3 maintenance branch (the last v3 tag is v3.37.0, May
2025), so GoBGP 3 users keep both bugs. A new upstream issue would only
make sense if upstream starts maintaining v3 again. None was filed.

Open deviations without a fix: see
[Open deviations ranked by impact](#open-deviations-ranked-by-impact).
Each needs a GoBGP or kernel-side change, or is kept for FRR or networkd
parity (the row says which).

[#698]: https://github.com/telekom/BOOTy/pull/698
[#699]: https://github.com/telekom/BOOTy/pull/699
[#702]: https://github.com/telekom/BOOTy/pull/702
[#710]: https://github.com/telekom/BOOTy/pull/710
[#723]: https://github.com/telekom/BOOTy/pull/723
[#729]: https://github.com/telekom/BOOTy/pull/729
[#734]: https://github.com/telekom/BOOTy/pull/734
[#735]: https://github.com/telekom/BOOTy/pull/735
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
[#755]: https://github.com/telekom/BOOTy/pull/755
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
[#812]: https://github.com/telekom/BOOTy/pull/812
[#819]: https://github.com/telekom/BOOTy/pull/819
[#820]: https://github.com/telekom/BOOTy/pull/820
[#825]: https://github.com/telekom/BOOTy/pull/825
[#831]: https://github.com/telekom/BOOTy/pull/831
[#833]: https://github.com/telekom/BOOTy/pull/833
[#835]: https://github.com/telekom/BOOTy/pull/835
[#845]: https://github.com/telekom/BOOTy/pull/845
[#846]: https://github.com/telekom/BOOTy/pull/846
[#851]: https://github.com/telekom/BOOTy/pull/851
[#852]: https://github.com/telekom/BOOTy/pull/852
[#874]: https://github.com/telekom/BOOTy/pull/874
[#876]: https://github.com/telekom/BOOTy/pull/876
[#884]: https://github.com/telekom/BOOTy/pull/884
[#892]: https://github.com/telekom/BOOTy/pull/892
[#908]: https://github.com/telekom/BOOTy/pull/908
[#915]: https://github.com/telekom/BOOTy/pull/915
[#916]: https://github.com/telekom/BOOTy/pull/916
[#917]: https://github.com/telekom/BOOTy/pull/917
[#918]: https://github.com/telekom/BOOTy/pull/918
[#919]: https://github.com/telekom/BOOTy/pull/919
