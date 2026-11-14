# Per-host module signing

Status: experimental. Opt-in with `MODULE_SIGNING_ENABLED=true`; off by default.

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
     symlink in the image cannot redirect a write;
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
   - `mokutil`: `mokutil --import` with `MOK_PASSWORD`; MokManager asks for
     the password once at the console;
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

Any signing or enrollment error fails provisioning (`BOOTY-EFI-010`,
`BOOTY-EFI-011`) unless `MODULE_SIGNING_ALLOW_FAILURE=true`.

### The helper in the ramdisk

`mok-enroll.efi` (about 55 KB) is built from
[hack/mok-enroll](../hack/mok-enroll) with gnu-efi in the `mok-enroll` stage
of `initrd.Dockerfile`, for amd64 (`pei-x86-64`) and arm64
(`efi-app-aarch64`), and installed as `/usr/lib/booty/mok-enroll.efi` in the
default, gobgp and slim ramdisks and the base of the split ramdisk. micro
creates no boot entries and has none, so `efi-helper` fails there; use
`external` with micro.

- The build uses general registers only. On amd64, `build.sh` rejects AVX
  instructions: they fault (#UD) in firmware, and some gcc builds default to
  `-march=x86-64-v3`.
- The PE header carries no build time, so the release build and
  `hack/check-reproducible.sh` give the same bytes.
- The ramdisk SBOMs list it as the Debian `gnu-efi` package that it links
  statically. The SBOM attestations of the release cover it with the rest of
  the ramdisk. `docker buildx build --target mok-enroll-efi` exports the
  shipped binary, as the KVM scenarios do.

**Unsigned on purpose.** No key signs the helper. That has these effects:

- The firmware starts it only while Secure Boot is off. With Secure Boot on,
  `BootNext` to it fails and nothing changes. The helper also refuses to
  run when `SecureBoot` reads 1. Enrollment therefore needs Secure Boot off
  from provisioning until the enrollment boot, and the provisioner turns it
  on afterwards (not with `SECUREBOOT_REENABLE`).
- An unsigned helper cannot become a Secure Boot bypass. A signed helper
  would let OS root add any certificate to `MokList` with Secure Boot on,
  through a boot entry and `BootNext`. Never sign it, and do not put it on
  ESPs of hosts that boot with Secure Boot on, except for the enrollment
  boot.
- Anyone with pre-boot code execution while Secure Boot is off can write
  `MokList` with or without this helper. It adds no capability an attacker
  in that window lacks; see [Security analysis](#security-analysis).
- The helper stays on the ESP as `\EFI\BOOTy\mok-enroll.efi` after the
  enrollment, without a boot entry. It does nothing with Secure Boot on. The
  next provisioning overwrites it.

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
The kernel reads those into the `.machine` keyring (Linux 5.18+, when shim
sets `MokListTrustedRT`, the default since shim 15.6) or, on older distro
kernels, into a keyring that verifies modules.

`mok-enroll.efi` writes `MokList` like MokManager does after the user
confirms: same GUID, attributes `NV|BS`, the certificate appended as an X.509
signature list. It removes earlier lists owned by the BOOTy owner GUID
`11378d35-e20e-4116-94d7-758d7540a17f` (lists of exactly one signature), so a
reprovisioned host keeps exactly one BOOTy certificate and other MOK entries
stay. It refuses a malformed `MokList` or payload and then changes nothing. A
`MokList` with other attributes than NV|BS (the OS could have written it) is
discarded, as shim does, and never carried over. The helper refuses to run
when `SecureBoot` is not 0, and deletes `Boot<BootCurrent>` only when its
description is `booty-mok-enroll`.

The helper is unsigned, and it must stay unsigned: signed, it would let OS
root add any certificate to `MokList` with Secure Boot on, through a boot
entry and `BootNext`. `BOOTyMokEnroll` has the runtime attribute, so the OS
can write it too; treat it as a hint, and check the `MokList` event in the
TPM event log (PCR 14) instead.

Tested: Ubuntu 24.04 (shim-signed 1.58+15.8, kernel 6.8.0-139) and Ubuntu
26.04 (shim-signed 1.59+15.8, kernel 7.0.0-34); `MokListTrustedRT` was 1 and
the key appeared in `/proc/keys` (`.machine`). Required versions: shim 15.6
or newer with Linux 5.18 or newer, or a distro kernel that loads
`MokListRT` into a module-verifying keyring. Debian 12 and 13 ship shim
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

The existing `MOKEnroller` path. It needs someone (or a BMC console script)
to confirm in MokManager, so it is a fallback only.

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

## LUKS2 + TPM2 (follow-up)

The key must be sealed to the final state. With `efi-helper`, BOOTy runs
with Secure Boot off and before `MokList` changes, so PCR 7 (Secure Boot
state, PK, KEK, db, dbx, and the authorities shim used) and PCR 14 (MOK)
during provisioning differ from the first Secure Boot boot. Measured in the
KVM scenario (OVMF with the Microsoft keys):

| | PCR 7 | PCR 14 |
|---|---|---|
| Ubuntu 24.04, Secure Boot on | `C86235C7…` | `A46F893D…` |
| Ubuntu 24.04, Secure Boot off | `CDF4FCEF…` | `A46F893D…` |
| Ubuntu 26.04, Secure Boot on | `C86235C7…` | `24BD0348…` |

PCR 7 changes with the Secure Boot state and is the same for both releases
although their MOKs differ: module signatures are checked by the kernel and
are not measured, and shim measures `MokList` into PCR 14. A policy on PCR 7
alone does not notice an extra MOK; PCR 7+14 does.

Proposed order:

1. BOOTy (after `sign-kernel-modules`): `cryptsetup reencrypt --encrypt
   --type luks2 --reduce-device-size 32M` on the root partition after
   shrinking the file system by 32 MiB; slot 0 gets a random recovery key,
   slot 1 a one-time enrollment key in `/etc/booty/luks-enroll.key` on the
   encrypted root; crypttab gets `tpm2-device=auto`.
2. The recovery key goes to the provisioner over a new
   `LUKS_RECOVERY_KEY_URL` (same token and TLS as the status callback). It is
   never logged or put in the report. Escrow is the provisioner's choice.
3. First Secure Boot boot (in the target, after the enrollment boot and the
   switch to Secure Boot on): a one-shot unit runs `systemd-cryptenroll
   --tpm2-device=auto --tpm2-pcrs=7+14 --unlock-key-file=...`, then wipes
   slot 1 and the key file. Until then the root unlocks with the recovery
   key only, so the first boot needs the key once (BMC console or network
   unlock), or the provisioner predicts PCR 7 and 14 with
   `systemd-pcrlock` from the expected variables.

## dm-verity roots

Signing modules changes the root file system after imaging, so it cannot
work with a dm-verity protected root: the root hash would no longer match.
Two ways keep the signed modules outside the verified root:

- **Per-host sysext.** BOOTy builds an EROFS image with
  `/usr/lib/modules/<kernel>/updates/...` and `modules.dep*`, adds a verity
  tree, and signs the root hash with the same per-host key before destroying
  it. `systemd-sysext` verifies the signature against the kernel keyring that
  holds the MOK (`CONFIG_DM_VERITY_VERIFY_ROOTHASH_SIG_SECONDARY_KEYRING`).
  This builds on the signed sysext preload work.
- **Extra partition.** A small partition mounted on
  `/usr/lib/modules/<kernel>/updates`. The kernel still checks each module
  signature, so tampered modules are refused, but `modules.dep` and modprobe
  configuration are not protected.

Modules needed in the initramfs need a per-host initrd addon in both cases.

## Evidence

- `mok-enroll.efi` for amd64 and arm64 under QEMU (TCG) with Debian OVMF and
  AAVMF, from a boot entry with the payload as optional data and `BootNext`.
  These cases pass on both architectures:
  - payload accepted;
  - an existing MOK entry of another owner kept;
  - an earlier BOOTy entry replaced;
  - a `MokList` with the runtime attribute discarded;
  - its own boot entry deleted;
  - `MokList` attributes `NV|BS`;
  - `BOOTyMokEnroll=ok`.

  No arm64 host runs the KVM scenario yet.
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
- The KVM scenarios use the helper from the `mok-enroll-efi` target of
  `initrd.Dockerfile`, the binary the ramdisks ship.

Run it locally on an x86-64 Linux host with KVM, as root:

```sh
sudo apt-get install -y qemu-system-x86 ovmf swtpm python3-virt-firmware zstd efibootmgr
docker buildx build --platform linux/amd64 -f initrd.Dockerfile --target mok-enroll-efi --output type=local,dest=/tmp/mok .
curl -LO https://cloud-images.ubuntu.com/releases/noble/release/ubuntu-24.04-server-cloudimg-amd64.img
sudo env BOOTY_MOK_ENROLL_EFI=/tmp/mok/mok-enroll.efi \
  MIRROR_OS_IMAGE=$PWD/ubuntu-24.04-server-cloudimg-amd64.img \
  go test -tags e2e -run '^TestModuleSigningSecureBootVM$' -v -timeout 50m ./test/e2e/kvm/
```
