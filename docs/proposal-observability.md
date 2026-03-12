# Proposal: Observability — Metrics, Debug Dump, and Structured Logging

## Status: Proposal

## Priority: P1 (debug dump), P2 (metrics)

## Summary

Enhance BOOTy's observability with three capabilities:

1. **Enhanced debug dump** (P1): Comprehensive system state capture on
   failure, automatically posted to CAPRF
2. **Provisioning metrics** (P2): Prometheus-compatible timing and counter
   metrics for each provisioning step
3. **Structured event stream** (P2): Real-time provisioning progress events
   for controller dashboards

## Motivation

When provisioning fails, the current error reporting is limited to a single
error message. Operators need:

- Full disk layout (`lsblk`, partition table, mount state)
- Network state (interfaces, routes, ARP/NDP, DNS resolution)
- Kernel messages (`dmesg`) — especially for driver/hardware failures
- Module load status
- Memory pressure (`/proc/meminfo`)
- NIC firmware and driver versions
- UEFI boot entries

For fleet operations, metrics are essential:

- Mean provisioning time per machine type
- Failure rate by step (imaging, networking, EFI)
- Image download speed / bandwidth utilization
- Disk write throughput

### Industry Context

| Tool | Observability |
|------|--------------|
| **Ironic** | Detailed deployment logs, Prometheus exporter, Oslo notifications |
| **MAAS** | Commissioning logs, system test results, rsyslog forwarding |
| **Tinkerbell** | gRPC event stream, basic Prometheus metrics |

## Design

### Part 1: Enhanced Debug Dump (P1)

On provisioning failure, BOOTy collects a comprehensive debug archive:

```go
// pkg/debug/dump.go
type DebugDump struct {
    Timestamp   time.Time         `json:"timestamp"`
    Error       string            `json:"error"`
    Step        string            `json:"failedStep"`
    System      SystemSnapshot    `json:"system"`
    Disks       DiskSnapshot      `json:"disks"`
    Network     NetworkSnapshot   `json:"network"`
    Kernel      KernelSnapshot    `json:"kernel"`
    Config      ConfigSnapshot    `json:"config"`
}

type SystemSnapshot struct {
    Hostname    string            `json:"hostname"`
    Uptime      string            `json:"uptime"`
    MemInfo     map[string]string `json:"meminfo"`
    LoadAvg     string            `json:"loadAvg"`
    Vendor      string            `json:"vendor"`
    Product     string            `json:"product"`
}

type DiskSnapshot struct {
    Lsblk       string   `json:"lsblk"`       // lsblk --json output
    Mounts      string   `json:"mounts"`       // /proc/mounts
    Partitions  string   `json:"partitions"`   // fdisk -l equivalent
    DiskFree    string   `json:"diskFree"`     // df -h
}

type NetworkSnapshot struct {
    Interfaces  string   `json:"interfaces"`   // ip addr
    Routes      string   `json:"routes"`       // ip route
    ARP         string   `json:"arp"`          // ip neigh
    DNS         string   `json:"dns"`          // /etc/resolv.conf contents
    Connectivity string  `json:"connectivity"` // ping results
}

type KernelSnapshot struct {
    Dmesg       string   `json:"dmesg"`
    Modules     string   `json:"modules"`      // lsmod
    Cmdline     string   `json:"cmdline"`      // /proc/cmdline
    UEFIVars    string   `json:"uefiVars"`     // efibootmgr -v
}
```

Collection is triggered automatically on failure:

```go
// pkg/provision/orchestrator.go
func (o *Orchestrator) execute(ctx context.Context, steps []Step) error {
    for _, step := range steps {
        if err := step.Fn(ctx); err != nil {
            dump := debug.Collect(ctx, step.Name, err)
            _ = o.client.ReportDebugDump(ctx, dump)
            return err
        }
    }
    return nil
}
```

### Part 2: Provisioning Metrics (P2)

