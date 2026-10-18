---
section: Behaviour changes in all modes
---

- The default, gobgp and slim ramdisks no longer ship `curl` and its
  libraries (libcurl, Kerberos, LDAP, SASL, libssh2, nghttp2/3, brotli and,
  in slim, OpenSSL and GnuTLS). BOOTy never ran `curl`: it fetches images,
  CAPRF callbacks and tokens with Go's `net/http`. In the rescue shell use
  busybox `wget` (HTTP and HTTPS; `--no-check-certificate` for a
  self-signed server, `--header` for a bearer token) (#814).
