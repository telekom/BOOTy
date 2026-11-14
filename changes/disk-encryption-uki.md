---
section: Upgrade notes for CAPRF
---

- Opt-in signed UKI boot with a signed PCR 11 policy
  (`DISK_ENCRYPTION=luks2-tpm2-uki`, `SECUREBOOT_SIGNING=per-host|ci`, see
  `docs/disk-encryption.md#signed-uki-and-pcr-11`). The image must be a
  cosign-verified `oci://` image with one UKI at `/EFI/Linux/*.efi` on the
  ESP and the manifest annotation `com.github.telekom.booty.uki.sha256`.
  With `per-host` the provisioner must enroll the reported certificate
  (`secureBoot.uki.cert`) in `db` before it turns Secure Boot on. Nothing
  changes with the defaults (#PR).
