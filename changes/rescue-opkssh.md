---
section: Highlights
---

- Rescue SSH accepts opkssh (OpenID Connect) logins (#961).
  `RESCUE_SSH_OPK_PROVIDERS` and `RESCUE_SSH_OPK_POLICY` take the opkssh
  `providers` and `auth_id` rows. With either set, BOOTy serves rescue SSH
  from its own hardened Go server instead of `dropbear`; it verifies the PK
  Token against the providers (JWKS over the BOOTy HTTPS client) and the
  policy and still accepts the `RESCUE_SSH_PUBKEY` keys. Standby payloads and
  deploy archives cannot change the two keys. Without them nothing changes.
  The opkssh code adds about 0.9 MB (zstd) to the default, gobgp and slim
  ramdisks; micro builds with `no_opkssh`.
