---
section: Upgrade notes for CAPRF
---

- Hard deprovisioning (`deprovision`, `hard`) and `decommission` now
  crypto-erase every LUKS2 header on the disks they wipe before the wipe
  (`cryptsetup luksErase`, then zeros over both header copies and the
  keyslots area, #948): the escrowed recovery key opens nothing afterwards. A
  host without LUKS2 runs no tool. A failure, also a damaged LUKS2 header,
  fails the run with `BOOTY-DSK-029`, also in `decommission`, which
  otherwise does not stop on disk errors. `DEPROVISION_CRYPTO_ERASE=false`
  skips the step; a standby payload or deploy archive cannot set it. The
  success status names the erased devices (`; crypto-erased: /dev/sda2
  luksUUID=…`): the consumer of `DISK_ENCRYPTION_RECOVERY_KEY_URL` can
  invalidate the escrowed key by that UUID. BOOTy sends nothing to the
  recovery key URL at deprovision; it has no call for that.
