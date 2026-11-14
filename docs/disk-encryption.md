# Disk encryption (LUKS2 + TPM2)

Opt-in with `DISK_ENCRYPTION=luks2-tpm2`; off by default. Without it BOOTy
runs the same steps as before: the encryption steps are not in the step
list. BOOTy formats one partition of the image with LUKS2 while it writes
the image, and writes that partition into the opened volume. The installed
system binds the volume to the TPM2 (PCR 7+14) once Secure Boot is on. A
root on LVM is supported: BOOTy then encrypts the physical volume of VG
`tcaas` ([LVM-on-LUKS](#root-on-lvm-vg-tcaas)). A random recovery key goes
to the provisioner. Keys are in
[configuration.md](configuration.md#disk-encryption). Read the
[threat model](#threat-model) before you rely on it.

## Flow

Provisioning (BOOTy, Secure Boot off):

1. `stream-image`:
   - checks the TPM2, Secure Boot (firmware support and user mode, see
     `DISK_ENCRYPTION_SKIP_SECURE_BOOT_CHECK`), the ramdisk tools and the
     image layout: a raw GPT image whose encrypted partition is the last
     partition. Nothing is written before these checks pass;
   - writes the image up to that partition (partition table, BIOS boot,
     ESP, `/boot`) to the disk;
   - grows the partition to the end of the disk (`growpart`, which writes a
     new backup GPT), generates a 256-bit recovery key in the
     `systemd-cryptenroll --recovery-key` format (modhex, 8 groups of 8),
     and posts it to `DISK_ENCRYPTION_RECOVERY_KEY_URL`. Without a 2xx
     answer the run stops before the partition is formatted;
   - formats the partition with LUKS2 (key slot 0: recovery key), adds a
     512-bit one-time enrollment key (slot 1), opens it as
     `/dev/mapper/booty_crypt` and streams the partition content of the
     image into the mapping. No byte of the partition reaches the disk in
     plain text. The rest of the image (its backup GPT) is not written.
   - with an [LVM physical volume](#root-on-lvm-vg-tcaas): checks the VG,
     grows the physical volume and activates VG `tcaas` on the mapping.
2. `parse-partitions` checks that the root it finds is the encrypted
   partition; from then on the root is the mapping (on LVM: the logical
   volume of VG `tcaas` that holds the root). `grow-partition` is
   skipped (the partition is grown), `resize-filesystem` grows the file
   system in the mapping.
3. `prepare-disk-encryption` (before `configure-grub`, target mounted)
   checks the image (separate `/boot` for the root; dracut, `lsinitrd`,
   `systemd-cryptsetup`, `systemd-cryptenroll`, `cryptsetup`, `shred`, the
   TPM2 libraries and token plugin; `curl` with a status URL) and writes:
   - `/etc/booty/luks-enroll.key` (0400), the enrollment key;
   - `/etc/crypttab`: `booty_crypt UUID=<luks uuid> /etc/booty/luks-enroll.key luks,x-initrd.attach`;
   - `/etc/dracut.conf.d/90-booty-luks.conf`: `hostonly="no"`, the `crypt`
     and `systemd-cryptsetup` modules, `/etc/crypttab`, the libtss2
     libraries that systemd loads with `dlopen` and the libcryptsetup TPM2
     token plugin. dracut does not see `dlopen` dependencies; without the
     plugin the initramfs falls back to the passphrase prompt silently. Its
     `tpm2-tss` module would add the plugin but needs `tpm2-tools`, and a
     host-only build drops `systemd-cryptsetup` without it;
   - `/etc/dracut.conf.d/91-booty-luks-enroll-key.conf`: the enrollment key
     in the initramfs;
   - `/etc/default/grub.d/99-booty-luks.cfg`: `GRUB_FORCE_PARTUUID=` (no
     initramfs-less boot) and `GRUB_DISABLE_RECOVERY=true`;
   - `/etc/grub.d/01_booty_luks_lock`: the [GRUB lock](#grub-lock);
   - the unit `booty-luks-tpm2-enroll.service` (enabled), its script
     `/usr/local/sbin/booty-luks-tpm2-enroll` and
     `/etc/booty/luks-tpm2.env`; with a status URL also
     `/etc/booty/luks-status.auth` (0600, the bearer token) and the
     `CA_BUNDLE_FILE` as `/etc/booty/luks-status-ca.pem`;
   - and runs `dracut --force --regenerate-all` in the target.
4. `close-encrypted-disk` (after `teardown-chroot`) grows the file system of
   an encrypted data partition (on LVM: deactivates VG `tcaas`) and closes
   the mapping. BOOTy reboots
   through the firmware; disk encryption turns kexec off.

Installed system, `booty-luks-tpm2-enroll.service`:

- Every boot until the enrollment: the initramfs unlocks with the
  enrollment key. The unit does nothing while Secure Boot is off or the
  firmware is in setup mode (`ConditionSecurity=uefi-secureboot`, and the
  script checks `SetupMode=0`).
- Phase 1, the first boot with Secure Boot on in user mode:
  1. `systemd-cryptenroll --tpm2-device=auto --tpm2-pcrs=7+14 --wipe-slot=tpm2`;
  2. `cryptsetup open --test-passphrase --token-only` proves that the TPM2
     opens the volume;
  3. rebuilds the crypttab line from the env file
     (`none luks,x-initrd.attach,tpm2-device=auto`), removes the key from
     the dracut configuration, builds every `/boot/initrd.img-*` into a new
     file and checks it with `lsinitrd` (no key; TPM2 libraries, token
     plugin, `systemd-cryptsetup`, crypttab present), then swaps them in;
  4. marks the volume bound and posts `tpm2-bound`.

  Until step 4 an EXIT trap restores the enrollment-key crypttab line and
  dracut file and removes half-built images; the next boot tries again.
  Slot 1 and the key stay.
- Phase 2, the next boot with Secure Boot on: the initramfs of this boot
  has no key, and the TPM2 opens the volume with the PCRs of this boot, so
  the TPM2 unlocked the root. Only then the script wipes key slot 1 (after
  checking that slot 1 holds the enrollment key), `shred -u` the key file,
  disables itself and posts `slot1-removed`. If slot 1 is gone already (a
  crash after the wipe), it only cleans up. A failed check posts `failed`
  and keeps everything for the next boot.
- Later boots: `systemd-cryptsetup` in the initramfs unseals the key with
  the TPM2. When the PCRs differ it asks on the console; the recovery key
  works there.

## Root on LVM (VG tcaas)

Images that put the root file system on a logical volume of VG `tcaas`
(LVM-on-LUKS) are encrypted at the physical volume. Nothing else changes:
the same key slots, escrow, enrollment unit, PCR 7+14 binding, GRUB lock
and locked configuration keys apply.

- The encrypted partition is an LVM physical volume when its GPT type is
  Linux LVM (`E6D6D379-F507-44C2-A23C-238F2A3DF928`). With
  `DISK_ENCRYPTION_TARGET=root` BOOTy takes it when the image has no Linux
  filesystem or Discoverable Partitions root partition (x86-64, ARM64) and
  exactly one LVM partition; the root is then on
  LVM (`ROOT_PARTITION_LABEL` and `ROOT_PARTITION_NUMBER` must be unset).
  A partition number selects an LVM partition as a data VG: the root stays
  the selected root partition, even if an LV looks like a root. It must be
  the last partition, like a root partition.
- `stream-image` writes the physical volume as it is in the image (LVM
  label, metadata, all logical volumes) into the opened mapping, so no
  byte of it reaches the disk in plain text. Then, with LVM limited to the
  mapping (`--devices /dev/mapper/booty_crypt`, so a VG `tcaas` on another
  disk is not touched):
  1. `lvm pvs` checks that the volume belongs to VG `tcaas`; another VG
     fails the run;
  2. `lvm pvresize` grows it to the mapping. The logical volumes keep the
     sizes of the image; the space of the grown disk stays free in the VG;
  3. `lvm vgchange -ay tcaas` activates it (`lvm vgmknodes` creates the
     device nodes, the ramdisk has no udev), and BOOTy mounts each logical
     volume read-only to find the root (`/etc/os-release`, or `/etc` and
     `/usr`; only with `DISK_ENCRYPTION_TARGET=root`). A VG without a root
     fails the run.
- The root is that logical volume (`/dev/mapper/tcaas-<lv>`) for the
  later steps; `grow-partition` is skipped and `resize-filesystem` finds
  nothing to grow. `enable-lvm` is skipped: its unscoped `vgchange -ay`
  would also activate a VG `tcaas` of another disk.
- `prepare-disk-encryption` also needs `/usr/sbin/lvm` in the image
  (Ubuntu: `lvm2`), adds the dracut `lvm` module to
  `90-booty-luks.conf`, and appends to the kernel command line in
  `99-booty-luks.cfg`:
  `GRUB_CMDLINE_LINUX="$GRUB_CMDLINE_LINUX rd.luks.uuid=<luks uuid> rd.lvm.vg=tcaas"`.
  The initramfs opens `booty_crypt` from crypttab and activates VG
  `tcaas` on it. The kernel command line is in neither PCR 7 nor 14, so
  the binding is the same.
- `close-encrypted-disk` deactivates VG `tcaas` before it closes the
  mapping.
- The ramdisk needs `lvm`. `stream-image` checks it after it reads the
  partition table of the image, before it writes any of the image; this is
  not a non-destructive preflight, since `wipe-disks` has already cleared
  the target. `DISK_ENCRYPTION_ALLOW_FAILURE` does not cover it.

Without `DISK_ENCRYPTION`, or with a partition that is not an LVM physical
volume, BOOTy writes the same files and runs the same commands as before.

## Threat model

PCR 7+14 without a PIN protects **data at rest**: a disk that leaves the
machine (stolen, returned for repair, decommissioned without a wipe) opens
only with the recovery key.

It does **not** protect against someone with the whole server, its BMC or
its console:

- The TPM2 releases the key to any boot chain with the same PCR 7 and 14:
  every image signed by the keys in `db` and the shim vendor certificate.
  Such a person can boot another Canonical-signed kernel or shim chain and
  get the key.
- `/boot` is not encrypted, and neither the kernel command line (PCR 8),
  the kernel and initramfs files (PCR 9) nor `grub.cfg` are bound. Whoever
  can write `/boot` (for example from another signed boot medium) can put a
  modified initramfs or command line there; the TPM2 still unlocks, and the
  modified initramfs reads the decrypted root.
- The [GRUB lock](#grub-lock) only stops edits at the GRUB console.

Binding more PCRs (8, 9, 11, a UKI with `systemd-pcrlock`) or a TPM2 PIN
closes some of these paths at the price of more re-enrollment; that is
not part of this change.

### GRUB lock

With encryption on, BOOTy writes `/etc/grub.d/01_booty_luks_lock`, which
puts into `grub.cfg`:

```text
set superusers="booty-locked"
password_pbkdf2 booty-locked grub.pbkdf2.sha512.10000.<random salt>.<random hash>
set menuentry_id_option="--unrestricted $menuentry_id_option"
```

The salt and hash are random, so no password matches: nobody can use the
GRUB editor (`e`) or shell (`c`). grub-mkconfig puts
`$menuentry_id_option` on every menu entry and submenu, so all entries stay
bootable without a password, also after kernel updates. Recovery mode
entries are off (`GRUB_DISABLE_RECOVERY=true`): they open a root shell
after the TPM2 unlocked the root. To remove the lock, delete the file and
run `update-grub`.

### The enrollment-key window

Until phase 2 of the first Secure Boot boot the enrollment key is on the
disk in plain text: in the initramfs on the unencrypted `/boot`, and for a
data partition also in `/etc/booty/luks-enroll.key` on the unencrypted
root. Anyone who reads the disk in that window can open the volume. The
volume holds the freshly installed image then. After phase 2 the key slot
is wiped, so copies left in old initramfs blocks or flash cells open
nothing. A host that never boots with Secure Boot on keeps the key file:
the provisioner must turn Secure Boot on and confirm the enrollment (the
status callback, or `cryptsetup luksDump`: a `systemd-tpm2` token, no slot
1). `DISK_ENCRYPTION_SKIP_SECURE_BOOT_CHECK` allows hosts that cannot do
that; they stay in this window.

The status token is stored in the target until phase 2 ends: on the
encrypted root, or on the unencrypted root for a data partition. Use a
token that is valid for the time until the first Secure Boot boot and that
can do nothing else.

## Design decisions

**Format first.** BOOTy formats the partition and writes the image
partition into the mapping in the same pass as the rest of the image. The
encrypted partition never holds plain text, and only the image size is
written, not the grown partition. This needs:

- a raw GPT image (any compression); **qcow2 and MBR images are refused**
  (the qcow2 path converts in RAM with a different writer);
- the encrypted partition as the **last partition** of the image, so it can
  grow to the disk end before it is formatted (the mapping is 16 MiB
  smaller than the partition; the file system of the image must fit).
  Ubuntu cloud images put the root last.

There is no in-place (`cryptsetup reencrypt`) path for other images. They
fail before the first disk write, or provision without encryption with
`DISK_ENCRYPTION_ALLOW_FAILURE`.

Provisioning time in the KVM scenario (Ubuntu 26.04 cloud image, 6 GiB
disk, same runner type), from `report-init` to `report-success`: in place
(an earlier version of this change: write the image, then `cryptsetup
reencrypt` of the grown 5.4 GiB partition) 82.5 s, of which the reencrypt
took 25.8 s; format first 71.2 s, of which writing the image with the
format took 45.7 s; without encryption 34.0 s. The reencrypt time grows
with the disk size (about 15 h for 8 TB at 150 MB/s); format first does
not.

**Bind on the first Secure Boot boot, in two phases.** BOOTy runs with
Secure Boot off and, with
[module signing](https://github.com/telekom/BOOTy/pull/935), before the MOK
enrollment, so it sees other PCR values. Measured in the KVM scenario
(OVMF with the Microsoft keys):

| | PCR 7 | PCR 14 |
|---|---|---|
| Ubuntu 24.04, Secure Boot on | `C86235C7…` | `A46F893D…` |
| Ubuntu 24.04, Secure Boot off | `CDF4FCEF…` | `A46F893D…` |
| Ubuntu 26.04, Secure Boot on | `C86235C7…` | `24BD0348…` |

PCR 7 holds the Secure Boot state, PK, KEK, db, dbx and the authorities
that verified the boot images. When a MOK verifies a boot image, shim
records that MOK in PCR 7 too. PCR 14 adds what PCR 7 does not see: MOKs
that only sign modules (the case of per-host module signing), `MokListX`
changes and `MokSBState`. The cost: every MOK, `MokListX` or `MokSBState`
change needs a new binding (see [PCR changes](#pcr-changes)). The second
phase keeps the enrollment key until a boot proves the TPM2 path.

**Temporary key file, not the recovery key, for the first boots.** It keeps
provisioning unattended and the recovery key off consoles; its slot is
wiped once the TPM2 unlocked a boot (see the window above).

**Recovery key hand-off.** One JSON document to
`DISK_ENCRYPTION_RECOVERY_KEY_URL`:

```json
{"hostname":"node-1","device":"/dev/sda1","luksUUID":"…","keySlot":0,"recoveryKey":"cbdefghi-…"}
```

- bearer token, HTTPS with TLS verification (`CA_BUNDLE_FILE` roots apply,
  `SKIP_TLS_VERIFY` is refused), plain HTTP only on loopback;
  `INSECURE_TRANSPORT` does not apply;
- no redirect is followed; any status other than 2xx fails (three
  attempts);
- a standby command payload or deploy archive cannot change the URL, the
  status URL or any other encryption setting except the target partition;
- the key is never logged, never in an argument, report or error, and
  reaches `cryptsetup` on stdin; the enrollment key goes through a memfd;
- the body is built without `encoding/json` (whose buffer pool would keep a
  copy), and key and body are zeroed. Copies in the TLS stack and the pipe
  to `cryptsetup` cannot be zeroed from Go; BOOTy runs from RAM and the
  host reboots right after.

**The recovery key must be retrievable without the provisioner.** The
recovery key is the only way into the volume when the TPM2 refuses (PCR
change, TPM or mainboard replacement) and the emergency path at the console
when the management or provisioning cluster is down. BOOTy hands it over
once and keeps no copy. Storing it in an escrow that stays reachable when
the provisioner or its cluster is not (an external secret store, a
break-glass vault, an offline copy) and controlling who may read it is the
responsibility of the consumer that serves `DISK_ENCRYPTION_RECOVERY_KEY_URL`.

BOOTy does not escrow a LUKS header backup. A backup taken at provisioning
holds key slot 1, which the enrollment wipes later; with the enrollment key
from a leftover copy it would open the volume. Take one after phase 2 if
needed: `cryptsetup luksHeaderBackup`.

**Status callback.** With `DISK_ENCRYPTION_STATUS_URL` the unit posts
`{"hostname","luksUUID","status","message"}` with `curl --proto =https` and
the bearer token of the provisioning run (`tpm2-bound`, `slot1-removed`,
`failed`). It is best effort: a lost status does not stop the enrollment.

**Fail closed.** Errors in `stream-image` (`BOOTY-DSK-028`, not retried),
`prepare-disk-encryption` (`BOOTY-DSK-026`) and `close-encrypted-disk`
(`BOOTY-DSK-027`) fail provisioning. `DISK_ENCRYPTION_ALLOW_FAILURE` only
covers the checks before the first disk write.

**Interrupted runs.** The keys exist only in the memory of the run. A
resumed run (`BOOTY_RESUME`) or a retry after the format fails at
`parse-partitions` or at the LUKS check and needs a new provisioning. No
resumed step writes to the encrypted partition before that check.

## Crypto-erase at deprovision

On by default; `DEPROVISION_CRYPTO_ERASE=false` turns it off. Without a
LUKS2 header on the disks nothing changes: BOOTy reads the headers itself
and runs no tool.

`deprovision`/`hard` and `decommission` run the step `crypto-erase` right
before the wipe (after `stop-raid`, before `wipe-disks`; before
`decommission-wipe`). In hard deprovisioning it runs the config checks of
`wipe-disks` first, so a run that would fail before its wipe erases
nothing, and it skips when `wipe-disks` keeps the disks (`IMAGE_MODE=ab`
with `AB_PRESERVE_EXISTING=true`). `decommission` always wipes and does not
look at the provisioning layout. `soft-deprovision` wipes nothing and does not run it.

1. **Which devices.** The disks that the wipe after it clears: the
   deprovision device (`DEPROVISION_DEVICE` or `DISK_DEVICE`) and its mirror
   companion, or every disk that the wipe of all disks takes (no loop, md,
   dm, ram or optical devices; disks in use are skipped like in the wipe; a
   named disk in use fails, like its wipe). On each, the whole disk and
   every partition (from sysfs) are checked for a LUKS2 header: the primary
   copy at 0 (`LUKS\xba\xbe`, version 2) or a secondary copy at 16 KiB to
   4 MiB (`SKUL\xba\xbe`) whose `hdr_offset` names that offset, so a header
   whose primary copy was lost is found too, also behind a read error at
   another copy. A secondary copy at its
   `hdr_offset` with another version or an `hdr_size` that does not match
   is damaged, not foreign: it fails the run, also when a later read of
   the device fails. Every LUKS2 header there counts as
   BOOTy-managed: the LUKS2 format has no field that tells the header of
   #939 from another one (random UUID, no label), and the disks are wiped
   next anyway, so the step never writes to a disk that the run would leave
   alone. LUKS2 inside an md array or an LVM volume is not looked at (BOOTy
   does not create it); LUKS1 is not touched by this step.
2. **Erase.** For each header, `cryptsetup luksErase --batch-mode <dev>`
   destroys every keyslot; then `dd` writes zeros over the header area as
   the header declares it: both copies (2 × `hdr_size`) and the keyslots
   area (`config.keyslots_size`), at most to the end of the device. The data
   area is not overwritten here (the wipe and, with `WIPE_DEPTH=thorough`,
   `blkdiscard` follow): without a key it is noise.
3. **Check.** BOOTy drops the cached blocks of the device and reads it
   again; a header copy that is still there fails the run. A header that
   is gone before its erase (a second path to the same disk, such as a
   dual-ported SAS disk without dm-multipath) counts as erased.
4. **Report.** The success status names each device and its LUKS UUID:
   `deprovisioning complete handoff=… ; crypto-erased: /dev/sda2
   luksUUID=…`. The header comes from the disk, so a UUID field that is
   not a UUID is reported as `luksUUID=invalid`. A device that cannot be read (no media, I/O error) is
   skipped and named (`; crypto-erase could not read: /dev/sdb`); BOOTy
   cannot erase it, and the wipe after it handles it as before. With
   `DEPROVISION_CRYPTO_ERASE=false` the status says `; crypto-erase off`.

**Fail closed.** A damaged header (the magic is there, the header does not
parse), a failed `luksErase`, `dd` or check fails the run with
`BOOTY-DSK-029` before the wipe, also in `decommission`, which otherwise
does not stop on disk errors. A missing `cryptsetup` in the ramdisk fails
too when a LUKS2 header is found. Set `DEPROVISION_CRYPTO_ERASE=false` to
deprovision such a host with the wipe only.

**Escrow.** `DISK_ENCRYPTION_RECOVERY_KEY_URL` has one call: it receives a
new recovery key. There is no call that marks a key invalid, so BOOTy sends
nothing to it at deprovision and does not invent one. After a successful
crypto-erase the escrowed key opens nothing; the consumer that serves the
URL can drop it by the `luksUUID` in the deprovision status, which is the
`luksUUID` of the escrow body. Match it together with the host: whoever
used the disk can write any UUID into its header.

**Not in standby payloads.** A standby command payload cannot change
`DEPROVISION_CRYPTO_ERASE` (the payload is refused), and a deploy archive
gets the value of the vars file back, like the `DISK_ENCRYPTION*` keys
except the target. Turning it off would leave the keyslots of a disk that
leaves the host to whoever holds the recovery key, a silent downgrade of
the same kind as `DISK_ENCRYPTION_ALLOW_FAILURE`. To turn it off for a
standby host, change the vars file and boot the ramdisk again.

**Logs.** `INFO`: `crypto-erasing LUKS2 device` (device, `luksUUID`,
`headerBytes`), `LUKS2 keyslots and header destroyed` and `LUKS2 header
already gone`. `WARN`:
`crypto-erase is off (DEPROVISION_CRYPTO_ERASE=false)…`, `cannot read the
device to look for a LUKS2 header`, `cannot list the partitions to look for
LUKS2 headers`. `DEBUG`: `no LUKS2 header on the disks to wipe`. A failure
is the step error (`ERROR deprovisioning step failed step=crypto-erase`).
No key is involved, so nothing is redacted.

## PCR changes

These change PCR 7 or 14, so the TPM2 refuses and the recovery key is
needed, unless you bind again before:

- `db`, `dbx` or `KEK` updates (firmware updates, `fwupd` or
  `secureboot-db` dbx updates, the Microsoft UEFI CA 2023 rollover and the
  revocation of the 2011 CA);
- shim updates that change the vendor `dbx` (`MokListX`/`MokListXRT`) or
  the SBAT level;
- MOK enrollment, rotation or deletion (also a new module signing key),
  `MokSBState` changes;
- turning Secure Boot off.

Before a planned change, bind to the PCR that stays, update, reboot, and
bind to 7+14 again:

```sh
# before a db/dbx/KEK or shim change: keep PCR 14 only for one boot
systemd-cryptenroll --unlock-tpm2-device=auto --wipe-slot=tpm2 --tpm2-device=auto --tpm2-pcrs=14 /dev/disk/by-uuid/<luks uuid>
# (before a MOK change use --tpm2-pcrs=7)
# apply the change, reboot, then:
systemd-cryptenroll --unlock-tpm2-device=auto --wipe-slot=tpm2 --tpm2-device=auto --tpm2-pcrs=7+14 /dev/disk/by-uuid/<luks uuid>
```

Without that step the recovery key unlocks at the console, and the same
`systemd-cryptenroll` command (it asks for the recovery key) binds again.
`systemd-pcrlock` can predict PCR 7 from the variables and sign policies
for future values; it is the way to avoid this window and a possible
follow-up.

## Requirements

- A TPM2 and UEFI Secure Boot in user mode.
- A whole-disk raw GPT image; the encrypted partition is the last one and
  not the ESP. A root on LVM: the physical volume of VG `tcaas`, with
  `lvm2` in the image and `lvm` in the ramdisk. Not with disk mirroring, RAID, `PARTITION_LAYOUT`, A/B,
  `SKIP_TLS_VERIFY` or `CAPRF_LEGACY_COMPAT` (its preset turns on
  `SKIP_TLS_VERIFY`).
- The root needs a separate `/boot` partition; a data partition must be
  ext4.
- The image has `dracut`, `lsinitrd`, `systemd-cryptsetup`,
  `systemd-cryptenroll`, `cryptsetup`, `shred`, `libtss2-esys.so.0`,
  `libtss2-rc.so.0`, `libtss2-mu.so.0`, `libtss2-tcti-device.so.0` and
  `libcryptsetup-token-systemd-tpm2.so`; `curl` with a status URL.
  - Ubuntu 26.04 cloud image: add `libtss2-rc0t64`.
  - Ubuntu 24.04 cloud image: add `dracut`, `systemd-cryptsetup` and
    `libtss2-rc0t64`; `cryptsetup-initramfs` cannot unlock with the TPM2.
- The ramdisk has `cryptsetup`, `partprobe`, `growpart`, `e2fsck`,
  `resize2fs` and `dm_crypt`.

## Module signing

With [per-host module signing](https://github.com/telekom/BOOTy/pull/935)
and `MODULE_SIGNING_ENROLLMENT=efi-helper` the MOK is staged during
provisioning and enrolled by `mok-enroll.efi` on the next firmware boot,
with Secure Boot still off, so the unit waits. The first Secure Boot boot
sees the final `MokList` in PCR 14 and binds.

With `MODULE_SIGNING_ENROLLMENT=mokutil` the certificate waits in `MokNew`
for a confirmation in MokManager, possibly on a boot with Secure Boot on.
Phase 1 does not bind while a MOK request (`MokNew`, `MokDel`, `MokXNew`,
`MokXDel`, `MokSB`, `MokDB`) is pending: shim would change PCR 14 on a
later boot, and the initramfs without the enrollment key would ask for the
recovery key. Without this enrollment method the unit does not look at MOK
requests. A host whose request nobody confirms stays in the
[enrollment-key window](#the-enrollment-key-window). With
`MODULE_SIGNING_ENROLLMENT=external`, enroll the certificate (a `db` update
changes PCR 7) before the first Secure Boot boot, or bind again as in
[PCR changes](#pcr-changes).

## Test

KVM scenarios (`.github/workflows/kvm-matrix.yml`):

- `disk-encryption-ubuntu-26.04` (`TestDiskEncryptionTPM2VM`): Ubuntu 26.04
  cloud image with `libtss2-rc0t64`, `OVMF_CODE_4M.secboot.fd` with the
  Microsoft keys, one swtpm state for all boots, an HTTPS test server for
  the recovery key and the status:
  1. provision with Secure Boot off; the recovery key arrives;
  2. Secure Boot off: the enrollment key unlocks, the unit waits;
  3. Secure Boot on (phase 1): bound to PCR 7+14, slot 1 and the key file
     remain, the initramfs has no key, status `tpm2-bound`;
  4. Secure Boot on (phase 2): no prompt; slot 1 and the key file are gone,
     the unit is disabled, status `slot1-removed`; GRUB is locked and has no
     recovery entries;
  5. the enrollment key, read from the volume after provisioning, opens
     nothing any more; its copies left on the disk are counted;
  6. Secure Boot off: PCR 7 differs, the TPM2 refuses, the recovery key
     typed at the prompt unlocks;
  7. the recovery key is in none of the serial console logs.
- `TestDiskEncryptionTPM2DataPartitionVM` (same scenario): the image gets a
  last ext4 partition 2 mounted at `/data` by PARTUUID; with
  `DISK_ENCRYPTION_TARGET=2` the root stays plain, `/data` mounts through
  the mapping (fstab rewritten), its file system has grown to the disk end
  inside LUKS, and the enrollment key unlocks it.
- `disk-encryption-lvm-ubuntu-26.04` (`TestDiskEncryptionLVMTPM2VM`): the
  same cloud image with the root moved into LV `root` of VG `tcaas` on
  partition 1 (Linux LVM type). BOOTy formats partition 1, writes the
  physical volume into the mapping and finds the root LV; the escrowed
  recovery key opens the volume; no LVM label is on the disk in plain text.
  The installed system boots from the LV (`rd.luks.uuid`,
  `rd.lvm.vg=tcaas`) with the enrollment key, binds to PCR 7+14 with Secure
  Boot on (phase 1) and unlocks with the TPM2 without a prompt (phase 2),
  slot 1 gone.
- `disk-encryption-crypto-erase-ubuntu-26.04`
  (`TestDiskEncryptionCryptoEraseVM`): provision with encryption, take a
  header backup, then `MODE=deprovision` with `WIPE_DEPTH=quick` (which
  leaves partition contents alone, so only the crypto-erase can remove the
  header). The success status names the device and the escrowed
  `luksUUID`. At the old partition start both header copies and the
  keyslots area are zeros, the data area is not, `cryptsetup isLuks` and
  `luksDump` find nothing; the old metadata without the keyslots area
  written back is a valid header again, but the recovery key opens no
  keyslot (with the full backup, the control, it opens).
- `disk-encryption-off-ubuntu-26.04` (`TestDiskEncryptionOffVM`): the same
  image without the keys: no encryption step runs, the partition table is
  the image's with the root grown, the root is plain ext4, and no BOOTy
  encryption file is in the target.

Run them locally on an x86-64 Linux host with KVM, as root:

```sh
sudo apt-get install -y qemu-system-x86 ovmf swtpm cryptsetup-bin lvm2 python3-virt-firmware
curl -LO https://cloud-images.ubuntu.com/releases/resolute/release-20260927/ubuntu-26.04-server-cloudimg-amd64.img
curl -LO https://launchpad.net/ubuntu/+archive/primary/+files/libtss2-rc0t64_4.1.3-6_amd64.deb
sudo env MIRROR_OS_IMAGE=$PWD/ubuntu-26.04-server-cloudimg-amd64.img \
  DISK_ENCRYPTION_DEBS=$PWD/libtss2-rc0t64_4.1.3-6_amd64.deb \
  go test -tags e2e -run '^TestDiskEncryption' -v -timeout 90m ./test/e2e/kvm/
```
