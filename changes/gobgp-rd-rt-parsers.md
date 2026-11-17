---
section: Behaviour changes in all modes
---

- GoBGP mode parses `EVPN_RD`, `EVPN_RT_IMPORT`, `EVPN_RT_EXPORT`, `vpn_rt`
  and the `frr.conf` `rd` and `route-target` values with GoBGP's
  `ParseRouteDistinguisher` and `ParseRouteTarget` (#871). Every value that
  BOOTy accepted before is still accepted with the same result, and the
  out-of-range values that BOOTy rejected stay rejected. New: an AS number
  in asdot form (`1.100:5`, the 4-byte AS 65636) is accepted like in FRR.
