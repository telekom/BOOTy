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

`DISK_ENCRYPTION=luks2-tpm2-uki` boots a signed unified kernel image (UKI)
instead of GRUB and binds the volume to PCR 7 and a signed PCR 11 policy;
see [Signed UKI and PCR 11](#signed-uki-and-pcr-11). The sections up to
there describe `luks2-tpm2`; the format-first write, the recovery key
hand-off and the requirements of the partition are the same for both modes.

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
   - `/etc/systemd/system/systemd-cryptsetup@booty_crypt.service.d/90-booty-tpm2-unlock.conf`,
     also in the initramfs: there it first opens the volume with the TPM2
     alone (`systemd-cryptsetup attach … tpm2-device=auto,headless=true`, three
     tries as systemd 255 does not retry a raced unseal, no
     prompt) and on success leaves `/run/booty/luks-tpm2-initrd-unlock`,
     which phase 2 needs. If the TPM2 fails, `systemd-cryptsetup` asks for a
     passphrase as before; in the installed system it does nothing;
   - `/etc/dracut.conf.d/91-booty-luks-enroll-key.conf`: the enrollment key
     in the initramfs;
   - `/etc/default/grub.d/99-booty-luks.cfg`: `GRUB_FORCE_PARTUUID=` (no
     initramfs-less boot) and `GRUB_DISABLE_RECOVERY=true`;
   - `/etc/grub.d/01_booty_luks_lock`: the [GRUB lock](#grub-lock);
   - the unit `booty-luks-tpm2-enroll.service` (enabled), its script
     `/usr/local/sbin/booty-luks-tpm2-enroll` and
     `/etc/booty/luks-tpm2.env`; with a status URL also
     `/etc/booty/luks-status.auth` (0600, the bearer token of
     `DISK_ENCRYPTION_STATUS_TOKEN`) and the
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
     plugin, `systemd-cryptsetup`, crypttab, the drop-in present), then
     swaps them in;
  4. marks the volume bound and posts `tpm2-bound`.

  Until step 4 an EXIT trap restores the enrollment-key crypttab line and
  dracut file and removes half-built images; the next boot tries again.
  Slot 1 and the key stay.
- Phase 2, the next boot with Secure Boot on: first the initramfs must
  have opened the root with the TPM2 on this boot
  (`/run/booty/luks-tpm2-initrd-unlock`, #963). A boot that a passphrase or
  the recovery key opened (an initramfs that cannot use the TPM2, for
  example without a library or with initramfs-tools) keeps slot 1 and the
  key, posts `tpm2-initrd-unlock-failed`, fails the unit and tries again on
  the next boot: `cryptsetup --token-only` from the booted system proves
  only that the TPM2 unseals now, not that the initramfs can use it.
  `headless` would let any token that needs no prompt open the volume, so
  a LUKS2 token other than `systemd-tpm2` fails the check (`failed`).
  Phase 1 removes a marker of its own boot, so a restart of the unit or a
  soft-reboot (both keep `/run`) cannot pass it on.
  `DISK_ENCRYPTION_SKIP_INITRD_UNLOCK_CHECK=true` skips both checks
  (transitional escape hatch, will be removed). Then the initramfs of this
  boot has no key, and the TPM2 opens the volume with the PCRs of this
  boot. Only then the script wipes key slot 1 (after
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
closes some of these paths at the price of more re-enrollment.
`luks2-tpm2-uki` closes the `/boot` and command line paths with a signed
UKI and a signed PCR 11 policy (see [UKI threat model](#uki-threat-model)).

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
  status URL, the status token or any other encryption setting except the
  target partition;
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
the bearer token `DISK_ENCRYPTION_STATUS_TOKEN` (`tpm2-bound`,
`slot1-removed`,
`tpm2-initrd-unlock-failed`, `failed`). It is best effort: a lost status does not stop
the enrollment. The token stays in `/etc/booty/luks-status.auth` of the
installed system until phase 2 has posted `slot1-removed` and removes it,
so it must stay valid until then: the second boot with Secure Boot on, or
later when the enrollment waits (MOK request, Secure Boot off) or is
retried. **Give it a token that can post the
status of this host only** and nothing else: not the provisioning token,
which would then have to live as long and could do everything a
provisioning run can. The token is required with the status URL, a single
header value without spaces or control characters, and a standby payload
or deploy archive cannot change it.
`DISK_ENCRYPTION_STATUS_REUSE_PROVISIONING_TOKEN=true` sends the bearer
token of the provisioning run instead (the behaviour before the status
token); it is a transitional escape hatch for providers that cannot issue
a status-only token yet and will be removed.

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
  `libtss2-sys.so.1`, `libtss2-rc.so.0`, `libtss2-mu.so.0`, `libtss2-tcti-device.so.0` and
  `libcryptsetup-token-systemd-tpm2.so`; `curl` with a status URL.
  - Ubuntu 26.04 cloud image: add `libtss2-rc0t64`.
  - Ubuntu 24.04 cloud image: replace `initramfs-tools` with `dracut`
    (060; it conflicts with `initramfs-tools`) and add `libtss2-rc0t64`.
    The `systemd` package of 24.04 already has `systemd-cryptsetup`,
    `systemd-cryptenroll` and the token plugin. dracut 060 has no
    `systemd-cryptsetup` module; BOOTy then adds only `crypt`, which
    installs it. dracut 060 does not add the link dependency
    `libtss2-sys.so.1` of `libtss2-esys` either; BOOTy lists it, as without
    it systemd-cryptsetup reports `Could not find TPM2 device: Operation
    not supported` and asks for the passphrase. The stock `cryptsetup-initramfs` (initramfs-tools) cannot
    unlock with the TPM2: with the volume bound, it stops at the passphrase
    prompt. KVM scenario `disk-encryption-ubuntu-24.04` (see [Test](#test)).
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
  4. Secure Boot on (phase 2): no prompt, the initramfs opened the root
     with the TPM2 (marker); slot 1 and the key file are gone, the unit is
     disabled, status `slot1-removed`; GRUB is locked and has no recovery
     entries;
  5. the enrollment key, read from the volume after provisioning, opens
     nothing any more; its copies left on the disk are counted;
  6. Secure Boot off: PCR 7 differs, the TPM2 refuses, the recovery key
     typed at the prompt unlocks; no marker;
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
- `disk-encryption-ubuntu-24.04` (`TestDiskEncryptionUbuntu2404VM`): the
  stock Ubuntu 24.04 cloud image with `initramfs-tools` replaced by
  `dracut` 060 and `libtss2-rc0t64`:
  1. provision with Secure Boot off;
  2. Secure Boot on (phase 1, dracut): bound to PCR 7+14, status
     `tpm2-bound`; then the installed system replaces dracut with
     `initramfs-tools` (the stock `cryptsetup-initramfs` stays) and builds
     the initramfs with `mkinitramfs`, which has the root in
     `cryptroot/crypttab`;
  3. same Secure Boot state, PCRs and TPM2 token, initramfs-tools (phase
     2): the boot stops at the `cryptsetup-initramfs` passphrase prompt
     (the expected failure); the recovery key opens the volume there.
     Phase 2 keeps slot 1 and the key file although the TPM2 unseals from
     the booted system, status `tpm2-initrd-unlock-failed` (#963); then the
     installed system goes back to dracut;
  4. phase 2 (dracut): the TPM2 unlocks without a prompt, slot 1 and the
     key file are gone, status `slot1-removed`.
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

## Signed UKI and PCR 11

With `luks2-tpm2` the initramfs and the kernel command line live on the
unencrypted `/boot` and are not measured into PCR 7 or 14: whoever can write
`/boot` (or type at the GRUB prompt) can boot the right kernel with
`init=/bin/sh` or a changed initramfs, and the TPM2 still unseals the disk.
`DISK_ENCRYPTION=luks2-tpm2-uki` closes that gap:

- the installed system boots one UKI (kernel, initramfs, command line,
  os-release in one PE file) straight from a firmware boot entry;
- the UKI is signed for Secure Boot, so the firmware runs no other code;
  systemd-stub ignores a command line from the boot loader with Secure Boot
  on;
- systemd-stub measures the UKI sections into PCR 11. The volume is bound to
  a *signed* PCR 11 policy (`systemd-cryptenroll --tpm2-public-key`
  `--tpm2-public-key-pcrs=11`): the TPM2 unseals for any UKI whose expected
  PCR 11 value is signed with the PCR policy key, and for nothing else.
  Changing one byte of the initramfs or the command line changes PCR 11 and
  the TPM2 refuses;
- PCR 7 (Secure Boot state, `db`, and the `db` entry that verified the UKI)
  is bound next to it, because anything that runs before the UKI could
  extend PCR 11 to the expected value. PCR 14 is not bound: the firmware
  boots the UKI directly, without shim, so `MokList` is not measured.

### Who holds the keys

Two keys matter: the PCR policy key (signs the expected PCR 11 values into
the `.pcrsig` section) and the Secure Boot key (signs the UKI). The
TPM2 binding holds only the public half of the PCR policy key, so whoever
holds the private half decides which UKIs unlock the disk.
`SECUREBOOT_SIGNING` selects the holder:

| | `per-host` | `ci` |
|---|---|---|
| Image | unsigned UKI | UKI signed for Secure Boot, with `.pcrsig` and `.pcrpkey` |
| PCR policy key | made by BOOTy for this host, used once, wiped | held by the image pipeline |
| Secure Boot key | made by BOOTy for this host, used once, wiped; the certificate goes to the provisioner, which enrolls it in `db` | held by the image pipeline; its certificate is in `db` (provisioner, Redfish) |
| Private keys in BOOTy | yes, in memory during `prepare-disk-encryption` | none |
| Kernel or initramfs update | reprovision | the pipeline signs a new UKI with the same keys; no enrollment |
| Blast radius of a leaked key | one host | every host with this key |

`per-host` keeps no long-lived key anywhere: an attacker needs the keys of
the very host, and they are gone after provisioning. The price is that
nobody can sign a new UKI for the host: kernel and initramfs updates come as
a new image that BOOTy provisions again. In-place or A/B updates without
reprovisioning would need the keys again and are not supported in this mode.

`ci` is the model for fleets that update in place: the pipeline that builds
the image holds both keys (in an HSM or a KMS) and signs every UKI. The keys
are long-lived and shared by all hosts of the image, so they need the
protection of a release signing key; revoking a signed UKI needs PCR 11
version policies (see [UKI threat model](#uki-threat-model)). A third option, a
provisioner-held key that signs on request, is the same as `ci` from the
host's view: BOOTy enrolls the public key and never sees the private one.

With `per-host` BOOTy

- generates two RSA-2048 keys with `crypto/rand`;
- signs in its own process (`pkg/provision/uki_sign.go`): the expected PCR
  11 values as `systemd-measure sign` does, the `.pcrpkey` and `.pcrsig`
  sections as `ukify` adds them, the Authenticode signature as `sbsign`
  makes it. No program runs while the private keys exist, in particular no
  binary of the image, which could copy a key and sign any UKI for this
  host with it (the reason why #935 stopped running the image's `mokutil`
  with `MOK_PASSWORD`). The keys never reach a file, an argument, a log or
  the report;
- zeroes the key numbers after use (best effort, like the module signing
  key of #935: copies in freed Go memory cannot be zeroed; BOOTy runs from
  RAM and the host reboots).

The per-host certificate is in the provisioning report
(`secureBoot.uki.cert`, DER, base64, with `certSHA256`) and on the ESP at
`/EFI/BOOTy/uki-signing.crt` (PEM). The provisioner enrolls it in `db`
(for example over Redfish) before it turns Secure Boot on.

### Image contract

The UKI image flavour (built by the image pipeline; existing images are
unchanged):

- exactly one UKI at `/EFI/Linux/<os-id>-<kernel release>.efi` on the ESP
  (UAPI Boot Loader Specification, type #2). BOOTy refuses zero or more than
  one `.efi` file there. With `per-host` it replaces the file with the
  signed one (see [Atomic install](#atomic-install)); with `ci` it does not
  change it;
- with `per-host`, an ESP of at least twice the UKI size plus margin: BOOTy
  needs free space for a second copy of the UKI plus 4 MiB. The UKI image
  flavour ships a 512 MiB ESP (partition 15, label `UEFI`), the `/boot`
  partition after it as XBOOTLDR and the root last, and guarantees the UKI
  size plus 32 MiB free. The 106 MB ESP of the Ubuntu cloud images is too
  small for an 86 MB UKI (`UKI_INPLACE_WRITE=true` is the escape hatch);
- the OCI manifest of the image carries the annotation
  `com.github.telekom.booty.uki.sha256` with the hex SHA-256 of that file
  as shipped. The cosign signature of the image covers the manifest, so the
  digest is signed. BOOTy needs a cosign-verified `oci://` image
  (`IMAGE_COSIGN_PUBKEYS`); with an HTTP image, no annotation or another
  digest, `prepare-disk-encryption` fails;
- the initramfs in the UKI unlocks the root by its GPT `PARTUUID`, which the
  image fixes and BOOTy keeps:

  ```
  booty_crypt PARTUUID=<root PARTUUID> none luks,x-initrd.attach,tpm2-device=auto,try-empty-password=yes
  ```

  and holds the dracut modules `crypt`, `systemd-cryptsetup` and
  `systemd-pcrextend` (which extends the `enter-initrd` phase into PCR 11
  before the unlock), the TPM2
  libraries and the libcryptsetup token plugin (see
  [Requirements](#requirements); `hostonly="no"`, without `tpm2-tss`). The
  command line names the root file system (`root=UUID=<ext4 UUID>`), which
  is the same inside the LUKS volume;
- with `ci`: the UKI is signed for Secure Boot and has `.pcrsig` and
  `.pcrpkey` (`ukify build --pcr-private-key=... --secureboot-private-key=...`).
  The `.pcrsig` must cover the phases of the booted system too (the ukify
  default does): the first-boot unit checks the policy against the PCR 11
  of the running system before it enrolls;
- the UKI has a non-empty `.cmdline`: without one, systemd-stub takes the
  command line of the boot entry even with Secure Boot on and measures it
  into PCR 12, which the volume is not bound to;
- with `per-host`: the UKI is unsigned, as `ukify build` writes it, has no
  `.pcrsig` or `.pcrpkey`, and no `.profile`, `.dtbauto`, `.hwids` or
  `.efifw`; its headers have room for two more sections (the stub's do);
- out-of-tree modules in the UKI initramfs are signed by the image
  pipeline: `sign-kernel-modules` (`per-host`) signs the modules of the
  root file system only, after the UKI is signed;
- GRUB and its EFI loader stay in the image: `configure-grub` and
  `create-efi-boot-entry` run as for other images, and the GRUB entry stays
  in `BootOrder` after `booty-uki`;
- `systemd-cryptenroll`, `cryptsetup` and the TPM2 libraries.

`DISK_ENCRYPTION_PCR_PUBLIC_KEY_FILE` pins the PCR policy key with `ci`, as
`IMAGE_COSIGN_PUBKEYS` pins the image key: the `.pcrpkey` of the UKI must be
the key in that file.

### Flow

Provisioning (BOOTy, Secure Boot off):

1. `verify-image` verifies the cosign signature of the image and reads the
   UKI digest annotation; without a hex SHA-256 there (or with an HTTP
   image) it fails before `wipe-disks` (with `DISK_ENCRYPTION_ALLOW_FAILURE`
   the `stream-image` preflight installs without encryption instead). It
   also reads `DISK_ENCRYPTION_PCR_PUBLIC_KEY_FILE` then, so a later
   `/deploy` swap cannot remove the pin.
2. `stream-image` checks as for `luks2-tpm2` and that the digest is there
   (before the first disk write, so `DISK_ENCRYPTION_ALLOW_FAILURE`
   applies), then formats and writes as for `luks2-tpm2`, with the empty
   passphrase in key slot 1 instead of an enrollment key.
3. `prepare-disk-encryption` checks the tools and the UKI: exactly one file, its SHA-256 equals the annotation,
   it is a PE with a `.linux` section, not a multi-profile UKI.
   - `per-host`: refuses a signed UKI or one with `.pcrsig` or `.pcrpkey`;
     checks the free space of the ESP and the
     [per-host kernel parameters](#per-host-kernel-parameters), both before
     any key exists; makes the keys; sets the `.cmdline` to the one of the
     image with the per-host kernel parameters appended; signs the
     expected PCR 11 values of the four default boot phases (SHA-256 bank)
     of the UKI with that `.cmdline` and the per-host `.pcrpkey`, adds
     `.pcrpkey` and `.pcrsig`, signs the UKI for Secure Boot, checks the
     result (signature, the new sections, the others unchanged), installs
     it over the UKI atomically and wipes the keys.
   - `ci`: needs a Secure Boot signature, `.pcrsig` and `.pcrpkey`; checks
     the pinned key, and that `.pcrsig` holds the policy of the
     `enter-initrd` phase of this UKI signed by its `.pcrpkey` (a `.pcrsig`
     for the userspace phases only would pass the first-boot test unseal
     and then lock the initramfs out); makes no key.
   - writes the PCR public key and signature to
     `/etc/booty/tpm2-pcr-public-key.pem` and `/etc/booty/tpm2-pcr-signature.json`,
     and the first-boot unit `booty-luks-tpm2-enroll.service`. crypttab,
     dracut and GRUB are not touched; the initramfs is the one in the UKI.
   The disk is encrypted then, so a failed check fails the run. The root
   needs no separate `/boot`.
4. `create-uki-boot-entry` (after `create-mirror-efi-boot-entry`) checks
   that the UKI on the ESP is still the one of step 3 (image code ran in
   the chroot since), replaces earlier `booty-uki` entries and adds the
   firmware boot entry `booty-uki` for `\EFI\Linux\<name>.efi`, first in
   `BootOrder`. Other entries stay; the GRUB entry comes second.

Installed system:

- Until the enrollment: no TPM2 token; the initramfs opens the volume with
  the empty passphrase (`try-empty-password`).
- First boot with Secure Boot on in user mode: the unit runs
  `systemd-cryptenroll --unlock-key-file=/dev/null --tpm2-device=auto
  --tpm2-public-key=... --tpm2-public-key-pcrs=11 --tpm2-signature=...
  --tpm2-pcrs=7`. With `--tpm2-signature` systemd-cryptenroll checks that the
  new policy unseals with the current PCR values before it writes the slot.
  Then the unit wipes key slot 1 and disables itself. A run that stopped
  after the wipe finishes on the next boot without the empty passphrase,
  once it sees the TPM2 token. One phase is enough:
  unlike `luks2-tpm2` nothing in the boot chain changes (the UKI and its
  initramfs ran this boot), and the slot is proven before it is written.
- Later boots: systemd-stub passes `.pcrsig` to the initramfs, where
  systemd-cryptsetup unseals with PCR 7 and the signed PCR 11. A changed
  UKI, another PCR 11 key, or a changed Secure Boot state ends at the
  passphrase prompt; the recovery key works there.

The empty passphrase has the exposure of the enrollment key of `luks2-tpm2`
(a key on the unencrypted `/boot`): until the first Secure Boot boot,
anyone with the disk opens the volume. The provisioner turns Secure Boot on
and checks the result (`cryptsetup luksDump`: one `systemd-tpm2` token with
`tpm2-pubkey-pcrs: 11` and `tpm2-hash-pcrs: 7`, no key slot 1).

### Per-host kernel parameters

With `per-host` BOOTy builds the PCR 11 policy itself, so the per-host
kernel parameters go into the signed UKI: the `.cmdline` becomes
`<.cmdline of the image> <parameters>`, and the PCR 11 policy and the
Secure Boot signature cover it like the rest of the UKI. The parameters are
the ones that `configure-grub` writes to the GRUB drop-in
`10-caprf-kernel-params.cfg`, so both boot entries get the same:
`MACHINE_EXTRA_KERNEL_PARAMS` and `EXTRA_KERNEL_PARAMS`, the resolved
serial console (`console=`, `MACHINE_SERIAL_CONSOLE`), and the cloud-init
datasource (`ds=nocloud`, `ci.datasource=ConfigDrive`); with
`GRUB_CMDLINE_MODE=append` the `ds=nocloud console=...` of that mode.
Without parameters the `.cmdline` of the image stays as it is.

The parameters must be printable ASCII without quotes or backslashes (the
kernel and systemd strip quotes, so a quoted name would pass the deny
list), and the `.cmdline` at most 2047
bytes (`COMMAND_LINE_SIZE` of x86-64). Parameters that replace init, stop
the boot in a shell, debug it or start other units are refused, as the PCR
11 signature would bless them for every boot: `init`, `rdinit`, `single`,
`s`, `S`, `1`, `emergency`, `rescue`, `rd.break`, `rd.shell`,
`rd.emergency`, `rd.rescue`, `rd.debug`, `rd.udev.debug`,
`rd.systemd.debug_shell`, `rd.systemd.unit`, `rd.systemd.run`,
`rd.systemd.break`, `rd.systemd.wants`, `systemd.debug_shell`,
`systemd.unit`, `systemd.run`, `systemd.break`, `systemd.wants`,
`systemd.mask`, `rd.systemd.mask` (they could mask the TPM2 enrollment and
keep the empty passphrase), `systemd.set_credential`,
`systemd.set_credential_binary`, `systemd.import_credentials` (a unit
drop-in credential can start a shell), `systemd.volatile` (a volatile
`/etc` hides the enrollment unit),
`systemd.run_success_action`, `systemd.run_failure_action`,
`systemd.confirm_spawn`, `systemd.log_level`, `systemd.crash_shell`,
`systemd.mount-extra` (a mount over `/etc` starts any unit), `rd.cmdline`
(`rd.cmdline=ask` reads more parameters from the console, unmeasured),
`root`, `mount.usr`, `roothash`, `usrhash` (after the initramfs unsealed the
volume, another root in a signed boot phase could unseal it again),
`kgdboc`, `kgdboc.kgdboc`, `kgdbwait`, `kgdbcon`, `ekgdboc` and `kgdbdbgp`
(with or without a value; `-` and `_` are the same, as systemd reads them,
and an `rd.` prefix does not count, as systemd reads every `systemd.*`
setting as `rd.systemd.*` in the initramfs: `rd.debug` refuses `debug`
too), and every word that starts with `-` (`-b`, and
`--`, after which the kernel hands the rest to init as arguments such as
`--unit=`). There is no override; such a parameter belongs in
the image `.cmdline`, where the image pipeline decides. The refusal fails
`prepare-disk-encryption` before any key exists.

A standby payload or deploy archive can not change these settings in the
UKI mode: it would change the measured command line of the signed UKI.

BOOTy rewrites the `.cmdline` section in its PE code: the section keeps its
address (a `.cmdline` of up to 2 KiB fits the 4 KiB section alignment of
systemd-stub), the data of the later sections moves in the file, and
BOOTy refuses the UKI when a data directory points into a section that
moves, or when a debug directory entry has its data (a file offset) in one.

`UKI_KEEP_IMAGE_CMDLINE=true` signs the image `.cmdline` unchanged, the
behavior before, as a transitional escape hatch.

With `ci` BOOTy holds no PCR policy key and can not change the signed
`.cmdline`. The per-host kernel parameters reach the GRUB fallback entry
only, and BOOTy logs a warning that names them; the run goes on, as a
`ci` host always has some (the resolved serial console). Per-host
parameters that the UKI boot needs go into the image `.cmdline`.

### Atomic install

With `per-host` BOOTy writes the signed UKI to `.<name>.efi.booty-tmp`
next to it (no UKI loader picks up that name), syncs it, renames it over
the UKI and syncs the directory. An interrupted install leaves the image
UKI or the signed one; the next `prepare-disk-encryption` removes a
leftover temporary file and reports a truncated UKI as such. FAT has no
journal: a power loss during the rename itself can still leave the
directory for `fsck.fat`, which `systemd-fsck` runs at boot; the rename is
the best primitive FAT offers.

Before it makes any key, BOOTy checks that the ESP has room for the
temporary copy: the size of the UKI plus 4 MiB (the added sections, the
certificate and cluster rounding). Otherwise `prepare-disk-encryption`
fails with the needed and the free space. The UKI size is known only
after `stream-image`, so the check can not run before `wipe-disks`; size
the ESP of the UKI image flavour for at least twice the UKI plus margin.
`UKI_INPLACE_WRITE=true` truncates and rewrites the UKI instead, the
behavior before, for ESPs that cannot fit two copies; an interrupted write
then leaves a truncated UKI that does not boot (the GRUB entry still does).

### Boot path

The UKI boots directly from a firmware boot entry, verified against `db`.
Shim and systemd-boot are not used: shim would accept anything signed by
the distribution vendor's key and adds `MokList` (PCR 14) to the policy;
systemd-boot adds a second loader that the policy has to cover. Direct boot
needs the certificate in `db`: the per-host certificate (provisioner,
Redfish) or the pipeline certificate. Machines that allow no `db` change
need the shim path with the MOK enrollment of #935 (`mok-enroll.efi`
boots, shim loads `\EFI\Linux\<name>.efi` with the per-host key in
`MokList`, PCR 14 bound). That is a follow-up.

### UKI threat model

Closed by `luks2-tpm2-uki`, compared with `luks2-tpm2`:

- changed initramfs or kernel on the ESP or `/boot`: PCR 11 differs (with
  Secure Boot on the firmware refuses a UKI without a valid signature
  anyway);
- a changed kernel command line (`init=/bin/sh`, `rd.break`) from the
  console, a BMC or the boot loader: it is in `.cmdline`, measured and
  signed; systemd-stub ignores overrides with Secure Boot on. The per-host
  kernel parameters are part of it, checked against a deny list and locked
  against standby payloads;
- booting another signed OS (a distribution live image) and extending PCR
  11 from there: PCR 7 differs (another `db` entry verified it);
- booting with Secure Boot off: PCR 7 differs.

Remaining:

- **Rollback.** Every UKI ever signed with the PCR policy key unlocks. A BMC
  or console attacker who has an older, validly signed UKI with a known
  vulnerability can boot it (the KVM test shows this). Countermeasures,
  not in this change: a PCR 11 policy reference per release (ukify
  `--policyref`, systemd 262), PCR 11 phase/version policies with
  systemd-pcrlock and an NV index, adding old UKIs to `dbx`, or rotating the
  PCR policy key (re-enrollment). `per-host` limits this to the UKIs signed
  for this host, which is one per provisioning.
- **Firmware and `db`.** A `db`/`dbx` or firmware update changes PCR 7; the
  recovery key unlocks, and the volume must be bound again
  (`systemd-cryptenroll --wipe-slot=tpm2 --tpm2-device=auto
  --tpm2-public-key=/etc/booty/tpm2-pcr-public-key.pem
  --tpm2-public-key-pcrs=11 --tpm2-pcrs=7 <device>`).
- **Before the first Secure Boot boot** the empty passphrase opens the
  volume (see above).
- **Runtime.** Root on the running system can read the volume key; the
  signed policy covers the booted phases, so it can also unseal the TPM2
  secret. Restricting the `.pcrsig` to the `enter-initrd` phase would stop
  the second, but then the first-boot unit cannot check the policy before
  it wipes the empty passphrase.
- **TPM bus and memory attacks** (sniffing the LPC/SPI bus, cold boot): not
  addressed; a TPM2 session encryption (`tpm2-salt`) is not set.
- **ci keys.** A leaked pipeline key unlocks every host of the image together
  with physical access. Keep it in an HSM.

### Configuration

| Key | Value |
|---|---|
| `DISK_ENCRYPTION` | `luks2-tpm2-uki` |
| `SECUREBOOT_SIGNING` | `per-host` or `ci` (default `off`; other values are refused) |
| `MODULE_SIGNING_ENROLLMENT` | `external`: the direct boot has no shim, so a `MokList` enrollment (`efi-helper`, `mokutil`) never reaches the kernel; the provisioner enrolls the module certificate in `db` |
| `IMAGE_COSIGN_PUBKEYS` | required: the UKI digest comes from the signed manifest |
| `MODULE_SIGNING_TARGET` | `in-place` or, with `per-host`, the experimental `sysext`: the kernel must verify the root hash signature with the `db` certificate in `.platform` (see [dm-verity roots](module-signing.md#dm-verity-roots-experimental)) |
| `DISK_ENCRYPTION_PCR_PUBLIC_KEY_FILE` | optional with `ci`: the pinned PCR policy key |
| `MACHINE_EXTRA_KERNEL_PARAMS`, `EXTRA_KERNEL_PARAMS` | with `per-host` also in the UKI `.cmdline` ([Per-host kernel parameters](#per-host-kernel-parameters)); with `ci` GRUB only |
| `UKI_KEEP_IMAGE_CMDLINE` | `true`: `per-host` signs the image `.cmdline` unchanged (transitional) |
| `UKI_INPLACE_WRITE` | `true`: `per-host` rewrites the UKI in place, for an ESP without room for two copies (transitional) |
| `DISK_ENCRYPTION_RECOVERY_KEY_URL`, `DISK_ENCRYPTION_TARGET`, `DISK_ENCRYPTION_ALLOW_FAILURE`, `DISK_ENCRYPTION_SKIP_SECURE_BOOT_CHECK` | as for `luks2-tpm2` |
| `DISK_ENCRYPTION_STATUS_URL` | not supported with the UKI mode yet (refused) |

Without `DISK_ENCRYPTION=luks2-tpm2-uki` nothing of this runs: no
annotation is read, no key is made, no boot entry is added.
`SECUREBOOT_SIGNING` is also the setting of
[module signing](module-signing.md): one setting selects the signer of the
modules and of the UKI. The keys stay separate on purpose. The module key
goes into `MokList` with the module-signing-only extended key usage, so it
can never sign a boot image; the UKI key goes into `db` and signs only the
UKI. With `ci` the pipeline holds both (`MOK_CERT_PATH` names the module
certificate in the image); with `per-host` BOOTy makes both and wipes
them.

### Limitations

- Per-host: kernel and initramfs updates need reprovisioning (see above).
- No status callback from the first-boot unit yet.
- One UKI, no multi-profile UKIs, no devicetree selection.
- No LVM physical volume yet: refused before the first write, so
  `DISK_ENCRYPTION_ALLOW_FAILURE` falls back to an unencrypted install.
- x86-64 with the SHA-256 PCR bank.

### Test

KVM scenarios `disk-encryption-uki-per-host` (`TestDiskEncryptionUKIPerHostVM`)
and `disk-encryption-uki-ci` (`TestDiskEncryptionUKICIVM`): the Ubuntu 26.04
cloud image, made into the UKI flavour by the test (systemd-ukify and
sbsigntool installed, the crypttab above, a generic dracut initramfs),
pushed to a local registry with the digest annotation and a cosign
signature; OVMF with the Microsoft keys and swtpm.

- per-host: the test gives the image the layout of the UKI image flavour
  (a 512 MiB ESP with the label and volume ID kept, `/boot` after it as
  XBOOTLDR, the root last and moved right), as the 106 MB ESP of the cloud
  image has no room for the atomic install; BOOTy signs with `EXTRA_KERNEL_PARAMS=booty.perhost=1`; the
  test enrolls the reported certificate in `db`; the first Secure Boot boot
  binds (one token, `tpm2-pubkey-pcrs: 11`, `tpm2-hash-pcrs: 7`, no key
  slot 1); the next unlocks with the TPM2 and has `booty.perhost=1` in
  `/proc/cmdline`; with Secure Boot off the recovery key unlocks.
- ci: BOOTy leaves the UKI as it is; the first Secure Boot boot binds; an
  updated UKI signed with the same keys unlocks with the same token; a UKI
  signed for Secure Boot whose PCR 11 policy is signed by another key gets
  the passphrase prompt and the recovery key; the older UKI still unlocks
  (rollback, see above).
- No recovery key and no private key in any console log.

Unit tests assert that `ci` never generates a key and that nothing of this
runs with the defaults; the per-host `.cmdline`, its PCR 11 policy against
`systemd-measure` output, the deny list and the payload lock; and the
atomic install (a failed write keeps the old UKI, the free-space refusal, a
leftover temporary file, a truncated UKI).
