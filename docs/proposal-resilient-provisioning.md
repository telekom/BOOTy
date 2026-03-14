# Proposal: Resilient Provisioning — Self-Healing Error Recovery

## Status: Proposal

## Priority: P1

## Summary

Make BOOTy's provisioning pipeline self-healing: configurable per-step retry
policies with exponential backoff, HTTP Range-based resume for image downloads,
checkpoint/resume from last successful step, structured error taxonomy
(transient vs permanent), watchdog self-reboot as last resort, and comprehensive
error reporting with retry history to CAPRF.

## Motivation

Current provisioning failures are terminal — any step failure aborts the
entire pipeline. In production, many failures are transient:

| Failure Type | Frequency | Current Behavior | Proposed |
|-------------|-----------|-----------------|----------|
| Image server 503 | Common during maintenance | Abort | Retry 5× with backoff |
| Network blip during image download | Occasional | Abort, re-download | Resume via HTTP Range |
| CAPRF report timeout | Common under load | Abort | Retry 3×, continue on failure |
| Disk I/O error during write | Rare | Abort | Retry after disk re-scan |
| DNS resolution failure | Common at boot | Abort | Retry with increasing timeout |
| NIC driver not loaded | Rare | Abort | Retry after module re-load |
| Partition table not visible | Occasional | Abort | `partprobe` + retry |

### Industry Context

| Tool | Error Recovery |
|------|---------------|
| **Ironic** | Configurable deploy timeout, manual rescue on failure |
| **MAAS** | Installation retry, machine marked "failed" for manual intervention |
| **Tinkerbell** | Workflow retries at action level |
| **Ansible** | `retries` + `until` + `delay` per task |

## Design

### Retry Framework

```go
// pkg/provision/retry.go
package provision

import (
    "context"
    "errors"
    "fmt"
    "math"
    "math/rand"
    "time"
)

// RetryPolicy defines how a provisioning step handles failures.
type RetryPolicy struct {
    MaxAttempts  int           // 0 = no retry
    InitialDelay time.Duration // base delay before first retry
    MaxDelay     time.Duration // cap on exponential backoff
    Jitter       float64       // 0.0-1.0, random delay fraction
    Transient    bool          // if true, errors are assumed transient
}

// DefaultPolicies maps step names to their retry policies.
var DefaultPolicies = map[string]RetryPolicy{
    "report-init":     {MaxAttempts: 5, InitialDelay: 2 * time.Second, MaxDelay: 30 * time.Second, Jitter: 0.2, Transient: true},
    "configure-dns":   {MaxAttempts: 5, InitialDelay: 1 * time.Second, MaxDelay: 15 * time.Second, Jitter: 0.1, Transient: true},
    "stream-image":    {MaxAttempts: 3, InitialDelay: 5 * time.Second, MaxDelay: 60 * time.Second, Jitter: 0.3, Transient: true},
    "detect-disk":     {MaxAttempts: 3, InitialDelay: 2 * time.Second, MaxDelay: 10 * time.Second, Jitter: 0.1, Transient: true},
    "partprobe":       {MaxAttempts: 3, InitialDelay: 1 * time.Second, MaxDelay: 5 * time.Second, Jitter: 0.0, Transient: true},
    "report-success":  {MaxAttempts: 5, InitialDelay: 2 * time.Second, MaxDelay: 30 * time.Second, Jitter: 0.2, Transient: true},
    "wipe-disks":      {MaxAttempts: 0, Transient: false}, // permanent failures — no retry
    "create-efi-boot": {MaxAttempts: 2, InitialDelay: 1 * time.Second, MaxDelay: 5 * time.Second, Jitter: 0.0, Transient: true},
}

// WithRetry executes fn with the given retry policy.
func WithRetry(ctx context.Context, name string, policy RetryPolicy, fn func(ctx context.Context) error) error {
    var lastErr error
    for attempt := range policy.MaxAttempts + 1 {
        if attempt > 0 {
            delay := time.Duration(float64(policy.InitialDelay) * math.Pow(2, float64(attempt-1)))
            if delay > policy.MaxDelay {
                delay = policy.MaxDelay
            }
            // Add jitter
            jitter := time.Duration(float64(delay) * policy.Jitter * rand.Float64())
            delay += jitter

            select {
            case <-time.After(delay):
            case <-ctx.Done():
                return fmt.Errorf("retry canceled for %s: %w", name, ctx.Err())
            }
        }

        if err := fn(ctx); err != nil {
            lastErr = err
            if !isTransient(err) && !policy.Transient {
                return fmt.Errorf("%s: permanent failure: %w", name, err)
            }
            continue
        }
        return nil // success
    }
    return fmt.Errorf("%s: exhausted %d attempts: %w", name, policy.MaxAttempts+1, lastErr)
}
```

### Error Classification

