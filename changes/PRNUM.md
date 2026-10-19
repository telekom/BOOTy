---
section: Behaviour changes in all modes
---

- DHCPv6 (RFC 8415): the client requests from the ADVERTISE with the
  highest preference of the first SOLICIT timeout, reads late replies on
  the socket of the transaction, and answers NoBinding in a RENEW or
  REBIND REPLY with a REQUEST (#PRNUM).
