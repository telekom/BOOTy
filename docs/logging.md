# Logging

This page is the logging reference of every BOOTy binary: how to set the log
level and format, where the lines go, and what each level logs. The
[message catalog](#message-catalog) at the end lists every log message of
the code by level and package; a unit test keeps it in sync with the code.

## Binaries and settings

| Binary | Runs | Level setting | Format setting | Default |
|--------|------|---------------|----------------|---------|
| `booty` (`/init` of the ramdisk) | as PID 1 in the provisioning ramdisk | `LOG_LEVEL` in `/deploy/vars` or on the kernel command line | `LOG_FORMAT` in `/deploy/vars` or on the kernel command line | `info`, `text` |
| `booty lldp neighbors\|status` | rescue shell of the ramdisk | `LOG_LEVEL` environment variable | `LOG_FORMAT` environment variable; the command output itself is text or `--json` | `info`, `text` |
| `booty-esp-sync` | `booty-esp-sync.service` in the installed OS, when the ESPs are mirrored | `--log-level` flag, else the `LOG_LEVEL` environment variable | `--log-format` flag, else the `LOG_FORMAT` environment variable | `info`, `text` |

Levels: `debug`, `info`, `warn` (alias `warning`), `error`, in any letter case. Each level
includes the levels above it: `warn` logs `WARN` and `ERROR` lines. Formats:
`text` and `json`.

The `cmd/` Cobra package (`booty provision --config ...`) is a library that no
binary builds (see [Configuration sources](configuration.md#configuration-sources));
it prints with `fmt` and has no log settings.

### `booty` (ramdisk agent)

BOOTy reads the settings twice:

1. At start, from its process environment. The kernel passes unknown
   `name=value` parameters of the kernel command line to init as environment
   variables, so `LOG_LEVEL=debug` on the ISO's kernel command line applies
   from the first log line on. An invalid value logs
   `ignoring log setting` (WARN) and uses the default.
2. Before the full parse of `/deploy/vars`. A `LOG_LEVEL` or `LOG_FORMAT`
   entry there wins over the environment. An invalid value logs
   `ignoring log setting` (WARN) and uses the default for that setting; it
   never stops the run.

```bash
# /deploy/vars
LOG_LEVEL=debug
LOG_FORMAT=json
```

`LOG_LEVEL` applies to the console and to `LOG_URL`. Loggers that packages
keep (`component=dhcp`, `component=provision`, ...) share the level, so a
level from the vars file reaches them too. A `LOG_FORMAT` from the vars
file does not: the few loggers that BOOTy creates before it reads the vars
file (the out-of-band status reporter, the block device watch) keep the
format of the environment, so with `LOG_FORMAT=json` only in the vars file
their lines stay text. Set `LOG_FORMAT` on the kernel command line for a
console that is JSON from the first line. The vars keys are listed in
[Configuration: Logging](configuration.md#logging).

### `booty-esp-sync`

The provisioning installs the service with no log flags, so it logs at
`info` in `text`. Change it on the installed OS with a drop-in:

```ini
# systemctl edit booty-esp-sync.service
[Service]
Environment=LOG_LEVEL=debug
Environment=LOG_FORMAT=json
```

`text` keeps the line format of the Go `log` package:
`2026/11/13 10:00:00 INFO synchronizing EFI system partition source=/boot/efi destination=/run/booty/esp-sync`.
journald stores stderr. An invalid level or format logs
`ignoring log setting` (WARN) and uses the default, so a typo does not make
systemd restart the service in a loop. A fatal error logs
`booty-esp-sync failed` (ERROR) and exits with 1; systemd restarts the
service.

## Destinations of the ramdisk agent

| Destination | What | Level | Format |
|-------------|------|-------|--------|
| Console (stderr of PID 1, `/dev/console`) | every log line | `LOG_LEVEL` | `LOG_FORMAT` |
| `LOG_URL` | every log line, one HTTP POST per line, in order | `LOG_LEVEL` | always `[LEVEL] message key=value ...`, independent of `LOG_FORMAT` |
| `DEBUG_URL` | the kernel ring buffer, once, after the vars file is read | not leveled | plain text |
| Active consoles (`/sys/class/tty/console/active`) | `BOOTY1` out-of-band status lines at each phase change, see `OOB_STATUS` | not leveled | fixed `BOOTY1 key=value` |
| stdout | the start banner and the system summary (`ux.Captain`, `ux.SysInfo`), and a screen clear (`ESC[2J`, `utils.ClearScreen`) | not leveled | plain text |
| stdout, stderr | output that does not go through slog: the image download progress (`\rDownloading... N MB complete`, `pkg/image` `tickerProgress`, ended by an empty line in the stream and qcow2 paths), `dd status=progress` of the partition copy (A/B and partition images), the output of the FRR daemon start (`frrinit.sh`, `systemctl`), and the rescue mode messages (`SSH daemon started ...`, `Mounted ...`, `Auto-mounted N disk(s) ...`) | not leveled | plain text |

`/dev/console` is the last `console=` of the kernel command line, so the
log lines reach that one console: the serial console with
`console=ttyS0,115200` last, the VGA console with `console=tty0` last. The
`BOOTY1` status lines go to every active console. The kernel's
own messages follow the kernel `loglevel=` (the ISO boots with `loglevel=4`:
only kernel errors); `DEBUG_URL` gets the whole ring buffer, and BOOTy logs
the kernel's Secure Boot and lockdown lines as `kernel security state`
(INFO).

With `LOG_FORMAT=json`, the console is not pure JSON: the lines in the two
rows above, the kernel messages and the `BOOTY1` lines are plain text. A
JSON consumer of the serial console must skip lines that are not JSON
objects.

`LOG_URL` details (buffer, drop order, flush before kexec, reboot or power
off) are in [Configuration](configuration.md#caprf-endpoints-authentication-and-transport)
(`LOG_URL`, `LOG_FLUSH_TIMEOUT_SEC`).

Redaction: the console handler redacts every log line from the first one
on, with or without `LOG_URL`. It replaces the value of an attribute whose
key (or group) names a secret (`token`, `password`, `secret`, `credential`,
`authorization`, `private`, `session`, `auth`, `cert`, `key`, ...) with
`[REDACTED]`, and removes JWTs, PEM private keys, LUKS key dumps,
`Authorization` headers, bearer tokens, URL credentials and `password=`,
`token:`-style values from the message and from every string or error
value. The `LOG_URL` handler applies the same rules to the line it ships.
Output that bypasses slog (the stdout and stderr rows above) is not
redacted.

The `pkg/logging` Kafka handler is not wired into any binary.

## Line format and fields

`text` (default):

```text
time=2026-11-13T10:00:00.000Z level=INFO msg="provisioning step" component=provision step=stream-image index=5 total=21
```

`json`, one object per line:

```json
{"time":"2026-11-13T10:00:00.000Z","level":"INFO","msg":"provisioning step","component":"provision","step":"stream-image","index":5,"total":21}
```

Every line has `time` (RFC 3339, the ramdisk clock: before the
[time sync](configuration.md#time-sync) it is the RTC time), `level`
(`DEBUG`, `INFO`, `WARN`, `ERROR`) and `msg` (the catalog entry). Then
the attributes of the call. Common attributes:

| Attribute | Meaning |
|-----------|---------|
| `component` | Logger of a package: `caprf`, `provision`, `dhcp`, `frr`, `gobgp` (the GoBGP library), `lldp`, `timesync`, `crash`, `drivers` |
| `tier`, `mode` | GoBGP stack part: `tier=underlay`, `tier=overlay`, `mode=gobgp` |
| `step`, `index`, `total` | Provisioning or deprovisioning step |
| `error` | The error of a failed operation |
| `code` | A failure code such as `BOOTY-TIM-001`, see [Failure codes](failure-codes.md) |
| `iface`, `nic`, `device`, `disk`, `url`, `peer` | The object of the line |

The GoBGP library logs its own messages through the `component=gobgp`
logger; they are not in the catalog and follow `LOG_LEVEL` like every
line.

## What each level logs

Choose `info` for production, `debug` to diagnose a network or disk
problem, `warn` to see only what went wrong or was skipped. The lists name
the kinds of messages per area; the catalog has every message.

### ERROR

A run, a mode or a part of the run failed. Most ERROR lines are followed
by the error status to `ERROR_URL` and the `FAILURE_ACTION` (reboot, wait,
power off); the image wipes and the time lines are not.

- Stages: `provisioning step failed`, `deprovisioning step failed` (with
  `step` and `error`), `run failed`, `run stopped`, `mode exited with error`,
  the failed modes of `pkg/runmode` (`provisioning failed`,
  `deprovisioning failed`, `health check failed`, `dry-run failed`,
  `hot provision failed`, standby `command not run`), checkpoint load and
  save errors (`BOOTY_RESUME`), a failed handoff (kexec, reboot, power off),
  `unknown operating mode`.
- Configuration and CAPRF: the vars file does not parse
  (`failed to create CAPRF client`), the config fetch, the JWT exchange and
  renewal (`failed to acquire jwt token`, `token renewal failed for good`,
  `token expiry reached`), and a failed error status report.
- Network: DHCP, static, VLAN, FRR and GoBGP setup failures, the
  connectivity check that ran out of retries, a BGP peer that stays down,
  the LLDP listener panic.
- Disk and image: a write, sync or checksum failure of the image, with the
  wipe of the partial image that follows; a disk that is in use and can not
  be discarded.
- Time: no time source and a clock before the build of BOOTy (TLS will
  fail), and conflicting NTP and CAPRF `Date` times.
- Watchdog: `hardware watchdog not armed` and the step that made no progress
  within its budget (the hardware then resets the host).
- Power: a failed reboot, power off, halt, suspend or rescue shell
  (`pkg/realm`).
- `booty-esp-sync`: the fatal error before the exit, and a failed
  synchronization or marker write of the ESP watcher.

### WARN

Something went wrong, was skipped or degraded, and the run goes on; and the
debug dumps.

- Debug dumps: after a failure BOOTy logs the state of the host between
  `=== DEBUG DUMP START ===` and `=== DEBUG DUMP END ===` (and the FRR dump
  `=== FRR STATE DUMP START ===`), one `debug` line per output line with
  `label` and `data`, and `=== CONFIG DUMP ===` with the main config values
  (image URLs redacted).
  They are WARN so that they reach `LOG_URL` at the default level.
- Retries: HTTP and OCI download retries, CAPRF request retries, renewal
  attempts, NTP queries that failed.
- Network: links that do not come up or can not be renamed, lost DHCP
  leases, LLDP frames that are not sent or not received, BFD sessions that
  are down or do not start, FRR and netplan statements that are skipped
  (`FRR_CONFIG_STRICT=false`, `NETPLAN_STRICT=false`), and the firmware LLDP
  agent of Intel NICs that could not be stopped.
- Disk: wipe and discard steps that failed (the wipe goes on), block devices
  that are still changing at the settle timeout, failed secure erases with
  `SECURE_ERASE_FALLBACK=wipe`, duplicate filesystem UUIDs, disk encryption
  that is skipped (`DISK_ENCRYPTION_ALLOW_FAILURE`), crypto-erase turned off
  (`DEPROVISION_CRYPTO_ERASE=false`) and devices that the LUKS2 header scan
  can not read.
- Boot and Secure Boot: EFI boot entry problems that do not stop the run
  (`EFI_BOOT_ENTRY_REQUIRED=false`, missing EFI variables), shim and
  PE/COFF candidates that fail validation, a Secure Boot state that can not
  be read, and kernel module signing or MOK enrollment that failed with
  `MODULE_SIGNING_ALLOW_FAILURE=true`.
- Hardware: SR-IOV drift with `SRIOV_ON_DRIFT=warn`, health check warnings,
  inventory and firmware report failures, crash artifact collection and
  upload failures.
- Configuration: deprecated or ignored keys (for example BGP keys next to
  BGP sessions), `SKIP_TLS_VERIFY` and plain HTTP transport, missing report
  URLs, and `ignoring log setting`.
- `LOG_URL` itself: `remote log handler is dropping entries: buffer full` on
  the console; the server gets one `[WARN] remote log handler dropped lines
  code=BOOTY-LOG-001` line after a gap (written by the handler, not a log
  call, so not in the catalog).

### INFO

The progress of the run. At the default level the console and `LOG_URL`
show one line per stage and per notable result:

- Start: `starting BOOTy` (`version`, `build`, `uptime`),
  `kernel security state`, `CAPRF mode active` (`hostname`, `mode`,
  `image_count`), `executing mode`, `CAPRF run complete`.
- Stages: `provisioning step` and `deprovisioning step` (`step`, `index`,
  `total`), `skipping completed step` (`BOOTY_RESUME`), `step started` and
  `step ended` with the duration, `dry-run check` results.
- Network: the network mode, DHCP leases (`DHCP succeeded`, `DHCP lease
  applied`, carrier changes), static and VLAN interfaces, FRR start and
  peers, GoBGP server start and stop, established BGP sessions, routes
  announced, BFD session state changes, LLDP neighbors (new, changed, left)
  and what BOOTy sends, connectivity results.
- Disk: the selected disk, RAID arrays, wipes and secure erases, partitions
  that are grown, the `/dev/disk` links, LUKS volumes, LUKS2 + TPM2 disk
  encryption (format, enrollment, recovery key handover, the encrypted
  image stream) and the crypto-erase of LUKS2 headers at deprovision.
- Image: the source, the download and write progress, checksum and GPG
  signature results, qcow2 conversion, A/B slot streaming, the memory peak
  of the image step.
- Boot handoff: EFI boot entries, GRUB and kernel command line, kexec load
  and execute, reboot and power off, the watchdog handoff.
- Kernel module signing: the signed out-of-tree modules and the certificate
  (path, SHA-256), and how it is enrolled (`mok-enroll.efi` staged,
  MokManager queued, or left for the provisioner).
- Time, firmware and reports: clock synchronization and skew, NIC firmware
  versions (`NIC_FIRMWARE_LOG`), SR-IOV settings and compliance, check
  results, the provisioning report, the previous run result
  (`OOB_EFI_VAR`).
- JWT: `jwt acquired`, `jwt renewed`.
- `booty-esp-sync`: `synchronizing EFI system partition` (`source`,
  `destination`) and `peer EFI system partition is back`.

### DEBUG

Detail for diagnosis; not shipped at the default level.

`LOG_LEVEL=debug` is chatty with GoBGP/EVPN: every route update, FDB and
neighbor change is a line, and DHCP logs each packet. With `LOG_URL` each
line is one HTTP POST to CAPRF. The buffer holds 8192 lines or 4 MiB; when
it is full it drops the oldest DEBUG lines first, then INFO, so the WARN and
ERROR lines survive, and `LOG_URL` gets a `BOOTY-LOG-001` line with the
counts. Use debug for a diagnosis run, not as the fleet default.

- DHCPv4 and DHCPv6 packets: every send and receive with the message type,
  `ciaddr` and wait, the bound lease (`addr`, `server`, `expire`), RELEASE,
  and the `SOL_MAX_RT` and `INF_MAX_RT` options.
- IPv6 router solicitations, address and route deletions, skipped NICs and
  links during the DHCP scan, failed connectivity probes before the retry
  gives up.
- BFD packets that are dropped or can not be read or sent.
- GoBGP EVPN route processing: each type-2, type-3 and type-5 route that is
  installed, skipped (route target mismatch, invalid next hop, MAC, prefix
  or VTEP) or withdrawn, and FDB and neighbor entries that are added or
  deleted.
- FRR cleanup steps that fail because the object is gone.
- Netplan parsing: the bonds, VLANs and default gateways found.
- Disk: waits for device nodes and serial numbers, the filesystem grow
  fallbacks (`resize2fs`, `xfs_growfs`, btrfs), missing `lvm` or `mdadm`,
  efivarfs in the chroot.
- Agent internals: reaped orphan processes, kernel modules that are not
  there, skipped report URLs, unchanged UEFI variables, retried steps,
  post-provision command output, the LLDP link watch.

## Message catalog

The catalog lists the message (`msg`) of every log call of the code, by
level and package (`.` is the ramdisk agent's `main` package; the
`booty-esp-sync` binary is `cmd/booty-esp-sync` and `pkg/espsync`). Every
message is a literal; the test fails on a computed one. A wrapper that logs one of its
parameters (`pkg/disk` `logStepFailure`) is listed with the message of each
call site.

`TestLoggingDocCatalog` in `logging_doc_test.go` scans the Go files (not
tests, `test/`, `hack/`) for `slog` level calls and fails when this block
differs from the code. After adding, changing or removing a log call, run:

```bash
UPDATE_LOGGING_DOC=1 go test -run TestLoggingDocCatalog .
```

and describe a new kind of message in [What each level logs](#what-each-level-logs).

<!-- log-catalog:start -->
<!-- Generated by TestLoggingDocCatalog (UPDATE_LOGGING_DOC=1). Do not edit. -->

### ERROR (64 messages)

#### ERROR `.`

- `DHCP setup failed`
- `FRR network setup failed, falling back to DHCP`
- `a/b preserveExisting requires kexec; refusing normal reboot because firmware boot state still points at the active slot`
- `applying provisioner files to the ramdisk failed, waiting`
- `early mount/device setup failed`
- `failed to acquire jwt token`
- `failed to create CAPRF client`
- `failed to get CAPRF config`
- `failed to start token renewal`
- `gobgp network setup failed`
- `handoff failed and the network teardown still runs, not setting the network up again`
- `hardware watchdog not armed`
- `mode exited with error`
- `network connectivity failed after all retries`
- `network connectivity timeout`
- `network setup after the failed handoff did not finish in time`
- `network setup after the failed handoff failed`
- `network setup failed`
- `no connectivity after the failed handoff`
- `provisioning handoff failed after success report`
- `run failed`
- `run stopped`
- `static network setup failed, falling back to DHCP`
- `token can not be renewed, stopping the task`
- `unknown operating mode`
- `vlan setup failed`

#### ERROR `cmd/booty-esp-sync`

- `booty-esp-sync failed`

#### ERROR `pkg/auth`

- `token renewal failed for good`

#### ERROR `pkg/caprf`

- `token expiry reached`

#### ERROR `pkg/disk`

- `disk is in use, not discarded`

#### ERROR `pkg/espsync`

- `EFI synchronization failed`
- `write EFI synchronization marker`

#### ERROR `pkg/image`

- `checksum mismatch: wiping partition table to prevent booting corrupt image`
- `image sync failed: attempting to wipe partial image`
- `image write failed: wiping partial image`
- `qcow2 image sync failed: attempting to wipe partial image`
- `qcow2 write failed: wiping partial image`

#### ERROR `pkg/linkcheck`

- `check result not encoded`

#### ERROR `pkg/network/frr`

- `FRR dump command failed`

#### ERROR `pkg/network/gobgp`

- `BGP peer deleted but not added again, it stays down`
- `watch best paths`

#### ERROR `pkg/network/lldp`

- `panic in LLDP listener goroutine`

#### ERROR `pkg/provision`

- `deprovisioning step failed`
- `failed to load checkpoint`
- `failed to report error status`
- `failed to save checkpoint`
- `no 2xx for the reboot-required status, rebooting anyway; the host may not boot BOOTy again`
- `provisioning step failed`

#### ERROR `pkg/realm`

- `halt failed`
- `power off failed`
- `reboot failed`
- `shell error`
- `suspend failed`

#### ERROR `pkg/runmode`

- `command not run`
- `deprovisioning failed`
- `dry-run failed`
- `health check failed`
- `hot deprovision failed`
- `hot provision failed`
- `provisioning failed`

#### ERROR `pkg/timesync`

- `NTP time differs from the authenticated CAPRF Date time, using the Date time`
- `no time source answered and the clock is before the build of BOOTy; TLS certificate checks will fail`
- `refusing to set a plausible clock back on the CAPRF Date header alone`

#### ERROR `pkg/watchdog`

- `step made no progress within its budget, stopping the watchdog keepalive; the hardware resets the host`

### WARN (383 messages)

#### WARN `.`

- `LLDP not restarted: the senders of the last stop are still running`
- `LLDP not sent: no physical NICs`
- `LLDP senders did not stop in time, handing off without waiting`
- `NICs that appear later send no LLDP`
- `failed to clean up target root after unsuccessful kexec`
- `failed to detect IPMI info`
- `failed to list child processes`
- `failed to reap child process`
- `failed to report provisioning handoff error`
- `failed to report the run failure`
- `failed to report the token failure`
- `failed to set PATH`
- `firmware LLDP agent not disabled`
- `firmware LLDP agent not disabled: SR-IOV ports unknown`
- `firmware LLDP agent not disabled: no physical NICs`
- `handoff failed after the network stop, setting the network up again`
- `ignoring log setting`
- `kernel log not sent to DEBUG_URL`
- `kernel module failed to load`
- `kernel.panic not set`
- `kernel.panic not set: cannot read the kernel command line`
- `kexec execute failed, falling back to reboot`
- `kexec load failed, falling back to reboot`
- `link watch for LLDP failed, subscribing again`
- `link-layer cleanup after GoBGP setup failure failed`
- `link-layer cleanup after links setup failure failed`
- `link-layer cleanup after resolver setup failure failed`
- `link-layer cleanup after the FRR payload check failed`
- `netplan match blocks with emit-lldp not resolved`
- `netplan match for the static address did not select one NIC, using the first NIC`
- `network mode cleanup after resolver setup failure failed`
- `network teardown before the handoff did not finish, going on`
- `network teardown before the handoff failed`
- `network teardown failed`
- `network.bgp.sessions (BGP_SESSIONS) applies to GoBGP mode only; the FRR stack ignores it`
- `no BootyLastResult UEFI variable: efivarfs is not available`
- `no kexec boot entry, rebooting instead`
- `no usable watchdog device, arming the BMC watchdog through IPMI`
- `setting the BGP hostname failed`
- `setting the ramdisk hostname failed`
- `startup crash artifact inspection failed`
- `static teardown failed during DHCP fallback`
- `target filesystems are still writable at kexec, the installed OS may replay their journal`
- `target filesystems unknown at kexec, the installed OS may replay their journal`
- `token can not be renewed after the task completed`
- `vars key ignored: the BGP sessions come from frr.conf or network.bgp.sessions`
- `watchdog handoff failed`

#### WARN `cmd/booty-esp-sync`

- `ignoring log setting`
- `peer EFI system partition is missing, the mirror is degraded; nothing to synchronize`
- `unmount destination ESP`

#### WARN `pkg/auth`

- `renewal attempt failed`
- `token renewal failed without fatal handler, continuing retry loop`

#### WARN `pkg/caprf`

- `CA_BUNDLE_FILE has no effect on the CAPRF callbacks, the connectivity check and image downloads: SKIP_TLS_VERIFY=true skips their certificate check`
- `command payload does not parse or validate`
- `crash artifact prepare failed`
- `crash artifact upload failed`
- `deploy archive vars have unknown keys, ignored`
- `insecure transport enabled: bearer tokens will be sent over plain HTTP`
- `no URL configured for status, skipping`
- `no health check URL configured, skipping report`
- `no inventory URL configured, skipping inventory report`
- `refusing request to non-HTTPS endpoint`
- `request failed`
- `tls certificate verification disabled, like curl --insecure in the legacy CAPRF provisioner`

#### WARN `pkg/crash`

- `crash artifact collection failed`
- `crash artifact read-only mount failed`
- `crash artifact root partition resolution failed`
- `crash artifact unmount failed`
- `crash artifact upload failed`
- `failed to add crash artifact`

#### WARN `pkg/disk`

- `ATA security erase failed`
- `block devices still changing, continuing`
- `cannot list disks for discard`
- `cannot list partitions and holders, wiping the disk only`
- `cannot list the partitions to look for LUKS2 headers`
- `cannot parse lsblk output, wiping the disk only`
- `cannot read removable attribute; treating as removable`
- `cannot read the device to look for a LUKS2 header`
- `cannot read the disk vendor for the secure erase method`
- `cannot remove the member label, wipefs clears it`
- `cannot unmount, leaving it`
- `cannot zero the device edge`
- `chroot bind rollback failed`
- `chroot unmount failed`
- `device state: cannot read /proc/partitions`
- `disk is in use, skipping it in the wipe of all disks`
- `duplicate disk link, keeping the first device`
- `e2fsck not found, skipping filesystem check`
- `e2fsck returned non-zero (errors corrected)`
- `failed to attach fallback namespace after reset failure`
- `failed to clear security password`
- `failed to clear temporary security password after erase`
- `failed to create fallback namespace after reset failure`
- `failed to detect filesystem type before resize, falling back to resize tools`
- `full-capacity namespace creation failed; attached fallback namespace to keep controller accessible`
- `lsblk disk selection is ambiguous: several disks of the same bus qualify and their names can change between boots; select the root disk with DISK_SERIAL_NUMBER, and exclude smaller disks with MIN_DISK_SIZE_GB`
- `lsblk failed`
- `lsblk serial lookup failed`
- `mdadm --zero-superblock failed, continuing`
- `mdev -s failed`
- `partprobe failed, trying blockdev --rereadpt`
- `secure erase failed, SECURE_ERASE_FALLBACK=wipe: wiping the disk instead; the data is not erased`
- `sgdisk --zap-all failed, wipefs clears the partition table signatures`
- `skipping removable media device`
- `sync before partprobe failed`
- `wipefs failed`

#### WARN `pkg/disk/luks`

- `isLUKS check failed`

#### WARN `pkg/efi`

- `restoring the immutable flag failed`

#### WARN `pkg/espsync`

- `EFI watcher error`

#### WARN `pkg/executil`

- `debug dump`

#### WARN `pkg/image`

- `HTTP request failed, retrying`
- `HTTP server error, retrying`
- `OCI pull failed, retrying`
- `cannot read the target past its page cache, writing every block`
- `failed to close device after wipe`
- `failed to detach loop device`
- `failed to open device for wipe`
- `failed to sync wipe`
- `failed to unmount ramdisk`
- `failed to wipe partition table`
- `failed to wipe the end of the device`
- `hole punch failed, writing zeros from here on`
- `image download broke, resuming`
- `image range failed, retrying`
- `image source probe failed`
- `mdev -s failed after partition settle`
- `partprobe failed, trying blockdev --rereadpt`
- `source image has no EFI partition; leaving shared boot partition unchanged`
- `sync before partition settle failed`

#### WARN `pkg/linkcheck`

- `check result sink failed`

#### WARN `pkg/network`

- `Cannot bring up link for DHCP`
- `Cannot find link for DHCP`
- `Cannot set promote_secondaries for DHCP`
- `DHCP lease ended (expired or refused), address removed until a new lease`
- `NIC keeps its kernel name`
- `NICs keep their kernel names`
- `NICs that appear later get no DHCP client`
- `configured NICs are missing after the wait`
- `failed to add a DHCP source route`
- `failed to add the DHCP source rule`
- `failed to add the DHCPv6 address`
- `failed to apply DHCP lease`
- `failed to bring down slave`
- `failed to enslave interface`
- `failed to list addresses for DHCP prefix cleanup`
- `failed to remove DHCP address with an old prefix`
- `failed to remove a DHCP source rule`
- `failed to remove a route of an ended DHCP lease`
- `failed to remove an old DHCP route`
- `failed to remove bond`
- `failed to remove superseded DHCP address`
- `failed to remove the DHCPv6 address`
- `failed to remove the address of an ended DHCP lease`
- `failed to restore DHCP address`
- `failed to restore the MTU of an ended DHCP lease`
- `failed to roll back DHCP address`
- `failed to set the DHCP MTU`
- `failed to write the DHCP DNS servers`
- `ignoring DHCP lease`
- `link watch for DHCP failed, subscribing again`
- `links mode has no routed network, not waiting`
- `netplan set-name needs exactly one matching NIC, skipping`
- `network connectivity established, but the TLS certificate is not valid at the local clock`
- `no DHCPv6 on this NIC`
- `slave interface not found`
- `unknown bond mode, using 802.3ad`

#### WARN `pkg/network/frr`

- `=== FRR STATE DUMP END ===`
- `=== FRR STATE DUMP START ===`
- `Cannot create dummy interface, using loopback for underlay IP`
- `FRR`
- `Failed to configure NIC`
- `Network connectivity established, but the TLS certificate is not valid at the local clock`
- `connectivity deadline reached, dumping FRR state for diagnostics`
- `connectivity timeout — dumping FRR state for diagnostics`
- `failed to create FRR directory`
- `failed to fully clean FRR network resources`
- `failed to restart FRR daemons`
- `failed to set FRR directory ownership`
- `failed to start daemon`
- `failed to stop FRR`
- `frr daemons still running after SIGTERM; sending SIGKILL`
- `frrinit.sh start failed, falling back to direct daemon start`

#### WARN `pkg/network/frrconf`

- `frr.conf statement skipped`

#### WARN `pkg/network/gobgp`

- `BFD does not listen before the peer starts it`
- `BFD down, resetting the BGP session`
- `BFD session not moved; the BGP session runs without it`
- `BFD session not started; the BGP session runs without it`
- `BFD: no link-local peer address`
- `BGP session came up while BFD stayed down, resetting it`
- `EVPN route without VXLAN encapsulation treated as withdraw`
- `FIB update failed`
- `Failed to add BGP peer`
- `Failed to add numbered peer`
- `Failed to add unnumbered peer`
- `Failed to assign NIC to VRF`
- `Failed to bring up NIC`
- `Failed to find NIC`
- `Failed to find VRF for NIC assignment`
- `Failed to release NIC from VRF`
- `Failed to remove underlay dummy`
- `Failed to set MTU`
- `Failed to tear down underlay after overlay failure`
- `Overlay teardown error`
- `RAs do not start fast again after a carrier loss`
- `Underlay teardown error`
- `a BGP peer refuses connections in Idle after its session ends`
- `accept-ra: false not applied`
- `bridge MAC or provision IP not set, skipping type-2 advertisement`
- `cannot find VXLAN for BUM update`
- `cannot find VXLAN for FDB update`
- `cannot find VXLAN for type-5 gateway FDB update`
- `cannot find bridge for neighbor update`
- `cannot find bridge for route install`
- `cannot install underlay VTEP route for type-5`
- `eBGP peer has no route-map, routes rejected like FRR bgp ebgp-requires-policy`
- `failed to add/update FDB entry`
- `failed to delete FDB entry`
- `failed to delete VRF`
- `failed to delete nexthop at teardown`
- `failed to delete nexthop, retried later`
- `failed to delete type-5 route after failed fallback`
- `failed to detect physical NICs for MTU setup`
- `failed to find NIC for MTU`
- `failed to install ES nexthop group, its MACs use their VTEP`
- `failed to install VTEP nexthop`
- `failed to install gateway route`
- `failed to install route from type-5`
- `failed to install type-5 gateway FDB entry`
- `failed to install type-5 gateway neighbor`
- `failed to remove blackhole route`
- `failed to remove interface`
- `failed to remove lo address`
- `failed to remove overlay loopback IP`
- `failed to restore type-5 gateway neighbor`
- `failed to set MTU`
- `failed to set multicast hop limit`
- `failed to tear down overlay after setup failure`
- `gateway BUM FDB entry failed (non-fatal)`
- `install route`
- `link updates`
- `move unnumbered peer to the new link-local address`
- `network is not in the local RIB, not advertised (import check)`
- `next hop not reachable; its routes are not installed`
- `no link-local neighbor yet; the peer moves to the switch address when it answers`
- `no remaining type-5 path could be installed, removed route`
- `old peer address stays in its neighbor sets`
- `overlay_vrf_table_id=1 conflicts with default routing table, using underlay VRF table`
- `policy config (communities, local-pref, MED) is configured but not yet applied to BGP sessions`
- `provision IP not set, skipping EVPN Type-5 advertisement`
- `remove route`
- `reset BGP peer`
- `route updates`
- `route watcher stopped`
- `router solicitations are not answered`
- `set ignore_routes_with_linkdown`
- `skip paths`
- `skip route`
- `start BGP session`
- `start the BGP peer again`
- `stop BGP session`
- `type-5 route for a prefix the underlay installed in the same table, underlay route stays`
- `type-5 route treated as withdraw`
- `vrf_table_id=1 conflicts with default routing table, overriding to 1000`
- `watch kernel routes; recursive next hops follow only the periodic resync`
- `watch uplink carrier; sessions end only after the hold time`

#### WARN `pkg/network/lldp`

- `LLDP frame dropped: decoder panic`
- `LLDP frame not sent`
- `LLDP receive not started`
- `LLDP receive not started: no physical NICs`
- `link watch for LLDP failed, subscribing again`
- `link watch for LLDP not started, trying again`

#### WARN `pkg/network/netname`

- `NIC already gets another configured name`
- `several NICs have this predictable name, not renaming`

#### WARN `pkg/network/netplan`

- `frr.conf statement parsed but not applied yet`
- `frr.conf statement skipped (FRR_CONFIG_STRICT=false)`
- `netplan key ignored (NETPLAN_STRICT=false)`
- `netplan route skipped (NETPLAN_STRICT=false): table, metric, scope and from are not supported`

#### WARN `pkg/network/vlan`

- `failed to cleanup partially configured vlan`
- `vlan teardown error`

#### WARN `pkg/oob`

- `reading the BootyLastResult UEFI variable failed`
- `the BootyLastResult UEFI variable is invalid`
- `writing the BootyLastResult UEFI variable failed`

#### WARN `pkg/provision`

- `=== CONFIG DUMP ===`
- `=== DEBUG DUMP END ===`
- `=== DEBUG DUMP START ===`
- `EFI variables unavailable, skipping the EFI boot entry for the mirror disk; it boots through the removable fallback loader`
- `Failed to marshal hardware inventory`
- `Failed to report hardware inventory`
- `Firmware collection failed, continuing`
- `Firmware validation`
- `Hardware inventory collection failed`
- `No EFI partition found`
- `SR-IOV firmware changed: rebooting into the ramdisk to verify the runtime settings`
- `SR-IOV inventory after configure-sriov failed`
- `SRIOV_VERIFY_REBOOT=off: the runtime settings that wait for the firmware change are not verified`
- `bundled EFI fallback loader unavailable, falling back to target-root grub-install`
- `cannot create serial console run directory`
- `cannot list target disk partitions`
- `cannot publish serial console artifact for CAPRF`
- `cannot resolve the /boot fstab source on the target disk, keeping /boot on the root filesystem`
- `create EFI boot entry failed, continuing because EFI_BOOT_ENTRY_REQUIRED=false`
- `create mirror EFI boot entry failed, continuing because EFI_BOOT_ENTRY_REQUIRED=false`
- `crypto-erase is off (DEPROVISION_CRYPTO_ERASE=false); LUKS2 keyslots stay until the wipe, which may leave them`
- `debug`
- `debug command failed`
- `debug env`
- `decommission: disabling LVM failed, continuing`
- `decommission: mounting efivarfs failed, continuing`
- `decommission: stopping RAID arrays failed, continuing`
- `disk encryption is not possible, provisioning without it (DISK_ENCRYPTION_ALLOW_FAILURE)`
- `disk wipe failed, continuing`
- `efibootmgr list failed (non-EFI system?)`
- `enroll module signing key failed, continuing because MODULE_SIGNING_ALLOW_FAILURE=true`
- `failed to remove EFI entry`
- `failed to remove checkpoint`
- `failed to remove shared data seed mountpoint`
- `failed to report dry-run status`
- `failed to report health checks`
- `failed to save checkpoint`
- `failed to unmount active A/B slot`
- `failed to unmount shared data from earlier attempt`
- `failed to unmount shared data seed target; leaving mountpoint intact`
- `image format probe failed, retrying before destructive storage`
- `invalid rescue mode, defaulting to reboot`
- `marshal provisioning report`
- `mellanox: cannot list PCI devices, skipping`
- `mount efivarfs failed, continuing because EFI_BOOT_ENTRY_REQUIRED=false`
- `nic firmware collection failed`
- `no /boot/efi mountpoint found in partition layout; efi boot entry creation may be skipped`
- `no /boot/grub on the root filesystem and no /boot entry in fstab`
- `no EFI partition found, skipping EFI boot entry creation`
- `no serial console configured`
- `provisioning report not written to the target`
- `provisioning report: reporting the configured partition layout`
- `read the CI module signing certificate failed, continuing because MODULE_SIGNING_ALLOW_FAILURE=true`
- `remove EFI boot entries failed, continuing because EFI_BOOT_ENTRY_REQUIRED=false`
- `reporting the reboot-required status failed, retrying`
- `retrying step`
- `root /etc/ not mounted, skipping DNS configuration`
- `serial console override ignored with GRUB_CMDLINE_MODE=append`
- `shared data seed was interrupted, cleaning and retrying`
- `sign kernel modules failed, continuing because MODULE_SIGNING_ALLOW_FAILURE=true`
- `skipping EFI boot entry creation`
- `skipping boot partition mount; partition is not a usable ESP and EFI runtime is unavailable`
- `skipping machine file below a chroot bind mount`
- `skipping machine file that resolves below a chroot bind mount`
- `skipping symlink in copy tree`
- `skipping unsupported shared data seed entry`
- `target root still mounted from an earlier attempt, unmounting`
- `the image layout cannot be encrypted, provisioning without it (DISK_ENCRYPTION_ALLOW_FAILURE)`

#### WARN `pkg/realm`

- `attempting a reboot`

#### WARN `pkg/rescue`

- `auto-mount failed`
- `could not start SSH daemon`

#### WARN `pkg/runmode`

- `LLDP neighbor wait skipped: no physical NICs`
- `cabling checks without NICs`
- `command poll failed`
- `failed to ACK command`
- `health check warning`
- `heartbeat failed`
- `no LLDP neighbor on NICs with carrier`
- `provisioning stopped for a reboot into the ramdisk`
- `rescue setup error`
- `standby status after deprovisioning failed`
- `unknown command type`

#### WARN `pkg/secureboot`

- `cannot determine secure boot status`
- `cannot determine setup mode`
- `digest validation failed, trying next candidate`
- `pe/coff validation failed, trying next candidate`

#### WARN `pkg/sriov`

- `SR-IOV drift`
- `SR-IOV journal`
- `SR-IOV runtime check deferred to a verify reboot`

#### WARN `pkg/telemetry`

- `retry recorded for unknown step`
- `step end called for unknown or non-running step`

#### WARN `pkg/timesync`

- `NTP query failed`
- `ignoring a time source that is before the build of BOOTy`
- `ignoring a time source that is more than five years after the build of BOOTy`
- `no time source answered, keeping the RTC time`
- `reading the time from the CAPRF Date header failed`
- `setting the system clock failed`
- `writing the RTC failed, the installed OS starts with the old time`

#### WARN `pkg/trust`

- `system root certificates unavailable, trusting only the CA bundle`

#### WARN `pkg/watchdog`

- `progress resumed, petting the watchdog again`
- `watchdog keepalive failed`

### INFO (482 messages)

#### INFO `.`

- `BGP hostname set`
- `CAPRF mode active`
- `CAPRF run complete`
- `KEXEC_UNMOUNT_TARGET=false, keeping the target mounted for kexec`
- `attempting kexec`
- `beginning provisioning process`
- `cleaned up target root after unsuccessful kexec`
- `detected IPMI info`
- `detected netplan configuration files`
- `dry-run mode: not arming the hardware watchdog`
- `dry-run mode: skipping active network setup and token renewal`
- `executing mode`
- `firmware LLDP agent disabled`
- `firmware LLDP agent left to the SR-IOV profile`
- `firmware values changed during provisioning, hard reboot required — skipping kexec`
- `handoff failed, sending LLDP frames again`
- `hardware watchdog armed`
- `keeping the provisioner resolv.conf in the ramdisk`
- `keeping the ramdisk running after the failure`
- `kernel security state`
- `kexec boot entry`
- `kexec disabled by configuration, skipping`
- `loaded kernel modules`
- `loaded watchdog module`
- `netplan config loaded`
- `netplan: provision VNI is an FRR L3VNI, using Type-5 routes only`
- `network connectivity established`
- `network stopped before the handoff`
- `network.bgp.bfdProfiles replaces the frr.conf BFD profiles`
- `network.bgp.communityLists replaces the frr.conf community-lists`
- `network.bgp.routeMaps replaces the frr.conf route-maps`
- `network.bgp.sessions replaces the frr.conf sessions`
- `network.blackholeRoutes replaces the netplan or frr.conf value`
- `network.devices replaces the provisioner netplan files`
- `network.emitLLDP replaces the netplan or frr.conf value`
- `network.evpn.type5Networks replaces the netplan or frr.conf value`
- `network.loopbackAddresses replaces the netplan or frr.conf value`
- `network.noAcceptRA replaces the netplan or frr.conf value`
- `no BootyLastResult UEFI variable: not a UEFI host`
- `no EFI firmware, skipping kernel module`
- `no IPMI interface found, using the underlay IP rules`
- `out-of-band status`
- `parsed FRR configuration`
- `provisioning succeeded, powering off for orchestrator to manage boot`
- `ramdisk hostname set`
- `released target filesystems before kexec`
- `secure boot re-enable or MOK enrollment requested, hard reboot required; skipping kexec`
- `setting up LACP bond`
- `setting up VLAN interfaces`
- `starting BOOTy`
- `startup crash artifact inspection complete`
- `tearing down network for retry`
- `time sync is off, keeping the RTC time`
- `using DHCP network mode`
- `using FRR/EVPN network mode`
- `using GoBGP/EVPN network mode`
- `using links network mode`
- `using static network mode`
- `waiting for network connectivity`

#### INFO `cmd/booty-esp-sync`

- `peer EFI system partition is back`
- `synchronizing EFI system partition`

#### INFO `pkg/auth`

- `jwt acquired`
- `jwt renewed`

#### INFO `pkg/caprf`

- `jwt token acquired, using for subsequent API calls`
- `retrying crash artifact request`
- `retrying request`

#### INFO `pkg/disk`

- `ATA secure erase`
- `LUKS2 header already gone`
- `NVMe secure erase`
- `RAID array created`
- `SCSI FORMAT UNIT complete`
- `SCSI SANITIZE complete`
- `SCSI secure erase`
- `activating LVM volume groups`
- `added mirror member`
- `applying partition layout`
- `bash unavailable in chroot, trying /bin/sh`
- `bash unavailable in syscall chroot, trying /bin/sh`
- `bind mount target already mounted, skipping`
- `blkdiscard complete`
- `blkdiscard failed, falling back to signature wipe`
- `blkdiscard not supported or failed`
- `block devices changed, waiting for them to settle`
- `block devices settled`
- `block devices unchanged since they settled`
- `blockdev --rereadpt succeeded`
- `cannot remove the member label, wipefs clears it: tool not available`
- `cannot zero the device edge: tool not available`
- `checking filesystem`
- `checking filesystem in preen mode`
- `chroot binary not found, using syscall fallback`
- `created /dev/disk links`
- `created degraded mirror`
- `created logical volume`
- `created nvme namespace`
- `created partition`
- `creating RAID array`
- `creating nvme namespace`
- `deactivating LVM volume groups`
- `detecting target disk`
- `detecting target disk by serial`
- `detecting target disk like the legacy CAPRF provisioner`
- `device appeared`
- `device appeared after mdev`
- `device state`
- `disk candidate`
- `disk has no partition table`
- `formatting nvme namespace`
- `formatting partition`
- `growing partition`
- `mounted partition`
- `mounting partition`
- `nvme namespaces reset to factory default`
- `partition already at max size`
- `performing secure erase on all disks`
- `re-reading partition table`
- `remounted busy mount read-only`
- `resetting nvme namespaces to factory default`
- `resizing filesystem`
- `selected disk`
- `selected disk by serial`
- `selected mirror pair`
- `setting up lvm`
- `sgdisk --zap-all failed, wipefs clears the partition table signatures: tool not available`
- `shrank filesystem for mirror metadata`
- `skipping format for lvm pv partition`
- `stopping RAID arrays`
- `volume group already exists, skipping create`
- `wiping all disks`
- `wiping disk`
- `wiping filesystem signatures`
- `zeroing RAID superblock`

#### INFO `pkg/disk/luks`

- `LUKS volume closed`
- `LUKS volume formatted`
- `LUKS volume opened`

#### INFO `pkg/espsync`

- `synchronizing EFI system partition`

#### INFO `pkg/firmware/nic/ethtool`

- `captured NIC firmware`

#### INFO `pkg/firmware/nic/mellanox`

- `applied Mellanox FW param`
- `captured Mellanox NIC firmware`

#### INFO `pkg/image`

- `GPG signature verified successfully (gpg)`
- `GPG signature verified successfully (gpgv)`
- `a/b image streaming`
- `checksum verified`
- `converting qcow2 to raw`
- `copying partition`
- `copying partition table to target disk`
- `copying source boot partition to shared A/B boot partition`
- `copying source root partition to A/B target slot`
- `decompressing image on ramdisk`
- `decompression complete`
- `detected decompressed image format`
- `detected image format`
- `downloaded image format`
- `downloading image in parallel ranges`
- `image server does not offer ranges with a strong ETag and a length: one GET without resume`
- `image source probe result`
- `image write skipped blocks`
- `image written`
- `image written to ramdisk`
- `loop device attached`
- `partition-by-partition imaging`
- `partition-by-partition imaging complete`
- `pulling OCI image`
- `qcow2 conversion complete`
- `qcow2 image detected, writing to ramdisk for conversion`
- `ramdisk mounted`
- `raw image written to device`
- `root image streaming`
- `selected best image source`
- `selecting best image source`
- `source image has no partition table; copying as root filesystem`
- `source image is not loop-mountable as a disk image; copying as root filesystem`
- `streaming image`
- `streaming image with an encrypted partition`
- `streaming raw image to device`
- `streaming source image to A/B root target`
- `streaming source partition to A/B target`
- `verifying image GPG signature`

#### INFO `pkg/image/oci`

- `initializing new cache index`

#### INFO `pkg/kexec`

- `executing kexec`
- `kernel loaded for kexec`
- `loading kernel for kexec`

#### INFO `pkg/linkcheck`

- `bm4x connection report`
- `check result`
- `check run done`

#### INFO `pkg/network`

- `DHCP lease applied`
- `DHCP succeeded`
- `DHCP succeeded at the timeout`
- `NIC has carrier again, confirming its DHCP lease`
- `NIC has carrier, starting DHCP at once`
- `NIC lost carrier, keeping its DHCP lease until the carrier returns`
- `Starting DHCP on a new NIC`
- `Starting DHCP on all interfaces`
- `Starting DHCPv6`
- `address already assigned`
- `bond interface already exists, reusing`
- `created bond interface`
- `default route already exists`
- `default route configured`
- `enslaved interface to bond`
- `links mode: NICs up with IPv6 link-local only`
- `network connectivity established`
- `renamed NIC like netplan set-name`
- `renamed NIC to its predictable name`
- `static IP configured`
- `static network teardown complete`
- `undid NIC rename after a failed rename`
- `waiting for interface names to stabilize (clab-* detected)`
- `waiting for physical interface set to stabilize`
- `waiting for the configured NICs`

#### INFO `pkg/network/bfd`

- `bfd session added`
- `bfd session state changed`

#### INFO `pkg/network/dhcp4`

- `DHCPNAK, starting over`

#### INFO `pkg/network/dhcp6`

- `DHCPv6 address revoked: valid lifetime 0`

#### INFO `pkg/network/frr`

- `Added BGP peer`
- `Assigned provision IP to bridge`
- `FRR daemons started via frrinit.sh`
- `FRR daemons started via systemctl`
- `FRR setup`
- `FRR teardown complete`
- `FRR/EVPN network setup complete`
- `Network connectivity established`
- `bgp session established, keeping FRR running`
- `restarting FRR daemons for connectivity recovery`
- `started FRR daemon`

#### INFO `pkg/network/gobgp`

- `Announced underlay route`
- `BFD down for an unknown BGP peer, not resetting it`
- `BFD down right after the BGP session came up, not resetting it yet, like FRR`
- `BFD down while the BGP session is coming up, not resetting it yet`
- `BGP established, polling target URL`
- `BGP server started`
- `BGP server stopped`
- `BGP session ended, peer started again at once like FRR`
- `Created underlay dummy`
- `Detected physical NICs`
- `ES has no active VTEP, its MACs are not installed`
- `ES nexthop group`
- `GoBGP network stack ready`
- `Setting up GoBGP network stack`
- `Skipping VRF assignment for management NIC`
- `Tearing down GoBGP network stack`
- `Waiting for BGP peer connectivity`
- `added BGP peer`
- `added lo address`
- `added numbered BGP peer`
- `added overlay loopback`
- `added unnumbered BGP peer`
- `advertised EVPN type-2 MAC/IP route`
- `advertised EVPN type-3 IMET route`
- `advertised EVPN type-5 host route`
- `advertised EVPN type-5 network`
- `assigned provision IP`
- `created VXLAN and bridge`
- `deleted type-5 gateway state`
- `installed BGP route policies`
- `installed BUM FDB entry from type-3 route`
- `installed FDB entry from type-2 route`
- `installed blackhole route`
- `installed gateway BUM FDB entry`
- `installed gateway VTEP route`
- `installed neighbor from type-2 route`
- `installed numbered gateway VTEP route`
- `installed route`
- `installed route from type-5`
- `installed type-5 gateway neighbor and FDB entry`
- `overlay type is none, skipping overlay setup`
- `removed BUM FDB entry from type-3 withdraw`
- `removed FDB entry`
- `removed gateway BUM FDB entry`
- `removed route`
- `removed route from type-5 withdraw`
- `tcp-md5 authentication configured for BGP peer`
- `type-5 path withdrawn, keeping route via remaining path`
- `underlay BGP peers established`
- `underlay route removed, installed the type-5 route again`
- `unnumbered peer moved to the new link-local address of the switch`
- `uplink has carrier again, starting its BGP session`
- `uplink lost carrier, stopping its BGP session`
- `vrf ready`

#### INFO `pkg/network/lldp`

- `LLDP neighbor`
- `LLDP neighbor discovered`
- `LLDP neighbor left`
- `listening for LLDP frames`
- `sending LLDP frames like networkd EmitLLDP=yes`
- `sending LLDP frames with the bm4x-cablecheck identity`

#### INFO `pkg/network/netplan`

- `netplan: DHCP interface(s) found, no EVPN config, using DHCP mode`

#### INFO `pkg/network/vlan`

- `vlan interface created`
- `vlan interface removed`

#### INFO `pkg/oob`

- `no BootyLastResult UEFI variable from an earlier run`
- `previous BOOTy run result`
- `wrote the BootyLastResult UEFI variable`

#### INFO `pkg/provision`

- `A/B image mode uses removable EFI fallback loader; skipping EFI NVRAM boot entry`
- `A/B preserveExisting enabled, preserving existing EFI boot entries`
- `A/B preserveExisting enabled, preserving existing EFI boot entry`
- `A/B preserveExisting enabled, preserving existing EFI fallback loader`
- `A/B preserveExisting enabled, reusing existing partition layout`
- `A/B preserveExisting enabled, skipping whole-disk wipe`
- `A/B preserveExisting keeps the disks, skipping crypto-erase`
- `CI module signing certificate to enroll`
- `DISK_LINKS=false, not creating /dev/disk links`
- `EFI boot entry created`
- `Firmware reporting disabled, skipping`
- `Firmware validation`
- `Hardware inventory collected`
- `Hardware inventory collection disabled, skipping`
- `Health check result`
- `Health checks disabled, skipping`
- `LUKS2 keyslots and header destroyed`
- `MOK enrollment queued: MokManager asks for MOK_PASSWORD at the next boot`
- `MOK enrollment staged: the next firmware boot runs mok-enroll.efi`
- `No SR-IOV profiles, skipping`
- `SR-IOV profiles set the ConnectX VFs, skipping setup-mellanox (NUM_VFS)`
- `Streaming image`
- `Streaming image into A/B target slot`
- `Streaming image partition-by-partition`
- `adding fstab entry for the /boot array`
- `applying custom partition layout`
- `applying provisioner files to the ramdisk`
- `applying the provisioner files of the command to the ramdisk`
- `boot partition already mounted`
- `boot partition from layout`
- `checking for Mellanox NICs`
- `cloud-init seed injected`
- `configured target overlayFS`
- `configuring DNS`
- `configuring Mellanox SR-IOV`
- `copying files`
- `creating EFI boot entry`
- `crypto-erasing LUKS2 device`
- `custom partition layout applied`
- `decommission: disk wipe complete`
- `deprovisioning step`
- `disk encryption prepared`
- `dry-run check`
- `efivarfs already mounted`
- `enabled serial getty unit`
- `folded operator console parameters into serial console resolution`
- `fstab entry now valid from both mirror disks`
- `fstab: the encrypted partition mounts through its mapping`
- `generated fstab for custom layout`
- `growing the last Linux partition, not the root`
- `grub.cfg already renamed`
- `host booted with BIOS, installing GRUB on both mirror disks`
- `image cosign signature verified`
- `image prerequisites validated`
- `image reachable`
- `image step memory`
- `installed bundled EFI fallback loader`
- `keeping target root mounted for kexec`
- `keeping the target resolv.conf`
- `machine command output`
- `mellanox SR-IOV setup disabled`
- `mellanox firmware values changed, hard reboot required`
- `mellanox: current NUM_OF_VFS`
- `mellanox: mstconfig not found, skipping`
- `mellanox: no ConnectX Ethernet NICs found, skipping`
- `mellanox: set NUM_OF_VFS`
- `mirror already assembled`
- `mirroring root disk`
- `module signing certificate left for the provisioner to enroll`
- `mounted efivarfs`
- `mounted shared data partition`
- `mounting separate /boot`
- `nic_firmware`
- `nic_firmware action=check`
- `no deprovision disk override configured; hard deprovision will wipe all disks`
- `no directory found`
- `no image signature URL configured, skipping verification`
- `no machine commands directory found`
- `no matching disk pair, provisioning a single disk`
- `no provisioner files to apply to the ramdisk`
- `non-EFI system detected, skipping efivarfs mount`
- `non-EFI system, skipping EFI boot entry removal`
- `oci pre-pulls enabled with no images`
- `partition formatted with LUKS2`
- `partition layout from URL`
- `partition layout from image`
- `persisted target network configuration`
- `provisioner files stay in the ramdisk, not copied to the target`
- `provisioning report`
- `provisioning report already written by the interrupted run`
- `provisioning report not written to the target`
- `provisioning step`
- `provisioning step requests a reboot into the ramdisk`
- `pulling OCI sysext layer`
- `pulling oci pre-pull image`
- `recovery key handed over`
- `removed conflicting serial getty unit`
- `removing EFI boot entry`
- `removing old EFI boot entries`
- `renaming grub.cfg`
- `resolved serial console`
- `restored mirror disk selection from checkpoint`
- `restored nvme target device from checkpoint`
- `restored partition layout from checkpoint`
- `restored raid target device from checkpoint`
- `root from lvm`
- `root from partition layout`
- `root partition already mounted`
- `root probe: cannot mount partition`
- `root probe: selected root partition`
- `root probe: using the mounted root partition`
- `running in a container, skipping the EFI boot entry steps`
- `running machine command`
- `running machine command script`
- `running post-provision command`
- `secure erase enabled, erasing raid member`
- `secure erase enabled, performing hardware-level erase`
- `secure erase enabled, performing hardware-level erase on all disks`
- `serial console comes from the kernel command line only`
- `set disk device from nvme namespace layout`
- `set disk device from raid configuration`
- `setting hostname`
- `shared data partition already has content, skipping seed`
- `shared data partition already mounted`
- `signed out-of-tree kernel modules`
- `skipping EFI fallback loader installation; no boot partition detected`
- `skipping boot partition mount; no boot partition detected`
- `skipping completed step`
- `skipping grow-partition for declarative partition layout`
- `skipping grow-partition, mirror-root grew the root partition before creating the array`
- `skipping grow-partition, stream-image grew the encrypted partition; close-encrypted-disk grows its file system`
- `skipping grow-partition, stream-image grew the encrypted root partition`
- `skipping the removal of every EFI boot entry`
- `starting deprovisioning`
- `starting dry-run - no destructive changes will be made`
- `streaming image into a LUKS2 partition`
- `streaming image into partition layout root`
- `sysext cosign signature verified`
- `sysext layer loaded`
- `sysext provisioning enabled with no layers`
- `update-grub not found in the target, skipping like the legacy CAPRF provisioner`
- `using configured deprovision disk`
- `using configured disk device`
- `using configured disk serial`
- `using partition layout device override`
- `using the disk selection made before resume`
- `wiping all disks`
- `wiping raid target device after array creation`
- `writing GRUB config`
- `writing GRUB config like the legacy CAPRF provisioner`
- `writing kubelet extra args`
- `wrote serial getty drop-in`

#### INFO `pkg/realm`

- `already mounted, skipping`
- `folder created`
- `mounted`
- `reboot suppressed (BOOTY_NO_REBOOT set)`
- `starting Shell`
- `unmounted`
- `waiting for command to finish...`

#### INFO `pkg/runmode`

- `LLDP neighbor on every NIC with carrier`
- `LLDP neighbor wait skipped: LLDP receive is off`
- `checks done, holding`
- `checks done, holding until power off`
- `deprovisioning complete, returning to standby`
- `dropping to rescue shell`
- `entering standby mode`
- `health check passed`
- `health check skipped`
- `health-check command received`
- `reboot command received`
- `received command`
- `rescue action`
- `retrying provisioning`
- `running command with payload`
- `standby context canceled, shutting down`
- `waiting after deprovisioning failure`
- `waiting for LLDP neighbors`
- `waiting for manual intervention`

#### INFO `pkg/secureboot`

- `enrolling mok certificate`
- `mok enrollment queued for next reboot`

#### INFO `pkg/sriov`

- `SR-IOV compliance`
- `SR-IOV setting`
- `sock_diag unavailable, the SR-IOV in-use check uses the network config and the routes`

#### INFO `pkg/telemetry`

- `step ended`
- `step started`

#### INFO `pkg/timesync`

- `RTC updated`
- `clock synchronized`
- `measured clock skew`

#### INFO `pkg/trust`

- `added extra root certificates`

#### INFO `pkg/watchdog`

- `disarming the watchdog`
- `leaving the watchdog armed for the target OS`

### DEBUG (101 messages)

#### DEBUG `.`

- `link watch for LLDP skipped a message`
- `no kernel modules directory, skipping`
- `no watchdog modules`
- `reaped orphan child process`
- `watchdog module not loaded`

#### DEBUG `pkg/auth`

- `token renewal stopped`

#### DEBUG `pkg/caprf`

- `no crash artifact URL configured, skipping`
- `no firmware URL configured, skipping report`
- `no metrics URL configured, skipping`
- `telemetry disabled, skipping metrics`

#### DEBUG `pkg/debug`

- `debug dump command failed`

#### DEBUG `pkg/disk`

- `cannot bind-mount efivarfs into chroot`
- `cannot create efivarfs mount target`
- `chroot efivarfs unmount skipped`
- `disk with serial has no size yet`
- `failed to detect filesystem type, falling back to ext check`
- `lsblk serial lookup failed`
- `lvm binary not found, skipping activation`
- `lvm binary not found, skipping deactivation`
- `mdadm stop reported no arrays`
- `mdev -s failed during device wait`
- `resize2fs failed, trying xfs_growfs`
- `waiting for device node`
- `waiting for disk serial`
- `xfs_growfs failed, trying btrfs resize`

#### DEBUG `pkg/firmware/nic/ethtool`

- `private flags not read (optional)`

#### DEBUG `pkg/image`

- `mdev -s failed while waiting for partition device`
- `waiting for partition device`

#### DEBUG `pkg/network`

- `address deletion (may already be gone)`
- `connectivity check failed`
- `link watch for DHCP skipped a message`
- `route deletion (may already be gone)`
- `router solicitation`
- `skipping NIC missing during DHCP scan`
- `skipping interface: address enumeration failed`

#### DEBUG `pkg/network/bfd`

- `bfd packet dropped`
- `bfd packet with wrong TTL dropped`
- `bfd read failed`
- `bfd send failed`

#### DEBUG `pkg/network/dhcp4`

- `DHCP lease bound`
- `DHCP receive`
- `DHCP send`
- `DHCP transport`
- `DHCPNAK from an unexpected server, ignoring`
- `DHCPRELEASE`

#### DEBUG `pkg/network/dhcp6`

- `DHCPv6 INF_MAX_RT`
- `DHCPv6 RELEASE`
- `DHCPv6 SOL_MAX_RT`
- `DHCPv6 address bound`
- `DHCPv6 send`

#### DEBUG `pkg/network/frr`

- `Connectivity check failed`
- `Failed to set sysctl`
- `VRF already exists`
- `daemon not found, skipping`
- `failed to remove VXLAN`
- `failed to remove bridge provision address`
- `failed to remove link`
- `failed to remove loopback address`
- `skipping loopback cleanup; failed to derive addresses`

#### DEBUG `pkg/network/gobgp`

- `Failed to set sysctl`
- `failed to append BUM FDB entry`
- `failed to delete BUM FDB entry`
- `failed to delete Type-5 VTEP route`
- `failed to delete gateway VTEP route`
- `failed to delete route from type-5 withdraw`
- `failed to delete type-2 neighbor`
- `failed to delete type-5 gateway FDB entry`
- `failed to delete type-5 gateway neighbor`
- `failed to install type-2 neighbor`
- `failed to list peers`
- `failed to remove gateway BUM FDB entry`
- `failed to roll back type-5 gateway neighbor`
- `no type-5 route to delete`
- `remove route`
- `route update`
- `route update skipped`
- `route update skipped: RT mismatch`
- `route update skipped: invalid configured import RT`
- `type-2 route with invalid MAC`
- `type-2 route with no valid next-hop`
- `type-3 route with no valid VTEP IP`
- `type-3 route without ingress replication, no BUM entry`
- `type-5 gateway IP not resolved by a type-2 route yet`
- `type-5 route for a static blackhole prefix, static route wins`
- `type-5 route with a gateway IP overlay index not installed: enable-resolve-overlay-index is off or no L2 overlay`
- `type-5 route with invalid prefix`
- `type-5 route with no valid VTEP`
- `type-5 route with no valid gateway`

#### DEBUG `pkg/network/lldp`

- `LLDP listen failed`
- `LLDP shutdown frame not sent`
- `link watch for LLDP skipped a message`

#### DEBUG `pkg/network/netplan`

- `netplan: default gateway from ethernet routes`
- `netplan: found VLAN`
- `netplan: found bond`

#### DEBUG `pkg/oob`

- `BootyLastResult UEFI variable unchanged, not writing`
- `writing the status line failed`

#### DEBUG `pkg/provision`

- `no LUKS2 header on the disks to wipe`
- `post-provision command output`
- `retrying step immediately`

#### DEBUG `pkg/secureboot`

- `close artifact after digest validation`
- `pe/coff close also failed`

<!-- log-catalog:end -->

