# Disk encryption (LUKS2 + TPM2)

Opt-in with `DISK_ENCRYPTION=luks2-tpm2`; off by default. Without it BOOTy
runs the same steps as before: the encryption steps are not in the step
list. BOOTy formats one partition of the image with LUKS2 while it writes
the image, and writes that partition into the opened volume. The installed
system binds the volume to the TPM2 (PCR 7+14) once Secure Boot is on. A
random recovery key goes to the provisioner. Keys are in
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
2. `parse-partitions` checks that the root it finds is the encrypted
   partition; from then on the root is the mapping. `grow-partition` is
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
   an encrypted data partition and closes the mapping. BOOTy reboots
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
  not the ESP. Not with disk mirroring, RAID, `PARTITION_LAYOUT`, A/B,
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
the MOK is staged during provisioning and enrolled by `mok-enroll.efi` on
the next firmware boot. If the system boots before Secure Boot is on, the
unit waits. The first Secure Boot boot sees the final `MokList` in PCR 14
and binds. No ordering code is needed.

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
- `disk-encryption-off-ubuntu-26.04` (`TestDiskEncryptionOffVM`): the same
  image without the keys: no encryption step runs, the partition table is
  the image's with the root grown, the root is plain ext4, and no BOOTy
  encryption file is in the target.

Run them locally on an x86-64 Linux host with KVM, as root:

