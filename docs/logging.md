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
line. The [message catalog](#message-catalog) lists the attributes of every
message.

## Personal data

BOOTy logs no person's data on purpose, but some attributes identify the
machine it provisions or the person who logs in to the rescue shell. The
machine can be a node of a tenant cluster, so its names and addresses
count as data of the tenant's staff. Classes and data subjects follow the
platform logging contract: classes `tenant-pii` (user name, account, host
name), `customer-data` (address, hardware identifier), `content`
(command line); data subjects `operator`, `tenant-user`, `end-customer`. A
field that can hold several data subjects names the one with the highest
need for protection (`end-customer`, then `tenant-user`, then `operator`).

The table is generated: `TestLoggingDocPersonalData` in
`logging_personal_data_test.go` fails when a log call (or a `With` of a
logger) uses an attribute key that is classified neither as personal data
nor as free of it. "Levels" are the levels of the calls that log the key;
"Value redacted" is what the console and `LOG_URL` handlers do with the
key's value.

<!-- personal-data:start -->
<!-- Generated by TestLoggingDocPersonalData (UPDATE_LOGGING_DOC=1). Do not edit. -->

| Field | Class | Data subject | Levels | Logged at `info` (default) | Value redacted | Meaning |
|-------|-------|--------------|--------|----------------------------|----------------|---------|
| `addr` | `customer-data` | `tenant-user` | WARN, INFO, DEBUG | yes | no | IP address of the machine (DHCP lease, static) or of a BGP peer |
| `address` | `customer-data` | `tenant-user` | INFO, DEBUG | yes | no | IP address of the machine (static) or of a BGP peer |
| `bridge_mac` | `customer-data` | `tenant-user` | INFO | yes | no | MAC address of the provisioning bridge of the machine |
| `ciaddr` | `customer-data` | `tenant-user` | DEBUG | no, `debug` only | no | client IP address of a DHCP message |
| `cidr` | `customer-data` | `tenant-user` | DEBUG | no, `debug` only | no | prefix of a loopback address of the machine |
| `command` | `content` | `tenant-user` | INFO | yes | no | command line of a machine or post-provision command of the config, values of secret flags removed |
| `data` | `customer-data` | `tenant-user` | WARN | yes | no | one line of a network state dump (`debug`): addresses, MACs and names of the machine |
| `dst` | `customer-data` | `tenant-user` | WARN, INFO, DEBUG | yes | no | destination prefix of a type-5 route; for file copies a path |
| `hostname` | `tenant-pii` | `tenant-user` | WARN, INFO | yes | no | host name of the machine (CAPRF, ramdisk, BGP, standby); it becomes the node name and can carry the tenant's cluster name |
| `ip` | `customer-data` | `tenant-user` | WARN, INFO, DEBUG | yes | no | IP address of the machine: IPMI, provision, overlay loopback, host route |
| `key` | `tenant-pii` | `tenant-user` | WARN, INFO | yes | yes | fingerprint of the SSH key of a rescue login; also a vars key name |
| `line` | `content` | `tenant-user` | WARN, INFO | yes | no | one output line of a machine command script (pattern-redacted only); for frr.conf a line number |
| `mac` | `customer-data` | `tenant-user` | WARN, INFO, DEBUG | yes | no | MAC address of a NIC or of the IPMI interface of the machine, or of a fabric router |
| `match_mac` | `customer-data` | `tenant-user` | WARN | yes | no | MAC address that a static network config matches |
| `output` | `content` | `tenant-user` | WARN, INFO, DEBUG | yes | no | output of a post-provision command of the config (`debug`, pattern-redacted only) or of a disk or EFI tool |
| `overlay_ip` | `customer-data` | `tenant-user` | INFO | yes | no | overlay loopback IP address of the machine |
| `params` | `content` | `tenant-user` | WARN, INFO | yes | no | per-host kernel parameters; for NIC firmware a count |
| `prefix` | `customer-data` | `tenant-user` | WARN, INFO, DEBUG | yes | no | a route prefix: the machine's underlay /32 or a tenant or fabric prefix |
| `principal` | `tenant-pii` | `tenant-user` | WARN, INFO | yes | no | user name that a rescue SSH client logs in or tries to log in as |
| `provision_ip` | `customer-data` | `tenant-user` | WARN | yes | no | provisioning IP address of the machine |
| `remote` | `customer-data` | `tenant-user` | WARN, INFO, DEBUG | yes | no | address and port of the rescue SSH client |
| `report` | `customer-data` | `tenant-user` | INFO | yes | no | the provisioning report (JSON) with the host name of the machine |
| `route` | `customer-data` | `tenant-user` | WARN, INFO, DEBUG | yes | no | destination of a DHCP route or a route key |
| `routerID` | `customer-data` | `tenant-user` | INFO | yes | no | BGP router ID, the underlay IP address of the machine |
| `serial` | `customer-data` | `tenant-user` | WARN, INFO, DEBUG | yes | no | serial number of a disk or of the LLDP neighbor; `out-of-band status` logs a bool |
| `statement` | `content` | `tenant-user` | WARN | yes | no | an frr.conf statement that is skipped or not applied |
| `static_ip` | `customer-data` | `tenant-user` | WARN | yes | no | static IP address of the machine |
| `subject` | `tenant-pii` | `tenant-user` | WARN, INFO | yes | no | OIDC subject of an opkssh rescue SSH login (an account ID or e-mail address) |
| `underlay_ip` | `customer-data` | `tenant-user` | WARN, INFO | yes | no | underlay loopback IP address of the machine |
| `via` | `customer-data` | `tenant-user` | WARN, INFO | yes | no | VTEP IP address an FDB entry points to |
| `vtep` | `customer-data` | `tenant-user` | WARN, INFO, DEBUG | yes | no | VTEP IP address of the machine or of a fabric peer |
| `vteps` | `customer-data` | `tenant-user` | WARN, INFO | yes | no | VTEP IP addresses of an Ethernet segment |
| `withdrawn_vtep` | `customer-data` | `tenant-user` | INFO | yes | no | VTEP IP address whose routes were withdrawn |

<!-- personal-data:end -->

Which level adds personal data:

- `info` (the default) already logs every field of the table except
  `ciaddr`: the host name and the provisioning report, the addresses,
  routes and MACs of the machine, the rescue SSH principal, client address
  and OIDC subject, the per-host kernel parameters, the machine commands
  and the output of machine command scripts.
- `warn` and `error` keep the failures among them: a refused rescue SSH
  login (`principal`, `remote`, `subject`), a failed address, route or
  hostname change, a skipped frr.conf statement, and the network state
  dump of a failed provisioning step (`debug` with `label` and `data`, at
  `WARN`).
- `debug` adds `ciaddr` and more lines with the fields above: DHCP lease
  details, address deletions, type-5 route details, rescue SSH
  connections that end before a login.

Free text: `error`, `reason`, `message`, `detail` and `url` are not
classified as personal data, but an error text can repeat a host name or
an address of the machine; treat them as `customer-data` of a
`tenant-user` when a line is kept. `output` is classified as `content`,
because it carries the output of the post-provision commands of the
config.

Fields of other kinds are free of personal data: states, counts, sizes,
durations, paths, device and interface names, and the names and addresses
of the data-center fabric and of platform services (`gateway`, `nexthop`,
`peer`, `neighbor`, `server`, `systemName`, CAPRF and image URLs).

Not logged by BOOTy's own log calls: LUKS passphrases and volume keys,
TPM-sealed secrets, the CAPRF token and JWTs, the rescue SSH password,
private keys, and secret values of `/deploy/vars`. The
[redaction](#destinations-of-the-ramdisk-agent) replaces values whose key
names a secret and text that looks like one (`password=`, bearer tokens,
JWTs, PEM keys, ...) as a second line of defense; a rescue SSH key
fingerprint (`key`) is redacted for the same reason. The redaction does
not know the secret values themselves: the output of a machine command
script (`line`), a machine or post-provision command (`command`) and an
error text are logged as they are once the patterns are removed. **Machine
commands and scripts must not print or take secrets in a form the patterns
miss** (an unlabelled passphrase or opaque token).

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
  fail), and conflicting NTP and CAPRF `Date` times; the opkssh rescue
  server starting on a clock that time sync did not set.
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
  be read, kernel module signing or MOK enrollment that failed with
  `MODULE_SIGNING_ALLOW_FAILURE=true`, and per-host kernel parameters that
  reach only the GRUB entry because the UKI is signed by the image pipeline
  (`SECUREBOOT_SIGNING=ci`). With disk encryption: a `KEK`/`db`/`dbx` update
  of the image that the firmware refused or that is not a signed signature
  list (`variable`, `file`, `error`), and `SECUREBOOT_APPLY_DBX_UPDATES=false`.
- Hardware: SR-IOV drift with `SRIOV_ON_DRIFT=warn`, health check warnings,
  inventory and firmware report failures, crash artifact collection and
  upload failures.
- Configuration: deprecated or ignored keys (for example BGP keys next to
  BGP sessions), `SKIP_TLS_VERIFY` and plain HTTP transport, missing report
  URLs, and `ignoring log setting`.
- Rescue SSH: malformed or RSA entries in `RESCUE_SSH_PUBKEY` (by position,
  not the key), keys with unsupported options, no valid key at all, password
  login without a key, `RESCUE_SSH_HARDENED=false`, too many connections
  without a login, a failed remote forward, a session whose shell did not
  start. With the opkssh settings: malformed `RESCUE_SSH_OPK_PROVIDERS` and
  `RESCUE_SSH_OPK_POLICY` rows (by position), no valid provider, a build
  without opkssh support. Every refused login (`rescue SSH login refused`:
  `method` `publickey`, `password` or `opkssh`, `principal`, `remote`, the
  `key` fingerprint or the `issuer` and `subject` that the PK Token claims,
  and the `reason`; never a password or token).
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
  image stream, the LVM physical volume of VG `tcaas` in the mapping and
  the skipped `enable-lvm`), the signed UKI (the per-host kernel parameters
  in its `.cmdline`, `UKI installed` with `path`, `atomic` and `bytes`, the
  removed temporary file of an interrupted install), the Secure Boot
  updates of the image before the PCR 7 binding (`Secure Boot update
  applied` or `already applied`: `variable`, `file`, `entries`, `new`)
  and the crypto-erase of LUKS2 headers at deprovision.
