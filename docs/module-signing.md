# Per-host module signing

Status: experimental and opt-in. `SECUREBOOT_SIGNING` selects the mode:

| `SECUREBOOT_SIGNING` | Who signs the modules | Key in BOOTy | What BOOTy enrolls |
|---|---|---|---|
| `off` (default) | nobody | none | nothing: no step changes the target, ESP or EFI variables, and `mok-enroll.efi` is never staged |
| `per-host` | BOOTy, while provisioning | per host, in memory only | the per-host certificate |
| `ci` | CI, before the image is built | none | `MOK_CERT_PATH`, the CI certificate in the image |

The enrollment (`MODULE_SIGNING_ENROLLMENT`: `efi-helper`, `mokutil`,
`external`) is the same for `per-host` and `ci`. With `ci`, every host that
gets the image trusts the CI key, the shared-key trade-off that `per-host`
avoids: a leaked CI key can sign modules for all of them. The CI certificate
must carry the module-signing-only EKU `1.3.6.1.4.1.2312.16.1.2`, or BOOTy
refuses it: shim trusts every other `MokList` certificate for boot loaders
and kernels too, and the image alone chooses this one. The UKI/PCR 11
signing work is meant to use the same setting and modes.

Out-of-tree kernel modules (DKMS output, vendor drivers) in a whole-disk
image are either unsigned or signed with a key shared by every host that got
the image. With Secure Boot on, the kernel refuses unsigned modules, and a
shared key that leaks lets anyone sign modules for every host. BOOTy can
instead give each host its own signing key, used once and destroyed while
provisioning.

## Flow

After the image is written and the target root is mounted:

