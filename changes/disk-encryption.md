---
section: Upgrade notes for CAPRF
---

- Opt-in LUKS2 + TPM2 disk encryption (`DISK_ENCRYPTION=luks2-tpm2`, see
  `docs/disk-encryption.md`, #939). Without it nothing changes. With it the
  provisioner must serve `DISK_ENCRYPTION_RECOVERY_KEY_URL` over HTTPS:
  BOOTy posts `{"hostname","device","luksUUID","keySlot","recoveryKey"}`
  with the bearer token before it formats the partition, follows no
  redirect and fails the run on any answer other than 2xx. The endpoint
  stores (escrows) the key. Optional `DISK_ENCRYPTION_STATUS_URL` receives
  `tpm2-bound`, `slot1-removed` or `failed` from the installed system.
  BOOTy reboots through the firmware instead of kexec, refuses
  `SKIP_TLS_VERIFY` (also the `CAPRF_LEGACY_COMPAT` preset), needs Secure
  Boot in user mode, and the provisioner must turn Secure Boot on for the
  binding. Standby payloads and deploy archives cannot change these keys.
