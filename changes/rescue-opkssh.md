---
section: Breaking changes
---

- BOOTy serves rescue SSH itself; the ramdisk no longer ships `dropbear`
  (#961). The behavior of the hardened dropbear setup stays: key-only once a
  key is set, password login only without (yescrypt, sha512crypt,
  sha256crypt, bcrypt and md5crypt hashes), several keys, the
  `authorized_keys` options dropbear honours, PTY, commands, signals, the
  hardened algorithms and the 15 minute idle timeout. Not kept:
  `sntrup761x25519-sha512` key exchange (clients fall back to
  `mlkem768x25519-sha256` or `curve25519-sha256`) and keys with
  `verify-required`, which are refused. The `DROPBEAR_HARDENED` build
  argument is gone: `RESCUE_SSH_HARDENED=false` now also allows RSA keys,
  an RSA host key, group14 and HMAC-SHA1 at runtime.
- Rescue SSH accepts opkssh (OpenID Connect) logins.
  `RESCUE_SSH_OPK_PROVIDERS` and `RESCUE_SSH_OPK_POLICY` take the opkssh
  `providers` and `auth_id` rows; the server verifies the PK Token against
  them (JWKS over the BOOTy HTTPS client) and still accepts the
  `RESCUE_SSH_PUBKEY` keys. Standby payloads and deploy archives cannot
  change the two keys. The micro flavor builds with `no_opkssh`.
