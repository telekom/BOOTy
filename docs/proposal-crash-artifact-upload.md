# Proposal: Startup Crash Artifact Upload

## Status: Implemented

## Summary

BOOTy can inspect the existing OS on the target disk before destructive
provisioning or deprovisioning steps. When it finds kernel crash evidence, it
creates a size-capped `tar.gz` archive and uploads it best-effort through CAPRF
or a CAPRF-provided presigned S3 URL.

The archive includes:

- `manifest.json` with artifact paths, skip reasons, target disk/root context,
  and upload correlation fields
- `metadata.json` with hardware inventory, firmware versions, debug/system
  state, BOOTy build info, and machine identity
- allowlisted crash artifacts from the existing OS and pstore

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `CRASH_ARTIFACTS_ENABLED` | `false` | Enable startup inspection and upload |
| `CRASH_ARTIFACTS_PREPARE_URL` | — | CAPRF endpoint that receives metadata and returns upload instructions |
| `CRASH_ARTIFACTS_UPLOAD_URL` | — | Direct CAPRF proxy upload endpoint |
| `CRASH_ARTIFACTS_MAX_MB` | `256` | Maximum artifact payload size |
| `CRASH_ARTIFACTS_UPLOAD_TIMEOUT_SEC` | `120` | Prepare/upload timeout |

## CAPRF Prepare Contract

BOOTy posts a JSON `PrepareRequest` containing the manifest, metadata summary,
artifact count, and size information. CAPRF responds with upload instructions:

```json
{
  "artifactId": "machine-123/2026-05-08T09:00:00Z",
  "uploadUrl": "https://bucket.example/object?...",
  "method": "PUT",
  "uploadMode": "presigned-put",
  "authMode": "none",
  "headers": { "Content-Type": "application/gzip" },
  "maxBytes": 268435456,
  "expiryUnix": 1778230800
}
```

Supported upload modes are `presigned-put`, `presigned-post`, and
`caprf-proxy`. Supported auth modes are `none` and `bearer`. BOOTy refuses
unknown modes before upload.

## Collection Scope

BOOTy mounts the detected root partition read-only and scans a strict allowlist:

- `/var/crash`
- `/var/lib/systemd/coredump`
- `/var/log/journal`
- `/var/log/kern.log*`
- `/var/log/messages*`
- `/var/log/syslog*`
- `/var/log/dmesg*`
- `/var/log/kdump*`
- `/sys/fs/pstore`

Symlinks are skipped, paths must remain under the mounted root, and oversized
files are skipped with manifest reasons. Upload is skipped when no crash
evidence is found.

## Metadata

The metadata section reuses existing collectors:

- `pkg/inventory` for hardware makeup: system, CPU, memory, disks, NICs, PCI,
  and accelerators
- `pkg/firmware` for BIOS, BMC, NIC, and storage firmware
- `pkg/debug` for disk, network, kernel, and system snapshots
- `pkg/buildinfo` for BOOTy build version, commit, architecture, and flavor

Metadata collection is best-effort. Failures are recorded in `metadata.errors`
and do not block artifact upload.

## Security and Privacy

Crash dumps, logs, serials, UUIDs, MAC addresses, and firmware metadata can be
sensitive. The feature is opt-in. BOOTy does not log artifact contents,
metadata payloads, or presigned query strings, and it never sends its bearer
token to presigned S3 URLs.

## Limitations

v1 supports plain GPT Linux filesystem roots. LVM, RAID, and LUKS roots are
skipped with manifest/log reasons unless future work adds safe
activation/assembly/unlock and cleanup.