# Disk encryption (LUKS2 + TPM2)

Opt-in with `DISK_ENCRYPTION=luks2-tpm2`; off by default. BOOTy encrypts one
partition of the installed system with LUKS2. The installed system binds it
to the TPM2 (PCR 7+14) on its first boot with Secure Boot on. A random
recovery key goes to the provisioner. Keys are in
[configuration.md](configuration.md#disk-encryption).

`DISK_ENCRYPTION=luks2-tpm2-uki` boots a signed unified kernel image (UKI)
instead of GRUB and binds the volume to PCR 7 and a signed PCR 11 policy;
see [Signed UKI and PCR 11](#signed-uki-and-pcr-11). The sections up to
there describe `luks2-tpm2`; encryption, the recovery key hand-off and the
requirements of the partition are the same for both modes.

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
version policies (see [Threat model](#threat-model)). A third option, a
provisioner-held key that signs on request, is the same as `ci` from the
host's view: BOOTy enrolls the public key and never sees the private one.

With `per-host` BOOTy

- generates two RSA-2048 keys with `crypto/rand`;
- hands the private keys to `systemd-measure` and `sbsign` (run in the
  target) as `/proc/<pid>/fd/<n>` paths of memfds: they never have a path
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
- with `per-host`: the UKI is unsigned and has no `.pcrsig`; the image has
  `ukify` (systemd 258 or later, for `--join-pcrsig`), `systemd-measure`
  and `sbsign` (Ubuntu: `systemd-ukify`, `sbsigntool`);
- `systemd-cryptenroll`, `cryptsetup` and the TPM2 libraries.

`DISK_ENCRYPTION_PCR_PUBLIC_KEY_FILE` pins the PCR policy key with `ci`, as
`IMAGE_COSIGN_PUBKEYS` pins the image key: the `.pcrpkey` of the UKI must be
the key in that file.

### Flow

Provisioning (BOOTy, Secure Boot off):

1. `verify-image` verifies the cosign signature of the image and reads the
   UKI digest annotation.
2. `prepare-disk-encryption` checks the partition (as for `luks2-tpm2`), the
   tools and the UKI: exactly one file, its SHA-256 equals the annotation,
   it is a PE with a `.linux` section, not a multi-profile UKI.
   - `per-host`: refuses a signed UKI or one with `.pcrsig`; makes the keys;
     `systemd-measure sign` computes and signs the expected PCR 11 values
     of the UKI sections for all boot phases (SHA-256 bank);
     `ukify build --join-pcrsig` adds `.pcrsig`; `sbsign` signs; BOOTy checks
     the result, writes it over the UKI and wipes the keys.
   - `ci`: needs a Secure Boot signature, `.pcrsig` and `.pcrpkey`; checks
     the pinned key; makes no key.
   - writes the PCR public key and signature to
     `/etc/booty/tpm2-pcr-public-key.pem` and `/etc/booty/tpm2-pcr-signature.json`,
     and the first-boot unit `booty-luks-tpm2-enroll.service`. crypttab,
     dracut and GRUB are not touched; the initramfs is the one in the UKI.
3. `encrypt-disk` encrypts as for `luks2-tpm2`, with the empty passphrase in
   key slot 1 instead of an enrollment key.
4. `create-uki-boot-entry` adds the firmware boot entry `booty-uki` for
   `\EFI\Linux\<name>.efi`, first in `BootOrder`. The GRUB entry stays.

Installed system:

- Until the enrollment: no TPM2 token; the initramfs opens the volume with
  the empty passphrase (`try-empty-password`).
- First boot with Secure Boot on: the unit runs
  `systemd-cryptenroll --unlock-key-file=/dev/null --tpm2-device=auto
  --tpm2-public-key=... --tpm2-public-key-pcrs=11 --tpm2-signature=...
  --tpm2-pcrs=7`. With `--tpm2-signature` systemd-cryptenroll checks that the
  new policy unseals with the current PCR values before it writes the slot.
  Then the unit wipes key slot 1 and disables itself.
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

### Threat model

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
| `DISK_ENCRYPTION_RECOVERY_KEY_URL`, `DISK_ENCRYPTION_TARGET`, `DISK_ENCRYPTION_ALLOW_FAILURE` | as for `luks2-tpm2`; `ALLOW_FAILURE` applies until BOOTy writes the signed UKI |

With `SECUREBOOT_SIGNING=off` and `DISK_ENCRYPTION` unset or `luks2-tpm2`
nothing of this runs: no annotation is read, no key is made, no boot entry
is added.

### Limitations

- Per-host: kernel and initramfs updates need reprovisioning (see above). A
  resumed run (`BOOTY_RESUME`) after the signed UKI was written fails the
  digest check; provision again.
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
