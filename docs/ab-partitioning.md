# A/B OS Provisioning

BOOTy supports an optional A/B image mode for dual-root OS provisioning and
upgrades. Existing `whole-disk` and `partition` modes are unchanged.

## Behavior

Set `provision.image.mode: ab` or `IMAGE_MODE=ab`. The default `dual-root`
scheme generates this GPT layout unless `preserveExisting` is set:

| Partition | Label | Purpose |
| --- | --- | --- |
| 1 | `BOOTY-EFI` | Shared EFI system partition |
| 2 | `BOOTY-ROOT-A` | Root slot A |
| 3 | `BOOTY-ROOT-B` | Root slot B |
| 4 | `BOOTY-STATE` | Persistent BOOTy state, fills remaining disk by default |

The `system-ab` scheme keeps the same shared EFI and root slots but replaces
`BOOTY-STATE` with shared runtime data partitions. By default it creates one
ext4 partition:

| Partition | Label | Mountpoint | Purpose |
| --- | --- | --- | --- |
| 4 | `BOOTY-DATA` | `/var` | Shared runtime data across root-slot upgrades |

Use `dataPartitions` or `AB_DATA_PARTITIONS` to add or replace shared data
mounts such as `/home`. If no data partitions are configured, `stateSizeMB` /
`AB_STATE_SIZE_MB` remains a compatibility alias for the default `/var`
partition size. A size of `0` still means "fill remaining disk".

Initial provisioning writes slot A unless `targetSlot` is set. Upgrades set
`preserveExisting: true`, provide `activeSlot`, and normally use
`targetSlot: inactive`; BOOTy then rewrites only the inactive root slot and
leaves the active slot available for rollback.

BOOTy copies the source image EFI partition into `BOOTY-EFI` during initial
provisioning when the source image contains one. During `preserveExisting`
upgrades, it leaves `BOOTY-EFI`
untouched so the active slot remains rollback-capable. The source root partition
is selected by `sourceRootLabel`, `sourceRootPartition`, a single common root
label such as `rootfs`, or one unambiguous Linux filesystem partition. Ambiguous
partitioned source images fail fast instead of guessing. If the source image is
a plain root filesystem image without a partition table, BOOTy copies that file
directly into the target root slot.

For `system-ab`, BOOTy mounts shared data partitions before hostname writes,
provisioner files, sysext preload/activation, fstab generation, cloud-init
injection, machine files, and post-provision commands. During first install,
BOOTy seeds an empty shared partition from the corresponding directory in the
target root slot before mounting it. During `preserveExisting` upgrades, shared
data partitions are validated and mounted but never formatted or seeded.

After mounting the target root, BOOTy writes `/etc/booty/ab-slot.env` with the
selected slot, booted slot marker, and root partition. Higher-level tooling can
read that after boot to report the active slot. BOOTy also writes an explicit
`root=PARTLABEL=BOOTY-ROOT-A` or `root=PARTLABEL=BOOTY-ROOT-B` kernel argument
into the target GRUB defaults so kexec and firmware boots select the same root
slot.

For `preserveExisting` upgrades, BOOTy validates the declared active root slot
before wiping the inactive target. If the current kernel command line identifies
an installed A/B root with `root=PARTLABEL=BOOTY-ROOT-A`,
`root=PARTLABEL=BOOTY-ROOT-B`, or a matching partition node such as
`root=/dev/sda2`, BOOTy rejects a conflicting active-slot value. When BOOTy is
booted from virtual media and `root=` does not identify an A/B slot, it skips the
cmdline proof and relies on the read-only validation of the declared active root
slot's `/etc/booty/ab-slot.env`. This keeps CAPRF-style virtual-media upgrades
working while still preventing a stale `AB_ACTIVE_SLOT` value from wiping the
currently active root partition. Preserve-existing upgrades require kexec; BOOTy
keeps `/newroot` mounted until kexec runs, and powers off instead of doing a
normal reboot if kexec is disabled or fails, because the firmware boot path may
still point at the old active slot.

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

`system-ab` example:

```yaml
provision:
  image:
    mode: ab
    urls:
      - oci://registry.example.com/tcaas/os-node:v2
  ab:
    scheme: system-ab
    activeSlot: a
    targetSlot: inactive
    preserveExisting: true
    sourceRootPartition: 2
    bootSizeMB: 512
    rootSizeMB: 65536
    dataPartitions:
      - label: BOOTY-HOME
        mountpoint: /home
        filesystem: ext4
        sizeMB: 65536
      - label: BOOTY-DATA
        mountpoint: /var
        filesystem: ext4
        sizeMB: 0
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

For `system-ab`, configure additional shared data partitions as JSON:

```sh
export AB_SCHEME="system-ab"
export AB_DATA_PARTITIONS='[{"label":"BOOTY-HOME","mountpoint":"/home","filesystem":"ext4","sizeMB":65536},{"label":"BOOTY-DATA","mountpoint":"/var","filesystem":"ext4","sizeMB":0}]'
```

Flatcar-like source images commonly expose immutable OS slots as `USR-A` and
`USR-B` plus a stateful `ROOT` partition. Use an explicit selector such as
`AB_SOURCE_ROOT_LABEL="USR-A"` for these images; BOOTy intentionally rejects
ambiguous source images instead of guessing between OS slots.

## Requirements

- Source images should be signed and pinned by checksum.
- `rootSizeMB` must fit the source root filesystem with operational headroom.
- Use `preserveExisting: true` only on disks already provisioned with the BOOTy
  A/B scheme.
- Prefer a kernel `root=` parameter that resolves to `BOOTY-ROOT-A`,
  `BOOTY-ROOT-B`, or the corresponding partition node. Virtual-media provisioner
  boots are also supported; in that case the active root slot state file becomes
  the required validation source.
- Do not set `DISABLE_KEXEC=true` for preserve-existing upgrades. The inactive
  slot is activated through kexec, while the active slot stays available for
  rollback.
- Keep `/etc/booty/ab-slot.env` in both root slots. BOOTy writes it during
  provisioning and validates it on later upgrades.
- Sysext layers remain independent from the OS image. Preload sysexts into the
  target root during A/B provisioning, then activate the desired composition at
  boot or through a higher-level updater.
