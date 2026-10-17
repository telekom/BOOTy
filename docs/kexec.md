# kexec: Behaviour, Limitations and Drawbacks

This page describes how BOOTy uses kexec to start the installed OS, and every
known limitation and risk of that path. It is reference documentation for
operators and reviewers. It does not propose changes; proposals that change
the kexec path are linked at the end.

Statements about BOOTy behaviour cite the code. Statements about the Linux
kernel, firmware, systemd, GRUB, Flatcar or vendor hardware that were not
checked against a primary source in this repository are marked "verify".

## Contents

1. [How BOOTy uses kexec today](#1-how-booty-uses-kexec-today)
2. [Firmware and hardware state that kexec skips](#2-firmware-and-hardware-state-that-kexec-skips)
3. [Measured boot and attestation](#3-measured-boot-and-attestation)
4. [Secure Boot and kernel lockdown](#4-secure-boot-and-kernel-lockdown)
5. [Boot loader semantics that kexec bypasses](#5-boot-loader-semantics-that-kexec-bypasses)
6. [Console, display and logs](#6-console-display-and-logs)
7. [Memory](#7-memory)
8. [Hardware and driver problems](#8-hardware-and-driver-problems)
9. [Networking and the fabric](#9-networking-and-the-fabric)
10. [Operations: CAPRF, BMC, watchdog, time](#10-operations-caprf-bmc-watchdog-time)
11. [Architecture specifics (arm64)](#11-architecture-specifics-arm64)
12. [Decision table: kexec or reboot](#12-decision-table-kexec-or-reboot)
13. [Test coverage and gaps](#13-test-coverage-and-gaps)

## 1. How BOOTy uses kexec today

### Code path

| Step | What happens | Reference |
|------|--------------|-----------|
| Trigger | A successful provision run (or a successful standby `provision` command) returns `runmode.ProvisionCompleteError`. `runCAPRF` calls `tryKexec`. | `main.go` (`case errors.As(modeErr, &provisionErr)`) |
| Gates | `tryKexec` returns `false` (no kexec) when `DISABLE_KEXEC=true`, when `SUCCESS_ACTION=poweroff` (`KexecDisabled`), when `SECUREBOOT_REENABLE=true`, or when a step changed firmware values (`FirmwareChanged`, today only the Mellanox `NUM_VFS` path). | `main.go` (`tryKexec`), `pkg/config/config.go` (`KexecDisabled`), `pkg/provision/orchestrator.go` (`FirmwareChanged`) |
| Target kept mounted | When kexec is possible, `teardown-chroot` keeps `/newroot` mounted so BOOTy can read the kernel. | `pkg/provision/orchestrator.go` (`ShouldKeepTargetRootMountedForKexec`) |
| Entry selection | BOOTy opens `/newroot/boot/grub/grub.cfg`, parses `menuentry` blocks and their `linux`, `linuxefi`, `linux16`, `initrd`, `initrdefi`, `initrd16` lines, and takes the **first** entry. | `pkg/kexec/grub.go` (`ParseGrubCfg`, `GetDefaultEntry`) |
| Path resolution | The kernel and initrd paths are resolved under the target root, then under `/boot` inside it. Symlinks are followed only inside the target root. | `main.go` (`resolveKexecPath`, `resolveKexecCandidate`) |
| Load | `kexec_file_load(kernel_fd, initrd_fd, cmdline, flags=0)`. | `pkg/kexec/kexec_linux.go` (`Load`) |
| Release target | After the load, BOOTy unmounts every mount below the target root; a busy block device mount is remounted read-only. `KEXEC_UNMOUNT_TARGET=false` keeps it mounted. | `main.go` (`releaseKexecTarget`), `pkg/disk` (`UnmountOrRemountReadOnly`) |
| Flush | BOOTy restores the original log handler and closes the CAPRF log handler. | `main.go` (`doKexecExecute`, `flushObservability`) |
| Execute | `sync()`, then `reboot(LINUX_REBOOT_CMD_KEXEC)`. | `pkg/kexec/kexec_linux.go` (`Execute`) |
| Fallback | Any failure before or in execute returns `false`; BOOTy cleans up the target mounts and reboots through `reboot(2)` (or powers off, see below). | `main.go` (`cleanupFailedKexecTarget`, `handleProvisionHandoff`) |
| A/B rule | With `IMAGE_MODE=ab` and `AB_PRESERVE_EXISTING=true`, a missing kexec leads to a power off, not a reboot, because the firmware boot path still points at the old slot. Config validation rejects `DISABLE_KEXEC`, `SECUREBOOT_REENABLE`, `SUCCESS_ACTION=poweroff` and `CAPRF_LEGACY_COMPAT` with it. | `main.go` (`requiresABKexec`), `docs/ab-partitioning.md` |

### What the entry selection does not do

- It reads only `/boot/grub/grub.cfg`. Targets with `/boot/grub2/grub.cfg`
  (RHEL-likes, verify per release), with BLS entries
  (`/boot/loader/entries/*.conf`, used by GRUB `blscfg` on Fedora and RHEL),
  with systemd-boot, or with UKIs (`EFI/Linux/*.efi`) produce no usable entry,
  and BOOTy reboots.
- It ignores `GRUB_DEFAULT`, `saved_entry` and `next_entry` in `grubenv`, and
  submenu structure. It always takes the first `menuentry` in the file. That
  is usually the newest kernel, but not always the entry that GRUB would boot.
- It does not run GRUB script. Variables in the `linux` line (for example
  `$vt_handoff` on Ubuntu, `${extra_cmdline}`, verify for the target images)
  are passed to the kernel literally. `search`, `set root`, `load_video`,
  `insmod`, `serial` and `terminal` commands are skipped.
- It does not read the GRUB environment block, so `GRUB_CMDLINE_LINUX`
  changes take effect only if `update-grub` regenerated `grub.cfg` (BOOTy runs
  `update-grub` in `configure-grub`).
- Flatcar's `grub.cfg` selects `USR-A` or `USR-B` with `gptprio.next` and
  builds the command line in script. The parser does not model that; see
  section 5.

### When BOOTy does not kexec

| Condition | Result |
|-----------|--------|
| `DISABLE_KEXEC=true` | Reboot. |
| `SUCCESS_ACTION=poweroff` (also via `CAPRF_LEGACY_COMPAT=true`) | Power off. |
| `SECUREBOOT_REENABLE=true` | Reboot. CAPRF turns Secure Boot on. |
| Firmware values changed (Mellanox `NUM_VFS`) | Reboot. |
| No `/boot/grub/grub.cfg`, no entry, kernel or initrd missing | Reboot. |
| `kexec_file_load` fails (for example signature rejected under lockdown) | Reboot. |
| `reboot(LINUX_REBOOT_CMD_KEXEC)` returns | Reboot. |
| A/B preserve-existing and any of the above | Power off. |

## 2. Firmware and hardware state that kexec skips

kexec jumps from the running kernel into a new kernel. The firmware does not
run again. Everything that POST and the firmware would do on a reboot does
not happen.

| Area | What a reboot does | What kexec does | Impact |
|------|--------------------|-----------------|--------|
| POST and device init | Resets and re-initializes devices, trains memory and links | Nothing. Devices stay in the state that the old kernel's `shutdown` callbacks left them in. | Devices with weak or missing `shutdown` handlers start in an unexpected state (section 8). |
| Device shutdown | Power or bus reset | The kernel calls `device_shutdown()` (each driver's `.shutdown`) before it jumps (verify for the BOOTy kernel version). | Quality of `.shutdown` decides success. |
| NIC, HBA, GPU firmware | Restarts on reset | Not restarted. Firmware-level settings (for example ConnectX `NUM_OF_VFS`, `SRIOV_EN`, `LINK_TYPE`) that need a firmware reset stay pending. | BOOTy already refuses kexec after the Mellanox change. Other firmware changes (by vendor tools, by CAPRF through Redfish while the host ran BOOTy, or BIOS changes) are not detected by BOOTy. |
| DMA in flight | Stopped by reset | Must be stopped by drivers. A device that keeps DMA running can write into memory that the new kernel now uses. | Memory corruption that is hard to diagnose (verify; classic kexec risk). |
| IOMMU | Reset, tables rebuilt by firmware and kernel | The old kernel disables translation on shutdown, or the new kernel inherits enabled translation and must handle it (the kdump case copies tables, verify for normal kexec). | DMAR or AMD-Vi faults at boot, devices that fail to probe (section 8). |
| SR-IOV VFs | Gone after reset | PF drivers usually remove VFs in `.shutdown` or `remove` (verify per driver). If not, VFs and their PCI state survive. | Unexpected VFs, wrong `sriov_numvfs`, or failure to create VFs in the target until a reboot. |
| NVMe controllers | Reset | The `nvme` driver shuts the controller down cleanly (verify). Namespace changes from `NVME_NAMESPACES` are already visible because BOOTy rescans. | Usually fine. Firmware activation that waits for a reset (`fw-commit` with action 2, verify) does not happen. |
| Option ROMs | Run by firmware (PXE, RAID setup, GOP) | Not run. | GOP framebuffer from the BOOTy boot is reused; RAID controller BIOS config utilities never run; boot-time controller init that a driver expects from the option ROM is missing (verify per controller). |
| BIOS settings | Applied at the next POST | Not applied. | A BIOS attribute change scheduled through Redfish `@Redfish.Settings` during the BOOTy run takes effect only at the next real reboot. BOOTy cannot see that such a change is pending. |
| Microcode | Firmware applies its microcode; the kernel loads early microcode from the initrd | The new kernel loads early microcode from its own initrd during early boot (verify). The CPU keeps the newest revision already loaded; a downgrade is not possible. | Usually fine. Late loading is disabled by default in recent kernels (verify). |
| ACPI tables | Rebuilt by firmware | The new kernel uses the tables the firmware built for the BOOTy boot. | BIOS changes that alter ACPI tables (for example SR-IOV, C-states, NUMA settings) are not reflected. |
| EFI runtime services | `SetVirtualAddressMap` once per boot | `SetVirtualAddressMap` cannot be called twice. On x86 the kernel passes the old runtime mapping to the new kernel through setup data (verify); if that fails, the new kernel runs without EFI runtime services. | Without runtime services, `efivarfs` is empty in the target: `efibootmgr`, `bootctl`, `fwupd` and the `booty-esp-sync` flows that read variables fail (verify per kernel). |
| SMBIOS | Rebuilt | Same tables as the BOOTy boot. | Changes made by the BMC during the run (for example asset tag) are not visible until a reboot (verify). |
| Syscall choice | n/a | BOOTy uses `kexec_file_load`. The older `kexec_load` lets user space supply the image and purgatory and is blocked by kernel lockdown. | See section 4. |
| `KEXEC_SIG` | n/a | With `CONFIG_KEXEC_SIG`, `kexec_file_load` checks the kernel signature; with `CONFIG_KEXEC_SIG_FORCE` or lockdown it requires a valid one (verify the Debian config of the BOOTy kernel, see `initrd.Dockerfile`). | Unsigned or foreign-signed target kernels fail to load under lockdown; BOOTy then reboots. |

## 3. Measured boot and attestation

| Topic | Behaviour after kexec |
|-------|-----------------------|
| PCR values | The TPM is not reset. PCRs keep the values of the BOOTy boot: firmware (PCR 0 to 7), shim and the BOOTy kernel (PCR 4, 7, 14 when shim is used), and whatever BOOTy or its kernel measured. Nothing measures the new kernel unless the old kernel does it on purpose. |
| Event log | The firmware TCG event log describes the BOOTy boot. Linux can pass the log to the new kernel through the EFI configuration table (verify for the BOOTy and target kernels). Either way, it does not describe the target kernel. |
| IMA | With `CONFIG_IMA_KEXEC`, the IMA measurement list is carried into the new kernel, and PCR 10 continues from the old value (verify that the BOOTy kernel enables IMA at all). |
| systemd-stub | A UKI started by kexec does not run systemd-stub as an EFI application, so PCR 11 (UKI sections), PCR 12 (command line) and PCR 13 (sysexts) are not measured (verify for extracted UKIs). |
| systemd-pcrphase | Extends PCR 11 at boot phases in the target, on top of BOOTy-era values. Predictions made for a firmware boot do not match. |
| systemd's PCR 15 rule | systemd measures machine identity and volume keys into PCR 15 and can bind secrets to PCR 15 being zero (verify the default). After kexec, PCR 15 is whatever the BOOTy boot left. |
| LUKS TPM unlock | A policy bound to raw PCR values (PCR 4, 7, 9, 11, 12) of a firmware boot of the target does not match after kexec, so unlock fails and the system asks for a recovery key. A PCR 7 only policy can still match because Secure Boot state did not change, but only if Secure Boot was on for the BOOTy boot. A signed PCR 11 policy (`--tpm2-public-key`) fails because the stub did not measure the UKI. |
| systemd-pcrlock | Predicts values from firmware boot components. A kexec boot is a variant that pcrlock does not predict (verify), so unlock fails. |
| Remote attestation | A verifier that replays the event log sees the BOOTy chain and cannot attest the target kernel. |
| Conclusion | Hosts that use TPM-sealed disk keys, remote attestation or measured-boot policies must reboot through the firmware after provisioning. The measured boot proposal in PR #700 (`docs/proposal-measured-boot-ab.md`) builds on this. |

## 4. Secure Boot and kernel lockdown

- The firmware and shim verify only what they load: shim, the boot loader
  and the BOOTy kernel. The kexec'd kernel is never seen by the firmware,
  shim, or MOK. Shim's SBAT checks, MOK enrollment and `dbx` revocations do
  not apply to it.
- `kexec_file_load` verifies the new kernel itself when `CONFIG_KEXEC_SIG` is
  set. It checks the PE signature against the kernel's built-in keys, the
  secondary keyring and the platform keyring (UEFI `db` and, where shim
  exports it, MOK, verify the keyring sources per kernel version).
- Kernel lockdown (`integrity` mode) is enabled automatically on Debian and
  Ubuntu kernels when Secure Boot is on (verify the config). Under lockdown:
  `kexec_load` is refused, and `kexec_file_load` requires a valid signature.
- A target kernel signed by a vendor key that is only trusted through shim's
  embedded vendor certificate (for example a distribution key) may not be in
  any keyring of the BOOTy kernel. Then `kexec_file_load` fails and BOOTy
  reboots (verify per target distribution and shim version).
- CAPRF disables Secure Boot through Redfish before it boots BOOTy
  (`disableSecureBoot` step in CAPRF). So today BOOTy usually runs without
  lockdown, and unsigned kexec works. This also means the provisioning boot
  itself is not Secure Boot protected.
- `SECUREBOOT_REENABLE=true` disables kexec because CAPRF turns Secure Boot on
  and the host must boot through the firmware.
- The pinned digests (`SECUREBOOT_*_SHA256`) check files on disk; they do not
  make a kexec boot equivalent to a verified firmware boot.

## 5. Boot loader semantics that kexec bypasses

| Mechanism | Effect of kexec |
|-----------|-----------------|
| UEFI `BootNext`, `BootOrder`, `BootCurrent` | Not used. `BootCurrent` in the target reports the BOOTy boot entry (virtual media or the BOOTy entry), not the installed OS (verify). Tools in the target that read `BootCurrent` draw wrong conclusions. |
| EFI boot entry created by BOOTy | Created, but not exercised. A broken entry is found only at the next reboot. |
| GRUB `grubenv` (`saved_entry`, `next_entry`, `boot_success`, `boot_indeterminate`) | Not read and not updated. One-shot entries stay pending; boot success counters do not move (verify the Fedora boot counting variables). |
| GRUB `gptprio` (Flatcar `USR-A`/`USR-B`) | Priority, tries and successful bits are not evaluated or decremented. Flatcar's fallback after a failed update does not happen. The command line built by Flatcar's `grub.cfg` (verity root hash, `mount.usr`) is not reproduced by BOOTy's parser. |
| systemd-boot boot counting (`+tries`) | The loader does not rename the entry, so the counter does not decrease. `systemd-bless-boot` in the target then marks an entry good that systemd-boot never tried (verify the behaviour when `LoaderBootCountPath` is absent). |
| `LoaderEntryOneShot`, `LoaderEntryDefault` | Not consumed. |
| BOOTy A/B preserve-existing | Kexec boots the new slot, but firmware still points at the old slot. The next firmware reboot goes back to the old slot. This is an implicit trial boot without a commit step ([ab-partitioning.md](ab-partitioning.md)). |
| Kernel command line edits in the boot loader | `GRUB_CMDLINE_LINUX_DEFAULT` changes that need `update-grub` are effective only if `grub.cfg` was regenerated before kexec. |

## 6. Console, display and logs

- Serial console: the new kernel uses the `console=` arguments from the parsed
  `grub.cfg` line. GRUB `serial` and `terminal` commands do not run, so GRUB's
  own serial setup is irrelevant. The UART keeps the baud rate set by the
  BOOTy kernel until the new kernel reprograms it.
- Framebuffer: the GOP or VGA framebuffer from the BOOTy boot is handed over
  through boot parameters (`screen_info`, verify). The firmware does not
  re-initialize the display. GPU drivers that expect a freshly posted device
  can fail (section 8). The BMC KVM can show a frozen or blank screen.
- dmesg: the new kernel starts a new ring buffer. BOOTy's kernel messages are
  lost unless they were shipped before the handoff.
- BOOTy logs: `flushObservability` closes the CAPRF log handler before
  execute, but records that are still buffered when the handler closes may be
  lost (see `pkg/caprf/loghandler.go` drop behaviour).
- pstore: a `ramoops` region in reserved memory survives kexec when both
  kernels reserve the same region (verify). `efi-pstore` needs EFI runtime
  services in the new kernel (section 2).
- No POST screen and no firmware boot messages appear between BOOTy and the
  target kernel. A hang right after the jump shows no output.

## 7. Memory

- The new kernel and initrd are loaded into memory before the jump. A large
  target initrd needs free memory while BOOTy's own initramfs is still in RAM.
- Placement limits: on x86 the initrd must fit where the new kernel's boot
  protocol allows (below 4 GiB unless the kernel supports higher placement,
  verify).
- `crashkernel=` reservations: the BOOTy kernel's reservation is released;
  the target kernel reserves its own region early. Fragmentation after a long
  BOOTy run is not a problem for boot-time reservations, because the new
  kernel builds a fresh memory map, but very large reservations can fail on
  hosts with odd firmware maps (verify).
- KASLR: `kexec_file_load` on x86 lets the new kernel randomize its placement
  (verify for arm64).
- Memory map: the new kernel receives the firmware memory map from the BOOTy
  boot (`e820` on x86, the EFI memory map). Memory that firmware would retrain
  or retire after errors is not re-evaluated.
- Purgatory: a small trampoline verifies a SHA-256 digest of the loaded
  segments before it jumps (verify). A large initrd adds a short delay.
- Memory errors: correctable errors counted by EDAC in BOOTy are lost; the
  target starts counting from zero.

## 8. Hardware and driver problems

All rows are "verify" unless a test in this repository covers them. Record
confirmed cases in the quirks database.

| Component | Known risk after kexec |
|-----------|------------------------|
| NVIDIA GPUs (`nouveau`, proprietary `nvidia`) | The GPU is not posted again; driver init can fail or hang. The proprietary driver documents limited kexec support (verify). |
| Other GPUs and display controllers | Same class of problem; BMC VGA usually works. |
| `mlx5_core` (ConnectX, BlueField) | Firmware state survives; a pending firmware change does not apply (BOOTy refuses kexec after its own change). PCI function reset behaviour after shutdown varies with firmware (verify). |
| `ice` (Intel E810) | Reset and DDP package reload behaviour after a warm handoff (verify). |
| `bnxt_en`, `i40e`, `ixgbe` | Generally robust; firmware settings changes need a reset (verify). |
| `megaraid_sas`, `mpt3sas` | Controllers can need a firmware-level reset; known issues in kdump scenarios (`reset_devices`) suggest care (verify for normal kexec). |
| IOMMU (`intel_iommu`, `amd_iommu`) | DMA remapping and interrupt remapping state from the old kernel; faults at boot when devices still DMA (verify). |
| TPM | Not reset (section 3). Some TPM drivers need a clean `TPM2_Shutdown` sequence (verify). |
| Watchdog devices | A running hardware watchdog keeps counting (section 10). |

## 9. Networking and the fabric

- BOOTy does not tear down its network mode before kexec. The provision
  handoff path calls `tryKexec` directly; `netMode.Teardown` runs only on
  other exit paths (`main.go`).
- GoBGP sessions end without a BGP NOTIFICATION. The leaf detects the loss by
  BFD (fast) or the BGP hold timer. FRR mode daemons die with the old kernel
  in the same way.
- The leaf withdraws the host routes, EVPN Type-2 and Type-5 entries. The
  target OS then establishes its own sessions. The fabric sees one session
  flap per handoff. With BFD this looks like a link failure in fabric
  monitoring.
- Link state: NIC drivers bring the link down in `.shutdown` or not, depending
  on the driver (verify). The switch port can see a short carrier loss, no
  loss, or a longer renegotiation.
- LLDP: BOOTy's LLDP frames stop; the neighbour entry times out on the switch
  until the target OS sends LLDP.
- MAC addresses: physical MACs are unchanged. Bond MACs and VF MACs set by
  BOOTy are not carried over, unless VFs survive (section 2).
- VLAN, VRF, VXLAN devices created by BOOTy disappear with the old kernel.
- Static addresses: the target applies its own config. If BOOTy persisted
  network config (`PERSIST_NETWORK`), it applies after kexec as after a
  reboot.
- Networking parity work: the target-side FRR or networkd setup must accept a
  fabric whose BGP and BFD state just flapped. Graceful restart is not used
  between BOOTy and the target (verify the leaf configuration).

## 10. Operations: CAPRF, BMC, watchdog, time

- Debugging: a hang right after kexec shows no POST, no firmware messages, and
  possibly no console output. The BMC shows the host as on and healthy.
- BMC and Redfish state: `PowerState` stays `On`. `BootProgress` and POST
  codes are not updated (verify per vendor). The BMC does not log a reset.
  Out-of-band tools cannot tell that the OS changed.
- CAPRF expectations: on the `feat/booty-consolidated` branch, CAPRF runs
  `shutdownSystem`, `ejectAllMedia` and `turnOnSystem` after the success
  callback for normal provisions (CAPRF `internal/provision/manager.go`,
  `provisionPostSuccessSteps`). BOOTy kexecs right after the success callback.
  So CAPRF's graceful shutdown (or force off after a timeout) can hit the
  installed OS during its first boot, for example while cloud-init or Ignition
  runs. The speed benefit of kexec is lost in this flow, and the first boot
  can be interrupted. Only A/B preserve-existing provisions skip the shutdown
  (`recordABKexecHandoff`). CAPRF can render `DISABLE_KEXEC=true`
  (`spec.DisableKexec` in CAPRF `internal/ramdisk/builder.go`). Align these
  two behaviours before production use.
- Virtual media: after kexec, the BOOTy ISO can still be inserted. The one-time
  boot override was consumed by the BOOTy boot. If the virtual CD is early in
  `BootOrder`, the next reboot of the installed OS boots BOOTy again (verify
  per vendor). CAPRF ejects media in its post-success steps.
- Standby agent fast cycle: in `MODE=standby`, a `provision` command ends with
  the same handoff. Kexec saves the full POST time (often minutes on servers),
  which is the main reason to keep it ([proposal-agent-mode.md](proposal-agent-mode.md)).
- Watchdog: BOOTy does not arm a watchdog today. If a hardware or BMC watchdog
  is armed (by firmware settings or a future BOOTy feature), it keeps running
  across kexec, and the target must pet it in time, or the host resets.
- Time and RTC: BOOTy does not sync time. The target reads the same RTC; a
  wrong clock stays wrong. Kernel time does not carry over.
- Logs and metrics from the jump itself are lost if the target never comes
  up; there is no firmware-level record.

## 11. Architecture specifics (arm64)

- `kexec_file_load` on arm64 accepts the `Image` format; support for
  compressed or EFI zboot images depends on the kernel version (verify).
- The device tree or ACPI tables come from the BOOTy boot; on ACPI servers
  the same considerations as x86 apply. On device tree systems,
  `kexec_file_load` passes the running DTB with updated `/chosen` (verify).
- EFI runtime services on arm64 use a fixed mapping created once per boot
  (verify the handover).
- BOOTy selects `BOOTAA64.EFI` and `arm64-efi` by `runtime.GOARCH`
  (`pkg/provision/configurator.go`); the kexec path itself has no
  architecture-specific code. No arm64 kexec scenario runs in CI.

## 12. Decision table: kexec or reboot

| Situation | Use | Why |
|-----------|-----|-----|
| Standby agent fast cycle, no firmware or security changes | kexec | Saves POST time. |
| A/B preserve-existing upgrade (today) | kexec (required by BOOTy) | Firmware still points at the old slot; see [ab-partitioning.md](ab-partitioning.md). |
| TPM-sealed disk keys, remote attestation, measured-boot policies | reboot | Section 3. |
| Firmware settings changed (NIC NV config, SR-IOV firmware, disk firmware activation) | reboot | Section 2. BOOTy enforces it only for the Mellanox path. |
| BIOS attributes changed or pending | reboot | Section 2. BOOTy cannot detect pending Redfish settings. |
| Secure Boot strict mode (Secure Boot on during BOOTy, target signed by keys BOOTy's kernel does not trust) | reboot | Section 4. |
| `SECUREBOOT_REENABLE=true` | reboot (enforced) | Section 4. |
| Targets with BLS, systemd-boot, UKI or `/boot/grub2` | reboot (automatic, no entry found) | Section 1. |
| Flatcar | reboot | Section 5 (gptprio, command line). |
| A/B trial boots that rely on boot counting or `BootNext` | reboot | Section 5. |
| GPUs or controllers with known kexec problems | reboot | Section 8. |
| CAPRF flow that shuts the host down after success (branch behaviour) | reboot or power off | Section 10. |

Knobs and recommended defaults:

| Key | Today | Recommendation |
|-----|-------|----------------|
| `DISABLE_KEXEC` | `false` | Set `true` for production provisions through CAPRF until the CAPRF post-success flow and BOOTy agree; keep `false` for standby fast cycles and A/B preserve-existing. |
| `SUCCESS_ACTION` | `reboot` (`poweroff` with legacy compat) | `poweroff` when CAPRF owns the power cycle. |
| `KEXEC_UNMOUNT_TARGET` | `true` | Keep `true`. |
| `SECUREBOOT_REENABLE` | `false` | `true` for Secure Boot hosts; it disables kexec. |

## 13. Test coverage and gaps

| Test | What it covers | Reference |
|------|----------------|-----------|
| `TestKexecHandoffPersistsTargetWritesQEMU` | Real kexec in QEMU from BOOTy into a kernel from the provisioned root; checks that writes made just before the handoff reached the disk and that the journal is clean. KVM scenario `kexec-persist`. | `test/e2e/kvm/kexec_persist_test.go` |
| `TestKexecLoadAndExecute` | Provisions, places a kernel and `grub.cfg`, and checks that BOOTy attempts kexec; accepts a failed attempt in QEMU. | `test/e2e/kvm/provision_gaps_test.go` |
| `TestKexecSmokeQEMU` (scenario `kexec`) | Boots BOOTy in QEMU. It does not perform a kexec. | `test/e2e/kvm/kexec_test.go` |
| `TestReleaseKexecTargetHonorsOption`, `TestReleaseKexecTargetMountsUnknown` | Target release before kexec. | `main_kexec_release_test.go` |
| `TestUnmountOrRemountReadOnly*` | Unmount or read-only fallback. | `pkg/disk/kexec_release_test.go` |
| `pkg/kexec` unit tests | `grub.cfg` parsing, kernel discovery, command line helpers. | `pkg/kexec/*_test.go` |
| A/B preserve-existing KVM scenarios | Upgrade paths that end in kexec. | `test/e2e/kvm/ab_sysext_test.go` |

Gaps:

- No test with Secure Boot on and lockdown active (signed and unsigned target
  kernels).
- No test with a TPM that checks PCR values or sealed unlock after kexec.
- No test with BLS, `/boot/grub2`, systemd-boot or UKI targets (all fall back
  to reboot; the fallback itself is untested for these layouts).
- No test that the first `menuentry` differs from `GRUB_DEFAULT` or
  `saved_entry`.
- No test with GRUB variables in the `linux` line.
- No test with SR-IOV VFs present at kexec (QEMU `igb` with VFs could cover
  part of it, verify).
- No test of EFI runtime services (`efivarfs`) in the target after kexec.
- No test of network teardown behaviour (BGP or BFD flap) at the handoff in
  the ContainerLab labs.
- No arm64 kexec test.
- No test of the CAPRF post-success shutdown racing the kexec'd OS.

## Related pages and proposals

- [caprf-integration.md](caprf-integration.md): end of a run and handoff.
- [ab-partitioning.md](ab-partitioning.md): why A/B preserve-existing needs
  kexec.
- [configuration.md](configuration.md): `DISABLE_KEXEC`,
  `KEXEC_UNMOUNT_TARGET`, `SUCCESS_ACTION`.
- [proposal-kexec-enhanced.md](proposal-kexec-enhanced.md): kernel selection
  and command line management.
- [proposal-agent-mode.md](proposal-agent-mode.md): the standby fast cycle.
- [proposal-tpm-attestation.md](proposal-tpm-attestation.md),
  [proposal-secureboot.md](proposal-secureboot.md),
  [proposal-bootloaders.md](proposal-bootloaders.md).
