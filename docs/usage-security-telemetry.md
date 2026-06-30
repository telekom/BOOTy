# Usage Guide: Security, Telemetry & Boot Management

This guide covers features introduced in PR #71: TPM attestation and
sealing, Secure Boot verification, MOK enrollment, telemetry collection,
bootloader detection, GRUB config parsing, and retry utilities.

## Environment Variables

| Variable | Type | Description |
|----------|------|-------------|
| `TELEMETRY_ENABLED` | bool | Enable provisioning metrics collection |
| `TELEMETRY_URL` | string | POST endpoint for metrics snapshot |
| `METRICS_URL` | string | POST endpoint for provisioning metrics (overrides TELEMETRY_URL) |
| `EVENT_URL` | string | POST endpoint for provisioning events |
| `SECUREBOOT_REENABLE` | bool | Signal CAPRF to re-enable Secure Boot after provisioning |
| `MOK_CERT_PATH` | string | Path to DER-encoded MOK certificate for enrollment |
| `MOK_PASSWORD` | string | One-time password for MokManager confirmation |

## TPM Operations (`pkg/tpm/`)

### Detection

```go
import "github.com/telekom/BOOTy/pkg/tpm"

// Detect TPM hardware (reads /sys/class/tpm/tpm0).
info := tpm.Detect()
fmt.Printf("Present: %v, Version: %s\n", info.Present, info.Version)
```

### PCR Measurement

Extend TPM Platform Configuration Registers with file or data hashes:

```go
dev, err := tpm.Open()  // opens /dev/tpmrm0 or /dev/tpm0
defer dev.Close()

// Measure a file into PCR 8.
digest, err := dev.MeasureFile(8, "/boot/vmlinuz")

// Measure arbitrary data from an io.Reader.
digest, err = dev.MeasureReader(8, reader)

// Read current PCR value.
val, err := dev.ReadPCR(8)
```

### Attestation (Quotes)

Generate TPM2 quotes for remote attestation:

```go
// Quote PCRs 0, 1, 7 with a server-provided nonce.
quote, err := dev.Quote([]int{0, 1, 7}, nonce)

// Verify the quote signature (ECDSA P-256).
valid, err := tpm.VerifyQuoteSignature(quote)

// Verify quote against a policy (expected PCR values).
result := tpm.VerifyQuoteAgainstPolicy(quote, &tpm.PCRPolicy{
    PCRs: []tpm.GoldenPCR{
        {Index: 0, Digest: expectedDigest0},
        {Index: 7, Digest: expectedDigest7},
    },
})
// result.Valid, result.Mismatches

// Serialize for transport to attestation server.
data, err := tpm.MarshalQuote(quote)
```

### Secret Sealing

Seal secrets to TPM PCR state (e.g., disk encryption keys):

```go
// Seal a secret — only unsealable when PCRs match current values.
sealed, err := dev.SealSecret([]int{0, 1, 7}, []byte("encryption-key"))

// Unseal — fails if PCR values have changed.
plaintext, err := dev.UnsealSecret([]int{0, 1, 7}, sealed)
```

The sealing uses a trial policy session to compute the correct AuthPolicy
digest, ensuring the sealed data is bound to the specified PCR values.

## Secure Boot (`pkg/secureboot/`)

### Verification

Check Secure Boot components (shim, GRUB, kernel artifacts):

```go
import "github.com/telekom/BOOTy/pkg/secureboot"

result := secureboot.Verify("/mnt/target")
fmt.Printf("Secure Boot: %v\n", result.Valid)
for _, c := range result.Components {
    fmt.Printf("  %s: signed=%v trusted=%v\n", c.Name, c.Signed, c.Trusted)
}
```

**Note**: Secure Boot verification checks component presence and PE/COFF
headers for EFI binaries. Configure pinned SHA256 digests for `shim`, `grub`,
and `kernel` when provisioning must fail on unexpected boot artifacts.
Full Authenticode trust-chain verification is not yet implemented.

### MOK Enrollment

Enroll Machine Owner Keys for custom kernel/module signing:

```go
enroller := secureboot.NewMOKEnroller("/path/to/cert.der", "one-time-password")

// Enroll — queues certificate for next reboot.
err := enroller.Enroll()

// Check enrollment status.
enrolled, err := enroller.IsEnrolled()
```

MOK enrollment requires `mokutil` to be available in the target system.
The one-time password is used during the MokManager EFI prompt at next
reboot.

## Telemetry (`pkg/telemetry/`)

### Step Tracking

Track provisioning step durations and outcomes:

```go
import "github.com/telekom/BOOTy/pkg/telemetry"

tracker := telemetry.NewStepTracker()
tracker.StartStep("download-image")
// ... do work ...
tracker.EndStep("download-image", nil)  // or pass error

// Record retries.
tracker.RecordRetry("download-image")
```

### Metrics

Monotonically increasing counters:

```go
counter := &telemetry.Counter{}
counter.Add(1)    // increments by 1
counter.Add(-1)   // ignored (counters are monotonic)
counter.Add(0)    // ignored
fmt.Println(counter.Value())
```

### Collection and Reporting

```go
collector := telemetry.NewCollector()
collector.SetImage(&telemetry.ImageInfo{Name: "ubuntu-22.04", Size: 2048})
collector.SetDisk(&telemetry.DiskInfo{Path: "/dev/sda", Size: 480})

// Get summary (safe — returns copies, not aliased pointers).
summary := collector.Summarize()
```

Telemetry reporting to CAPRF is gated on `TelemetryEnabled` in the
machine config. When enabled, metrics are posted to `MetricsURL` (falls
back to `TelemetryURL` if MetricsURL is empty).
