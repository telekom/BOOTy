# Proposal: Pre-Provisioning Health Checks

## Status: Implemented (PR #17)

## Priority: P1

## Summary

Run a suite of hardware health checks **before** starting the provisioning
pipeline. If a machine fails any critical check, provisioning is aborted and
the machine is flagged for manual intervention — preventing wasted time
imaging a machine that will fail in production.

## Implementation Details

The health check framework and 7 built-in checks are fully implemented.
Key decisions and deviations from the original proposal:

- **Status type**: Uses typed `Status` string constants (`StatusPass`,
  `StatusFail`, `StatusSkip`) instead of raw strings.
- **Severity**: Uses typed `Severity` constants (`SeverityInfo`,
  `SeverityWarning`, `SeverityCritical`).
- **Skip support**: `StatusSkip` used when hardware is not available
  (e.g., no EDAC for ECC, no thermal zones), distinguishing from `Pass`.
- **Check skipping**: `RunAll()` accepts a skip list to disable specific
  checks by name via `HEALTH_SKIP_CHECKS` config.
- **Counter accuracy**: `checked` counters increment only after
  successfully reading the relevant data (e.g., ioerr_cnt, temp file).
- **Error specificity**: Critical failure error message includes the
  names of the failed checks.
- **CAPRF reporting**: `ReportHealthChecks()` POSTs results with retry
  logic; best-effort (does not fail provisioning on report error).
- **FirmwareVersion check**: Not implemented in this PR — firmware
  checks are handled separately in PR #19.
- **ThermalState**: Reads from sysfs `/sys/class/thermal` (not Redfish),
  consistent with the initrd-only approach.

### Implemented Checks

| Check | Source | Severity | Status |
|-------|--------|----------|--------|
| `disk-presence` | `/sys/block/` | Critical | Implemented |
| `disk-smart` | `/sys/block/*/device/ioerr_cnt` | Warning | Implemented |
| `memory-ecc` | `/sys/devices/system/edac/mc/` | Critical | Implemented |
| `minimum-memory` | `/proc/meminfo` | Critical | Implemented |
| `minimum-cpu` | `/proc/cpuinfo` | Warning | Implemented |
| `nic-link-state` | `/sys/class/net/*/carrier` | Warning | Implemented |
| `thermal-state` | `/sys/class/thermal/` | Warning | Implemented |

### Files Changed

| File | Change |
|------|--------|
| `pkg/health/check.go` | Check framework, `RunAll()`, types |
| `pkg/health/check_test.go` | Comprehensive unit tests |
| `pkg/health/cpu.go` | Minimum CPU check |
| `pkg/health/disk.go` | Disk presence + SMART checks |
| `pkg/health/memory.go` | ECC + minimum memory checks |
| `pkg/health/network.go` | NIC link state check |
| `pkg/health/thermal.go` | Thermal zone check |
| `pkg/provision/orchestrator.go` | `runHealthChecks()` step |
| `pkg/caprf/client.go` | `ReportHealthChecks()` |
| `pkg/caprf/client_test.go` | Health reporting tests |
| `pkg/config/provider.go` | Health check config fields |

## Motivation

Common failure modes that waste provisioning cycles:

| Failure | Symptom | Wasted Time |
|---------|---------|-------------|
| Failed DIMM | Reduced memory, ECC errors | Full provision + kernel panic |
| Degraded RAID | Array not optimal | Provision succeeds, data loss risk |
| NIC link down | Network unreachable | Provision hangs on connectivity |
| Disk SMART warning | Disk about to fail | Provision succeeds, disk dies within weeks |
| Fan failure | Thermal throttling | Machine performs poorly |
| Wrong firmware | Known bugs | Intermittent failures in production |

### Industry Context

| Tool | Health Checks |
|------|--------------|
| **Ironic** | `inspect` step runs configurable healthchecks via IPA (Ironic Python Agent) |
| **MAAS** | Commissioning runs storage, memory, CPU stress tests |
| **Tinkerbell** | No built-in health checks |

## Design

### Check Framework

