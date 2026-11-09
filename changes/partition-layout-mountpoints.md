---
section: Behaviour changes in all modes
---

- Seeding a data partition (system-ab `AB_DATA_PARTITIONS`, and now every
  `PARTITION_LAYOUT` mountpoint other than `/` and `/boot/efi`) copies the
  extended attributes of files and directories, so POSIX ACLs, file
  capabilities and security labels are kept. A data filesystem that cannot
  store an attribute of the source now fails the seed instead of dropping it.
  Outside A/B mode, `PARTITION_LAYOUT` accepts any clean, unique mountpoint
  except `/boot` and paths below it other than `/boot/efi`, `/etc`,
  `/etc/booty`, `/proc`, `/sys`, `/dev` and `/run`; `vfat` only for
  `/boot/efi` (partition layout mountpoints PR).
- Outside A/B mode, `install-efi-fallback` now puts BOOTy's removable
  fallback loader on the new ESP of a `PARTITION_LAYOUT`, as in A/B mode.
  Before, the ESP stayed empty because only the image root is written.
