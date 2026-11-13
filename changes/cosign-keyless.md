---
section: Upgrade notes for CAPRF
---

- Keyless cosign verification: `IMAGE_COSIGN_TRUSTED_ROOT`,
  `IMAGE_COSIGN_IDENTITY_REGEXP` and `IMAGE_COSIGN_OIDC_ISSUER` (and the same
  `SYSEXT_COSIGN_*` options) check a Fulcio/Rekor signature of `oci://`
  sources offline against a Sigstore `trusted_root.json` in the ramdisk,
  before the wipe. With keys as well, either kind of signature is accepted.
  All are unset by default.
- A standby command payload can no longer change `IMAGE_COSIGN_PUBKEYS`,
  `SYSEXT_COSIGN_PUBKEYS` or the new keyless options; such a payload is
  refused like one that changes the transport.
