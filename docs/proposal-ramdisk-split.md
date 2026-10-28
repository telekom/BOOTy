# Proposal: Split FRR, qemu-img and ipmitool out of the Main Ramdisk

## Status

Proposal. Nothing is implemented. The user approves an option before any
implementation starts.

## Priority

P2. The gain is boot time over slow virtual media and RAM on small hosts;
no function is missing today.

## Summary

Three tool sets in the default ramdisk serve only some runs:

| Payload | Needed when | zstd size | Unpacked (RAM) |
|---|---|---:|---:|
| FRR (`bgpd`, `zebra`, `bfdd`, `vtysh`, `watchfrr`, `libfrr`) | FRR network mode (`asn_server` with `underlay_ip` or `underlay_subnet`) | 4,245,549 | 26,752,616 |
| `qemu-img` with glib and gnutls | a qcow2 image (`pkg/image`, checked before the disk wipe) | 2,303,788 | 7,037,160 |
| `ipmitool` with libfreeipmi | `WATCHDOG_IPMI_FALLBACK=true` without a watchdog device | 1,286,081 | 6,444,512 |
| **Total** | | **7,835,418** | **40,234,288** |

Sizes from [proposal-artifact-size.md](proposal-artifact-size.md)
(`zstd -19 --long`, linux/amd64). #757 packs with `-22` and groups files
by kind, so the real numbers differ by a few percent. An implementation
measures them again.

The `gobgp` flavour already has no FRR. It keeps `qemu-img` and
`ipmitool`. The `slim` flavour has none of the three, and it has no BGP.

This proposal compares three ways to deliver the payloads only when a
run needs them, and recommends one.

## Boot time model