- Image: the source, the download and write progress, checksum and GPG
  signature results, qcow2 conversion, A/B slot streaming, the memory peak
  of the image step.
- Rescue SSH: the server start (hardened or not, number of keys, password
  login on or off, opkssh on or off, host key fingerprint) and every login (`rescue SSH login accepted`: `method`, `principal`,
  `remote`, the verified `issuer` and `subject` or the `key` fingerprint).
- Boot handoff: EFI boot entries, GRUB and kernel command line, kexec load
  and execute, reboot and power off, the watchdog handoff.
- Kernel module signing: the signed out-of-tree modules and the certificate
  (path, SHA-256), the module system extension they go into (or that there
  are none), and how the certificate is enrolled (`mok-enroll.efi` staged,
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
- Rescue SSH: connections that end before a login, with
  the handshake error.

## Message catalog

The catalog lists the message (`msg`) of every log call of the code, by
level and package, with the attribute keys of its calls after the colon
(not the `component`, `tier`, `mode`, `iface`, `interface` or `device` that a
package logger adds to each of its lines) (`.` is the ramdisk agent's `main` package; the
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

### ERROR (65 messages)

#### ERROR `.`

- `DHCP setup failed`: `error`
- `FRR network setup failed, falling back to DHCP`: `error`
- `a/b preserveExisting requires kexec; refusing normal reboot because firmware boot state still points at the active slot`
- `applying provisioner files to the ramdisk failed, waiting`: `error`
- `early mount/device setup failed`: `error`
- `failed to acquire jwt token`: `error`
- `failed to create CAPRF client`: `error`
- `failed to get CAPRF config`: `error`
- `failed to start token renewal`: `error`
- `gobgp network setup failed`: `error`
- `handoff failed and the network teardown still runs, not setting the network up again`
- `hardware watchdog not armed`: `error`
- `mode exited with error`: `error`, `mode`
- `network connectivity failed after all retries`: `attempts`
- `network connectivity timeout`: `attempt`
- `network setup after the failed handoff did not finish in time`: `timeout`
- `network setup after the failed handoff failed`: `error`
- `network setup failed`: `error`
- `no connectivity after the failed handoff`: `error`
- `provisioning handoff failed after success report`: `action`, `error`
- `run failed`: `code`, `error`, `step`
- `run stopped`: `reason`
- `static network setup failed, falling back to DHCP`: `error`
- `token can not be renewed, stopping the task`: `reason`
- `unknown operating mode`: `error`, `mode`
- `vlan setup failed`: `error`, `parent`, `vlan`

#### ERROR `cmd/booty-esp-sync`

- `booty-esp-sync failed`: `error`

#### ERROR `pkg/auth`

- `token renewal failed for good`: `error`

#### ERROR `pkg/caprf`

- `token expiry reached`: `error`

#### ERROR `pkg/disk`

- `disk is in use, not discarded`: `device`, `error`

#### ERROR `pkg/espsync`

- `EFI synchronization failed`: `error`
- `write EFI synchronization marker`: `error`

#### ERROR `pkg/image`

- `checksum mismatch: wiping partition table to prevent booting corrupt image`: `device`, `error`
- `image sync failed: attempting to wipe partial image`: `device`, `error`
- `image write failed: wiping partial image`: `device`, `error`
- `qcow2 image sync failed: attempting to wipe partial image`: `device`, `error`
- `qcow2 write failed: wiping partial image`: `device`, `error`

#### ERROR `pkg/linkcheck`

- `check result not encoded`: `error`

#### ERROR `pkg/network/frr`

- `FRR dump command failed`: `error`, `label`

#### ERROR `pkg/network/gobgp`

- `BGP peer deleted but not added again, it stays down`: `error`, `neighbor`
- `watch best paths`: `error`

#### ERROR `pkg/network/lldp`

- `panic in LLDP listener goroutine`: `interface`, `panic`

#### ERROR `pkg/provision`

- `deprovisioning step failed`: `error`, `step`
- `failed to load checkpoint`: `error`
- `failed to report error status`: `error`
- `failed to save checkpoint`: `error`
- `no 2xx for the reboot-required status, rebooting anyway; the host may not boot BOOTy again`: `attempts`, `error`
- `provisioning step failed`: `error`, `step`

#### ERROR `pkg/realm`

- `halt failed`: `error`
- `power off failed`: `error`
- `reboot failed`: `error`
- `shell error`: `error`
- `suspend failed`: `error`

#### ERROR `pkg/rescue`

- `rescue SSH: the clock was not synced (TIME_SYNC), so the PK Token times are checked against the RTC`

#### ERROR `pkg/runmode`

- `command not run`: `error`, `id`, `type`
- `deprovisioning failed`: `error`
- `dry-run failed`: `error`
- `health check failed`: `check`, `message`, `severity`
- `hot deprovision failed`: `error`
- `hot provision failed`: `error`
- `provisioning failed`: `error`

#### ERROR `pkg/timesync`

- `NTP time differs from the authenticated CAPRF Date time, using the Date time`: `difference`, `max_skew`, `ntp`
- `no time source answered and the clock is before the build of BOOTy; TLS certificate checks will fail`: `clock`, `code`
- `refusing to set a plausible clock back on the CAPRF Date header alone`: `clock`, `max_skew`, `set_back_by`

#### ERROR `pkg/watchdog`

- `step made no progress within its budget, stopping the watchdog keepalive; the hardware resets the host`: `budget`, `code`, `idle`, `reset_within`, `step`

### WARN (403 messages)

#### WARN `.`

- `LLDP not restarted: the senders of the last stop are still running`
- `LLDP not sent: no physical NICs`: `error`
- `LLDP senders did not stop in time, handing off without waiting`: `timeout`
- `NICs that appear later send no LLDP`: `error`
- `failed to clean up target root after unsuccessful kexec`: `error`, `root`
- `failed to detect IPMI info`: `error`
- `failed to list child processes`: `error`
- `failed to reap child process`: `error`
- `failed to report provisioning handoff error`: `error`
- `failed to report the run failure`: `error`
- `failed to report the token failure`: `error`
- `failed to set PATH`: `error`
- `firmware LLDP agent not disabled`: `error`, `flag`, `interface`
- `firmware LLDP agent not disabled: SR-IOV ports unknown`: `error`
- `firmware LLDP agent not disabled: no physical NICs`: `error`
- `handoff failed after the network stop, setting the network up again`
- `ignoring log setting`: `error`
- `kernel log not sent to DEBUG_URL`: `reason`
- `kernel module failed to load`: `error`, `module`
- `kernel.panic not set`: `error`
- `kernel.panic not set: cannot read the kernel command line`: `error`
- `kexec execute failed, falling back to reboot`: `error`
- `kexec load failed, falling back to reboot`: `error`
- `link watch for LLDP failed, subscribing again`: `error`
- `link-layer cleanup after GoBGP setup failure failed`: `error`
- `link-layer cleanup after links setup failure failed`: `error`
- `link-layer cleanup after resolver setup failure failed`: `error`
- `link-layer cleanup after the FRR payload check failed`: `error`
- `netplan match blocks with emit-lldp not resolved`: `error`
- `netplan match for the static address did not select one NIC, using the first NIC`: `error`
- `network mode cleanup after resolver setup failure failed`: `error`
- `network teardown before the handoff did not finish, going on`: `timeout`
- `network teardown before the handoff failed`: `error`
- `network teardown failed`: `error`
- `network.bgp.sessions (BGP_SESSIONS) applies to GoBGP mode only; the FRR stack ignores it`
- `no BootyLastResult UEFI variable: efivarfs is not available`: `error`
- `no kexec boot entry, rebooting instead`: `error`
- `no usable watchdog device, arming the BMC watchdog through IPMI`: `error`
- `setting the BGP hostname failed`: `error`, `hostname`
- `setting the ramdisk hostname failed`: `error`, `hostname`
- `startup crash artifact inspection failed`: `error`
- `static teardown failed during DHCP fallback`: `error`
- `target filesystems are still writable at kexec, the installed OS may replay their journal`: `error`, `writable`
- `target filesystems unknown at kexec, the installed OS may replay their journal`: `error`, `root`
- `token can not be renewed after the task completed`: `reason`
- `vars key ignored: the BGP sessions come from frr.conf or network.bgp.sessions`: `key`
- `watchdog handoff failed`: `error`

#### WARN `cmd/booty-esp-sync`

- `ignoring log setting`: `error`
- `peer EFI system partition is missing, the mirror is degraded; nothing to synchronize`: `peer`, `source`
- `unmount destination ESP`: `error`, `path`

#### WARN `pkg/auth`

- `renewal attempt failed`: `attempt`, `error`, `of`
- `token renewal failed without fatal handler, continuing retry loop`

#### WARN `pkg/caprf`

- `CA_BUNDLE_FILE has no effect on the CAPRF callbacks, the connectivity check and image downloads: SKIP_TLS_VERIFY=true skips their certificate check`
- `command payload does not parse or validate`: `keys`
- `crash artifact prepare failed`: `attempt`, `error`, `url`
- `crash artifact upload failed`: `attempt`, `error`, `url`
- `deploy archive vars have unknown keys, ignored`: `keys`
- `insecure transport enabled: bearer tokens will be sent over plain HTTP`
- `no URL configured for status, skipping`: `status`
- `no health check URL configured, skipping report`
- `no inventory URL configured, skipping inventory report`
- `refusing request to non-HTTPS endpoint`: `url`
- `request failed`: `attempt`, `error`, `url`
- `tls certificate verification disabled, like curl --insecure in the legacy CAPRF provisioner`

#### WARN `pkg/crash`

- `crash artifact collection failed`: `error`
- `crash artifact read-only mount failed`: `error`, `partition`
- `crash artifact root partition resolution failed`: `error`, `reason`
- `crash artifact unmount failed`: `error`, `mountpoint`
- `crash artifact upload failed`: `error`
- `failed to add crash artifact`: `error`, `path`

#### WARN `pkg/disk`

- `ATA security erase failed`: `device`, `output`
- `block devices still changing, continuing`: `devices`, `waited`
- `cannot list disks for discard`: `error`, `path`
- `cannot list partitions and holders, wiping the disk only`: `device`, `error`
- `cannot list the partitions to look for LUKS2 headers`: `device`, `error`
- `cannot parse lsblk output, wiping the disk only`: `device`, `error`
- `cannot read removable attribute; treating as removable`: `device`, `err`
- `cannot read the device to look for a LUKS2 header`: `device`, `error`
- `cannot read the disk vendor for the secure erase method`: `device`, `error`
- `cannot remove the member label, wipefs clears it`: `device`, `error`, `output`
- `cannot unmount, leaving it`: `error`, `mount`
- `cannot zero the device edge`: `device`, `error`, `output`
- `chroot bind rollback failed`: `error`, `path`
- `chroot unmount failed`: `error`, `path`
- `device state: cannot read /proc/partitions`: `error`, `label`
- `disk is in use, skipping it in the wipe of all disks`: `device`, `reason`
- `duplicate disk link, keeping the first device`: `device`, `link`
- `e2fsck not found, skipping filesystem check`: `device`
- `e2fsck returned non-zero (errors corrected)`: `device`, `exit_code`, `output`
- `failed to attach fallback namespace after reset failure`: `controller`, `error`, `fallback_blocks`, `fallback_nsid`
- `failed to clear security password`: `device`, `output`
- `failed to clear temporary security password after erase`: `device`, `error`, `output`
- `failed to create fallback namespace after reset failure`: `controller`, `error`, `fallback_blocks`
- `failed to detect filesystem type before resize, falling back to resize tools`: `device`, `error`
- `full-capacity namespace creation failed; attached fallback namespace to keep controller accessible`: `controller`, `fallback_blocks`, `fallback_nsid`
- `lsblk disk selection is ambiguous: several disks of the same bus qualify and their names can change between boots; select the root disk with DISK_SERIAL_NUMBER, and exclude smaller disks with MIN_DISK_SIZE_GB`: `candidates`, `selected`
- `lsblk failed`: `error`, `include`, `output`
- `lsblk serial lookup failed`: `err`, `serial`
- `mdadm --zero-superblock failed, continuing`: `device`, `error`, `output`
- `mdev -s failed`: `error`, `output`
- `partprobe failed, trying blockdev --rereadpt`: `disk`, `error`, `output`
- `secure erase failed, SECURE_ERASE_FALLBACK=wipe: wiping the disk instead; the data is not erased`: `device`, `error`
- `sgdisk --zap-all failed, wipefs clears the partition table signatures`: `device`, `error`, `output`
- `skipping removable media device`: `device`
- `sync before partprobe failed`: `error`, `output`
- `wipefs failed`: `device`, `error`, `output`

#### WARN `pkg/disk/luks`

- `isLUKS check failed`: `device`, `error`

#### WARN `pkg/efi`

- `restoring the immutable flag failed`: `error`, `path`

#### WARN `pkg/espsync`

- `EFI watcher error`: `error`

#### WARN `pkg/executil`

- `debug dump`: `count`, `data`, `dir`, `error`, `label`

#### WARN `pkg/image`

- `HTTP request failed, retrying`: `attempt`, `backoff`, `error`
- `HTTP server error, retrying`: `attempt`, `backoff`, `status`
- `OCI pull failed, retrying`: `attempt`, `backoff`, `error`, `ref`
- `cannot read the target past its page cache, writing every block`: `error`
- `failed to close device after wipe`: `device`, `error`
- `failed to detach loop device`: `device`, `error`
- `failed to open device for wipe`: `device`, `error`
- `failed to sync wipe`: `device`, `error`
- `failed to unmount ramdisk`: `error`
- `failed to wipe partition table`: `device`, `error`
- `failed to wipe the end of the device`: `device`, `error`
- `hole punch failed, writing zeros from here on`: `error`
- `image download broke, resuming`: `attempt`, `error`, `offset`
- `image range failed, retrying`: `attempt`, `error`, `offset`
- `image source probe failed`: `error`, `url`
- `mdev -s failed after partition settle`: `disk`, `error`, `output`
- `partprobe failed, trying blockdev --rereadpt`: `disk`, `error`, `output`
- `source image has no EFI partition; leaving shared boot partition unchanged`
- `sync before partition settle failed`: `disk`, `error`, `output`

#### WARN `pkg/linkcheck`

- `check result sink failed`: `error`, `sink`

#### WARN `pkg/network`

- `Cannot bring up link for DHCP`: `error`, `interface`
- `Cannot find link for DHCP`: `error`, `interface`
- `Cannot set promote_secondaries for DHCP`: `error`, `interface`
- `DHCP lease ended (expired or refused), address removed until a new lease`: `addr`, `iface`
- `NIC keeps its kernel name`: `error`, `nic`, `udev_name`
- `NICs keep their kernel names`: `error`
- `NICs that appear later get no DHCP client`: `error`
- `configured NICs are missing after the wait`: `nics`, `wait`
- `failed to add a DHCP source route`: `error`, `iface`, `route`
- `failed to add the DHCP source rule`: `error`, `iface`
- `failed to add the DHCPv6 address`: `addr`, `error`, `iface`
- `failed to apply DHCP lease`: `error`, `iface`
- `failed to bring down slave`: `error`, `name`
- `failed to enslave interface`: `error`, `name`
- `failed to list addresses for DHCP prefix cleanup`: `error`, `iface`
- `failed to remove DHCP address with an old prefix`: `addr`, `error`, `iface`
- `failed to remove a DHCP source rule`: `error`
- `failed to remove a route of an ended DHCP lease`: `error`, `iface`, `route`
- `failed to remove an old DHCP route`: `error`, `iface`, `route`
- `failed to remove bond`: `error`
- `failed to remove superseded DHCP address`: `error`, `iface`
- `failed to remove the DHCPv6 address`: `error`, `iface`
- `failed to remove the address of an ended DHCP lease`: `error`, `iface`
- `failed to restore DHCP address`: `error`, `iface`
- `failed to restore the MTU of an ended DHCP lease`: `error`, `iface`
- `failed to roll back DHCP address`: `error`, `iface`
- `failed to set the DHCP MTU`: `error`, `iface`, `mtu`
- `failed to write the DHCP DNS servers`: `error`, `search`, `servers`
- `ignoring DHCP lease`: `error`, `iface`
- `link watch for DHCP failed, subscribing again`: `error`
- `links mode has no routed network, not waiting`: `target`
- `netplan set-name needs exactly one matching NIC, skipping`: `match_mac`, `match_name`, `matches`, `set_name`
- `network connectivity established, but the TLS certificate is not valid at the local clock`: `attempt`, `error`, `target`
- `no DHCPv6 on this NIC`: `error`, `interface`
- `slave interface not found`: `error`, `name`
- `unknown bond mode, using 802.3ad`: `mode`

#### WARN `pkg/network/frr`

- `=== FRR STATE DUMP END ===`
- `=== FRR STATE DUMP START ===`
- `Cannot create dummy interface, using loopback for underlay IP`: `error`
- `FRR`: `data`, `label`
- `Failed to configure NIC`: `error`, `nic`
- `Network connectivity established, but the TLS certificate is not valid at the local clock`: `attempt`, `error`, `target`
- `connectivity deadline reached, dumping FRR state for diagnostics`
- `connectivity timeout — dumping FRR state for diagnostics`
- `failed to create FRR directory`: `error`, `path`
- `failed to fully clean FRR network resources`: `error`
- `failed to restart FRR daemons`: `error`
- `failed to set FRR directory ownership`: `error`, `path`
- `failed to start daemon`: `daemon`, `error`
- `failed to stop FRR`: `error`
- `frr daemons still running after SIGTERM; sending SIGKILL`: `error`
- `frrinit.sh start failed, falling back to direct daemon start`

#### WARN `pkg/network/frrconf`

- `frr.conf statement skipped`: `error`, `line`, `statement`

#### WARN `pkg/network/gobgp`

- `BFD does not listen before the peer starts it`: `error`, `peer`
- `BFD down, resetting the BGP session`: `diag`, `peer`
- `BFD session not moved; the BGP session runs without it`: `error`, `peer`
- `BFD session not started; the BGP session runs without it`: `error`, `peer`
- `BFD: no link-local peer address`: `interface`
- `BGP session came up while BFD stayed down, resetting it`: `peer`
- `EVPN route without VXLAN encapsulation treated as withdraw`: `tunnel_types`, `type`, `vtep`
- `FIB update failed`: `error`
- `Failed to add BGP peer`: `error`, `neighbor`
- `Failed to add numbered peer`: `addr`, `error`
- `Failed to add unnumbered peer`: `error`, `nic`
- `Failed to assign NIC to VRF`: `error`, `nic`
- `Failed to bring up NIC`: `error`, `nic`
- `Failed to find NIC`: `error`, `nic`
- `Failed to find VRF for NIC assignment`: `error`, `nic`, `vrf`
- `Failed to release NIC from VRF`: `error`, `nic`
- `Failed to remove underlay dummy`: `error`
- `Failed to set MTU`: `error`, `nic`
- `Failed to tear down underlay after overlay failure`: `error`
- `Overlay teardown error`: `error`
- `RAs do not start fast again after a carrier loss`: `error`
- `Underlay teardown error`: `error`
- `a BGP peer refuses connections in Idle after its session ends`: `error`
- `accept-ra: false not applied`: `error`, `nic`
- `bridge MAC or provision IP not set, skipping type-2 advertisement`
- `cannot find VXLAN for BUM update`: `error`, `vxlan`
- `cannot find VXLAN for FDB update`: `error`, `vxlan`
- `cannot find VXLAN for type-5 gateway FDB update`: `error`, `vxlan`
- `cannot find bridge for neighbor update`: `bridge`, `error`
- `cannot find bridge for route install`: `bridge`, `error`
- `cannot install underlay VTEP route for type-5`: `error`, `vtep`
- `eBGP peer has no route-map, routes rejected like FRR bgp ebgp-requires-policy`: `direction`, `family`, `peer`
- `failed to add/update FDB entry`: `error`, `mac`, `via`
- `failed to delete FDB entry`: `error`, `mac`, `via`
- `failed to delete VRF`: `error`, `name`
- `failed to delete nexthop at teardown`: `error`, `id`
- `failed to delete nexthop, retried later`: `error`, `id`
- `failed to delete type-5 route after failed fallback`: `dst`, `error`
- `failed to detect physical NICs for MTU setup`: `error`
- `failed to find NIC for MTU`: `error`, `nic`
- `failed to install ES nexthop group, its MACs use their VTEP`: `error`, `esi`, `vteps`
- `failed to install VTEP nexthop`: `error`, `vtep`
- `failed to install gateway route`: `error`
- `failed to install route from type-5`: `dst`, `error`, `gw`
- `failed to install type-5 gateway FDB entry`: `error`, `mac`, `vtep`
- `failed to install type-5 gateway neighbor`: `error`, `ip`, `mac`
- `failed to remove blackhole route`: `dst`, `error`
- `failed to remove interface`: `error`, `name`
- `failed to remove lo address`: `error`, `ip`
- `failed to remove overlay loopback IP`: `error`, `ip`
- `failed to restore type-5 gateway neighbor`: `error`, `ip`, `mac`
- `failed to set MTU`: `error`, `mtu`, `nic`
- `failed to set multicast hop limit`: `error`, `iface`
- `failed to tear down overlay after setup failure`: `error`, `step`
- `gateway BUM FDB entry failed (non-fatal)`: `error`
- `install route`: `error`, `route`
- `link updates`: `error`
- `move unnumbered peer to the new link-local address`: `error`, `interface`, `new`, `old`
- `network is not in the local RIB, not advertised (import check)`: `prefix`
- `next hop not reachable; its routes are not installed`: `error`, `nexthop`, `table`
- `no link-local neighbor yet; the peer moves to the switch address when it answers`: `error`, `interface`
- `no remaining type-5 path could be installed, removed route`: `dst`
- `old peer address stays in its neighbor sets`: `error`, `peer`
- `overlay_vrf_table_id=1 conflicts with default routing table, using underlay VRF table`
- `policy config (communities, local-pref, MED) is configured but not yet applied to BGP sessions`
- `provision IP not set, skipping EVPN Type-5 advertisement`
- `remove route`: `error`, `route`
- `reset BGP peer`: `error`, `peer`, `reason`
- `route updates`: `error`
- `route watcher stopped`: `error`
- `router solicitations are not answered`: `error`, `nic`
- `set ignore_routes_with_linkdown`: `error`
- `skip paths`: `error`
- `skip route`: `error`, `prefix`
- `start BGP session`: `error`, `neighbor`
- `start the BGP peer again`: `error`, `neighbor`
- `stop BGP session`: `error`, `neighbor`
- `type-5 route for a prefix the underlay installed in the same table, underlay route stays`: `prefix`, `table`, `vtep`
- `type-5 route treated as withdraw`: `len`, `prefix`, `reason`, `vtep`
- `vrf_table_id=1 conflicts with default routing table, overriding to 1000`
- `watch kernel routes; recursive next hops follow only the periodic resync`: `error`
- `watch uplink carrier; sessions end only after the hold time`: `error`

#### WARN `pkg/network/lldp`

- `LLDP frame dropped: decoder panic`: `interface`, `panic`
- `LLDP frame not sent`: `error`
- `LLDP receive not started`: `error`, `interface`
- `LLDP receive not started: no physical NICs`: `error`
- `link watch for LLDP failed, subscribing again`: `error`
- `link watch for LLDP not started, trying again`: `error`

#### WARN `pkg/network/netname`

- `NIC already gets another configured name`: `also`, `name`, `nic`
- `several NICs have this predictable name, not renaming`: `name`, `nics`

#### WARN `pkg/network/netplan`

- `frr.conf statement parsed but not applied yet`: `family`, `line`, `neighbor`, `rd`, `router`, `statement`, `vni`
- `frr.conf statement skipped (FRR_CONFIG_STRICT=false)`: `error`, `file`, `line`, `statement`
- `netplan key ignored (NETPLAN_STRICT=false)`: `error`, `file`
- `netplan route skipped (NETPLAN_STRICT=false): table, metric, scope and from are not supported`: `device`, `to`

#### WARN `pkg/network/vlan`

- `failed to cleanup partially configured vlan`: `error`, `vlan`
- `vlan teardown error`: `error`, `vlan`

#### WARN `pkg/oob`

- `reading the BootyLastResult UEFI variable failed`: `error`
- `the BootyLastResult UEFI variable is invalid`: `error`
- `writing the BootyLastResult UEFI variable failed`: `error`

#### WARN `pkg/provision`

- `=== CONFIG DUMP ===`: `asn`, `bfd_receive_ms`, `bfd_transmit_ms`, `bgp_hold`, `bgp_interfaces`, `bgp_keepalive`, `bond_interfaces`, `dcgw_ips`, `dns_resolver`, `hostname`, `images`, `ipmi_subnet`, `leaf_asn`, `local_asn`, `min_disk_size_gb`, `mode`, `overlay_aggregate`, `overlay_subnet`, `provision_ip`, `provision_vni`, `static_gateway`, `static_ip`, `underlay_ip`, `underlay_subnet`, `vpn_rt`, `vrf_table_id`
- `=== DEBUG DUMP END ===`: `failedStep`
- `=== DEBUG DUMP START ===`: `failedStep`
- `EFI variables unavailable, skipping the EFI boot entry for the mirror disk; it boots through the removable fallback loader`: `disk`, `reason`
- `Failed to marshal hardware inventory`: `error`
- `Failed to report hardware inventory`: `error`
- `Firmware collection failed, continuing`: `error`
- `Firmware validation`: `message`, `name`, `status`
- `Hardware inventory collection failed`: `error`
- `No EFI partition found`: `error`
- `SR-IOV firmware changed: rebooting into the ramdisk to verify the runtime settings`: `status`
- `SR-IOV inventory after configure-sriov failed`: `error`
- `SRIOV_VERIFY_REBOOT=off: the runtime settings that wait for the firmware change are not verified`: `detail`
- `Secure Boot update skipped: not a signed signature list`: `error`, `file`, `variable`
- `Secure Boot updates of the image are not applied (SECUREBOOT_APPLY_DBX_UPDATES=false); if secureboot-db applies one on the first boot, PCR 7 changes after the binding`
- `bundled EFI fallback loader unavailable, falling back to target-root grub-install`: `error`
- `cannot create serial console run directory`: `error`, `path`
- `cannot list target disk partitions`: `disk`, `error`
- `cannot publish serial console artifact for CAPRF`: `error`, `path`
- `cannot resolve the /boot fstab source on the target disk, keeping /boot on the root filesystem`: `source`
- `create EFI boot entry failed, continuing because EFI_BOOT_ENTRY_REQUIRED=false`: `error`
- `create mirror EFI boot entry failed, continuing because EFI_BOOT_ENTRY_REQUIRED=false`: `error`
- `crypto-erase is off (DEPROVISION_CRYPTO_ERASE=false); LUKS2 keyslots stay until the wipe, which may leave them`
- `debug`: `data`, `label`
- `debug command failed`: `cmd`, `error`, `label`
- `debug env`: `var`
- `decommission: disabling LVM failed, continuing`: `error`
- `decommission: mounting efivarfs failed, continuing`: `error`
- `decommission: stopping RAID arrays failed, continuing`: `error`
- `disk encryption is not possible, provisioning without it (DISK_ENCRYPTION_ALLOW_FAILURE)`: `error`
- `disk wipe failed, continuing`: `error`
- `efibootmgr list failed (non-EFI system?)`: `error`, `output`
- `enroll module signing key failed, continuing because MODULE_SIGNING_ALLOW_FAILURE=true`: `error`
- `failed to remove EFI entry`: `entry`, `error`, `output`
- `failed to remove checkpoint`: `error`
- `failed to remove shared data seed mountpoint`: `error`, `mountpoint`
- `failed to report dry-run status`: `error`
- `failed to report health checks`: `error`
- `failed to save checkpoint`: `error`
- `failed to unmount active A/B slot`: `error`, `mountpoint`
- `failed to unmount shared data from earlier attempt`: `error`
- `failed to unmount shared data seed target; leaving mountpoint intact`: `error`, `mountpoint`
- `image format probe failed, retrying before destructive storage`: `attempt`, `error`, `url`
- `invalid rescue mode, defaulting to reboot`: `error`, `mode`
- `marshal provisioning report`: `error`
- `mellanox: cannot list PCI devices, skipping`: `error`
- `mount efivarfs failed, continuing because EFI_BOOT_ENTRY_REQUIRED=false`: `error`
- `nic firmware collection failed`: `error`
- `no /boot/efi mountpoint found in partition layout; efi boot entry creation may be skipped`
- `no /boot/grub on the root filesystem and no /boot entry in fstab`
- `no EFI partition found, skipping EFI boot entry creation`
- `no serial console configured`: `baud`, `conflicts`, `device`, `reason`, `selectedBy`, `state`
- `provisioning report not written to the target`: `error`, `path`
- `provisioning report: reporting the configured partition layout`: `error`
- `read the CI module signing certificate failed, continuing because MODULE_SIGNING_ALLOW_FAILURE=true`: `error`
- `remove EFI boot entries failed, continuing because EFI_BOOT_ENTRY_REQUIRED=false`: `error`
- `reporting the reboot-required status failed, retrying`: `attempt`, `error`
- `retrying step`: `attempt`, `delay`, `step`
- `root /etc/ not mounted, skipping DNS configuration`: `path`
- `serial console override ignored with GRUB_CMDLINE_MODE=append`: `MACHINE_SERIAL_CONSOLE`
- `shared data seed was interrupted, cleaning and retrying`: `device`
- `sign kernel modules failed, continuing because MODULE_SIGNING_ALLOW_FAILURE=true`: `error`
- `skipping EFI boot entry creation`: `reason`
- `skipping boot partition mount; partition is not a usable ESP and EFI runtime is unavailable`: `error`, `partition`, `reason`
- `skipping machine file below a chroot bind mount`: `path`
- `skipping machine file that resolves below a chroot bind mount`: `path`
- `skipping symlink in copy tree`: `path`
- `skipping unsupported shared data seed entry`: `mode`, `path`
- `target root still mounted from an earlier attempt, unmounting`: `root`
- `the UKI signed by the image pipeline keeps its .cmdline: the per-host kernel parameters reach the GRUB fallback entry only`: `params`
- `the firmware refused the Secure Boot update; secureboot-db of the image gets the same answer, so PCR 7 stays`: `error`, `file`, `variable`
- `the firmware variable has a malformed signature list: comparing with the entries before it`: `entries`, `error`, `variable`
- `the image layout cannot be encrypted, provisioning without it (DISK_ENCRYPTION_ALLOW_FAILURE)`: `error`

#### WARN `pkg/realm`

- `attempting a reboot`

#### WARN `pkg/rescue`

- `auto-mount failed`: `error`
- `could not start SSH daemon`: `error`
- `ignoring malformed RESCUE_SSH_OPK_POLICY row`: `reason`, `row`
- `ignoring malformed RESCUE_SSH_OPK_PROVIDERS row`: `reason`, `row`
- `ignoring malformed rescue SSH key`: `entry`
- `ignoring rescue SSH key with unsupported options`: `entry`, `error`
- `ignoring the opkssh settings`: `error`
- `rescue SSH accepts no opkssh login`: `error`
- `rescue SSH allows password login: a password hash is set and no SSH key`
- `rescue SSH connection refused: too many connections without a login`: `remote`
- `rescue SSH hardening disabled (RESCUE_SSH_HARDENED=false): password login, RSA keys and port forwarding allowed, no idle timeout`
- `rescue SSH has no valid key in RESCUE_SSH_PUBKEY: SSH login is refused, also with a password hash`
- `rescue SSH key is RSA, which only RESCUE_SSH_HARDENED=false accepts`: `entry`
- `rescue SSH login refused`: `issuer`, `key`, `method`, `principal`, `reason`, `remote`, `subject`
- `rescue SSH remote forward failed`: `error`, `port`
- `rescue SSH server stopped`: `error`
- `rescue SSH session did not start`: `error`

#### WARN `pkg/runmode`

- `LLDP neighbor wait skipped: no physical NICs`: `error`
- `cabling checks without NICs`: `error`
- `command poll failed`: `error`
- `failed to ACK command`: `cmdID`, `error`
- `health check warning`: `check`, `message`, `severity`
- `heartbeat failed`: `error`
- `no LLDP neighbor on NICs with carrier`: `nics`
- `provisioning stopped for a reboot into the ramdisk`: `status`
- `rescue setup error`: `error`
- `standby status after deprovisioning failed`: `error`
- `unknown command type`: `type`

#### WARN `pkg/secureboot`

- `cannot determine secure boot status`: `error`
- `cannot determine setup mode`: `error`
- `digest validation failed, trying next candidate`: `component`, `error`, `path`
- `pe/coff validation failed, trying next candidate`: `component`, `error`, `path`

#### WARN `pkg/sriov`

- `SR-IOV drift`: `detail`, `got`, `interface`, `level`, `onDrift`, `profile`, `setting`, `status`, `want`
- `SR-IOV journal`: `error`
- `SR-IOV runtime check deferred to a verify reboot`: `detail`, `interface`, `profile`

#### WARN `pkg/telemetry`

- `retry recorded for unknown step`: `step`
- `step end called for unknown or non-running step`: `step`

#### WARN `pkg/timesync`

- `NTP query failed`: `error`, `server`
- `ignoring a time source that is before the build of BOOTy`: `floor`, `source`, `time`
- `ignoring a time source that is more than five years after the build of BOOTy`: `floor`, `source`, `time`
- `no time source answered, keeping the RTC time`: `clock`
- `reading the time from the CAPRF Date header failed`: `error`
- `setting the system clock failed`: `error`
- `writing the RTC failed, the installed OS starts with the old time`: `error`

#### WARN `pkg/trust`

- `system root certificates unavailable, trusting only the CA bundle`: `error`, `path`

#### WARN `pkg/watchdog`

- `progress resumed, petting the watchdog again`: `step`
- `watchdog keepalive failed`: `error`

### INFO (496 messages)

#### INFO `.`

- `BGP hostname set`: `hostname`
- `CAPRF mode active`: `hostname`, `image_count`, `mode`
- `CAPRF run complete`
- `KEXEC_UNMOUNT_TARGET=false, keeping the target mounted for kexec`: `root`
- `attempting kexec`: `initrd`, `kernel`
- `beginning provisioning process`
- `cleaned up target root after unsuccessful kexec`: `root`
- `detected IPMI info`: `ip`, `mac`
- `detected netplan configuration files`: `dir`
- `dry-run mode: not arming the hardware watchdog`
- `dry-run mode: skipping active network setup and token renewal`
- `executing mode`: `mode`
- `firmware LLDP agent disabled`: `flag`, `interface`
- `firmware LLDP agent left to the SR-IOV profile`: `interface`
- `firmware values changed during provisioning, hard reboot required — skipping kexec`
- `handoff failed, sending LLDP frames again`
- `hardware watchdog armed`: `device`, `handoff`, `keepalive_every`, `timeout`
- `keeping the provisioner resolv.conf in the ramdisk`
- `keeping the ramdisk running after the failure`
- `kernel security state`: `kernel`, `secureBoot`
- `kexec boot entry`: `entry`
- `kexec disabled by configuration, skipping`: `successAction`
- `loaded kernel modules`: `count`, `duration`, `failed`, `modules`
- `loaded watchdog module`: `module`
- `netplan config loaded`: `asn`, `mode`, `underlay`, `vni`
- `netplan: provision VNI is an FRR L3VNI, using Type-5 routes only`: `vni`
- `network connectivity established`: `target`
- `network stopped before the handoff`
- `network.bgp.bfdProfiles replaces the frr.conf BFD profiles`: `native`
- `network.bgp.communityLists replaces the frr.conf community-lists`: `frr.conf`, `native`
- `network.bgp.routeMaps replaces the frr.conf route-maps`: `frr.conf`, `native`
- `network.bgp.sessions replaces the frr.conf sessions`: `frr.conf`, `native`
- `network.blackholeRoutes replaces the netplan or frr.conf value`: `native`, `parsed`
- `network.devices replaces the provisioner netplan files`: `dir`
- `network.emitLLDP replaces the netplan or frr.conf value`: `native`, `parsed`
- `network.evpn.type5Networks replaces the netplan or frr.conf value`: `native`, `parsed`
- `network.loopbackAddresses replaces the netplan or frr.conf value`: `native`, `parsed`
- `network.noAcceptRA replaces the netplan or frr.conf value`: `native`, `parsed`
- `no BootyLastResult UEFI variable: not a UEFI host`
- `no EFI firmware, skipping kernel module`: `module`
- `no IPMI interface found, using the underlay IP rules`
- `out-of-band status`: `efi_var`, `run`, `serial`
- `parsed FRR configuration`: `asn`, `evpn`
- `provisioning succeeded, powering off for orchestrator to manage boot`
- `ramdisk hostname set`: `hostname`
- `released target filesystems before kexec`: `leftAlone`, `readOnly`, `root`, `unmounted`
- `secure boot re-enable or MOK enrollment requested, hard reboot required; skipping kexec`
- `setting up LACP bond`
- `setting up VLAN interfaces`: `count`
- `starting BOOTy`: `build`, `uptime`, `version`
- `startup crash artifact inspection complete`: `evidence`, `skip_reason`, `uploaded`
- `tearing down network for retry`: `attempt`
- `time sync is off, keeping the RTC time`: `clock`
- `using DHCP network mode`
- `using FRR/EVPN network mode`: `asn`
- `using GoBGP/EVPN network mode`: `asn`
- `using links network mode`
- `using static network mode`: `ip`
- `waiting for network connectivity`: `attempt`, `target`, `timeout`

#### INFO `cmd/booty-esp-sync`

- `peer EFI system partition is back`: `peer`
- `synchronizing EFI system partition`: `destination`, `source`

#### INFO `pkg/auth`

- `jwt acquired`: `expiresIn`
- `jwt renewed`: `expiresIn`

#### INFO `pkg/caprf`

- `jwt token acquired, using for subsequent API calls`
- `retrying crash artifact request`: `attempt`, `backoff`, `url`
- `retrying request`: `attempt`, `backoff`, `url`

#### INFO `pkg/disk`

- `ATA secure erase`: `device`
- `LUKS2 header already gone`: `device`, `luksUUID`
- `NVMe secure erase`: `device`
- `RAID array created`: `name`
- `SCSI FORMAT UNIT complete`: `device`
- `SCSI SANITIZE complete`: `action`, `device`
- `SCSI secure erase`: `device`
- `activating LVM volume groups`
- `added mirror member`: `array`, `member`
- `applying partition layout`: `device`, `partitions`, `table`
- `bash unavailable in chroot, trying /bin/sh`: `root`
- `bash unavailable in syscall chroot, trying /bin/sh`: `root`
- `bind mount target already mounted, skipping`: `source`, `target`
- `blkdiscard complete`: `device`
- `blkdiscard failed, falling back to signature wipe`: `device`, `error`, `output`
- `blkdiscard not supported or failed`: `device`, `error`, `output`
- `block devices changed, waiting for them to settle`: `devices`
- `block devices settled`: `devices`
- `block devices unchanged since they settled`: `devices`
- `blockdev --rereadpt succeeded`: `disk`
- `cannot remove the member label, wipefs clears it: tool not available`: `device`, `error`
- `cannot zero the device edge: tool not available`: `device`, `error`
- `checking filesystem`: `device`
- `checking filesystem in preen mode`: `device`
- `chroot binary not found, using syscall fallback`: `root`
- `created /dev/disk links`: `count`
- `created degraded mirror`: `array`, `member`
- `created logical volume`: `lv`, `vg`
- `created nvme namespace`: `controller`, `label`, `nsid`, `sizePct`
- `created partition`: `fs`, `label`, `num`, `sizeMB`
- `creating RAID array`: `devices`, `level`, `name`
- `creating nvme namespace`: `blockSize`, `blocks`, `controller`
- `deactivating LVM volume groups`
- `detecting target disk`: `minSizeGB`
- `detecting target disk by serial`: `minSizeGB`, `serial`
- `detecting target disk like the legacy CAPRF provisioner`: `minSizeBytes`
- `device appeared`: `device`, `iteration`
- `device appeared after mdev`: `device`, `iteration`
- `device state`: `dev_nodes`, `label`, `proc_partitions`, `sys_entries`
- `disk candidate`: `device`, `majors`, `sizeBytes`
- `disk has no partition table`: `disk`
- `formatting nvme namespace`: `blockSize`, `device`
- `formatting partition`: `device`, `filesystem`
- `growing partition`: `disk`, `partition`
- `mounted partition`: `device`, `fsType`
- `mounting partition`: `device`, `mountpoint`
- `nvme namespaces reset to factory default`: `controller`
- `partition already at max size`
- `performing secure erase on all disks`
- `re-reading partition table`: `disk`
- `remounted busy mount read-only`: `mount`, `unmountError`
- `resetting nvme namespaces to factory default`: `controller`
- `resizing filesystem`: `device`, `mountpoint`
- `selected disk`: `device`, `nvme`, `sizeGB`
- `selected disk by serial`: `device`, `serial`
- `selected mirror pair`: `disk1`, `disk2`, `sizeBytes`
- `setting up lvm`: `pv`, `vg`, `volumes`
- `sgdisk --zap-all failed, wipefs clears the partition table signatures: tool not available`: `device`, `error`
- `shrank filesystem for mirror metadata`: `device`, `size`
- `skipping format for lvm pv partition`: `label`, `partition`
- `stopping RAID arrays`
- `volume group already exists, skipping create`: `vg`
- `wiping all disks`: `quick`
- `wiping disk`: `device`, `quick`
- `wiping filesystem signatures`: `device`
- `zeroing RAID superblock`: `device`

#### INFO `pkg/disk/luks`

- `LUKS volume closed`: `mapped`
- `LUKS volume formatted`: `cipher`, `device`
- `LUKS volume opened`: `device`, `mapped`

#### INFO `pkg/espsync`

- `synchronizing EFI system partition`: `destination`, `source`

#### INFO `pkg/firmware/nic/ethtool`

- `captured NIC firmware`: `params`, `pci`, `vendor`

#### INFO `pkg/firmware/nic/mellanox`

- `applied Mellanox FW param`: `param`, `value`
- `captured Mellanox NIC firmware`: `params`, `pci`

#### INFO `pkg/image`

- `GPG signature verified successfully (gpg)`
- `GPG signature verified successfully (gpgv)`
- `a/b image streaming`: `boot`, `disk`, `root`, `url`
- `checksum verified`: `type`
- `converting qcow2 to raw`: `dst`, `src`
- `copying partition`: `dst`, `sectors`, `source_partition`, `src`, `type`
- `copying partition table to target disk`: `target`
- `copying source boot partition to shared A/B boot partition`: `dst`, `src`
- `copying source root partition to A/B target slot`: `dst`, `src`
- `decompressing image on ramdisk`: `format`, `src`
- `decompression complete`: `bytes`
- `detected decompressed image format`: `outer_format`, `payload_format`
- `detected image format`: `format`
- `downloaded image format`: `format`
- `downloading image in parallel ranges`: `bytes`, `ranges`
- `image server does not offer ranges with a strong ETag and a length: one GET without resume`: `url`
- `image source probe result`: `response_time`, `url`
- `image write skipped blocks`: `punched_zero_bytes`, `unchanged_bytes`, `written_bytes`
- `image written`: `bytes`, `device`
- `image written to ramdisk`: `bytes`
- `loop device attached`: `device`, `image`
- `partition-by-partition imaging`: `device`, `url`
- `partition-by-partition imaging complete`: `partitions`
- `pulling OCI image`: `ref`
- `qcow2 conversion complete`
- `qcow2 image detected, writing to ramdisk for conversion`: `device`, `source`
- `ramdisk mounted`: `mem_available_mib`, `path`, `size_mib`
- `raw image written to device`: `bytes`
- `root image streaming`: `root`, `url`
- `selected best image source`: `response_time`, `url`
- `selecting best image source`: `candidates`
- `source image has no partition table; copying as root filesystem`: `error`
- `source image is not loop-mountable as a disk image; copying as root filesystem`: `error`
- `streaming image`: `device`, `url`
- `streaming image with an encrypted partition`: `bytes`, `disk`, `partition`, `url`
- `streaming raw image to device`: `device`, `src`
- `streaming source image to A/B root target`: `dst`
- `streaming source partition to A/B target`: `bytes`, `dst`, `partition`
- `verifying image GPG signature`: `image`, `pubkey`, `signature`

#### INFO `pkg/image/oci`

- `initializing new cache index`

#### INFO `pkg/kexec`

- `executing kexec`
- `kernel loaded for kexec`
- `loading kernel for kexec`: `initrd`, `kernel`

#### INFO `pkg/linkcheck`

- `bm4x connection report`: `report`
- `check result`: `check`, `details`, `message`, `severity`, `status`
- `check run done`: `checks`, `diff`, `links`, `status`

#### INFO `pkg/network`

- `DHCP lease applied`: `addr`, `iface`
- `DHCP succeeded`: `interface`
- `DHCP succeeded at the timeout`
- `NIC has carrier again, confirming its DHCP lease`: `interface`
- `NIC has carrier, starting DHCP at once`: `interface`
- `NIC lost carrier, keeping its DHCP lease until the carrier returns`: `interface`
- `Starting DHCP on a new NIC`: `interface`
- `Starting DHCP on all interfaces`: `count`
- `Starting DHCPv6`: `interface`, `managed`
- `address already assigned`: `addr`, `iface`
- `bond interface already exists, reusing`: `name`
- `created bond interface`: `mode`, `name`
- `default route already exists`: `gateway`
- `default route configured`: `gateway`
- `enslaved interface to bond`: `slave`
- `links mode: NICs up with IPv6 link-local only`: `nics`
- `network connectivity established`: `attempt`, `status`, `target`
- `renamed NIC like netplan set-name`: `from`, `to`
- `renamed NIC to its predictable name`: `from`, `to`
- `static IP configured`: `address`, `iface`
- `static network teardown complete`: `iface`
- `undid NIC rename after a failed rename`: `from`, `to`
- `waiting for interface names to stabilize (clab-* detected)`
- `waiting for physical interface set to stabilize`: `nics`
- `waiting for the configured NICs`: `max`, `nics`

#### INFO `pkg/network/bfd`

- `bfd session added`: `address`, `device`, `multihop`, `multiplier`, `peer`, `rx`, `tx`
- `bfd session state changed`: `detection_time`, `diag`, `from`, `peer`, `to`

#### INFO `pkg/network/dhcp4`

- `DHCPNAK, starting over`

#### INFO `pkg/network/dhcp6`

- `DHCPv6 address revoked: valid lifetime 0`: `addr`

#### INFO `pkg/network/frr`

- `Added BGP peer`: `nic`, `vrf`
- `Assigned provision IP to bridge`: `bridge`, `ip`
- `FRR daemons started via frrinit.sh`
- `FRR daemons started via systemctl`
- `FRR setup`: `asn`, `bridge_mac`, `overlay_ip`, `underlay_ip`, `vni`
- `FRR teardown complete`
- `FRR/EVPN network setup complete`: `nics`
- `Network connectivity established`: `attempt`, `target`
- `bgp session established, keeping FRR running`
- `restarting FRR daemons for connectivity recovery`
- `started FRR daemon`: `daemon`

#### INFO `pkg/network/gobgp`

- `Announced underlay route`: `prefix`
- `BFD down for an unknown BGP peer, not resetting it`: `diag`, `peer`
- `BFD down right after the BGP session came up, not resetting it yet, like FRR`: `diag`, `peer`
- `BFD down while the BGP session is coming up, not resetting it yet`: `diag`, `peer`, `state`
- `BGP established, polling target URL`: `target`
- `BGP server started`: `asn`, `port`, `routerID`
- `BGP server stopped`
- `BGP session ended, peer started again at once like FRR`: `neighbor`
- `Created underlay dummy`: `ip`
- `Detected physical NICs`: `nics`
- `ES has no active VTEP, its MACs are not installed`: `esi`
- `ES nexthop group`: `esi`, `id`, `vteps`
- `GoBGP network stack ready`
- `Setting up GoBGP network stack`: `asn`, `routerID`, `vni`
- `Skipping VRF assignment for management NIC`: `nic`
- `Tearing down GoBGP network stack`
- `Waiting for BGP peer connectivity`: `timeout`
- `added BGP peer`: `address`, `families`, `hold`, `internal`, `keepalive`, `localASN`, `neighbor`, `remoteASN`, `vrf`
- `added lo address`: `ip`
- `added numbered BGP peer`: `address`, `remoteASN`, `type`
- `added overlay loopback`: `ip`
- `added unnumbered BGP peer`: `address`, `interface`
- `advertised EVPN type-2 MAC/IP route`: `ip`, `mac`, `vni`
- `advertised EVPN type-3 IMET route`: `vni`, `vtep`
- `advertised EVPN type-5 host route`: `ip`, `vni`
- `advertised EVPN type-5 network`: `prefix`, `vni`
- `assigned provision IP`: `bridge`, `ip`
- `created VXLAN and bridge`: `bridge`, `vni`, `vxlan`
- `deleted type-5 gateway state`: `fdb`, `ip`, `mac`, `neighbor`, `vtep`
- `installed BGP route policies`: `export_statements`, `import_statements`
- `installed BUM FDB entry from type-3 route`: `vtep`
- `installed FDB entry from type-2 route`: `esi`, `mac`, `seq`, `via`
- `installed blackhole route`: `dst`
- `installed gateway BUM FDB entry`: `vtep`, `vxlan`
- `installed gateway VTEP route`: `gateway`, `nic`
- `installed neighbor from type-2 route`: `ip`, `mac`, `router`
- `installed numbered gateway VTEP route`: `gateway`, `linkIndex`, `neighbor`
- `installed route`: `nexthops`, `route`
- `installed route from type-5`: `dst`, `gw`
- `installed type-5 gateway neighbor and FDB entry`: `ip`, `mac`, `vtep`
- `overlay type is none, skipping overlay setup`
- `removed BUM FDB entry from type-3 withdraw`: `vtep`
- `removed FDB entry`: `mac`, `via`
- `removed gateway BUM FDB entry`: `vtep`
- `removed route`: `route`
- `removed route from type-5 withdraw`: `dst`, `gw`
- `tcp-md5 authentication configured for BGP peer`: `address`, `interface`
- `type-5 path withdrawn, keeping route via remaining path`: `dst`, `vtep`, `withdrawn_vtep`
- `underlay BGP peers established`: `count`, `required`
- `underlay route removed, installed the type-5 route again`: `prefix`, `vtep`
- `unnumbered peer moved to the new link-local address of the switch`: `interface`, `new`, `old`
- `uplink has carrier again, starting its BGP session`: `interface`, `neighbor`
- `uplink lost carrier, stopping its BGP session`: `interface`, `neighbor`
- `vrf ready`: `name`, `table`

#### INFO `pkg/network/lldp`

- `LLDP neighbor`: `changes`, `chassisID`, `interface`, `maxFrameSize`, `mgmtAddresses`, `portDescription`, `portID`, `portIDSubtype`, `systemName`, `ttl`, `vlan`
- `LLDP neighbor discovered`: `chassisID`, `interface`, `portID`, `systemName`
- `LLDP neighbor left`: `chassisID`, `interface`, `portID`
- `listening for LLDP frames`: `interface`, `timeout`
- `sending LLDP frames like networkd EmitLLDP=yes`
- `sending LLDP frames with the bm4x-cablecheck identity`

#### INFO `pkg/network/netplan`

- `netplan: DHCP interface(s) found, no EVPN config, using DHCP mode`: `dhcpCount`

#### INFO `pkg/network/vlan`

- `vlan interface created`: `id`, `name`, `parent`
- `vlan interface removed`: `name`

#### INFO `pkg/oob`

- `no BootyLastResult UEFI variable from an earlier run`
- `previous BOOTy run result`: `booty`, `code`, `mode`, `phase`, `result`, `step`
- `wrote the BootyLastResult UEFI variable`: `code`, `phase`, `result`

#### INFO `pkg/provision`

- `A/B image mode uses removable EFI fallback loader; skipping EFI NVRAM boot entry`
- `A/B preserveExisting enabled, preserving existing EFI boot entries`
- `A/B preserveExisting enabled, preserving existing EFI boot entry`
- `A/B preserveExisting enabled, preserving existing EFI fallback loader`
- `A/B preserveExisting enabled, reusing existing partition layout`
- `A/B preserveExisting enabled, skipping whole-disk wipe`
- `A/B preserveExisting keeps the disks, skipping crypto-erase`
- `CI module signing certificate to enroll`: `cert`, `certSHA256`
- `DISK_LINKS=false, not creating /dev/disk links`
- `EFI boot entry created`: `output`
- `Firmware reporting disabled, skipping`
- `Firmware validation`: `message`, `name`, `status`
- `Hardware inventory collected`: `cpus`, `disks`, `lldp_neighbors`, `nics`, `pci_devices`
- `Hardware inventory collection disabled, skipping`
- `Health check result`: `check`, `details`, `message`, `severity`, `status`
- `Health checks disabled, skipping`
- `LUKS2 keyslots and header destroyed`: `device`, `luksUUID`
- `LVM physical volume opened in the LUKS2 mapping`: `device`, `rootLV`, `vg`
- `MOK enrollment queued: MokManager asks for MOK_PASSWORD at the next boot`
- `MOK enrollment staged: the next firmware boot runs mok-enroll.efi`: `entry`
- `No SR-IOV profiles, skipping`
- `SR-IOV profiles set the ConnectX VFs, skipping setup-mellanox (NUM_VFS)`
- `Secure Boot update already applied`: `entries`, `file`, `variable`
- `Secure Boot update applied`: `entries`, `file`, `new`, `variable`
- `Streaming image`: `disk`, `url`
- `Streaming image into A/B target slot`: `boot`, `disk`, `root`, `url`
- `Streaming image partition-by-partition`: `disk`, `url`
- `UKI boot entry created`: `loader`
- `UKI installed`: `atomic`, `bytes`, `path`
- `UKI prepared`: `certSHA256`, `loader`, `sha256`, `signing`
- `UKI signed with per-host keys; the private keys are wiped`: `certSHA256`
- `adding fstab entry for the /boot array`: `array`
- `applying custom partition layout`: `device`, `partitions`
- `applying provisioner files to the ramdisk`: `dst`, `src`
- `applying the provisioner files of the command to the ramdisk`: `skipped`, `src`
- `boot partition already mounted`: `mountpoint`, `source`
- `boot partition from layout`: `device`
- `checking for Mellanox NICs`
- `cloud-init seed injected`: `datasource`, `root`
- `configured target overlayFS`: `mode`, `path`
- `configuring DNS`: `resolvers`
- `configuring Mellanox SR-IOV`: `device`, `numVFs`
- `copying files`: `label`, `src`
- `creating EFI boot entry`: `disk`, `partition`
- `crypto-erasing LUKS2 device`: `device`, `headerBytes`, `luksUUID`
- `custom partition layout applied`
- `decommission: disk wipe complete`
- `deprovisioning step`: `index`, `step`, `total`
- `disk encryption prepared`: `device`, `luksUUID`, `tpm2PCRs`, `tpm2PublicKeyPCRs`
- `dry-run check`: `message`, `status`, `step`
- `efivarfs already mounted`
- `enabled serial getty unit`: `path`, `unit`
- `folded operator console parameters into serial console resolution`: `dropped`
- `fstab entry now valid from both mirror disks`: `from`, `mountpoint`, `to`
- `fstab: the encrypted partition mounts through its mapping`
- `generated fstab for custom layout`
- `growing the last Linux partition, not the root`: `partition`, `root`
- `grub.cfg already renamed`: `path`
- `host booted with BIOS, installing GRUB on both mirror disks`: `disk1`, `disk2`
- `image cosign signature verified`: `method`, `url`
- `image prerequisites validated`: `format`, `url`
- `image reachable`: `url`
- `image step memory`: `min_available_mib`, `peak_used_mib`, `total_mib`
- `installed bundled EFI fallback loader`: `loader`, `marker`, `root`
- `keeping target root mounted for kexec`: `root`
- `keeping the target resolv.conf`
- `machine command output`: `file`, `line`
- `mellanox SR-IOV setup disabled`: `numVFs`
- `mellanox firmware values changed, hard reboot required`
- `mellanox: current NUM_OF_VFS`: `current`, `nic`
- `mellanox: mstconfig not found, skipping`: `error`
- `mellanox: no ConnectX Ethernet NICs found, skipping`
- `mellanox: set NUM_OF_VFS`: `nic`, `target`
- `mirror already assembled`: `array`
- `mirroring root disk`: `disk1`, `disk2`, `mirroringMode`
- `module signing certificate left for the provisioner to enroll`: `cert`
- `mounted efivarfs`: `path`
- `mounted shared data partition`: `device`, `label`, `mountpoint`
- `mounting separate /boot`: `device`, `mountpoint`
- `nic_firmware`: `driver`, `nic`, `version`
- `nic_firmware action=check`
- `no deprovision disk override configured; hard deprovision will wipe all disks`
- `no directory found`: `label`, `path`
- `no image signature URL configured, skipping verification`
- `no machine commands directory found`: `path`
- `no matching disk pair, provisioning a single disk`: `mirroringMode`
- `no out-of-tree kernel modules, no module sysext`: `certSHA256`
- `no provisioner files to apply to the ramdisk`: `path`
- `non-EFI system detected, skipping efivarfs mount`
- `non-EFI system, skipping EFI boot entry removal`
- `oci pre-pulls enabled with no images`
- `partition formatted with LUKS2`: `device`, `enrollSlot`, `luksUUID`, `recoverySlot`
- `partition layout from URL`: `partitions`, `url`
- `partition layout from image`: `partitions`, `path`
- `per-host kernel parameters go into the UKI .cmdline`: `params`
- `persisted target network configuration`: `osFamily`
- `provisioner files stay in the ramdisk, not copied to the target`
- `provisioning report`: `report`
- `provisioning report already written by the interrupted run`: `path`
- `provisioning report not written to the target`: `reportPath`
- `provisioning step`: `index`, `step`, `total`
- `provisioning step requests a reboot into the ramdisk`: `status`, `step`
- `pulling OCI sysext layer`: `ref`
- `pulling oci pre-pull image`: `ref`
- `recovery key handed over`: `device`, `luksUUID`
- `removed conflicting serial getty unit`: `unit`
- `removed the temporary file of an interrupted UKI install`: `file`
- `removing EFI boot entry`: `entry`
- `removing old EFI boot entries`
- `renaming grub.cfg`: `from`, `to`
- `resolved serial console`: `baud`, `conflicts`, `device`, `reason`, `selectedBy`, `state`
- `restored mirror disk selection from checkpoint`: `disk1`, `disk2`
- `restored nvme target device from checkpoint`: `device`
- `restored partition layout from checkpoint`
- `restored raid target device from checkpoint`: `device`
- `root from lvm`: `device`
- `root from partition layout`: `device`
- `root partition already mounted`: `mountpoint`, `source`
- `root probe: cannot mount partition`: `error`, `partition`
- `root probe: selected root partition`: `partition`
- `root probe: using the mounted root partition`: `partition`
- `running in a container, skipping the EFI boot entry steps`
- `running machine command`: `command`, `file`
- `running machine command script`: `file`
- `running post-provision command`: `command`, `index`
- `secure erase enabled, erasing raid member`: `device`
- `secure erase enabled, performing hardware-level erase`: `device`
- `secure erase enabled, performing hardware-level erase on all disks`
- `serial console comes from the kernel command line only`
- `set disk device from nvme namespace layout`: `device`
- `set disk device from raid configuration`: `device`
- `setting hostname`: `hostname`, `path`
- `shared data partition already has content, skipping seed`: `device`
- `shared data partition already mounted`: `label`, `mountpoint`, `source`
- `signed out-of-tree kernel modules`: `cert`, `certSHA256`, `modules`
- `signed out-of-tree kernel modules into a system extension`: `certSHA256`, `modules`, `rootHash`, `sysext`
- `skipping EFI fallback loader installation; no boot partition detected`
- `skipping boot partition mount; no boot partition detected`
- `skipping completed step`: `step`
- `skipping enable-lvm, stream-image activated VG tcaas in the LUKS2 volume`
- `skipping grow-partition for declarative partition layout`
- `skipping grow-partition, mirror-root grew the root partition before creating the array`
- `skipping grow-partition, stream-image grew the encrypted partition; close-encrypted-disk grows its file system`
- `skipping grow-partition, stream-image grew the encrypted root partition`
- `skipping the removal of every EFI boot entry`: `error`, `uefi`
- `starting deprovisioning`: `mode`
- `starting dry-run - no destructive changes will be made`
- `streaming image into a LUKS2 partition`: `disk`, `luksUUID`, `url`
- `streaming image into partition layout root`: `disk`, `root`, `url`
- `sysext cosign signature verified`: `method`, `name`, `url`
- `sysext layer loaded`: `mode`, `name`, `path`
- `sysext provisioning enabled with no layers`
- `update-grub not found in the target, skipping like the legacy CAPRF provisioner`
- `using configured deprovision disk`: `device`
- `using configured disk device`: `device`
- `using configured disk serial`: `device`, `serial`
- `using partition layout device override`: `device`
- `using the disk selection made before resume`: `disk1`, `disk2`
- `wiping all disks`
- `wiping raid target device after array creation`: `device`
- `writing GRUB config`: `console`, `path`
- `writing GRUB config like the legacy CAPRF provisioner`: `path`
- `writing kubelet extra args`: `path`
- `wrote serial getty drop-in`: `baud`, `device`, `path`

#### INFO `pkg/realm`

- `already mounted, skipping`: `name`, `path`
- `folder created`: `name`, `path`
- `mounted`: `name`, `path`
- `reboot suppressed (BOOTY_NO_REBOOT set)`
- `starting Shell`
- `unmounted`: `name`, `path`
- `waiting for command to finish...`

#### INFO `pkg/rescue`

- `rescue SSH login accepted`: `issuer`, `key`, `method`, `principal`, `remote`, `subject`
- `rescue SSH server started`: `hardened`, `host_key`, `keys`, `opkssh`, `password`

#### INFO `pkg/runmode`

- `LLDP neighbor on every NIC with carrier`
- `LLDP neighbor wait skipped: LLDP receive is off`
- `checks done, holding`: `duration`
- `checks done, holding until power off`
- `deprovisioning complete, returning to standby`
- `dropping to rescue shell`
- `entering standby mode`: `memAvailableMiB`, `memTotalMiB`
- `health check passed`: `check`
- `health check skipped`: `check`
- `health-check command received`
- `reboot command received`
- `received command`: `id`, `type`
- `rescue action`: `message`, `type`
- `retrying provisioning`: `attempt`, `delay`
- `running command with payload`: `hostname`, `id`, `keys`, `type`
- `standby context canceled, shutting down`
- `waiting after deprovisioning failure`
- `waiting for LLDP neighbors`: `nics`, `timeout`
- `waiting for manual intervention`

#### INFO `pkg/secureboot`

- `enrolling mok certificate`: `cert`
- `mok enrollment queued for next reboot`

#### INFO `pkg/sriov`

- `SR-IOV compliance`: `compliant`, `failed`, `findings`, `reboot`
- `SR-IOV setting`: `detail`, `got`, `interface`, `level`, `profile`, `setting`, `status`, `want`
- `sock_diag unavailable, the SR-IOV in-use check uses the network config and the routes`: `error`, `interface`

#### INFO `pkg/telemetry`

- `step ended`: `duration`, `status`, `step`
- `step started`: `step`

#### INFO `pkg/timesync`

- `RTC updated`
- `clock synchronized`: `corrected_by`, `source`, `time`
- `measured clock skew`: `authenticated`, `skew`, `skew_seconds`, `source`

#### INFO `pkg/trust`

- `added extra root certificates`: `count`, `path`

#### INFO `pkg/watchdog`

- `disarming the watchdog`
- `leaving the watchdog armed for the target OS`: `timeout`

### DEBUG (102 messages)

#### DEBUG `.`

- `link watch for LLDP skipped a message`: `error`
- `no kernel modules directory, skipping`: `path`
- `no watchdog modules`: `path`
- `reaped orphan child process`: `pid`, `status`
- `watchdog module not loaded`: `error`, `module`

#### DEBUG `pkg/auth`

- `token renewal stopped`: `reason`

#### DEBUG `pkg/caprf`

- `no crash artifact URL configured, skipping`
- `no firmware URL configured, skipping report`
- `no metrics URL configured, skipping`
- `telemetry disabled, skipping metrics`

#### DEBUG `pkg/debug`

- `debug dump command failed`: `cmd`, `error`

#### DEBUG `pkg/disk`

- `cannot bind-mount efivarfs into chroot`: `error`
- `cannot create efivarfs mount target`: `error`, `path`
- `chroot efivarfs unmount skipped`: `error`, `path`
- `disk with serial has no size yet`: `device`, `serial`
- `failed to detect filesystem type, falling back to ext check`: `device`, `error`
- `lsblk serial lookup failed`: `error`, `serial`
- `lvm binary not found, skipping activation`: `error`
- `lvm binary not found, skipping deactivation`: `error`
- `mdadm stop reported no arrays`: `error`, `output`
- `mdev -s failed during device wait`: `device`, `error`
- `resize2fs failed, trying xfs_growfs`: `output`
- `waiting for device node`: `device`, `iteration`
- `waiting for disk serial`: `matches`, `serial`
- `xfs_growfs failed, trying btrfs resize`: `output`

#### DEBUG `pkg/firmware/nic/ethtool`

- `private flags not read (optional)`: `error`, `interface`

#### DEBUG `pkg/image`

- `mdev -s failed while waiting for partition device`: `device`, `error`, `output`
- `waiting for partition device`: `device`, `iteration`

#### DEBUG `pkg/network`

- `address deletion (may already be gone)`: `address`, `error`
- `connectivity check failed`: `attempt`, `error`, `target`
- `link watch for DHCP skipped a message`: `error`
- `route deletion (may already be gone)`: `error`, `gateway`
- `router solicitation`: `error`, `interface`
- `skipping NIC missing during DHCP scan`: `error`, `interface`
- `skipping interface: address enumeration failed`: `error`, `interface`

#### DEBUG `pkg/network/bfd`

- `bfd packet dropped`: `error`, `source`
- `bfd packet with wrong TTL dropped`: `source`, `ttl`
- `bfd read failed`: `device`, `error`
- `bfd send failed`: `error`, `peer`

#### DEBUG `pkg/network/dhcp4`

- `DHCP lease bound`: `addr`, `expire`, `server`
- `DHCP receive`: `error`
- `DHCP send`: `ciaddr`, `error`, `type`, `wait`
- `DHCP transport`: `error`
- `DHCPNAK from an unexpected server, ignoring`: `server`
- `DHCPRELEASE`: `error`

#### DEBUG `pkg/network/dhcp6`

- `DHCPv6 INF_MAX_RT`: `value`
- `DHCPv6 RELEASE`: `error`
- `DHCPv6 SOL_MAX_RT`: `value`
- `DHCPv6 address bound`: `addr`, `valid`
- `DHCPv6 send`: `error`, `type`, `wait`

#### DEBUG `pkg/network/frr`

- `Connectivity check failed`: `attempt`, `error`, `target`
- `Failed to set sysctl`: `error`, `path`
- `VRF already exists`: `name`
- `daemon not found, skipping`: `daemon`
- `failed to remove VXLAN`: `error`, `name`
- `failed to remove bridge provision address`: `addr`, `bridge`, `error`
- `failed to remove link`: `error`, `name`
- `failed to remove loopback address`: `cidr`, `error`, `ip`
- `skipping loopback cleanup; failed to derive addresses`: `error`

#### DEBUG `pkg/network/gobgp`

- `Failed to set sysctl`: `error`, `path`
- `failed to append BUM FDB entry`: `error`, `vtep`
- `failed to delete BUM FDB entry`: `error`, `vtep`
- `failed to delete Type-5 VTEP route`: `error`, `vtep`
- `failed to delete gateway VTEP route`: `error`, `gateway`
- `failed to delete route from type-5 withdraw`: `dst`, `error`, `gw`
- `failed to delete type-2 neighbor`: `error`, `ip`, `mac`
- `failed to delete type-5 gateway FDB entry`: `error`, `mac`, `vtep`
- `failed to delete type-5 gateway neighbor`: `error`, `ip`, `mac`
- `failed to install type-2 neighbor`: `error`, `ip`, `mac`
- `failed to list peers`: `error`
- `failed to remove gateway BUM FDB entry`: `error`, `vtep`
- `failed to roll back type-5 gateway neighbor`: `error`, `ip`, `mac`
- `no type-5 route to delete`: `dst`
- `remove route`: `error`, `route`
- `route update`: `action`, `type`
- `route update skipped`: `error`
- `route update skipped: RT mismatch`: `action`, `type`
- `route update skipped: invalid configured import RT`: `action`, `error`, `type`
- `type-2 route with invalid MAC`: `error`, `mac`
- `type-2 route with no valid next-hop`: `vtep`
- `type-3 route with no valid VTEP IP`
- `type-3 route without ingress replication, no BUM entry`: `vtep`
- `type-5 gateway IP not resolved by a type-2 route yet`: `gw`, `prefix`
- `type-5 route for a static blackhole prefix, static route wins`: `prefix`
- `type-5 route with a gateway IP overlay index not installed: enable-resolve-overlay-index is off or no L2 overlay`: `gw`, `prefix`
- `type-5 route with invalid prefix`: `error`, `len`, `prefix`
- `type-5 route with no valid VTEP`: `vtep`
- `type-5 route with no valid gateway`: `gw`, `prefix`

#### DEBUG `pkg/network/lldp`

- `LLDP listen failed`: `error`, `interface`
- `LLDP shutdown frame not sent`: `error`
- `link watch for LLDP skipped a message`: `error`

#### DEBUG `pkg/network/netplan`

- `netplan: default gateway from ethernet routes`: `gw`, `iface`
- `netplan: found VLAN`: `id`, `link`, `name`
- `netplan: found bond`: `members`, `name`

#### DEBUG `pkg/oob`

- `BootyLastResult UEFI variable unchanged, not writing`
- `writing the status line failed`: `device`, `error`

#### DEBUG `pkg/provision`

- `no LUKS2 header on the disks to wipe`
- `post-provision command output`: `index`, `output`
- `retrying step immediately`: `attempt`, `step`

#### DEBUG `pkg/rescue`

- `rescue SSH connection closed before a login`: `error`, `remote`

#### DEBUG `pkg/secureboot`

- `close artifact after digest validation`: `error`, `path`
- `pe/coff close also failed`: `error`, `path`

<!-- log-catalog:end -->

