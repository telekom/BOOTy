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

BOOTy copies the source image EFI partition into `BOOTY-EFI` during initial
provisioning. During `preserveExisting` upgrades, it leaves `BOOTY-EFI`
untouched so the active slot remains rollback-capable. The source root partition
is selected by `sourceRootLabel`, `sourceRootPartition`, a single common root
label such as `rootfs`, or one unambiguous Linux filesystem partition. Ambiguous
partitioned source images fail fast instead of guessing. If the source image is
a plain root filesystem image without a partition table, BOOTy copies that file
directly into the target root slot.

After mounting the target root, BOOTy writes `/etc/booty/ab-slot.env` with the
selected slot, booted slot marker, and root partition. Higher-level tooling can
read that after boot to report the active slot.

For `preserveExisting` upgrades, BOOTy first verifies that the kernel command
line identifies the booted slot with `root=PARTLABEL=BOOTY-ROOT-A`,
`root=PARTLABEL=BOOTY-ROOT-B`, or the matching partition node such as
`root=/dev/sda2`. It rejects a conflicting or missing booted-slot signal before
it mounts the declared active root slot read-only and validates
`/etc/booty/ab-slot.env`. This prevents a stale `AB_ACTIVE_SLOT` value from
wiping the currently active root partition. Preserve-existing upgrades require
kexec; BOOTy keeps `/newroot` mounted until kexec runs, and powers off instead
of doing a normal reboot if kexec is disabled or fails, because the firmware
boot path may still point at the old active slot.

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
    sourceRootLabel: rootfs
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
export AB_SOURCE_ROOT_LABEL="rootfs"
export AB_BOOT_SIZE_MB="512"
export AB_ROOT_SIZE_MB="65536"
export AB_STATE_SIZE_MB="0"
```

Use `AB_SOURCE_ROOT_PARTITION="2"` instead of `AB_SOURCE_ROOT_LABEL` only for
source images that do not carry a stable GPT root partition label.

## Requirements

- Source images should be signed and pinned by checksum.
- `rootSizeMB` must fit the source root filesystem with operational headroom.
- Use `preserveExisting: true` only on disks already provisioned with the BOOTy
  A/B scheme.
- Boot preserve-existing upgrades with a kernel `root=` parameter that resolves
  to `BOOTY-ROOT-A`, `BOOTY-ROOT-B`, or the corresponding partition node.
- Do not set `DISABLE_KEXEC=true` for preserve-existing upgrades. The inactive
  slot is activated through kexec, while the active slot stays available for
  rollback.
- Keep `/etc/booty/ab-slot.env` in both root slots. BOOTy writes it during
  provisioning and validates it on later upgrades.
- Sysext layers remain independent from the OS image. Preload sysexts into the
  target root during A/B provisioning, then activate the desired composition at
  boot or through a higher-level updater.