From [size-optimization.md](size-optimization.md#boot-time). There are
two kinds of BMC:

- **Block read** (iDRAC remote file share, iLO, Supermicro over HTTP, NFS
  or CIFS): the firmware reads only the files it boots. Time = bytes read
  / `B`.
- **Upload first** (for example Lenovo XCC "mount from web"): the whole
  ISO goes to the BMC before the boot. Time = ISO bytes / `B_up`, plus a
  fast local read.

**Assumption, not measured:** `B` and `B_up` are 2 to 10 MB/s. In-kernel
decompression of 7.8 MB of zstd takes well under 0.1 s, so it is left out.

Time saved per boot when a run does not need a payload:

| Payload left out | Bytes | 2 MB/s | 5 MB/s | 10 MB/s |
|---|---:|---:|---:|---:|
| all three | 7.8 MB | 3.9 s | 1.6 s | 0.8 s |
| FRR only | 4.2 MB | 2.1 s | 0.85 s | 0.4 s |
| qemu-img and ipmitool | 3.6 MB | 1.8 s | 0.7 s | 0.4 s |

Whether a saving applies depends on the option and on the BMC kind. The
"Boot time" row of each option says which bytes leave the boot path.

## Option 1: one ISO flavour per mode

Build more flavours. For example, `gobgp-lean`: GoBGP without FRR,
`qemu-img` or `ipmitool`. Or `frr-lean`: FRR without `qemu-img` or
`ipmitool`. CAPRF selects the flavour per machine.

- **Boot time:** the bytes are not in the ISO at all, so the saving above
  applies on both BMC kinds. `gobgp` against the default flavour already
  saves the FRR row today.
- **Secure Boot:** no change. shim and GRUB verify the kernel; no stage
  verifies the initrd, in any option. Every flavour uses the same signed
  kernel.
- **CAPRF contract:** CAPRF must map each machine to a flavour from its
  vars (network mode, image format, watchdog fallback) and know every
  flavour's artifact. Each new flavour adds a release artifact and a
  digest pin.
- **Failure modes and fallback:**
  - A qcow2 image on a flavour without `qemu-img` fails before the disk
    wipe (`ValidateStreamingPrerequisites`), so no data is lost.
  - FRR mode on a flavour without FRR falls back to DHCP (networking.md,
    stack order). On an EVPN-only fabric, DHCP then fails late and the
    cause is unclear. This needs a new preflight error: "FRR mode
    requested, FRR not in this ramdisk".
  - `WATCHDOG_IPMI_FALLBACK` without `ipmitool` leaves the host without a
    watchdog. This must also fail or warn with a code.
  - The fallback is that CAPRF boots the full flavour again. That is one
    more cold cycle.
- **Ops impact:** the flavour count doubles (build time, the KVM matrix,
  release size, docs). Operators must know which flavour a host got, and
  each flavour is a variant to debug.

## Option 2: a second initrd that the boot loader loads only for some modes

The base ramdisk ships without the payloads. Each payload is its own cpio
(`frr.cpio.zst`, `qemu-img.cpio.zst`, `ipmitool.cpio.zst`) from the same
build. The kernel unpacks every initrd that the boot loader passes, one
after the other:

- ISOLINUX: `initrd=/boot/initrd.img,/boot/frr.cpio.zst`
- GRUB: `initrd /boot/initrd.img /boot/frr.cpio.zst`
- iPXE: one `initrd` line per file

CAPRF already uses this. Its ISO builder adds `deploy.cpio` (the
`/deploy` vars) as a second initrd (agent-fast-cycle.md, "Running from
RAM"). It writes the boot loader config per machine, so it can add only
the payloads that the vars need.

- **Boot time:**
  - Block read: only the listed files are read, so the saving applies even
    when the ISO holds every payload.
  - Upload first: the saving applies only when CAPRF leaves the unused
    payload files out of the per-machine ISO. CAPRF builds a per-machine
    ISO already, so this is a file list, not a new step. The published
    all-in-one ISO does not gain on these BMCs.
- **Secure Boot:** no change. Like the main initrd, the payload cpios are
  not verified by shim or GRUB, and the kernel lockdown does not cover
  userspace binaries. The trust is the same as today: whoever can change
  the ISO can change the initrd.
- **CAPRF contract:**
  - The release publishes the base initrd plus one cpio per payload, with
    digests.
  - The CAPRF ISO builder adds the payload initrds from the vars, with the
    same mapping as option 1 but per file, not per flavour.
  - The published BOOTy ISO lists all payloads, so it works without CAPRF.
- **Failure modes and fallback:**
  - A missing payload has the failure modes of option 1, with the same
    preflight errors.
  - A payload from another build has an ABI mismatch with the base
    libraries. Each payload carries a manifest with the build ID; BOOTy
    checks it at start and fails with a code on a mismatch, before the
    network setup.
  - The fallback is that CAPRF rebuilds the ISO with every payload. This
    is also the safe default for unknown vars.
- **Ops impact:**
  - One build and one test matrix: the KVM tests boot base plus all
    payloads, and a few cases boot without.
  - The release gains three small artifacts.
  - Debugging is the same: `ls /deploy`, and the manifest is in the debug
    dump.

## Option 3: fetch the payload over HTTP after the network is up

The base ramdisk ships without the payloads. When a run needs one, BOOTy
downloads it from CAPRF or the image server, checks it and unpacks it.

- **FRR cannot use this option.** FRR mode needs FRR to bring the network
  up, so FRR would stay in the base. Only `qemu-img` and `ipmitool` (3.6
  MB) move.
- **Boot time:** the saving for 3.6 MB applies on both BMC kinds. The
  download goes over the data network, at about 100 MB/s or more, which
  takes well under 0.1 s. It does not go over virtual media.
- **Secure Boot:** BOOTy runs code that was not on the boot medium, so it
  must check the code itself:
  - a SHA-256 per payload, pinned in the BOOTy build;
  - and a detached signature with a key embedded in BOOTy (`gpgv` is
    already in the ramdisk).
  - Lockdown does not help here. Without the pinned checksum, the server
    could inject code.
- **CAPRF contract:** a new var for the payload base URL (or a fixed path
  next to the image), and CAPRF or the artifact mirror serves the
  payloads of every BOOTy release in use.
- **Failure modes and fallback:**
  - The server cannot be reached, or a proxy blocks it. BOOTy retries,
    then fails with a code before the disk wipe.
  - A wrong checksum or signature fails closed with a code.
  - The IPMI watchdog fallback is armed before the network is up today. It
    would be armed only after the download, so the early boot has no
    watchdog.
  - Air-gapped sites need a mirror. There is no fallback without one.
- **Ops impact:** a new artifact host per site, versioned with the
  ramdisk, plus the mirror and proxy setup and one more download per run
  to monitor.

## Comparison

| | Option 1: flavours | Option 2: second initrd | Option 3: HTTP fetch |
|---|---|---|---|
| Bytes off the boot path | up to 7.8 MB | up to 7.8 MB | 3.6 MB (FRR stays) |
| Upload-first BMC (XCC) | gains | gains when CAPRF omits unused files | gains |
| Secure Boot | no change | no change | new: checksum and signature in BOOTy |
| CAPRF change | flavour mapping, more artifacts | payload list in the ISO builder | payload URL, hosting |
| Wrong selection | full extra cycle | rebuild ISO, full extra cycle | none, the download is per run |
| New failure mode | none | build mismatch (manifest check) | network, proxy, mirror |
| Build and test cost | doubles | small | small |

## Recommendation

Option 2: one build, a base initrd and one cpio per payload, selected by
the CAPRF ISO builder from the vars.

- It moves all 7.8 MB, including FRR, which option 3 cannot.
- It uses a mechanism that CAPRF already runs for `deploy.cpio`.
- It keeps one build and one test matrix, unlike option 1.
- It adds no new trust path for Secure Boot, unlike option 3.

Two parts are needed in every option and should come first:

1. Preflight errors with codes when a run needs a missing tool: FRR mode
   without FRR, qcow2 without `qemu-img` (exists), the IPMI watchdog
   fallback without `ipmitool`.
2. A build ID in each payload manifest, checked at start.

Open questions before an implementation:

- Which flavour and network mode does production use per site? If it is
  only GoBGP, the `gobgp` flavour already removes FRR, and only 3.6 MB
  remain to split.
- How many production hosts use qcow2 images or the IPMI watchdog
  fallback? If none do, dropping `qemu-img` and `ipmitool` from one
  production flavour could be enough.
- Which BMCs in production upload the whole ISO first? Their share
  decides how much the CAPRF file list matters.
