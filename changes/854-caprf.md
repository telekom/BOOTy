---
section: Upgrade notes for CAPRF
---

- `oci://` image sources with an artifact layer (`oras push`, for example
  `application/vnd.tcaas.node-image+raw.zst`): a `download` scope
  `IMAGE_CHECKSUM` (the default) is now the digest of the pushed file, the
  value of the published `.raw.zst.sha256` or `.raw.lz4.sha256`. Before, it
  had to be the digest after go-containerregistry decompressed a gzip or
  zstd blob. A machine that sets such a decompressed digest needs
  `IMAGE_CHECKSUM_SCOPE=decompressed`. Container layer types
  (`application/vnd.oci.image.layer.v1.tar+gzip`) do not change (#854).
