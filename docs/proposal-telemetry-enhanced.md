# Proposal: Enhanced Telemetry — OpenTelemetry + Prometheus Metrics

## Status: Implemented

## Priority: P2

## Dependencies: [Observability](proposal-observability.md)

## Summary

Extend the existing observability proposal with concrete metric definitions,
OTLP export implementation, Prometheus-compatible metric exposition, 
provisioning-specific spans and events, and a reference Grafana dashboard.
Covers BOOTy runtime metrics (step timings, image throughput, error rates),
hardware metrics (disk I/O, NIC stats, temperatures), and fleet-level
aggregation in CAPRF.

## Motivation

The existing observability proposal provides OpenTelemetry as a design
direction. This companion proposal specifies the exact metrics, spans,
and integration points:

| Existing Proposal | This Companion |
|------------------|---------------|
| Mentions OpenTelemetry | Defines exact metrics, spans, events |
| No Prometheus endpoint | Adds push-gateway metric export |
| No dashboard | Reference Grafana dashboard JSON |
| No metric naming convention | OpenTelemetry semantic conventions |

### Metric Categories

| Category | Example Metrics |
|----------|----------------|
| Provisioning | Step duration, total provision time, retry count |
| Image | Download throughput (MB/s), decompression ratio, write speed |
| Network | Link speed, BGP session state, LLDP neighbor count |
| Disk | Partition count, RAID status, LUKS setup time |
| Hardware | CPU temp, DIMM count, NIC count, disk count |
| Fleet | Machines provisioned/hour, failure rate, avg provision time |

## Design

### Metric Definitions

```go
// pkg/telemetry/metrics.go
package telemetry

import (
    "go.opentelemetry.io/otel/metric"
)

// Provisioning metrics (counter, histogram, gauge).
type Metrics struct {
    // Step execution
    StepDuration       metric.Float64Histogram // step.duration (seconds)
    StepRetries        metric.Int64Counter     // step.retries
    StepErrors         metric.Int64Counter     // step.errors
    ProvisionDuration  metric.Float64Histogram // provision.duration (seconds)

    // Image streaming
    ImageBytes         metric.Int64Counter     // image.bytes.total
    ImageThroughput    metric.Float64Gauge     // image.throughput.mbps
    ImageSize          metric.Int64Gauge       // image.size.bytes

    // Network
    LinkSpeed          metric.Int64Gauge       // network.link.speed.mbps
    BGPSessions        metric.Int64Gauge       // network.bgp.sessions
    LLDPNeighbors      metric.Int64Gauge       // network.lldp.neighbors

    // Hardware
    DiskCount          metric.Int64Gauge       // hardware.disk.count
    NICCount           metric.Int64Gauge       // hardware.nic.count
    MemoryTotal        metric.Int64Gauge       // hardware.memory.total.bytes
    CPUCount           metric.Int64Gauge       // hardware.cpu.count
}

func NewMetrics(meter metric.Meter) (*Metrics, error) {
    m := &Metrics{}
    var err error

    m.StepDuration, err = meter.Float64Histogram("booty.step.duration",
        metric.WithDescription("Duration of each provisioning step"),
        metric.WithUnit("s"))
    if err != nil {
        return nil, err
    }

    m.StepRetries, err = meter.Int64Counter("booty.step.retries",
        metric.WithDescription("Number of retries per step"))
    if err != nil {
        return nil, err
    }

    m.ImageThroughput, err = meter.Float64Gauge("booty.image.throughput",
        metric.WithDescription("Image streaming throughput"),
        metric.WithUnit("MiBy/s"))
    if err != nil {
        return nil, err
    }

    // ... remaining metric registrations ...
    return m, nil
}
```

### Tracing Spans

```go
// pkg/telemetry/tracing.go
package telemetry

import (
    "context"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("booty")

// StartStepSpan creates a span for a provisioning step.
func StartStepSpan(ctx context.Context, stepName string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
    allAttrs := append([]attribute.KeyValue{
        attribute.String("step.name", stepName),
    }, attrs...)
    return tracer.Start(ctx, "provision."+stepName,
        trace.WithAttributes(allAttrs...))
}

// Provisioning trace hierarchy:
//   provision (root span)
//   ├── provision.report-init
//   ├── provision.configure-network
//   │   ├── provision.configure-network.detect-interfaces
//   │   ├── provision.configure-network.configure-bgp
//   │   └── provision.configure-network.wait-for-convergence
//   ├── provision.detect-disk
//   ├── provision.stream-image
//   │   ├── provision.stream-image.download
//   │   ├── provision.stream-image.decompress
//   │   └── provision.stream-image.write
//   ├── provision.configure-bootloader
//   └── provision.report-success
```

