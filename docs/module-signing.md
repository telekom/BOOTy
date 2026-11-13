# Per-host module signing

Status: spike. Opt-in with `MODULE_SIGNING_ENABLED=true`; off by default.

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
   - destroy the private key (see [Key handling](#key-handling));
   - install the DER certificate as
     `<MODULE_SIGNING_CERT_DIR>/booty-module-signing.der`
     (default `/usr/share/booty/secureboot`);
   - run `depmod -a <kernel>` and `update-initramfs -u -k all` (or
     `dracut --force --regenerate-all`) in the target chroot.
2. `enroll-module-signing-key` (after the firmware boot entries):
   - `efi-helper` (default): copy `mok-enroll.efi` to
     `\EFI\BOOTy\mok-enroll.efi` on the ESP, create an inactive boot entry
     whose optional data is the certificate as an `EFI_SIGNATURE_LIST`, and
     set `BootNext` to it;
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
   (`SECUREBOOT_REENABLE=true`, Redfish or firmware setup).

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
| (a) `efi-helper`: BootNext to `mok-enroll.efi` while Secure Boot is off | yes | yes, see [evidence](#evidence) | Needs Secure Boot off during provisioning and a firmware reboot. |
| (b) Redfish `SecureBootDatabases/db` by the provisioner | yes | no for modules | The kernel puts `db` certificates in the `.platform` keyring, which verifies kexec images, not modules. Useful for boot loaders only. |
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
`11378d35-e20e-4116-94d7-758d7540a17f`, so a reprovisioned host keeps exactly
one BOOTy certificate and other MOK entries stay. It refuses a malformed
`MokList` or payload and then changes nothing.

Required versions: shim 15.6 or newer with Linux 5.18 or newer, or a distro
kernel that loads `MokListRT` into a module-verifying keyring. The
certificate is a CA with `keyCertSign`, because kernels built with
`CONFIG_INTEGRITY_CA_MACHINE_KEYRING` accept only CA certificates from MOK
into `.machine`.

### (b) db over Redfish

A BMC that implements `SecureBootDatabase` accepts
`POST .../SecureBoot/SecureBootDatabases/db/Certificates` with the PEM from
the report. The firmware then trusts EFI images signed by that key, which a
module key should not do, and the kernel does not use `db` for modules.
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
during provisioning differ from the first Secure Boot boot. The KVM scenario
records PCR 7 with Secure Boot on and off and checks that they differ.
Module signatures are checked by the kernel and are not measured, so a new
per-host MOK changes PCR 14, not PCR 7.

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

- `hack/mok-enroll/mok-enroll.c` under QEMU with upstream OVMF: payload
  accepted, existing MOK entry of another owner kept, earlier BOOTy entry
  replaced, own boot entry deleted, `MokList` attributes `NV|BS`,
  `BOOTyMokEnroll=ok`.
- KVM scenario `module-signing-ubuntu-24.04` and `module-signing-ubuntu-26.04`
  (`TestModuleSigningSecureBootVM`): Ubuntu cloud image with distro shim and
  kernel, OVMF Secure Boot firmware with the Microsoft keys, swtpm. Steps:
  provision with Secure Boot off; enrollment boot; boot with Secure Boot on
  (BOOTy-signed module loads, unsigned and other-host modules are refused);
  boot with Secure Boot off (unsigned module loads, PCR 7 differs);
  `db`-only enrollment (module refused); reprovision (one new certificate in
  `MokList`).

Run it locally on an x86-64 Linux host with KVM, as root:

```sh
sudo apt-get install -y qemu-system-x86 ovmf swtpm gnu-efi gcc python3-virt-firmware zstd efibootmgr
sh hack/mok-enroll/build.sh /tmp/mok-enroll.efi
curl -LO https://cloud-images.ubuntu.com/releases/noble/release/ubuntu-24.04-server-cloudimg-amd64.img
sudo env BOOTY_MOK_ENROLL_EFI=/tmp/mok-enroll.efi \
  MIRROR_OS_IMAGE=$PWD/ubuntu-24.04-server-cloudimg-amd64.img \
  go test -tags e2e -run '^TestModuleSigningSecureBootVM$' -v -timeout 50m ./test/e2e/kvm/
```
