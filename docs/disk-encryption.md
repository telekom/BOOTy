# Disk encryption (LUKS2 + TPM2)

Opt-in with `DISK_ENCRYPTION=luks2-tpm2`; off by default. BOOTy encrypts one
partition of the installed system with LUKS2. The installed system binds it
to the TPM2 (PCR 7+14) on its first boot with Secure Boot on. A random
recovery key goes to the provisioner. Keys are in
[configuration.md](configuration.md#disk-encryption).

## Flow

Provisioning (BOOTy, Secure Boot off):

1. `prepare-disk-encryption`, before `configure-grub`, with the target root
   mounted:
   - checks the prerequisites (see [Requirements](#requirements)) and
     generates the LUKS UUID and a 512-bit enrollment key;
   - writes into the target:
     - `/etc/booty/luks-enroll.key` (mode 0400), the enrollment key;
     - `/etc/crypttab`: `booty_crypt UUID=<luks uuid> /etc/booty/luks-enroll.key luks,x-initrd.attach`;
     - `/etc/dracut.conf.d/90-booty-luks.conf`: the `crypt` and
       `systemd-cryptsetup` modules, `/etc/crypttab`, the TPM2 libraries
       that systemd loads with `dlopen` and the libcryptsetup TPM2 token
       plugin (dracut does not see them; without the plugin the initramfs
       silently falls back to the passphrase prompt), and
       `hostonly="no"`: for `tpm2-device=` in crypttab a host-only build
       pulls in the dracut `tpm2-tss` module, which needs `tpm2-tools`, and
       drops `systemd-cryptsetup` without it. Generic is the Ubuntu default;
     - `/etc/dracut.conf.d/91-booty-luks-enroll-key.conf`: the enrollment
       key in the initramfs;
     - `/etc/default/grub.d/99-booty-luks.cfg`: `GRUB_FORCE_PARTUUID=`, so
       Ubuntu cloud images do not try to boot the root without initramfs;
     - the unit `booty-luks-tpm2-enroll.service` (enabled) and its script
       `/usr/local/sbin/booty-luks-tpm2-enroll`;
   - runs `dracut --force --regenerate-all` in the target.
2. `configure-grub` and the later steps run as usual.
3. `encrypt-disk`, after `teardown-chroot` unmounted the target:
   - generates a 256-bit recovery key in the `systemd-cryptenroll
     --recovery-key` format (modhex, 8 groups of 8 characters);
   - posts it to `DISK_ENCRYPTION_RECOVERY_KEY_URL`. Without a 2xx answer the
     run fails before the disk changes;
   - shrinks the ext4 file system by 32 MiB (`e2fsck -p -f`, `resize2fs`) and
     runs `cryptsetup reencrypt --encrypt --type luks2 --reduce-device-size 32M`
     with the recovery key in key slot 0;
   - adds the enrollment key to key slot 1 and checks that the recovery key
     opens the volume.
4. BOOTy reboots through the firmware. Disk encryption turns kexec off: the
   target must be unmounted and the TPM must see a firmware boot.

Installed system:

- Every boot until the enrollment: the initramfs unlocks the root with the
  enrollment key. Nobody types anything.
- First boot with Secure Boot on (`ConditionSecurity=uefi-secureboot`), the
  unit:
  1. `systemd-cryptenroll --tpm2-device=auto --tpm2-pcrs=7+14 --wipe-slot=tpm2`
     (unlocked with the enrollment key);
  2. checks that the TPM2 opens the volume
     (`cryptsetup open --test-passphrase --token-only`);
  3. changes the crypttab line to `none luks,x-initrd.attach,tpm2-device=auto`,
     removes `91-booty-luks-enroll-key.conf` and builds every
     `/boot/initrd.img-*` again into a new file; `lsinitrd` must show
     `systemd-cryptsetup`, the TPM2 libraries and `/etc/crypttab`, and must
     not show the key. Only then the new files replace the old ones;
  4. wipes key slot 1, `shred -u` the key file and disables itself.

  A failure before step 4 restores crypttab and the dracut file, and the
  unit runs again on the next boot. The volume stays unlockable with the
  enrollment key until then.
- Later boots: `systemd-cryptsetup` in the initramfs unseals the key with
  the TPM2. When the PCRs differ, it asks for a passphrase on the console;
  the recovery key works there.

## Design decisions

**Encrypt in place after imaging.** BOOTy writes whole-disk images as they
are, with their partition table and file systems, so it cannot create the
LUKS2 volume first and write a file system into it. `cryptsetup reencrypt
--encrypt` turns the written partition into a LUKS2 volume and keeps the
data. The price is a full rewrite of the partition, which `grow-partition`
has already grown to the disk size (see [Limitations](#limitations)).

**Bind on the first Secure Boot boot, not in BOOTy.** BOOTy runs with Secure
Boot off and, with [module signing](https://github.com/telekom/BOOTy/pull/935),
before the MOK enrollment. PCR 7 and PCR 14 then differ from the installed
system with Secure Boot on. The MOK spike measured this in the KVM scenario
(OVMF with the Microsoft keys):

| | PCR 7 | PCR 14 |
|---|---|---|
| Ubuntu 24.04, Secure Boot on | `C86235C7…` | `A46F893D…` |
| Ubuntu 24.04, Secure Boot off | `CDF4FCEF…` | `A46F893D…` |
| Ubuntu 26.04, Secure Boot on | `C86235C7…` | `24BD0348…` |

PCR 7 is the same for both releases although their MOKs differ: shim
measures `MokList` into PCR 14, not into PCR 7. A policy on PCR 7 alone does
not notice an added MOK, so the volume is bound to PCR 7+14. A host that
boots with Secure Boot off (for example the boot right after the MOK
enrollment, before the provisioner turns Secure Boot on) keeps the
enrollment key and binds on a later boot.

**Temporary key file in the initramfs, not the recovery key.** The other
option is to unlock the first boots with the recovery key at the console or
over the network. That needs a console or network unlock path at every
provisioning, and it puts the recovery key on a console, which is the
secret that must stay with the provisioner. The key file is the smaller
exposure:

- it unlocks only the volume of a fresh installation, between provisioning
  and the first Secure Boot boot, and it is not the recovery key;
- the TPM2 slot is tested before anything is removed;
- its key slot is wiped after the enrollment. A copy that survives in flash
  cells or in an old initramfs file opens nothing;
- the key file and the dracut file that includes it are removed, and every
  initramfs is rebuilt and checked without it.

Without Secure Boot the host never binds and keeps the key file. A
provisioner that needs encryption must turn Secure Boot on and check the
result, for example with `cryptsetup luksDump` (one `systemd-tpm2` token,
key slots 0 and the TPM2 slot, no slot 1).

**Recovery key hand-off.** BOOTy posts one JSON document to
`DISK_ENCRYPTION_RECOVERY_KEY_URL`:

```json
{"hostname":"node-1","device":"/dev/sda1","luksUUID":"…","keySlot":0,"recoveryKey":"cbdefghi-…"}
```

The request uses the bearer token of the other callbacks and needs HTTPS (or
loopback) unless `INSECURE_TRANSPORT=true`. Three attempts; any status other
than 2xx fails the run. Escrow (KMS, vault, BMC) is the provisioner's job;
the endpoint only has to store the key before it answers. BOOTy

- never logs the key, puts it in no report, error or argument list, and
  passes it to `cryptsetup` on stdin only; the enrollment key reaches
  `cryptsetup luksAddKey` through a memfd, without a path on a file system;
- builds the JSON body by hand (the `encoding/json` buffer pool would keep a
  copy) and zeroes the key and the body after use. Copies in the TLS stack
  and in the pipe to `cryptsetup` cannot be zeroed from Go; BOOTy runs from
  RAM and the host reboots right after.

**Fail closed.** Any error fails provisioning: `BOOTY-DSK-026` in
`prepare-disk-encryption`, `BOOTY-DSK-027` in `encrypt-disk`. With
`DISK_ENCRYPTION_ALLOW_FAILURE=true` a failed check in
`prepare-disk-encryption` (no TPM, image without the tools, unsupported
layout) provisions without encryption and logs a warning. Nothing on the
disk has changed then. Errors after the checks always fail the run.

## Requirements

- A TPM2 (`/sys/class/tpm/tpm0` in the ramdisk) and UEFI with Secure Boot.
- A whole-disk image. Not with disk mirroring, RAID, `PARTITION_LAYOUT` or
  an image mode other than `whole-disk`.
- The target partition (`DISK_ENCRYPTION_TARGET`, default the root) is ext4
  on the target disk and is not `/boot` or the ESP. The root needs a
  separate `/boot` partition: GRUB cannot read the kernel from the
  encrypted root.
- The image has `dracut` and `lsinitrd`, `systemd-cryptsetup` and
  `systemd-cryptenroll`, `cryptsetup`, `shred` and the TPM2 libraries
  `libtss2-esys.so.0`, `libtss2-rc.so.0`, `libtss2-mu.so.0`,
  `libtss2-tcti-device.so.0`, and the token plugin
  `libcryptsetup-token-systemd-tpm2.so` (package `systemd-cryptsetup`).
  BOOTy checks them before it changes anything.
  - Ubuntu 26.04 cloud image: add `libtss2-rc0t64`. dracut and
    `systemd-cryptsetup` are installed.
  - Ubuntu 24.04 cloud image: add `dracut`, `systemd-cryptsetup` and
    `libtss2-rc0t64`. `initramfs-tools` cannot unlock with the TPM2
    (`cryptsetup-initramfs` ignores `tpm2-device=`), so it must be replaced
    by dracut.
- The ramdisk has `cryptsetup`, `e2fsck`, `resize2fs` and `dm_crypt` (the
  production initramfs has them).

## Module signing

With [per-host module signing](https://github.com/telekom/BOOTy/pull/935)
the order is: `sign-kernel-modules` rebuilds the initramfs (the BOOTy
dracut files stay in place), `enroll-module-signing-key` stages the MOK,
`encrypt-disk` runs after both. The next firmware boot runs `mok-enroll.efi`;
if the installed system then boots before Secure Boot is on, the unit does
nothing. The first Secure Boot boot sees the final `MokList` in PCR 14 and
binds. No ordering code is needed for this.

## Limitations

- `cryptsetup reencrypt` rewrites the whole partition: roughly the disk
  size at the disk's write rate (about 15 h for 8 TB at 150 MB/s). The
  watchdog budget of `encrypt-disk` is 24 h.
- One partition, ext4 only (XFS cannot shrink).
- Firmware, `db` or `dbx` updates change PCR 7; a MOK change changes PCR 14.
  The TPM2 then refuses, and the recovery key unlocks. Bind again after
  such an update:
  `systemd-cryptenroll --wipe-slot=tpm2 --tpm2-device=auto --tpm2-pcrs=7+14 <device>`
  (it asks for the recovery key).
- The provisioner gets no report from the first boot; it checks the
  enrollment itself (see above).

## Test

KVM scenario `disk-encryption-ubuntu-26.04` (`TestDiskEncryptionTPM2VM`):
Ubuntu 26.04 cloud image with `libtss2-rc0t64`, `OVMF_CODE_4M.secboot.fd`
with the Microsoft keys, one swtpm state for all boots.

1. Provision with Secure Boot off; the recovery key reaches the test
   server.
2. Boot with Secure Boot off: unlocked with the enrollment key, no TPM2
   slot yet.
3. Boot with Secure Boot on: the unit binds to PCR 7+14; key slot 1, the key
   file and the key in the initramfs are gone.
4. Boot with Secure Boot on: the TPM2 unlocks, no passphrase prompt.
5. Boot with Secure Boot off: PCR 7 differs, the TPM2 refuses, the
   passphrase prompt accepts the recovery key and the system boots.
6. The recovery key is in none of the serial console logs.

Run it locally on an x86-64 Linux host with KVM, as root:

```sh
sudo apt-get install -y qemu-system-x86 ovmf swtpm cryptsetup-bin python3-virt-firmware
curl -LO https://cloud-images.ubuntu.com/releases/resolute/release-20260927/ubuntu-26.04-server-cloudimg-amd64.img
curl -LO https://launchpad.net/ubuntu/+archive/primary/+files/libtss2-rc0t64_4.1.3-6_amd64.deb
sudo env MIRROR_OS_IMAGE=$PWD/ubuntu-26.04-server-cloudimg-amd64.img \
  DISK_ENCRYPTION_DEBS=$PWD/libtss2-rc0t64_4.1.3-6_amd64.deb \
  go test -tags e2e -run '^TestDiskEncryptionTPM2VM$' -v -timeout 50m ./test/e2e/kvm/
```
