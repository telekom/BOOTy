---
section: Upgrade notes for CAPRF
---

- With `DISK_ENCRYPTION`, BOOTy now appends the signed `KEK`, `db` and
  `dbx` updates of the image (`/usr/share/secureboot/updates`, what
  `secureboot-db` applies on the first boot) to the firmware before the
  reboot (step `apply-secureboot-updates`, #976). The first boot then binds
  the volume to the final PCR 7, and the second boot unlocks without the
  recovery key on hosts whose `dbx` is older than the image's. A firmware
  refusal is a warning; an unreadable update file or variable fails the
  run with `BOOTY-EFI-013`. `SECUREBOOT_APPLY_DBX_UPDATES=false` turns it
  off.