```go
// pkg/health/check.go
type Severity int

const (
    SeverityInfo     Severity = iota // Log only
    SeverityWarning                   // Log + report, continue
    SeverityCritical                  // Abort provisioning
)

type CheckResult struct {
    Name     string   `json:"name"`
    Status   string   `json:"status"`   // "pass", "warn", "fail"
    Severity Severity `json:"severity"`
    Message  string   `json:"message"`
    Details  string   `json:"details,omitempty"`
}

type Check interface {
    Name() string
    Severity() Severity
    Run(ctx context.Context) CheckResult
}

func RunAll(ctx context.Context, checks []Check) ([]CheckResult, error) {
    var results []CheckResult
    var critical bool
    for _, c := range checks {
        r := c.Run(ctx)
        results = append(results, r)
        if r.Status == "fail" && c.Severity() == SeverityCritical {
            critical = true
        }
    }
    if critical {
        return results, fmt.Errorf("critical health check(s) failed")
    }
    return results, nil
}
```

### Built-in Checks

| Check | Source | Severity | What it does |
|-------|--------|----------|-------------|
| `DiskSMART` | `/sys/block/*/device/` | Critical | Read SMART status; fail if any disk reports errors |
| `MemoryECC` | `/sys/devices/system/edac/` | Critical | Check for uncorrectable ECC errors |
| `NICLinkState` | `/sys/class/net/*/carrier` | Warning | Warn if any expected NIC has no link |
| `DiskPresence` | `/sys/block/` | Critical | Verify root disk target exists |
| `MinimumMemory` | `/proc/meminfo` | Critical | Fail if total RAM below threshold |
| `MinimumCPU` | `/proc/cpuinfo` | Warning | Warn if CPU count below expected |
| `FirmwareVersion` | Redfish via CAPRF | Warning | Warn if BMC/BIOS firmware is below minimum |
| `ThermalState` | Redfish `Thermal` resource | Warning | Check fan and temperature status |

### Integration

```go
// pkg/provision/orchestrator.go
func (o *Orchestrator) Provision(ctx context.Context) error {
    steps := []Step{
        {Name: "health-checks", Fn: o.RunHealthChecks},
        // ... existing steps ...
    }
    return o.execute(ctx, steps)
}

func (o *Orchestrator) RunHealthChecks(ctx context.Context) error {
    checks := health.DefaultChecks(o.cfg)
    results, err := health.RunAll(ctx, checks)

    // Report results to CAPRF
    if reportErr := o.client.ReportHealthChecks(ctx, results); reportErr != nil {
        slog.Warn("failed to report health checks", "error", reportErr)
    }

    return err // nil if all passed, error if critical failure
}
```

### Configuration

```bash
# /deploy/vars
export HEALTH_CHECKS_ENABLED="true"
export HEALTH_MIN_MEMORY_GB="64"
export HEALTH_MIN_CPUS="16"
export HEALTH_SKIP_CHECKS="thermal,firmware"  # skip specific checks
```

### Reporting

Health check results are posted to CAPRF and stored in machine status:

```
POST /status/health-checks
Content-Type: application/json

{
    "results": [
        {"name": "DiskSMART", "status": "pass", "severity": 2, "message": "All disks healthy"},
        {"name": "MemoryECC", "status": "pass", "severity": 2, "message": "No ECC errors"},
        {"name": "NICLinkState", "status": "warn", "severity": 1, "message": "eno3 has no carrier"}
    ]
}
```

## Affected Files

| File | Change |
|------|--------|
| `pkg/health/check.go` | New — check framework |
| `pkg/health/disk.go` | New — SMART + disk presence checks |
| `pkg/health/memory.go` | New — ECC + minimum memory checks |
| `pkg/health/network.go` | New — NIC link state check |
| `pkg/health/cpu.go` | New — minimum CPU check |
| `pkg/health/check_test.go` | New — unit tests |
| `pkg/provision/orchestrator.go` | Add `RunHealthChecks()` step |
| `pkg/caprf/client.go` | Add `ReportHealthChecks()` |
| `pkg/config/provider.go` | Add health check config fields |

## Risks

- **False positives**: Some SMART attributes trigger warnings on healthy disks
  (e.g., reallocated sector count on SSDs with wear leveling). Need
  vendor-specific thresholds.
- **Check duration**: SMART queries can take 1-2 seconds per disk. For machines
  with many disks, run checks in parallel.
- **Missing sysfs**: Some checks may not work in minimal initrd environments.
  Each check should handle missing sysfs gracefully.

## Effort Estimate

- Check framework: **1 day**
- Built-in checks (8 checks): **3-4 days**
- CAPRF integration (endpoint + status): **2 days**
- Testing: **2 days**
- Total: **8-10 days**
