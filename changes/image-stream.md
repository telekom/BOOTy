---
section: Behaviour changes in all modes
---

- New option `IMAGE_STREAM=true` (YAML `provision.image.stream`, CAPRF
  `additionalVars`) forbids staging the image in RAM. Raw and compressed raw
  images (lz4, zstd, gzip, xz, bzip2) already stream in every mode and do not
  change. A QCOW2 image then fails before the disk wipe with `BOOTY-IMG-004`,
  and `IMAGE_MODE=partition` is an invalid config. Default off: QCOW2 and
  partition mode stage in tmpfs as before (#PRNUM).
- A failed image write or checksum mismatch now zeroes the first and the last
  MiB of the target disk or partition, not only the first MiB, so a backup
  GPT cannot bring back a partial image (#PRNUM).
- An `oci://` artifact of the T-CaaS image builder, which has the image, its
  ansible facts and optionally a QCOW2 layer, now works: BOOTy takes the one
  `application/vnd.tcaas.node-image+raw*` layer. Before, more than one
  non-text layer failed the pull (#PRNUM).
- BOOTy logs the RAM high-water mark of the image step (`image step memory`,
  `peak_used_mib`) (#PRNUM).