Timing metrics for each provisioning step:

```go
// pkg/metrics/metrics.go
type StepMetrics struct {
    StepName     string        `json:"stepName"`
    Duration     time.Duration `json:"duration"`
    Status       string        `json:"status"` // "success", "error", "skipped"
    Error        string        `json:"error,omitempty"`
}

type ProvisioningMetrics struct {
    MachineID    string         `json:"machineId"`
    Action       string         `json:"action"` // "provision", "deprovision"
    TotalTime    time.Duration  `json:"totalTime"`
    Steps        []StepMetrics  `json:"steps"`
    ImageSize    int64          `json:"imageSizeBytes"`
    ImageSpeed   float64        `json:"imageSpeedMBps"`
    DiskWriteSpeed float64      `json:"diskWriteSpeedMBps"`
}
```

Instrumentation wrapper:

```go
func (o *Orchestrator) instrumentedStep(step Step) Step {
    return Step{
        Name: step.Name,
        Fn: func(ctx context.Context) error {
            start := time.Now()
            err := step.Fn(ctx)
            o.metrics.Steps = append(o.metrics.Steps, StepMetrics{
                StepName: step.Name,
                Duration: time.Since(start),
                Status:   statusString(err),
            })
            return err
        },
    }
}
```

### Part 3: Structured Event Stream (P2)

Real-time progress events sent to CAPRF during provisioning:

```go
// pkg/caprf/client.go
func (c *Client) SendEvent(ctx context.Context, event ProvisionEvent) error {
    return c.postWithAuth(ctx, c.cfg.EventURL, event)
}

type ProvisionEvent struct {
    Type      string `json:"type"`      // "step_start", "step_complete", "progress"
    Step      string `json:"step"`
    Message   string `json:"message"`
    Progress  int    `json:"progress"`  // 0-100 percent
    Timestamp int64  `json:"timestamp"`
}
```

This enables the CAPRF controller to show live provisioning progress in
machine status:

```
Phase: Provisioning (Step 5/12: streaming-image, 67%)
```

### CAPRF Server Integration

CAPRF exposes metrics via its existing Prometheus endpoint:

```go
// Prometheus metrics (CAPRF controller side)
var (
    provisionDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "caprf_provision_duration_seconds",
            Help:    "Time spent provisioning machines",
            Buckets: []float64{60, 120, 300, 600, 900, 1200},
        },
        []string{"machine", "step", "status"},
    )
    provisionStepErrors = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "caprf_provision_step_errors_total",
            Help: "Number of provisioning step failures",
        },
        []string{"machine", "step"},
    )
)
```

## Affected Files

| File | Change |
|------|--------|
| `pkg/debug/dump.go` | New — debug dump collection |
| `pkg/debug/dump_test.go` | New — unit tests |
| `pkg/metrics/metrics.go` | New — step timing metrics |
| `pkg/caprf/client.go` | Add `ReportDebugDump()`, `ReportMetrics()`, `SendEvent()` |
| `pkg/provision/orchestrator.go` | Instrument steps, collect dump on failure |
| `pkg/config/provider.go` | Add `DebugDumpEnabled`, `MetricsEnabled`, `EventURL` |
| CAPRF `internal/server/` | Add debug dump + metrics endpoints |

## Risks

- **Dump size**: Full `dmesg` output can be >1 MB. Truncate to last 10,000
  lines or compress with gzip before sending.
- **Sensitive data**: Debug dumps may contain passwords, tokens, or keys from
  `/deploy/vars`. Redact known secret patterns before sending.
- **Network dependency**: If the failure is network-related, the debug dump
  can't be posted to CAPRF. Store locally as fallback and retry on recovery.

## Effort Estimate

- Debug dump collection: **3 days**
- Metrics instrumentation: **2-3 days**
- Event stream: **2 days**
- CAPRF server endpoints: **2-3 days**
- Testing: **2 days**
- Total: **11-15 days**