### OTLP Export

```go
// pkg/telemetry/export.go
package telemetry

import (
    "context"
    "fmt"

    "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
    "go.opentelemetry.io/otel/sdk/metric"
    "go.opentelemetry.io/otel/sdk/trace"
)

// ExportConfig specifies telemetry export settings.
type ExportConfig struct {
    Enabled         bool   `json:"enabled"`
    OTLPEndpoint    string `json:"otlpEndpoint"`    // "http://otel-collector:4318"
    PushGateway     string `json:"pushGateway"`     // "http://pushgateway:9091" (Prometheus)
    ServiceName     string `json:"serviceName"`     // "booty"
    ServiceVersion  string `json:"serviceVersion"`
    ExportInterval  int    `json:"exportInterval"`  // seconds (default: 30)
}

// Setup initializes OpenTelemetry with OTLP export.
func Setup(ctx context.Context, cfg ExportConfig) (func(), error) {
    // Trace exporter
    traceExp, err := otlptracehttp.New(ctx,
        otlptracehttp.WithEndpoint(cfg.OTLPEndpoint),
        otlptracehttp.WithInsecure(),
    )
    if err != nil {
        return nil, fmt.Errorf("create trace exporter: %w", err)
    }

    tp := trace.NewTracerProvider(
        trace.WithBatcher(traceExp),
        trace.WithResource(newResource(cfg)),
    )

    // Metric exporter
    metricExp, err := otlpmetrichttp.New(ctx,
        otlpmetrichttp.WithEndpoint(cfg.OTLPEndpoint),
        otlpmetrichttp.WithInsecure(),
    )
    if err != nil {
        return nil, fmt.Errorf("create metric exporter: %w", err)
    }

    mp := metric.NewMeterProvider(
        metric.WithReader(metric.NewPeriodicReader(metricExp)),
        metric.WithResource(newResource(cfg)),
    )

    otel.SetTracerProvider(tp)
    otel.SetMeterProvider(mp)

    shutdown := func() {
        tp.Shutdown(ctx)
        mp.Shutdown(ctx)
    }
    return shutdown, nil
}
```

### Required Binaries in Initramfs

No additional binaries needed. OpenTelemetry SDK is a pure Go library.

### Go Dependencies

| Package | Purpose | Size Impact |
|---------|---------|-------------|
| `go.opentelemetry.io/otel` | Core OTel API | ~1 MB |
| `go.opentelemetry.io/otel/sdk` | OTel SDK | included |
| `go.opentelemetry.io/otel/exporters/otlp/*` | OTLP export | ~0.5 MB |

**Build tag gating**: `//go:build telemetry` — not included in slim/micro.

### Configuration

```bash
# /deploy/vars
export TELEMETRY_ENABLED="true"
export OTEL_ENDPOINT="http://otel-collector.monitoring:4318"
export PROM_PUSHGATEWAY="http://pushgateway.monitoring:9091"
export TELEMETRY_INTERVAL="30"  # export interval in seconds
```

## Files Changed

| File | Change |
|------|--------|
| `pkg/telemetry/metrics.go` | Metric definitions |
| `pkg/telemetry/tracing.go` | Span helpers |
| `pkg/telemetry/export.go` | OTLP + Prometheus push-gateway setup |
| `pkg/telemetry/metrics_test.go` | Unit tests |
| `pkg/provision/orchestrator.go` | Instrument steps with spans + metrics |
| `pkg/image/streamer.go` | Add throughput metric recording |
| `cmd/booty.go` | Initialize telemetry if enabled |
| `go.mod` | OpenTelemetry dependencies |

## Testing

### Unit Tests

- `telemetry/metrics_test.go`:
  - Metric registration without duplicate errors
  - Step duration histogram records correctly
  - In-memory exporter captures expected metrics
- `telemetry/tracing_test.go`:
  - Span creation with correct parent-child hierarchy
  - Span attributes include step name, machine identity
  - In-memory exporter captures expected spans

### E2E Tests

- **ContainerLab** (tag `e2e_integration`):
  - Deploy OpenTelemetry Collector (otel/opentelemetry-collector) in topology
  - BOOTy provisions → verify traces appear in collector output
  - Query Prometheus push-gateway for BOOTy metrics

## Risks

| Risk | Mitigation |
|------|------------|
| OTel SDK binary size | Build tag gating; not in slim/micro |
| OTLP endpoint unavailable | Best-effort export; don't block provisioning |
| High-frequency metrics flood | 30s export interval; batch mode |
| Trace context lost across steps | Pass `context.Context` consistently |

## Effort Estimate

8–12 engineering days (metric definitions + tracing + export config + 
orchestrator instrumentation + Grafana dashboard + tests).
