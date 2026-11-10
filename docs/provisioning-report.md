# Provisioning report

After a successful provision BOOTy produces a machine-readable report of what
it installed: the image and its verification, the applied disk layout, the
A/B slot, the encryption, TPM and Secure Boot state, the system extensions,
the BOOTy build and the step timings.

The report goes to two places:

| Where | When | Content |
|-------|------|---------|
| Installed OS, `PROVISION_REPORT_PATH` (default `/etc/booty/provision.json`) | `write-provision-report` step, after `inject-cloudinit` and before `copy-machine-files` | Pretty-printed JSON, mode `0644`. Step timings up to this step. |
| `LOG_URL` (and the console) | `report-success`, before the success status | One log line `[INFO] provisioning report component=provision report=<json>`, sent once also when `report-success` is retried. Step timings up to `report-success`. |

The file sits next to the A/B slot state (`/etc/booty/ab-slot.env`), so
`copy-machine-files` and the machine commands can still read or replace it.
`PROVISION_REPORT_PATH=none` (YAML `provision.reportPath: none`) writes no
file; the log line is still sent. No new endpoint is needed: a CAPRF that
stores the log lines has the report.

The file is best effort, like the other reports: a failed write (for example
a symlink in the image that leaves the target root) logs a warning and the
run goes on; the log line still carries the report. A resumed run
(`BOOTY_RESUME`) that finds `write-provision-report` done rebuilds the report
for the log line but does not write the file again, so a file that
`copy-machine-files` or a machine command put there stays.

### A/B mode

With `IMAGE_MODE=ab` the default path is in the root slot that BOOTy
installs, next to its `ab-slot.env`. Each slot keeps the report of its own
install, and in `system-ab` the file is read-only after boot like the rest
of the slot. To keep the report of the latest provision across slots, set
`PROVISION_REPORT_PATH` below a shared data mountpoint, for example
`/var/lib/booty/provision.json`; BOOTy mounts the shared data partitions
before this step, so the file goes to the shared partition.

## Format

The report is one JSON object. `schema` names its version; a version keeps
its fields and their meaning, new fields can be added. A consumer ignores
fields it does not know.

```json
{
  "schema": "booty.provision-report/v1",
  "generatedAt": "2026-01-02T03:04:05Z",
  "hostname": "node-1",
  "booty": {"version": "v0.2.0", "commit": "abc1234", "flavor": "full"},
  "image": {
    "reference": "oci://registry.example.com/os/node:1.0",
    "digest": "sha256:0f1e...",
    "mode": "ab",
    "checksum": {"algorithm": "sha256", "value": "9a8b...", "scope": "download"}
  },
  "verification": {
    "checksum": "verified",
    "signature": {"method": "gpg", "result": "verified"}
  },
  "disk": {
    "device": "/dev/nvme0n1",
    "rootPartition": "/dev/nvme0n1p3",
    "layout": {"table": "gpt", "partitions": [{"label": "EFI", "sizeMB": 512, "filesystem": "vfat", "mountpoint": "/boot/efi"}]}
  },
  "ab": {"scheme": "system-ab", "targetSlot": "b", "activeSlot": "a", "preserveExisting": false},
  "encryption": {"luks": "absent", "tpm": "present", "tpmVersion": "2.0"},
  "secureBoot": {"state": "disabled", "reEnable": true},
  "sysexts": [{"name": "tools", "version": "1.2.3", "mode": "preload", "sha256": "cd..."}],
  "timings": {
    "startTime": "2026-01-02T02:58:00Z",
    "totalDurationMs": 365000,
    "steps": [{"name": "stream-image", "durationMs": 120000, "status": "done"}]
  }
}
```

| Field | Meaning |
|-------|---------|
| `image.reference` | The image source that `verify-image` selected. Credentials, query and fragment are removed. |
| `image.digest` | OCI sources: the manifest digest that the reference resolved to in `verify-image`; for a multi-platform index, the digest of the selected platform image, not of the index. Pin the reference by digest to make sure that `stream-image` writes the same image; a resumed run (`BOOTY_RESUME`) resolves the reference again in `verify-image`, also when `stream-image` is skipped. Empty for HTTP sources. |
| `image.mode` | `IMAGE_MODE`: `whole-disk`, `partition` or `ab`. |
| `image.checksum` | `IMAGE_CHECKSUM`, its type and scope. Not set without a checksum. |
| `verification.checksum` | `verified` when a checksum is configured (a mismatch fails the run), else `not-configured`. |
| `verification.signature` | `method` `cosign` and `result` `verified` for an `oci://` image with `IMAGE_COSIGN_PUBKEYS`, `method` `gpg` and `result` `verified` with `IMAGE_SIGNATURE_URL`, else `result` `not-configured`. |
| `disk.layout` | The applied `PARTITION_LAYOUT` with the root filesystem of the streamed image (as in fstab), or the layout that A/B mode generated. Not set for a whole-disk image without a layout. |
| `ab` | Only in `IMAGE_MODE=ab`: the same values as `/etc/booty/ab-slot.env`. |
| `encryption.luks` | `present` when the installed `/etc/crypttab` names a volume, `absent` when it names none or does not exist, `unknown` when it can not be read or is not a regular file of at most 64 KiB below the target root (a symlink, a FIFO or a device is not read). |
| `encryption.tpm` | `present` when the ramdisk sees a TPM (`/sys/class/tpm/tpm0`), else `absent`. |
| `secureBoot.state` | Firmware Secure Boot during provisioning: `enabled`, `disabled`, or `unknown` without EFI variables. `reEnable` is `SECUREBOOT_REENABLE`. |
| `sysexts` | The configured system extensions that `apply-sysexts` installed, with the mode they were installed in (`preload` when neither the layer nor `provision.sysext.defaultMode` sets one). |
| `timings` | The step summary of the [telemetry metrics](../README.md#telemetry-and-metrics) up to the report, without the error text of failed steps (which can hold command output); a failed step keeps its `status` and failure `code`. |

The report holds no tokens, passwords or keys. It is written before
`copy-machine-files`, so steps after it do not appear in the file but in
the log line.
