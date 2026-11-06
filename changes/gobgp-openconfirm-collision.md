---
section: Behaviour changes in all modes
---

- GoBGP mode: after a carrier fault, the leaf's and BOOTy's connections
  collide. When BOOTy has the higher BGP Identifier and the leaf's OPEN
  arrives on BOOTy's connection while the leaf's connection is in
  OpenConfirm, GoBGP now waits for the leaf's choice (a NOTIFICATION or
  close on one connection, or its KEEPALIVE on BOOTy's) instead of closing
  the leaf's connection at once. The leaf could already be Established on
  its connection; then both connections ended and the leaf counted two
  session drops for one fault. Without a choice within 500 ms, RFC 4271
  section 6.8 applies as before.
