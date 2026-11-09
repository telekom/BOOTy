---
section: Behaviour changes in all modes
---

- Seeding a data partition (system-ab `AB_DATA_PARTITIONS`, and now every
  `PARTITION_LAYOUT` mountpoint other than `/` and `/boot/efi`) copies the
  extended attributes of files and directories, so POSIX ACLs, file
  capabilities and security labels are kept. A data filesystem that cannot
  store an attribute of the source now fails the seed instead of dropping it.
  Outside A/B mode, `PARTITION_LAYOUT` accepts any clean, unique mountpoint
  except `/boot`, `/proc`, `/sys`, `/dev`, `/run` and paths below
  `/boot/efi` (partition layout mountpoints PR).
