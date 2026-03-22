# EVPN E2E Gap Analysis — BOOTy GoBGP Data Plane

> **Status:** Investigation only — no code changes  
> **Date:** 2026-03-22  
> **Pipeline:** 60178989 (CAPRF MR #69, branch `feat/e2e-virtual-bm4x`)  
> **Result:** `e2e-kindmetal` ✅ (12m17s) | `e2e-kindmetal-evpn` ❌ (39m59s, test timeout)

---

## 1. How the Two E2E Suites Differ

### Non-EVPN (`e2e-kindmetal`)

| Aspect | Detail |
|--------|--------|
| **Topology** | Single Linux bridge, no ContainerLab, no BGP |
| **Network mode** | DHCP (dnsmasq inside host pod) |
| **Connectivity** | BOOTy ↔ L2 bridge ↔ NAT/MASQUERADE ↔ Kind node ↔ CAPRF NodePort |
| **BOOTy ISO** | `booty.iso` (slim/DHCP build, FRR-less) |
| **Timeouts** | Provision: 15m, test: 20m |
| **Key tests** | VM boots, BOOTy calls back, provisioning completes, deprovision |

The non-EVPN path is essentially a flat L2 segment with DHCP — no overlay, no routing, no BGP peering. It works reliably because there are zero data-plane components beyond a Linux bridge and NAT.

### EVPN (`e2e-kindmetal-evpn`)

| Aspect | Detail |
|--------|--------|
| **Topology** | ContainerLab: spine01 (FRR 10.3.1, AS 65000) + leaf01 (AS 65001) + client (10.100.0.100) |
| **Network mode** | GoBGP (`NETWORK_MODE=gobgp`, `BGP_PEER_MODE=unnumbered`) |
| **Connectivity** | BOOTy ↔ VXLAN VNI 100 ↔ spine01 br100 ↔ DNAT ↔ Kind node ↔ CAPRF NodePort |
| **BOOTy ISO** | `booty-gobgp.iso` (GoBGP build, pure-Go BGP) |
| **Timeouts** | Provision: 25m, BGP converge: 3m, test: 30m |
| **Key tests** | Fabric up, BGP converge, VM boot, GoBGP overlay, **BOOTy callback**, provisioning, deprovision |
| **Diagnostic hooks** | AfterAll dumps BGP summary, L2VPN EVPN, bridge FDB, controller logs, serial console |

The EVPN path adds a full VXLAN overlay — every packet from BOOTy to CAPRF must be encapsulated in VXLAN, routed to the remote VTEP (spine01 at 10.0.0.1), decapsulated, DNATed, and forwarded to Kind. This introduces **five additional failure points** compared to non-EVPN.

### Per-VM EVPN Configuration (from `setup-evpn.sh`)

| VM | ASN | Underlay IP | Provision IP | VTEP (Router ID) | Provision Gateway |
|----|-----|-------------|-------------|-------------------|-------------------|
| vm-0 | 65100 | 192.168.4.10 | 10.100.0.20/24 | derived | 10.0.0.1 |
| vm-1 | 65101 | 192.168.4.11 | 10.100.0.21/24 | derived | 10.0.0.1 |
| vm-2 | 65102 | 192.168.4.12 | 10.100.0.22/24 | derived | 10.0.0.1 |

---

## 2. Observed Failure

**Test:** `#12 "BOOTy calls back through the EVPN overlay"`  
**Assertion:** Wait for Kubernetes event `ProvisionerStarted` on `RedfishMachine` objects  
**Timeout:** 30 minutes  
**Result:** No `ProvisionerStarted` event ever emitted

### What the Pipeline Trace Shows

```
10:51:22 — Test #11 "BOOTy establishes GoBGP overlay in VMs" → PASSED (15.38s)
           Serial console showed "Assigned provision IP" — GoBGP stack initialized
10:51:37 — Test #12 "BOOTy calls back through the EVPN overlay" → starts polling
11:21:11 — panic: test timed out after 30m0s
```

Between 10:51:37 and 11:21:11 there is **zero activity** — the `Eventually` loop silently polls for an event that never arrives.

### What Actually Happens Inside the VM

1. BOOTy boots as PID 1 in initramfs
2. Parses `/deploy/vars` → detects `NETWORK_MODE=gobgp`
3. Creates GoBGP stack: underlay eBGP unnumbered → discovers link-local peer via NDP → BGP ESTABLISHED
4. Creates VXLAN VNI 100, bridge `br-vxlan`, assigns provision IP (10.100.0.20/24)
5. Adds BUM FDB entry: `00:00:00:00:00:00 → 10.0.0.1` (gateway VTEP) ← **fix from 88086d4**
6. Calls `WaitForConnectivity(ctx, "http://10.100.0.1:<port>", 5*time.Minute)`
7. HTTP HEAD to `10.100.0.1` — packet enters br-vxlan → routed to VXLAN device → **FAILS**
8. After 5 minutes: `"Network connectivity timeout"` → `realm.Reboot()`
9. VM reboots → cycle repeats indefinitely
10. Test never sees `ProvisionerStarted` → times out at 30m

---

## 3. Root Cause: VXLAN Data-Plane Broken

The VXLAN overlay is non-functional because of multiple compounding gaps. Each gap is independently sufficient to break connectivity, but all must be fixed for a working data plane.

### Gap 1: No Kernel Route to Remote VTEP ⚠️ CRITICAL

**Problem:** When BOOTy encapsulates a frame in VXLAN, the outer IP packet has destination `10.0.0.1` (spine01's loopback / VTEP address). The kernel needs a route to this IP to send the packet. **There is no such route.**

**Why:** GoBGP is a pure-Go BGP daemon. Unlike FRR (which has zebra to install routes into the kernel FIB), GoBGP ONLY maintains BGP RIB internally. It never calls `netlink.RouteAdd()`. Even if spine01 advertises its loopback via BGP and GoBGP receives it, the kernel routing table remains empty.

**Evidence:**
- `pkg/network/gobgp/underlay.go` — `discoverLinkLocalPeer()` discovers the link-local neighbor and establishes BGP, but the underlay only advertises the local RouterID /32 — it never installs received routes
- `pkg/network/gobgp/overlay.go` — `watchRoutes()` (line ~340) is a **log-only stub**: it receives EVPN routes from GoBGP but only logs them, never install kernel routes or FDB entries

**Fix:** `installGatewayRoute()` — add a /32 host route to the gateway VTEP via the first physical NIC.  
**Status:** ⚠️ Coded locally in `stack.go` (commit `ad34c5f`), but **NOT pushed** (non-fast-forward rejection) and **NOT in the CI ISO**.

```go
// Needed: netlink.RouteAdd for 10.0.0.1/32 via first NIC
route := &netlink.Route{
    Dst:       &net.IPNet{IP: gatewayIP, Mask: net.CIDRMask(32, 32)},
    LinkIndex: firstNIC.Attrs().Index,
    Scope:     netlink.SCOPE_LINK,
}
```

### Gap 2: BUM FDB Missing (before fix) ✅ FIXED

**Problem:** Without a static FDB entry for BUM (Broadcast, Unknown unicast, Multicast) traffic, ARP requests from BOOTy have no VXLAN destination — they are silently dropped.

**Fix:** `addGatewayFDB()` — installs `00:00:00:00:00:00 → 10.0.0.1` on the VXLAN device so BUM frames flood to spine01.  
**Status:** ✅ Fixed in commit `88086d4`, deployed in ISO, but **insufficient alone** (Gap 1 blocks it).

### Gap 3: `watchRoutes()` is a Stub — No FDB Entry Installation

**Problem:** `overlay.go` `watchRoutes()` connects to GoBGP's route monitoring API but only logs received EVPN routes. It never:
- Installs MAC→VTEP FDB entries from received Type-2 routes
- Installs VTEP→VTEP multicast/flood entries from received Type-3 routes
- Updates kernel routes for received Type-5 routes

This means even if spine01 sends EVPN routes advertising its MAC (via Type-2) or VTEP membership (via Type-3), BOOTy's VXLAN FDB remains empty except for the static BUM entry.

**Impact:** For the E2E test, this is **mitigated** by Gap 2's BUM FDB fix — since we hardcode the gateway VTEP, we don't strictly need dynamic FDB learning. But for production multi-VTEP deployments, this is a critical gap.

**Fix:** Implement `watchRoutes()` to process:
- Type-2 routes → `netlink.NeighAdd()` with `NDA_DST` (remote VTEP)
- Type-3 routes → `netlink.NeighAdd()` BUM FDB entry per remote VTEP
- Type-5 routes → `netlink.RouteAdd()` into VRF routing table

**Status:** ❌ Not implemented. **Not blocking E2E** (single-VTEP topology uses static BUM entry).

### Gap 4: Only Type-5 Routes Advertised — No Type-2 or Type-3

**Problem:** `overlay.go` `advertiseType5()` sends IP prefix routes (Type-5), but:
- **No Type-2 (MAC/IP):** spine01 never learns BOOTy's bridge MAC via BGP. It relies on data-plane MAC learning (enabled on spine01's vxlan100 — no `nolearning` flag).
- **No Type-3 (Inclusive Multicast):** spine01 doesn't know BOOTy's VTEP participates in VNI 100's flood domain. BUM from spine01 to BOOTy only works because of static FDB entries in the topology or data-plane learning.

**Impact:** In the E2E topology this is partially mitigated:
- spine01's `vxlan100` has MAC learning **enabled** (no `nolearning` flag in `topology.clab.yml`), so it CAN learn BOOTy's source MAC from incoming VXLAN frames
- But this creates a chicken-and-egg: spine01 can only learn BOOTy's MAC from a frame that BOOTy successfully sends, which requires Gap 1 to be fixed first

**Fix:** Advertise Type-2 route for the bridge MAC after VXLAN is created, and Type-3 inclusive multicast route with the local VTEP IP.

**Status:** ❌ Not implemented. **Partially mitigated for E2E** by spine01 data-plane learning (once Gap 1 is fixed).

### Gap 5: Asymmetric VXLAN Learning Configuration

**Problem:**
- spine01's `vxlan100`: MAC learning **enabled** (data-plane learning)
- BOOTy's VXLAN: `Learning: false` (line 231 of `overlay.go`)

This means:
- spine01 CAN learn BOOTy's MAC from incoming VXLAN frames ✅
- BOOTy CANNOT learn spine01's MAC from incoming VXLAN frames ❌

**Impact:** For the E2E test, this is **acceptable** because:
- BOOTy only needs to reach spine01 at 10.100.0.1 (the gateway)
- The BUM FDB entry (Gap 2 fix) handles ARP resolution toward spine01
- spine01 learns BOOTy's MAC via data-plane learning and can send return traffic

For production: both sides should use control-plane learning (Type-2/3 route exchange) with `Learning: false`.

**Fix:** Not blocking E2E. For production, combine with Gap 3+4 fixes (control-plane FDB population) and keep `Learning: false` on both sides.

**Status:** ⚠️ Cosmetic for E2E, important for production.

### Gap 6: BGP Ghost Entries "leaf AS 65500"

**Problem:** spine01 BGP summary shows:

```
leaf(eth3) AS 65500 Idle MsgRcvd=9 MsgSent=6
leaf(eth4) AS 65500 Idle MsgRcvd=9 MsgSent=6  
leaf(eth5) AS 65500 Idle MsgRcvd=9 MsgSent=6
```

These don't match our topology — VMs are AS 65100-65102, not "leaf AS 65500". Yet messages were exchanged.

**Hypothesis:** The QEMU VMs boot from a base ISO/initrd image before CAPRF attaches the provisioner ISO via Redfish virtual media. If that base image contains a GoBGP/networking stack with default configuration (hostname "leaf", ASN 65500), it would attempt peering with spine01. When CAPRF later attaches the provisioner ISO and the VM reboots into it, the stale session remains on spine01.

**Impact:** Potentially delays BGP convergence — spine01 has stale sessions on the underlay ports that may interfere with new sessions from BOOTy.

**Fix:** 
1. Investigate the base VM image to confirm if it ships a default BGP config
2. Add `neighbor shutdown` or explicit `neighbor clear` for eth3/4/5 during ContainerLab setup, before provisioner ISO boot
3. The existing `setup-evpn.sh` BGP bounce script may partially address this, but the Idle state suggests sessions aren't fully cleaned

**Status:** ⚠️ Investigation needed. May be a red herring if Gap 1 is the true blocker.

---

## 4. Failure Chain Summary

```
BOOTy boots → GoBGP underlay ESTABLISHED ✅
  → VXLAN interface created ✅
  → BUM FDB entry added (00:00:00:00:00:00 → 10.0.0.1) ✅
  → Provision IP assigned (10.100.0.20/24) ✅
  → WaitForConnectivity("http://10.100.0.1:<port>")
    → ARP for 10.100.0.1 sent on br-vxlan
    → ARP enters VXLAN device → encapsulate → outer dst 10.0.0.1
    → Kernel route lookup for 10.0.0.1 → *** NO ROUTE *** ❌
    → Packet dropped silently
    → 5 minute timeout → "Network connectivity timeout" → reboot
    → Infinite reboot loop
```

**The single critical blocker is Gap 1 (no kernel route to remote VTEP).** All other gaps are either already fixed (Gap 2), not blocking for the E2E topology (Gaps 3-5), or unconfirmed (Gap 6).

---

## 5. Fix Priority Matrix

| # | Gap | Severity | E2E Blocking? | Fix Exists? | Effort |
|---|-----|----------|---------------|-------------|--------|
| 1 | No kernel route to VTEP | **CRITICAL** | **YES** | ⚠️ Coded, not deployed | Small — push + rebuild ISO |
| 2 | BUM FDB missing | CRITICAL | Was blocking | ✅ Deployed | Done |
| 3 | `watchRoutes()` stub | High | No (single VTEP) | ❌ Not started | Medium |
| 4 | No Type-2/3 adverts | High | No (spine learns via data-plane) | ❌ Not started | Medium |
| 5 | Asymmetric learning | Low | No | N/A (design choice) | N/A |
| 6 | BGP ghost entries | Unknown | Unlikely | Needs investigation | Small |

---

## 6. Minimum Fixes to Unblock EVPN E2E

To make test #12 "BOOTy calls back through the EVPN overlay" pass:

### Step 1: Resolve BOOTy branch divergence
The local branch `fix/e2e-no-skips` (at `ad34c5f`) has the gateway route fix but can't be pushed because it diverged from remote `refactor/remove-legacy` (at `1790f0d`). Options:
- Rebase `fix/e2e-no-skips` onto remote `refactor/remove-legacy`
- Force-push (if safe — no other contributors)
- Cherry-pick the two fixes onto `refactor/remove-legacy`

### Step 2: Push `installGatewayRoute()` fix
Commit `ad34c5f` adds `installGatewayRoute()` in `stack.go` — a /32 host route to the gateway VTEP IP via the first physical NIC. This is the missing piece.

### Step 3: Rebuild `booty-gobgp.iso`
Current ISO in CAPRF is from `88086d4` (BUM FDB fix only). Rebuild from the commit that includes both:
- `addGatewayFDB()` (BUM flooding)
- `installGatewayRoute()` (kernel route to VTEP)

### Step 4: Update ISO in CAPRF and push
Replace `test/e2e/assets/booty-gobgp.iso` + `.sha256` in CAPRF MR #69.

### Step 5: Trigger pipeline
Expect `e2e-kindmetal-evpn` test #12 to pass within ~5 minutes (BGP converge + ARP resolve + HTTP POST).

---

## 7. Future Work (Not Blocking E2E)

| Item | Description | Priority |
|------|-------------|----------|
| Implement `watchRoutes()` | Process received Type-2/3 EVPN routes → install FDB entries | High (multi-VTEP) |
| Advertise Type-2 routes | Announce bridge MAC so remote VTEPs learn via control plane | High (multi-VTEP) |
| Advertise Type-3 routes | Announce VTEP membership for proper BUM flooding | High (multi-VTEP) |
| Investigate ghost BGP entries | Determine if base VM ISO starts BGP with wrong config | Medium |
| Add VXLAN data-plane unit tests | Test FDB installation, route installation, encap/decap | Medium |
| Consider `nolearning` on spine01 | Match BOOTy's `Learning: false` for symmetric design | Low |

---

## 8. Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                        Kind Cluster                              │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │ CAPRF Controller (NodePort :XXXXX)                        │  │
│  │   ← POST /status/init = ProvisionerStarted event         │  │
│  └───────────────────────────────────────────────────────────┘  │
│                           ↑                                      │
│                      Kind Node IP                                │
└─────────────────────────────────────────────────────────────────┘
                           ↑ DNAT
                           │
┌──────────────── spine01 (FRR, AS 65000) ────────────────────────┐
│  lo: 10.0.0.1/32          eth0 → Kind network                   │
│  br100: 10.100.0.1/24     vxlan100 (VNI 100, learning=ON)       │
│  eth3,4,5: eBGP underlay to VMs                                 │
│  iptables DNAT: 10.100.0.1 → Kind Node IP                       │
│  iptables MASQUERADE: outbound via eth0                          │
└──────────┬────────────┬────────────┬────────────────────────────┘
          eth3         eth4         eth5
     (link-local) (link-local) (link-local)
           │            │            │
    ┌──────┘     ┌──────┘     ┌──────┘
    │            │            │
┌───┴──────┐ ┌──┴───────┐ ┌──┴───────┐
│  VM-0    │ │  VM-1    │ │  VM-2    │
│ AS 65100 │ │ AS 65101 │ │ AS 65102 │
│ BOOTy    │ │ BOOTy    │ │ BOOTy    │
│ GoBGP    │ │ GoBGP    │ │ GoBGP    │
│          │ │          │ │          │
│ VXLAN    │ │ VXLAN    │ │ VXLAN    │
│ VNI 100  │ │ VNI 100  │ │ VNI 100  │
│ Learning │ │ Learning │ │ Learning │
│  =false  │ │  =false  │ │  =false  │
│          │ │          │ │          │
│ br-vxlan │ │ br-vxlan │ │ br-vxlan │
│10.100.0  │ │10.100.0  │ │10.100.0  │
│   .20/24 │ │   .21/24 │ │   .22/24 │
└──────────┘ └──────────┘ └──────────┘

Packet path (broken):
  VM-0 → br-vxlan → VXLAN encap (dst VTEP 10.0.0.1)
       → kernel route lookup → *** NO ROUTE *** → DROP

Packet path (with Gap 1 fix):
  VM-0 → br-vxlan → VXLAN encap (dst VTEP 10.0.0.1)
       → kernel route 10.0.0.1/32 via eth1 → ARP spine01 link-local
       → spine01 vxlan100 decap → br100 → DNAT → Kind → CAPRF ✅
```
