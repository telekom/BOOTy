---
section: Breaking changes
---

- The rescue SSH server is hardened. `dropbear` is built from the pinned
  Debian trixie source without `diffie-hellman-group14-sha256`, RSA keys and
  SFTP, uses an Ed25519 host key, and refuses password login once
  `RESCUE_SSH_PUBKEY` holds a key, local and remote port forwarding, and
  sessions idle for 15 minutes. RSA keys in `RESCUE_SSH_PUBKEY` no longer
  work; use Ed25519 or ECDSA keys. `RESCUE_PASSWORD_HASH` gives SSH password
  login only when no key is set. `RESCUE_SSH_HARDENED=false` restores the old
  runtime behaviour, `--build-arg DROPBEAR_HARDENED=false` the old algorithms,
  both for the transition only.
- `RESCUE_SSH_PUBKEY` accepts several keys separated by `;`. Malformed
  entries are logged and skipped.
- A standby command payload or deploy archive can no longer change
  `RESCUE_SSH_PUBKEY`, `RESCUE_PASSWORD_HASH` or `RESCUE_SSH_HARDENED`: a
  failed command drops to the rescue login of `/deploy/vars`.