1. `sign-kernel-modules` (after `install-efi-fallback`):
   - generate an RSA-3072 key and a self-signed certificate in memory
     (`CN=BOOTy module signing <hostname>`, code signing EKU and the
     module-signing-only EKU `1.3.6.1.4.1.2312.16.1.2`);
   - sign every `*.ko`, `*.ko.zst`, `*.ko.xz`, `*.ko.gz` below
     `/lib/modules/*/updates` and `/lib/modules/*/extra`, in the format of
     the kernel's `scripts/sign-file` (detached CMS, SHA-256, no signed
     attributes, `struct module_signature` and the magic string). An existing
     signature is replaced. Files are read and written through `os.Root`, so a
     symlink in the image cannot redirect a write. Every module is checked
     before the first one is rewritten, and a file that is not ELF is
     refused: the signature is a bare RSA signature over the content, so
     signing image-chosen bytes would let the image obtain, for example, a
     certificate issued by the per-host key;
   - modules are rewritten in place, so owner, mode and extended attributes
     (the SELinux label) stay; files larger than 256 MiB, compressed or not,
     are refused;
   - destroy the private key (see [Key handling](#key-handling));
   - install the DER certificate as
     `<MODULE_SIGNING_CERT_DIR>/booty-module-signing.der`
     (default `/usr/share/booty/secureboot`);
   - run `depmod -a <kernel>` and `update-initramfs -u -k all` (or
     `dracut --force --regenerate-all`) in the target chroot.
2. `enroll-module-signing-key` (after the firmware boot entries) enrolls the
   certificate created in step 1 of this run, never a file read from the
   image (on a resume, `sign-kernel-modules` runs again):
   - `efi-helper` (default): copy `mok-enroll.efi` to
     `\EFI\BOOTy\mok-enroll.efi` on the ESP, create an inactive boot entry
     whose optional data is the certificate as an `EFI_SIGNATURE_LIST`, and
     set `BootNext` to it. The entry is inactive and not in `BootOrder`, so
     it can run only through `BootNext`. OVMF boots it; firmware that skips
     inactive `BootNext` entries needs an active one (not tested on
     hardware);
   - `mokutil`: BOOTy writes `MokNew` and `MokAuth` (SHA-256 over `MokNew`
     and the UCS-2 `MOK_PASSWORD`) itself, as `mokutil --import
     --simple-hash` does; no binary of the image runs with the password.
     MokManager asks for the password once at the console;
   - `external`: nothing; the provisioner enrolls the certificate.
3. The provisioning report (`secureBoot.moduleSigning`) carries the
   certificate, its SHA-256 and the number of signed modules, so the
   provisioner can enroll it out of band and later check the TPM event log.
4. BOOTy reboots through the firmware (no kexec). With `efi-helper` the
   firmware runs `mok-enroll.efi`, which merges the certificate into
   `MokList`, deletes its boot entry, sets `BOOTyMokEnroll` to `ok` (or an
   error), and resets. The provisioner then turns Secure Boot on
   after the enrollment boot, over Redfish or in the firmware setup.
   `SECUREBOOT_REENABLE=true` is rejected with `efi-helper`: the provisioner
   acts on it when BOOTy reports success, before the enrollment boot, and the
   firmware would then refuse the unsigned helper.

Modules that `copy-machine-files`, machine commands or post-provision
commands add or rebuild run after signing and stay unsigned: build them into
the image. Signing writes into the target, so it must run before
`copy-machine-files`. On a resume (`BOOTY_RESUME`) signing and enrollment run
again with a new key. The provisioning report sent to `LOG_URL` carries the
new certificate. A report file that the interrupted run already installed is
kept, so it may name the earlier certificate.

Any signing or enrollment error fails provisioning (`BOOTY-EFI-010`,
`BOOTY-EFI-011`) unless `MODULE_SIGNING_ALLOW_FAILURE=true`.

`mok-enroll.efi` is built from [hack/mok-enroll](../hack/mok-enroll) with
gnu-efi. It is not in the production initramfs yet; the KVM scenario adds it
to the test ramdisk.

## Key handling

- The key exists only in BOOTy's memory. It is never written to disk, never
  leaves `secureboot.SignModuleTree`, and is not logged (only the certificate
  SHA-256 is).
- After signing, the private exponent, primes and CRT values are
  overwritten. `crypto/rsa` keeps a precomputed copy that Go does not expose;
  building with `GOEXPERIMENT=runtimesecret` and wrapping the signing in
  `runtime/secret.Do` would erase it too. BOOTy runs from RAM and the host
  reboots right after, which ends the remaining exposure.
- A key cannot sign anything after provisioning. Rebuilding a DKMS module
  on the installed host needs a new provisioning, or a host-local key that
  the operator enrolls separately.

## Enrollment options

| Option | Unattended | Works with stock shim + kernel | Notes |
|---|---|---|---|
| (a) `efi-helper`: BootNext to `mok-enroll.efi` while Secure Boot is off | yes | yes: Ubuntu 24.04 and 26.04, see [evidence](#evidence) | Needs Secure Boot off during provisioning and a firmware reboot. |
| (b) Redfish `SecureBootDatabases/db` by the provisioner | yes | distribution kernels only: yes on Ubuntu 24.04 and 26.04 | The kernel puts `db` certificates in the `.platform` keyring. Debian and Ubuntu patch the kernel to verify modules with it; upstream uses it for kexec only. The firmware also trusts EFI images signed by the key. |
| (c) `mokutil --import` + MokManager | no (one console confirmation) | yes | MokManager waits 10 seconds, then boots without enrolling. |

### (a) EFI helper

`MokList` (`605dab50-e046-4300-abb6-3dd810dd8b23`) is a non-volatile,
boot-services-only variable without authentication. shim reads it, deletes
it if it has the runtime attribute (the OS could have written it), checks the
`EFI_SIGNATURE_LIST` structure, and copies it with its built-in vendor
certificate to the volatile `MokListRT` and to the MOK configuration table.
Upstream kernels read those into the `.machine` keyring (Linux 5.18+, when
shim sets `MokListTrustedRT`, the default since shim 15.6). That keyring is
linked to the secondary keyring, which verifies modules. Some distribution
kernels use the `.platform` keyring instead and verify modules with it
through a distribution patch, as the Ubuntu kernels below do.

`mok-enroll.efi` writes `MokList` like MokManager does after the user
confirms: same GUID, attributes `NV|BS`, the certificate appended as an X.509
signature list. It removes earlier lists owned by the BOOTy owner GUID
`11378d35-e20e-4116-94d7-758d7540a17f` (lists of exactly one signature), so a
reprovisioned host keeps exactly one BOOTy certificate and other MOK entries
stay. It refuses a malformed `MokList` or payload and then changes nothing. A
`MokList` with other attributes than NV|BS (the OS could have written it) is
discarded, as shim does, and never carried over. The helper refuses to run
when `SecureBoot` reads 1, and deletes `Boot<BootCurrent>` only when its
description is `booty-mok-enroll`.

The helper is unsigned, and it must stay unsigned: signed, it would let OS
root add any certificate to `MokList` with Secure Boot on, through a boot
entry and `BootNext`. `BOOTyMokEnroll` has the runtime attribute, so the OS
can write it too; treat it as a hint, and check the `MokList` event in the
TPM event log (PCR 14) instead.

Tested: Ubuntu 24.04 (shim-signed 1.58+15.8, kernel 6.8.0-139) and Ubuntu
26.04 (shim-signed 1.59+15.8, kernel 7.0.0-34). On both, `MokListTrustedRT`
was 1, the kernel logged `integrity: Loaded X.509 cert 'BOOTy module
signing …'`, and the key was in `.platform`, not `.machine`: `/proc/keys`
listed `.machine: empty` and `.platform: 7`. Those kernels verify modules
against `.platform` (Debian/Ubuntu patch), so the module loads. The same
patch explains option (b). Required versions: shim 15.6 or newer with Linux
5.18 or newer (`.machine`), or a distribution kernel that verifies modules
with the keyring that it loads `MokListRT` into. Debian 12 and 13 ship shim
15.8 and newer; they were not run in CI. The
certificate is a CA with `keyCertSign`, because kernels built with
`CONFIG_INTEGRITY_CA_MACHINE_KEYRING` accept only CA certificates from MOK
into `.machine`.

### (b) db over Redfish

A BMC that implements `SecureBootDatabase` accepts
`POST .../SecureBoot/SecureBootDatabases/db/Certificates` with the
certificate from the report (PEM). `db` is an authenticated variable, so
nothing in the OS or pre-boot code can add to it without a KEK signature;
only the BMC (platform owner path) or Setup Mode can. That makes it the
stronger store, but:

- the firmware then trusts EFI images signed by the key (the module-signing
  EKU is ignored by firmware). The key is destroyed, so nothing new can be
  signed, but the trust anchor is broader than needed;
- the kernel keeps `db` certificates in `.platform`. In the KVM scenario the
  Ubuntu 24.04 (6.8) and 26.04 (7.0) kernels loaded a module signed by a
  `db`-only certificate (Debian/Ubuntu patch "make use of platform keyring
  for module signature verify"). Upstream kernels and distributions without
  that patch refuse it;
- every BMC vendor needs its own Redfish call and the update takes effect
  at the next POST.

BOOTy only emits the certificate for this path (`enrollment: external`).

### (c) mokutil

`MokNew` and `MokAuth`, written by BOOTy. It needs someone (or a BMC
console script) to confirm in MokManager, so it is a fallback only.

## Security analysis

**Who else can write MokList while Secure Boot is off.** Any code that runs
before `ExitBootServices`: every EFI application the firmware boots (USB,
PXE/HTTP boot, EFI shell), option ROMs, and the firmware setup. The OS cannot
(no runtime attribute; writing `MokListRT` has no effect after reboot).
While Secure Boot is off, an attacker with pre-boot execution can also boot
anything directly, so the new exposure is persistence: a MOK added during the
window survives the switch to Secure Boot on.

**Does shim validate MokList.** Only its attributes and structure. MOK
entries carry no signature; MokManager's password prompt is the only proof
of physical presence, and shim cannot tell whether MokManager wrote the
variable. With Secure Boot on, only signed boot code can write `MokList`
(shim, MokManager, other signed EFI applications), which is the normal shim
threat model.

**Risk window.** From Secure Boot off at the start of provisioning until it
is on again after the enrollment boot. The same window exists today for
every host that is provisioned with Secure Boot off.

**Detection.** shim measures `MokList`, `MokListX` and `MokSBState` into
PCR 14. The provisioner can compare the `MokList` event in the TPM event log
of the first Secure Boot boot with the certificate in the report; an extra
entry shows a tampered window. Sealing disk keys to PCR 14 (see below)
makes such a change break automatic unlock.

**Revocation and cleanup.** Reprovisioning replaces the BOOTy certificate.
Without reprovisioning, `mokutil --delete` (one MokManager confirmation) or
`MokListX` removes it.

## LUKS2 + TPM2

[Disk encryption](disk-encryption.md) (`DISK_ENCRYPTION=luks2-tpm2`) binds
the volume to PCR 7+14 on the first boot with Secure Boot on, not while
BOOTy runs: Secure Boot is off then, and `MokList` changes at the
enrollment boot. Measured in the KVM scenario (OVMF with the Microsoft
keys):

| | PCR 7 | PCR 14 |
|---|---|---|
| Ubuntu 24.04, Secure Boot on | `C86235C7…` | `A46F893D…` |
| Ubuntu 24.04, Secure Boot off | `CDF4FCEF…` | `A46F893D…` |
| Ubuntu 26.04, Secure Boot on | `C86235C7…` | `24BD0348…` |

PCR 7 changes with the Secure Boot state and is the same for both releases
although their MOKs differ: module signatures are checked by the kernel and
are not measured, and shim measures `MokList` into PCR 14. A policy on PCR 7
alone does not notice an extra MOK; PCR 7+14 does.

With `efi-helper` the enrollment boot runs with Secure Boot off, so the
binding sees the final `MokList`. With `mokutil` the binding waits while a
MOK request is pending in MokManager. With `external`, a `db` update after
the binding changes PCR 7; see
[Module signing](disk-encryption.md#module-signing) there.

## dm-verity roots (experimental)

Signing modules in place changes the root file system after imaging, so it
cannot work with a dm-verity protected root or `/usr`: the root hash would no
longer match. `MODULE_SIGNING_TARGET=sysext` keeps the signed modules outside
it. **Experimental:** opt-in, the layout and file names may change, and only
the KVM scenario below has run it.

### What BOOTy does

With `SECUREBOOT_SIGNING=per-host` and `MODULE_SIGNING_TARGET=sysext`,
`sign-kernel-modules`:

1. reads the out-of-tree modules of the target
   (`/usr/lib/modules/*/{updates,extra}`, a merged `/usr` is required) and
   writes signed copies to the same paths in a staging tree in RAM. Nothing
   below `/usr` of the target changes, and no `depmod` or initramfs rebuild
   runs in the target;
2. adds `usr/lib/extension-release.d/extension-release.booty-modules`
   (`ID` and `VERSION_ID` of the target) and the certificate
   (`MODULE_SIGNING_CERT_DIR`, below `/usr`);
3. builds an ext4 image of the tree (no journal), computes its dm-verity hash
   tree (SHA-256, 4 KiB blocks, veritysetup format with superblock), and
   signs the root hash, as the lower-case hex string that dm-verity checks,
   with the per-host key. Then it destroys the key;
4. installs `/var/lib/extensions/booty-modules.raw` with the sidecar files
   `systemd-sysext` reads: `.verity` (hash tree), `.roothash` and
   `.roothash.p7s` (detached CMS signature, the format of module signatures);
5. enables `systemd-sysext.service` with a drop-in that refreshes with
   `--image-policy=root=signed+absent:usr=signed+absent`, so only images
   with a signed root hash merge. The image policy does not cover
   directory extensions, so the drop-in also fails the refresh (nothing
   merges) while a directory is in `/etc/extensions`, `/run/extensions`,
   `/var/lib/extensions` or `/usr/local/lib/extensions`, or
   `/var/lib/extensions.mutable` exists: otherwise anyone who can write
   `/var` could put files over `/usr`.

At boot, `systemd-sysext` opens the image with dm-verity and passes the
signature to the kernel. The kernel checks it against the secondary keyring
(`CONFIG_DM_VERITY_VERIFY_ROOTHASH_SIG_SECONDARY_KEYRING`, which includes
`.machine`), then against `.platform`
(`CONFIG_DM_VERITY_VERIFY_ROOTHASH_SIG_PLATFORM_KEYRING`; Linux 6.8 lacks the
option, and the Ubuntu 26.04 kernel enables it). The
signature verifies only where the keyring that holds the MOK is in that list.
The overlay then shadows the unsigned modules of the base `/usr` with the
signed copies at the same paths, so `modprobe` and the `modules.dep` of the
image find them. The report carries the root hash
(`secureBoot.moduleSigning.rootHash`). Enrollment works as for in-place
signing.

### Limits

- The image policy applies to every extension on the host: unsigned
  extensions (for example from `SYSEXT_LAYERS`) no longer merge, and a
  directory extension stops every merge.
- ext4, not EROFS or squashfs: `mkfs.ext4` is already in the ramdisk, and
  EROFS would add erofs-utils to every ramdisk. dm-verity and the signature
  work the same way. EROFS is the better production format (smaller, built
  read-only), and switching means changing one `mkfs` call.
- The extension carries no `modules.dep`. Modules that the image's
  `modules.dep` does not list (not run through `depmod` when the image was
  built) load only with `insmod <path>`.
- Modules that the initramfs needs are not covered: the initramfs is not
  rebuilt, and the extension merges after the root is mounted.
- The drop-in and the `sysinit.target.wants` link go to `/etc`. On images
  whose `/etc` is read-only too, they belong in the image or a confext.
- The image signature uses the module signing certificate: the kernel
  ignores the module-signing-only EKU, so the same key verifies both.
- systemd 255 (Ubuntu 24.04) asks for a passphrase after the kernel refuses a
  signature. The drop-in limits `systemd-sysext.service` to 90 s
  (`TimeoutStartSec`), so the boot goes on without the extension.

### Results

KVM scenario `module-sysext-ubuntu-24.04` and `-26.04`
(`TestModuleSigningSysextVM`). The setup is the one of the in-place scenario:
OVMF with the Microsoft keys, the distribution shim and kernel, swtpm, and the
MOK written by `mok-enroll.efi`.

| | Ubuntu 26.04 (7.0.0-34, systemd 259) | Ubuntu 24.04 (6.8.0-139, systemd 255) |
|---|---|---|
| `/usr` of the image after provisioning | byte-identical | byte-identical |
| keyring with the MOK | `.platform` (`.machine` empty) | `.platform` (`.machine` empty) |
| Secure Boot on: extension | merged | refused, `Root hash verification failed (-ENOKEY)` |
| Secure Boot on: module | signed copy loads | refused (the unsigned module of the image) |
| root hash signed by another host's key | not merged (`-ENOKEY`), module refused | (same as above) |
| one byte changed in the module data | `data block … is corrupted`, not merged, module refused | (same as above) |

Ubuntu 24.04 cannot use the prototype. Its kernel keeps the MOK in
`.platform`, and 6.8 checks root hash signatures against the secondary
keyring only. The failure is safe: the extension stays out and the module is
refused. Kernels that put the MOK into `.machine` (upstream with
`MokListTrustedRT`) should verify it through the secondary keyring. That
case is not tested.

### Per-host sysext and extra partition compared

| | Per-host sysext (prototype) | Extra partition on `.../updates` |
|---|---|---|
| Integrity of the module files | dm-verity, root hash signed by the per-host key | per-module signature only |
| `modules.dep`, modprobe configuration, extension content | covered by verity | not protected |
| A changed or unsigned image | refused at merge (`signed` policy); a changed block fails the read | a changed module is refused at load, other files are not checked |
| Disk layout | files in `/var/lib/extensions`, no repartitioning | needs a partition in the image layout and an fstab or mount unit |
| Needs | systemd-sysext (252+), merged `/usr`, the kernel option above | nothing beyond module signature checks |
| Initramfs modules | not covered | not covered |

The sysext keeps the image read-only and protects all extension files, so
the prototype uses it. The extra partition is simpler and works with any
init, but it protects only what the kernel checks per module.

## Evidence

- `hack/mok-enroll/mok-enroll.c` under QEMU with upstream OVMF: payload
  accepted, existing MOK entry of another owner kept, earlier BOOTy entry
  replaced, own boot entry deleted, `MokList` attributes `NV|BS`,
  `BOOTyMokEnroll=ok`.
- KVM scenario `module-signing-ubuntu-24.04` and `module-signing-ubuntu-26.04`
  (`TestModuleSigningSecureBootVM`): Ubuntu cloud image with distro shim and
  kernel, `OVMF_CODE_4M.secboot.fd` with `OVMF_VARS_4M.ms.fd`, swtpm.
  1. Provision with Secure Boot off: one module signed, BootNext staged.
  2. Enrollment boot: `MokList` holds one BOOTy certificate, NV|BS.
  3. Secure Boot on: lockdown `integrity`; the BOOTy-signed module loads
     (`signer` is the per-host certificate); an unsigned module and a module
     signed by another host's key fail with "Key was rejected by service".
  4. Secure Boot off: the system boots, unsigned modules load (tainted),
     PCR 7 differs from step 3.
  5. Reprovision: the helper replaces the certificate; one new one remains.
  6. `db` only (certificate moved from `MokList` to `db`): see option (b).
- The helper must be built without AVX: the runner gcc defaults to
  `-march=x86-64-v3`, and VEX instructions fault (#UD) in firmware.
  `build.sh` checks the result.

Run it locally on an x86-64 Linux host with KVM, as root:

```sh
sudo apt-get install -y qemu-system-x86 ovmf swtpm gnu-efi gcc python3-virt-firmware zstd efibootmgr
sh hack/mok-enroll/build.sh /tmp/mok-enroll.efi
curl -LO https://cloud-images.ubuntu.com/releases/noble/release/ubuntu-24.04-server-cloudimg-amd64.img
sudo env BOOTY_MOK_ENROLL_EFI=/tmp/mok-enroll.efi \
  MIRROR_OS_IMAGE=$PWD/ubuntu-24.04-server-cloudimg-amd64.img \
  go test -tags e2e -run '^TestModuleSigningSecureBootVM$' -v -timeout 50m ./test/e2e/kvm/
```
