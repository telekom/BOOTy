---
section: Behaviour changes in all modes
---

- GoBGP mode: after a carrier fault, the leaf's and BOOTy's connections
  collide. When BOOTy has the higher BGP Identifier, GoBGP now resolves the
  collision the way FRR does. It closes the leaf's connection as soon as
  the leaf's OPEN arrives there while BOOTy's OPEN is out on its own
  connection, and does not send a KEEPALIVE on the leaf's connection
  first. When its OPEN went out only after that KEEPALIVE, GoBGP waits for
  the leaf's choice instead of deciding first. Before, the leaf could
  become Established on its own connection while GoBGP kept the other one.
  Both connections then ended, and the leaf counted two session drops for
  one fault.
