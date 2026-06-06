# A/B OS Provisioning

BOOTy supports an optional A/B image mode for dual-root OS provisioning and
upgrades. Existing `whole-disk` and `partition` modes are unchanged.

## Behavior

Set `provision.image.mode: ab` or `IMAGE_MODE=ab`. BOOTy generates this GPT
scheme unless `preserveExisting` is set:

| Partition | Label | Purpose |
| --- | --- | --- |
| 1 | `BOOTY-EFI` | Shared EFI system partition |
| 2 | `BOOTY-ROOT-A` | Root slot A |
| 3 | `BOOTY-ROOT-B` | Root slot B |
| 4 | `BOOTY-STATE` | Persistent BOOTy state, fills remaining disk by default |

Initial provisioning writes slot A unless `targetSlot` is set. Upgrades set
`preserveExisting: true`, provide `activeSlot`, and normally use
`targetSlot: inactive`; BOOTy then rewrites only the inactive root slot and
leaves the active slot available for rollback.

BOOTy copies the source image EFI partition into `BOOTY-EFI` and the largest
Linux/root partition into the target root slot. If the source image is a plain
root filesystem image without a partition table, BOOTy copies that file directly
into the target root slot.

After mounting the target root, BOOTy writes `/etc/booty/ab-slot.env` with the
selected slot and root partition. Higher-level tooling can read that after boot
to report the active slot.

## YAML

```yaml
provision:
  image:
    mode: ab
    urls:
      - oci://registry.example.com/tcaas/os-node:v2
    checksumType: sha256
    checksum: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  ab:
    scheme: dual-root
    activeSlot: a
    targetSlot: inactive
    preserveExisting: true
    bootSizeMB: 512
    rootSizeMB: 65536
    stateSizeMB: 0
```

## CAPRF Vars

```sh
export IMAGE="oci://registry.example.com/tcaas/os-node:v2"
export IMAGE_MODE="ab"
export AB_SCHEME="dual-root"
export AB_ACTIVE_SLOT="a"
export AB_TARGET_SLOT="inactive"
export AB_PRESERVE_EXISTING="true"
export AB_BOOT_SIZE_MB="512"
export AB_ROOT_SIZE_MB="65536"
export AB_STATE_SIZE_MB="0"
```

## Requirements

- Source images should be signed and pinned by checksum.
- `rootSizeMB` must fit the source root filesystem with operational headroom.
- Use `preserveExisting: true` only on disks already provisioned with the BOOTy
  A/B scheme.
- Sysext layers remain independent from the OS image. Preload sysexts into the
  target root during A/B provisioning, then activate the desired composition at
  boot or through a higher-level updater.
