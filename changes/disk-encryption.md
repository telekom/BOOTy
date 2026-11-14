---
section: Upgrade notes for CAPRF
---

- Opt-in LUKS2 + TPM2 disk encryption (`DISK_ENCRYPTION=luks2-tpm2`, see
  `docs/disk-encryption.md`). The provisioner must serve
  `DISK_ENCRYPTION_RECOVERY_KEY_URL`: BOOTy posts
  `{"hostname","device","luksUUID","keySlot","recoveryKey"}` with the bearer
  token before it encrypts, and fails the run on any answer other than 2xx.
  The endpoint stores (escrows) the key. With encryption on, BOOTy reboots
  through the firmware instead of kexec. The provisioner must turn Secure
  Boot on for the installed system to bind the volume to the TPM2 (#938).
