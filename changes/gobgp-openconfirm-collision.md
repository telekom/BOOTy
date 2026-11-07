---
section: Behaviour changes in all modes
---

- GoBGP mode: after a carrier fault, the leaf's and BOOTy's connections
  collide. When BOOTy has the higher BGP Identifier and both connections
  carry its OPEN, GoBGP now waits for the leaf's choice (a NOTIFICATION or
  close on one connection, or its KEEPALIVE on BOOTy's connection) instead
  of deciding first. GoBGP used to close the leaf's connection while the
  leaf was already Established there, or go Established on the leaf's
  connection while the leaf kept BOOTy's. Either way both connections
  ended, and the leaf counted two session drops for one fault. Without a
  choice within 500 ms, GoBGP behaves as before.