```sh
sudo apt-get install -y qemu-system-x86 ovmf swtpm cryptsetup-bin python3-virt-firmware
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
- hands the private keys to `ukify` (which runs `systemd-measure` and
  `sbsign` in the target) as `/proc/<pid>/fd/<n>` paths of memfds: they never have a path
  on a file system and never appear in an argument, a log or the report;
- zeroes the memfds and the key numbers after use (best effort, like the
  module signing key of #935: copies inside the two tools and in freed Go
  memory cannot be zeroed; BOOTy runs from RAM and the host reboots).

The per-host certificate is in the provisioning report
(`secureBoot.uki.cert`, DER, base64, with `certSHA256`) and on the ESP at
`/EFI/BOOTy/uki-signing.crt` (PEM). The provisioner enrolls it in `db`
(for example over Redfish) before it turns Secure Boot on.

### Image contract

The UKI image flavour (built by the image pipeline; existing images are
unchanged):

- exactly one UKI at `/EFI/Linux/<os-id>-<kernel release>.efi` on the ESP
  (UAPI Boot Loader Specification, type #2). BOOTy refuses zero or more than
  one `.efi` file there. With `per-host` it replaces the file in place
  with the signed one; with `ci` it does not change it;
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
- with `per-host`: the UKI is unsigned, has no `.pcrsig` and only the
  sections `.linux`, `.initrd`, `.cmdline`, `.osrel`, `.uname`, `.ucode`,
  `.splash`, `.dtb` (plus the stub and `.sbat`); the image has `ukify`,
  `systemd-measure`, the systemd-stub and `sbsign` (Ubuntu:
  `systemd-ukify`, `systemd-boot-efi`, `sbsigntool`);
- `systemd-cryptenroll`, `cryptsetup` and the TPM2 libraries.

`DISK_ENCRYPTION_PCR_PUBLIC_KEY_FILE` pins the PCR policy key with `ci`, as
`IMAGE_COSIGN_PUBKEYS` pins the image key: the `.pcrpkey` of the UKI must be
the key in that file.

### Flow

Provisioning (BOOTy, Secure Boot off):

1. `verify-image` verifies the cosign signature of the image and reads the
   UKI digest annotation.
2. `stream-image` checks as for `luks2-tpm2` and that the digest is there
   (before the first disk write, so `DISK_ENCRYPTION_ALLOW_FAILURE`
   applies), then formats and writes as for `luks2-tpm2`, with the empty
   passphrase in key slot 1 instead of an enrollment key.
3. `prepare-disk-encryption` checks the tools and the UKI: exactly one file, its SHA-256 equals the annotation,
   it is a PE with a `.linux` section, not a multi-profile UKI.
   - `per-host`: refuses a signed UKI or one with `.pcrsig`; makes the keys;
     rebuilds the UKI from its sections with `ukify build` in the target
     (the image's systemd-stub, a new `.sbat`, `.pcrpkey` with the per-host
     public key), which runs `systemd-measure sign` for the expected PCR 11
     values of all boot phases (SHA-256 bank) into `.pcrsig` and `sbsign`;
     BOOTy checks the result (Secure Boot signature, `.pcrsig`, its
     `.pcrpkey`), writes it over the UKI and wipes the keys. ukify fills
     `.pcrsig` only into an existing placeholder (`--join-pcrsig`), so a
     plain unsigned UKI is rebuilt rather than extended.
   - `ci`: needs a Secure Boot signature, `.pcrsig` and `.pcrpkey`; checks
     the pinned key; makes no key.
   - writes the PCR public key and signature to
     `/etc/booty/tpm2-pcr-public-key.pem` and `/etc/booty/tpm2-pcr-signature.json`,
     and the first-boot unit `booty-luks-tpm2-enroll.service`. crypttab,
     dracut and GRUB are not touched; the initramfs is the one in the UKI.
   The disk is encrypted then, so a failed check fails the run. The root
   needs no separate `/boot`.
4. `create-uki-boot-entry` (after `create-mirror-efi-boot-entry`) adds the firmware boot entry `booty-uki` for
   `\EFI\Linux\<name>.efi`, first in `BootOrder`. The GRUB entry stays.

Installed system:

- Until the enrollment: no TPM2 token; the initramfs opens the volume with
  the empty passphrase (`try-empty-password`).
- First boot with Secure Boot on in user mode: the unit runs
  `systemd-cryptenroll --unlock-key-file=/dev/null --tpm2-device=auto
  --tpm2-public-key=... --tpm2-public-key-pcrs=11 --tpm2-signature=...
  --tpm2-pcrs=7`. With `--tpm2-signature` systemd-cryptenroll checks that the
  new policy unseals with the current PCR values before it writes the slot.
  Then the unit wipes key slot 1 and disables itself. One phase is enough:
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

### Boot path

The UKI boots directly from a firmware boot entry, verified against `db`.
Shim and systemd-boot are not used: shim would accept anything signed by
the distribution vendor's key and adds `MokList` (PCR 14) to the policy;
systemd-boot adds a second loader that the policy has to cover. Direct boot
needs the certificate in `db`: the per-host certificate (provisioner,
Redfish) or the pipeline certificate. Machines that allow no `db` change
need the shim path with the MOK enrollment of #935 (`mok-enroll.efi`
boots, shim loads `\EFI\Linux\<name>.efi` with the per-host key in
`MokList`, PCR 14 bound). That is a follow-up once #935 is merged; the
per-host key of this mode and the module signing key of #935 then become
one key with one enrollment.

### UKI threat model

Closed by `luks2-tpm2-uki`, compared with `luks2-tpm2`:

- changed initramfs or kernel on the ESP or `/boot`: PCR 11 differs (with
  Secure Boot on the firmware refuses a UKI without a valid signature
  anyway);
- a changed kernel command line (`init=/bin/sh`, `rd.break`) from the
  console, a BMC or the boot loader: it is in `.cmdline`, measured and
  signed; systemd-stub ignores overrides with Secure Boot on;
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
| `IMAGE_COSIGN_PUBKEYS` | required: the UKI digest comes from the signed manifest |
| `DISK_ENCRYPTION_PCR_PUBLIC_KEY_FILE` | optional with `ci`: the pinned PCR policy key |
| `DISK_ENCRYPTION_RECOVERY_KEY_URL`, `DISK_ENCRYPTION_TARGET`, `DISK_ENCRYPTION_ALLOW_FAILURE`, `DISK_ENCRYPTION_SKIP_SECURE_BOOT_CHECK` | as for `luks2-tpm2` |
| `DISK_ENCRYPTION_STATUS_URL` | not supported with the UKI mode yet (refused) |

With `SECUREBOOT_SIGNING=off` and `DISK_ENCRYPTION` unset or `luks2-tpm2`
nothing of this runs: no annotation is read, no key is made, no boot entry
is added.

### Limitations

- Per-host: kernel and initramfs updates need reprovisioning (see above).
- No status callback from the first-boot unit yet.
- One UKI, no multi-profile UKIs, no devicetree selection.
- x86-64 with the SHA-256 PCR bank.

### Test

KVM scenarios `disk-encryption-uki-per-host` (`TestDiskEncryptionUKIPerHostVM`)
and `disk-encryption-uki-ci` (`TestDiskEncryptionUKICIVM`): the Ubuntu 26.04
cloud image, made into the UKI flavour by the test (systemd-ukify and
sbsigntool installed, the crypttab above, a generic dracut initramfs),
pushed to a local registry with the digest annotation and a cosign
signature; OVMF with the Microsoft keys and swtpm.

- per-host: BOOTy signs; the test enrolls the reported certificate in `db`;
  the first Secure Boot boot binds (one token, `tpm2-pubkey-pcrs: 11`,
  `tpm2-hash-pcrs: 7`, no key slot 1); the next unlocks with the TPM2; with
  Secure Boot off the recovery key unlocks.
- ci: BOOTy leaves the UKI as it is; the first Secure Boot boot binds; an
  updated UKI signed with the same keys unlocks with the same token; a UKI
  signed for Secure Boot whose PCR 11 policy is signed by another key gets
  the passphrase prompt and the recovery key; the older UKI still unlocks
  (rollback, see above).
- No recovery key and no private key in any console log.

Unit tests assert that `ci` never generates a key and that nothing of this
runs with the defaults.