```go
// pkg/provision/errors.go
package provision

// TransientError wraps an error that may succeed on retry.
type TransientError struct {
    Err error
}

func (e *TransientError) Error() string { return e.Err.Error() }
func (e *TransientError) Unwrap() error { return e.Err }

// PermanentError wraps an error that will not succeed on retry.
type PermanentError struct {
    Err error
}

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

func isTransient(err error) bool {
    var transient *TransientError
    return errors.As(err, &transient)
}
```

### Checkpoint/Resume

```go
// pkg/provision/checkpoint.go
package provision

import (
    "encoding/json"
    "fmt"
    "os"
)

// Checkpoint records provisioning progress to tmpfs.
type Checkpoint struct {
    LastCompletedStep string   `json:"lastCompletedStep"`
    CompletedSteps    []string `json:"completedSteps"`
    AttemptCount      int      `json:"attemptCount"`
    Errors           []string  `json:"errors,omitempty"`
}

const checkpointPath = "/tmp/booty-checkpoint.json"

// Save writes the current checkpoint to tmpfs.
func (c *Checkpoint) Save() error {
    data, err := json.Marshal(c)
    if err != nil {
        return fmt.Errorf("marshal checkpoint: %w", err)
    }
    return os.WriteFile(checkpointPath, data, 0o600)
}

// Load reads a checkpoint from tmpfs (returns nil if none exists).
func LoadCheckpoint() *Checkpoint {
    data, err := os.ReadFile(checkpointPath)
    if err != nil {
        return nil
    }
    var cp Checkpoint
    if json.Unmarshal(data, &cp) != nil {
        return nil
    }
    return &cp
}
```

### HTTP Range Resume for Image Download

```go
// pkg/image/resume.go
package image

// ResumableDownload supports HTTP Range-based resume for interrupted downloads.
func (s *Streamer) DownloadWithResume(ctx context.Context, url string, offset int64) (io.ReadCloser, error) {
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
    if err != nil {
        return nil, err
    }
    if offset > 0 {
        req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
    }
    resp, err := s.client.Do(req)
    if err != nil {
        return nil, &TransientError{Err: err}
    }
    if offset > 0 && resp.StatusCode != http.StatusPartialContent {
        // Server doesn't support Range — restart from beginning
        offset = 0
    }
    return resp.Body, nil
}
```

### Watchdog Self-Reboot

```go
// pkg/provision/watchdog.go
package provision

// Watchdog triggers a system reboot after N consecutive provisioning failures.
// Uses exponential backoff between reboots to avoid boot loops.
func (o *Orchestrator) Watchdog(ctx context.Context, maxFailures int) {
    cp := LoadCheckpoint()
    if cp != nil && cp.AttemptCount >= maxFailures {
        slog.Error("max provisioning attempts reached", "attempts", cp.AttemptCount)
        // Report to CAPRF before reboot
        o.client.ReportStatus(ctx, config.StatusError,
            fmt.Sprintf("watchdog: %d consecutive failures, rebooting", cp.AttemptCount))
        // Reboot via /proc/sysrq-trigger
        os.WriteFile("/proc/sysrq-trigger", []byte("b"), 0o200)
    }
}
```

### Required Binaries in Initramfs

No additional binaries needed. This proposal modifies existing Go code only.

## Files Changed

| File | Change |
|------|--------|
| `pkg/provision/retry.go` | Retry framework with backoff + jitter |
| `pkg/provision/errors.go` | Error taxonomy (transient/permanent) |
| `pkg/provision/checkpoint.go` | Checkpoint save/load to tmpfs |
| `pkg/provision/watchdog.go` | Self-reboot after N failures |
| `pkg/provision/orchestrator.go` | Integrate retry into step execution |
| `pkg/image/resume.go` | HTTP Range-based download resume |
| `pkg/caprf/client.go` | Enhanced error reporting with retry history |

## Testing

### Unit Tests

- `provision/retry_test.go` — Table-driven: successful retry, exhausted
  retries, permanent error, context cancellation, jitter bounds.
- `provision/checkpoint_test.go` — Save/load roundtrip, corrupt file
  handling, missing file.
- `provision/errors_test.go` — `isTransient()` with wrapped errors.
- `image/resume_test.go` — Mock HTTP server with Range support. Test
  partial download, server-no-Range fallback, resume after interrupt.

### E2E Tests

- **ContainerLab** (tag `e2e_integration`):
  - Inject network fault (iptables DROP for 10s during image download)
  - Verify BOOTy retries and completes successfully
  - Verify CAPRF receives retry history in error report

## Risks

| Risk | Mitigation |
|------|------------|
| Infinite retry loops | Hard cap via RetryPolicy.MaxAttempts |
| Checkpoint file corruption | JSON validation on load; ignore corrupt |
| Watchdog reboot loop | Exponential backoff between reboots; max attempts |
| Resume corrupts image | Checksum verification after download completes |

## Effort Estimate

8–12 engineering days (retry framework + error taxonomy + checkpoint +
resume + watchdog + tests).
