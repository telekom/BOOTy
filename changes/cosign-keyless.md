---
section: Upgrade notes for CAPRF
---

- Keyless cosign verification: `IMAGE_COSIGN_TRUSTED_ROOT`,
  `IMAGE_COSIGN_IDENTITY_REGEXP` and `IMAGE_COSIGN_OIDC_ISSUER` (and the same
  `SYSEXT_COSIGN_*` options) check a Fulcio/Rekor signature of `oci://`
  sources offline against a Sigstore `trusted_root.json` in the ramdisk,
  before the wipe. With keys as well, either kind of signature is accepted.
  All are unset by default. BOOTy reads bundles v0.1 to v0.3 (DSSE or message
  signature) attached as OCI referrers and the cosign 2 `.sig` tag, with Rekor
  v1 entries (SET, inclusion proof) or Rekor v2 entries (`hashedrekord` 0.0.2,
  checkpoint) and RFC 3161 timestamps of the trusted root's timestamp
  authorities. A Rekor v2 signature needs a timestamp: the certificate is
  checked at every verified time and needs at least one.
- A standby command payload can no longer change `IMAGE_COSIGN_PUBKEYS`,
  `SYSEXT_COSIGN_PUBKEYS` or the new keyless options; such a payload is
  refused like one that changes the transport. Under a cosign policy of the
  vars file (keys or keyless, also since #921) it can no longer add an image
  or sysext source that is not `oci://` or change the checksum
  (`IMAGE_CHECKSUM*`, the layer `sha256`) of such a source, which would have
  replaced the signed image with one checked only by the payload's checksum.
  Dropping such a source, or switching to another `oci://` source, which is
  verified, still works.
