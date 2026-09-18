# Serial Console Resolution

**Status:** Implemented

BOOTy configures the serial console of the provisioned system from firmware
evidence and explicit operator intent. This replaces the former vendor
heuristic in `Configurator.ConfigureGRUB` ("Lenovo means `ttyS1`, everything
else means `ttyS0`"), which silently produced an unreachable console on every
host whose firmware redirects to a different port than its vendor string
suggested.

The resolver lives in [`pkg/serialconsole`](../pkg/serialconsole) and is the
single source of truth for both the kernel command line and the serial getty.

## Guarantees

| Guarantee | How it is enforced |
|-----------|--------------------|
| Exactly one kernel console | `ConfigureGRUB` emits one `console=` parameter and folds operator-supplied `console=` tokens into the resolution instead of appending them again |
| Kernel console and getty always match | Both consumers read one cached `Resolution` per provisioning run |
| No destructive UART probing | Resolution only reads files below `/sys` and `/proc`; `/dev/tty*` is never opened and `TIOCSSERIAL` autoconfiguration is never issued |
| Fail closed on ambiguity | Conflicting evidence aborts the provisioning run instead of guessing a port |
| Auditable outcome | A machine-readable artifact records the decision and the full evidence trail |

## Evidence precedence

The first tier that yields a usable candidate wins. Lower tiers may still
contribute a missing baud rate.

| Tier | Source | Read from |
|------|--------|-----------|
| 1 | `explicit-override` | `provision.serialConsole` (`MACHINE_SERIAL_CONSOLE` / `SERIAL_CONSOLE`) and `console=` inside `provision.extraKernelParams` |
| 2 | `acpi-spcr` | `/sys/firmware/acpi/tables/SPCR`, mapped onto a tty via `/sys/class/tty/*/port`, `iomem_base` or `device/resource` |
| 3 | `device-tree` | `/sys/firmware/devicetree/base/chosen/stdout-path` plus `aliases/`, mapped via `/sys/class/tty/*/device/of_node` |
| 4 | `boot-console` | `/sys/class/tty/console/active` and `/proc/cmdline` of the running BOOTy kernel |
| 5 | `sysfs-uart` | `/sys/class/tty/ttyS*/type`, used only when exactly one present UART exists |
| — | `dmi` | `/sys/class/dmi/id/*`, recorded as host context and **never** selectable |

DMI is deliberately not selectable: hard-coding vendor-to-port mappings is the
defect this design removes. The vendor string is still recorded so fleet-wide
console anomalies can be correlated per hardware model.

## Fail-closed conditions

Provisioning aborts with `ErrAmbiguous` when:

- an explicit override is syntactically invalid or names a virtual terminal
  (`tty0`), or `extraKernelParams` carries an unsupported `console=` value;
- two sources in the same tier name different devices or different baud rates
  (for example two conflicting `console=` parameters);
- ACPI SPCR declares a console address that no enumerated tty is backed by,
  while other ttys do expose addresses;
- more than one present UART is enumerated and no firmware or operator source
  names one of them.

Two outcomes are **not** failures:

- `disabled` — the operator set `provision.serialConsole: none`. No `console=`
  parameter and no serial getty are written.
- `no-evidence` — nothing claims a serial console (for example a VM without
  SPCR and without serial ports). No serial console is configured; this is
  recorded in the artifact rather than guessed.

When firmware evidence exists but cannot be mapped to a tty while no tty
exposes an address at all, the gap is recorded under `degraded` and weaker
evidence is allowed to decide.

## What gets written

For a resolution of `ttyS1,115200n8`:

- `/etc/default/grub.d/10-caprf-kernel-params.cfg`

  ```
  GRUB_CMDLINE_LINUX="console=ttyS1,115200n8"
  GRUB_TERMINAL="serial console"
  GRUB_SERIAL_COMMAND="serial --unit=1 --speed=115200"
  ```

- `/etc/systemd/system/serial-getty@ttyS1.service.d/10-booty-console.conf`

  ```
  [Service]
  ExecStart=
  ExecStart=-/sbin/agetty -o '-p -- \\u' --keep-baud 115200 %I $TERM
  ```

- `/etc/systemd/system/getty.target.wants/serial-getty@ttyS1.service`
  → `serial-getty@.service`

Serial getty instances for any other device, and BOOTy-owned drop-ins for them,
are removed so exactly one serial getty starts. Virtual terminal gettys
(`getty@tty1.service`) are left untouched.

## Resolution artifact (CAPRF output contract)

The artifact is written to two locations:

| Path | Purpose |
|------|---------|
| `/var/lib/booty/serial-console.json` (in the provisioned root) | Durable record available after reboot |
| `/run/booty/serial-console.json` (in the initramfs) | Collected by CAPRF during the provisioning run |

It is always written — including when resolution fails closed — so the
controller can explain the failure without a serial console.

```json
{
  "schemaVersion": 1,
  "kind": "booty.telekom.de/serial-console-resolution",
  "generatedAt": "2026-09-18T12:00:00Z",
  "resolution": {
    "state": "resolved",
    "console": { "device": "ttyS1", "baud": 115200, "parity": "n", "bits": 8 },
    "selectedBy": "acpi-spcr",
    "baudSource": "acpi-spcr",
    "reason": "selected ttyS1 from acpi-spcr",
    "evidence": [
      {
        "source": "acpi-spcr",
        "device": "ttyS1",
        "baud": 115200,
        "parity": "n",
        "bits": 8,
        "address": "0x2f8",
        "selectable": true,
        "path": "/sys/firmware/acpi/tables/SPCR",
        "detail": "interface=0x0 space=io address=0x2f8 class=ttyS"
      },
      {
        "source": "dmi",
        "selectable": false,
        "path": "/sys/class/dmi/id",
        "detail": "vendor=Lenovo product=ThinkSystem SR650 V3 board= bios="
      }
    ],
    "host": { "vendor": "Lenovo", "product": "ThinkSystem SR650 V3" }
  },
  "kernelParam": "console=ttyS1,115200n8",
  "gettyUnit": "serial-getty@ttyS1.service"
}
```

Field contract for consumers:

| Field | Contract |
|-------|----------|
| `schemaVersion` | Reject unknown versions; increments are breaking |
| `kind` | Constant discriminator for the payload |
| `resolution.state` | `resolved`, `disabled`, `no-evidence` or `ambiguous` |
| `resolution.console` | Present only when `state` is `resolved` |
| `resolution.evidence[].selectable` | `false` marks context-only evidence such as DMI |
| `resolution.conflicts` | Non-empty only for `ambiguous`; explains the fail-closed decision |
| `resolution.degraded` | Non-fatal gaps worth alerting on across a fleet |
| `kernelParam` / `gettyUnit` | Empty unless a console was configured; always consistent with each other |
| `error` | Set only when resolution failed closed |

All strings are bounded (evidence details truncated to 256 bytes, at most 32
evidence entries) and contain no credentials or operator-supplied free text
beyond validated console values.

## Operator escape hatch

```bash
# Pin the console explicitly (highest precedence).
export MACHINE_SERIAL_CONSOLE="ttyS1,115200n8"

# Or disable serial console configuration entirely.
export MACHINE_SERIAL_CONSOLE="none"
```

In YAML machine configs the same field is `provision.serialConsole`. Invalid
values are rejected by `Config.Validate()` before provisioning starts.

## Testing

| Level | Location |
|-------|----------|
| Resolver unit tests (SPCR encodings, device tree, precedence, fail-closed, read-only audit) | `pkg/serialconsole/*_test.go` |
| Provisioning integration tests (GRUB drop-in, getty drop-in, symlink pruning, artifact contract) | `pkg/provision/serialconsole_test.go` |

Run them with:

```bash
go test ./pkg/serialconsole/... ./pkg/provision/... -count=1
```
