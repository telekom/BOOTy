# Decision record: Split optional payloads out of the main ramdisk

## Status

Implemented in BOOTy: option 2 (a second initrd), #782. FRR is the only
split payload today, see [Since then](#since-then). CAPRF does not load
the split archives yet (CAPRF draft MR !311), so no deployment uses them.

## Context

At the time of the decision, three tool sets in the default ramdisk served
only some runs: FRR, `qemu-img` and `ipmitool`. They cost boot time over
slow virtual media and RAM on small hosts. No function was missing.

Sizes at the time of the decision (historical, from
[proposal-artifact-size.md](proposal-artifact-size.md), `zstd -19 --long`,
linux/amd64):

| Payload | Needed when | zstd size | Unpacked (RAM) |
|---|---|---:|---:|
| FRR (`bgpd`, `zebra`, `bfdd`, `vtysh`, `watchfrr`, `libfrr`) | FRR network mode | 4,245,549 | 26,752,616 |
| `qemu-img` with glib and gnutls | a qcow2 image | 2,303,788 | 7,037,160 |
| `ipmitool` with libfreeipmi | `WATCHDOG_IPMI_FALLBACK=true` without a watchdog device | 1,286,081 | 6,444,512 |
| **Total** | | **7,835,418** | **40,234,288** |

The boot time model is in
[size-optimization.md](size-optimization.md#boot-time): a BMC that reads
blocks reads only the files it boots, and a BMC that uploads first (for
example Lenovo XCC "mount from web") copies the whole ISO. At an assumed,
not measured, 2 to 10 MB/s, 7.8 MB is 0.8 to 3.9 s per boot.

## Options considered

1. **One ISO flavour per mode** (for example `gobgp-lean`, `frr-lean`).
   The bytes leave the ISO on every BMC kind, but the flavour count
   doubles (build time, the KVM matrix, release artifacts, digest pins),
   and CAPRF must map each machine to a flavour.
2. **A second initrd that the boot loader loads only for some modes.**
   One build makes a base archive plus one cpio per payload. The kernel
   unpacks every initrd that the boot loader passes, one after the other.
   CAPRF already passes `deploy.cpio` as a second initrd
   ([agent-fast-cycle.md](agent-fast-cycle.md)), so it can add only the
   payloads that the vars need. One build and one test matrix; Secure Boot
   is unchanged, because no stage verifies the initrd in any option.
3. **Fetch the payload over HTTP after the network is up.** FRR cannot
   use this, because FRR mode needs FRR to bring the network up. BOOTy
   would also have to verify code that was not on the boot medium
   (pinned checksum and signature), and air-gapped sites need a mirror.

## Decision

Option 2. It moves FRR, which option 3 cannot; it reuses a mechanism that
CAPRF already runs; it keeps one build, unlike option 1; and it adds no
new trust path, unlike option 3.

Two parts were needed in every option and came with it:

- a preflight error when a run needs a missing payload, and
- a build ID in each payload manifest, checked at start.

## What #782 and #781 built

- `initrd.Dockerfile --target split` writes `base.cpio.zst`,
  `frr.cpio.zst`, the `vmlinuz` of the same build and `SHA256SUMS`
  (`hack/split-initramfs.sh`). The base has no payload executables and
  none of the libraries that only they load. The other targets are
  unchanged: the default ramdisk and the `iso` target still have FRR
  built in.
- Each payload carries `/etc/booty/payloads/<name>` with `build=` and
  `arch=`. The base lists the payloads it left out in
  `/etc/booty/payloads/split` (`pkg/payload`).
- `BOOTY-PAY-001`: the vars need FRR and the boot loader did not load
  `frr.cpio.zst`. It fails where the network stack is chosen, before
  FRR starts, and does not fall back to DHCP.
  `BOOTY-PAY-002`: a payload is from another build or architecture
  ([failure-codes.md](failure-codes.md)).
- PR artifacts (`booty-pr-split`) and releases (`split-<arch>-*`) publish
  the archives. `test/e2e/kvm/split_ramdisk_test.go` boots base + frr +
  vars and base + vars.
- How to load the archives is in the [README](../README.md) (`split`
  target).

## Since then

#815 converts qcow2 to raw inside BOOTy, and #816 drives the BMC watchdog
over `/dev/ipmi0`. The ramdisk no longer ships `qemu-img` or `ipmitool` in
any flavour, so they are no longer payloads. FRR is the only split payload.

Sizes in v0.2.0-rc.19 (release assets):

| Asset | amd64 | arm64 |
|---|---:|---:|
| `default` ramdisk (FRR built in) | 38,446,564 | 35,507,504 |
| `split` `base.cpio.zst` | 33,693,932 | 30,806,631 |
| `split` `frr.cpio.zst` | 4,827,857 | 4,756,433 |
| `gobgp` ramdisk (no FRR) | 33,676,587 | 30,806,237 |

A run without FRR on the split build reads 4.75 MB (amd64) less than on
the default ramdisk. The base is about the size of the gobgp flavour, as
expected.

## Open questions, answered

- *Is GoBGP the only production mode, so that the gobgp flavour already
  removes FRR?* The split still pays off for any site that runs both modes
  from one build. Which mode each site runs is a CAPRF deployment choice
  and not recorded here.
- *How many hosts use qcow2 or the IPMI watchdog fallback?* No longer
  relevant: #815 and #816 removed the tools, and both functions work
  without them.
- *Which BMCs upload the whole ISO first?* Not measured. On those BMCs the
  split saves time only when the ISO leaves out `frr.cpio.zst`; the
  published `iso` target still has FRR built in.
