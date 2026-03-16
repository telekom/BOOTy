# Proposal: Kafka Structured Logging

## Status: Implemented (PR #54)

## Priority: P2

## Dependencies: [Observability](proposal-observability.md)

## Summary

Add Kafka as a structured logging sink alongside existing CAPRF log shipping.
BOOTy streams `slog` output to a Kafka topic as structured JSON messages,
providing real-time provisioning event streaming for fleet monitoring
dashboards, alerting pipelines, and audit trails.

## Motivation

The existing observability proposal covers OpenTelemetry for metrics/traces.
This companion proposal addresses structured log streaming:

| Current | Gap |
|---------|-----|
| Logs go to serial console + CAPRF | No real-time streaming to central log systems |
| CAPRF log shipping is request/response | No event-driven log consumption |
| No structured log format guarantee | CAPRF receives free-form text |
| No metadata enrichment | Machine identity not in log stream |

### Use Cases

1. **Fleet provisioning dashboard**: Live view of 100+ machines provisioning
2. **Alert on error patterns**: Kafka → alerting pipeline for recurring issues
3. **Audit trail**: Immutable log of all provisioning actions
4. **Debug correlation**: Trace provisioning issues across machine fleet

### Industry Context

| Tool | Log Streaming |
|------|--------------|
| **Kubernetes** | Container stdout → fluentd/vector → Kafka/Loki |
| **Ironic** | oslo.messaging → RabbitMQ → ELK |
| **Tinkerbell** | gRPC streaming |
| **systemd** | journal → journald → forwarding |

## Design

### Architecture

```
BOOTy (initrd)
  └─ slog.Handler (multiplexed)
       ├─ Console handler (serial, stderr)
       ├─ CAPRF handler (HTTP POST, buffered)
       └─ Kafka handler (Kafka producer, async)
            │
            ▼
      Kafka Cluster (booty.provisioning.logs)
            │
            ├─ Loki / Elasticsearch (storage)
            ├─ Grafana dashboard (visualization)
            └─ Alert manager (alerting)
```

### Kafka slog Handler

```go
// pkg/logging/kafka.go
package logging

import (
    "context"
    "encoding/json"
    "fmt"
    "log/slog"
    "time"

    "github.com/IBM/sarama"
)

// KafkaHandler implements slog.Handler for Kafka output.
type KafkaHandler struct {
    producer sarama.AsyncProducer
    topic    string
    attrs    []slog.Attr // machine identity attributes
    level    slog.Level
}

// KafkaConfig holds Kafka connection settings.
type KafkaConfig struct {
    Brokers      []string `json:"brokers"`       // ["kafka1:9092", "kafka2:9092"]
    Topic        string   `json:"topic"`          // "booty.provisioning.logs"
    TLS          bool     `json:"tls"`
    SASLUser     string   `json:"saslUser,omitempty"`
    SASLPassword string   `json:"saslPassword,omitempty"`
    Compression  string   `json:"compression"`    // "snappy", "lz4", "zstd", "none"
}

// LogMessage is the structured Kafka message format.
type LogMessage struct {
    Timestamp      time.Time              `json:"timestamp"`
    Level          string                 `json:"level"`
    Message        string                 `json:"message"`
    MachineSerial  string                 `json:"machineSerial"`
    BMCMAC         string                 `json:"bmcMac"`
    ProvisioningID string                 `json:"provisioningId"`
    Step           string                 `json:"step,omitempty"`
    Attrs          map[string]interface{} `json:"attrs,omitempty"`
}

// NewKafkaHandler creates a Kafka slog handler.
func NewKafkaHandler(cfg KafkaConfig, machineSerial, bmcMAC, provID string) (*KafkaHandler, error) {
    config := sarama.NewConfig()
    config.Producer.Return.Errors = true
    config.Producer.Compression = compressionCodec(cfg.Compression)
    config.Producer.Flush.Frequency = 500 * time.Millisecond
    config.Producer.Flush.Messages = 100

    if cfg.TLS {
        config.Net.TLS.Enable = true
    }
    if cfg.SASLUser != "" {
        config.Net.SASL.Enable = true
        config.Net.SASL.User = cfg.SASLUser
        config.Net.SASL.Password = cfg.SASLPassword
        config.Net.SASL.Mechanism = sarama.SASLTypePlaintext
    }

    producer, err := sarama.NewAsyncProducer(cfg.Brokers, config)
    if err != nil {
        return nil, fmt.Errorf("create Kafka producer: %w", err)
    }

    h := &KafkaHandler{
        producer: producer,
        topic:    cfg.Topic,
        level:    slog.LevelInfo,
        attrs: []slog.Attr{
            slog.String("machineSerial", machineSerial),
            slog.String("bmcMac", bmcMAC),
            slog.String("provisioningId", provID),
        },
    }

    // Drain error channel in background
    go func() {
        for err := range producer.Errors() {
            // Log to stderr only (avoid recursion)
            fmt.Fprintf(os.Stderr, "kafka producer error: %v\n", err)
        }
    }()

    return h, nil
}

func (h *KafkaHandler) Handle(ctx context.Context, r slog.Record) error {
    msg := LogMessage{
        Timestamp: r.Time,
        Level:     r.Level.String(),
        Message:   r.Message,
    }

    // Extract machine identity from handler attrs
    for _, a := range h.attrs {
        switch a.Key {
        case "machineSerial":
            msg.MachineSerial = a.Value.String()
        case "bmcMac":
            msg.BMCMAC = a.Value.String()
        case "provisioningId":
            msg.ProvisioningID = a.Value.String()
        }
    }

    // Collect record attributes
    msg.Attrs = make(map[string]interface{})
    r.Attrs(func(a slog.Attr) bool {
        msg.Attrs[a.Key] = a.Value.Any()
        return true
    })

    data, err := json.Marshal(msg)
    if err != nil {
        return err
    }

    h.producer.Input() <- &sarama.ProducerMessage{
        Topic: h.topic,
        Key:   sarama.StringEncoder(msg.MachineSerial),
        Value: sarama.ByteEncoder(data),
    }
    return nil
}

func (h *KafkaHandler) Enabled(_ context.Context, level slog.Level) bool {
    return level >= h.level
}

func (h *KafkaHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
    return &KafkaHandler{
        producer: h.producer,
        topic:    h.topic,
        attrs:    append(h.attrs, attrs...),
        level:    h.level,
    }
}

func (h *KafkaHandler) WithGroup(name string) slog.Handler {
    return h // groups not supported for Kafka output
}

// Close flushes and closes the Kafka producer.
func (h *KafkaHandler) Close() error {
    return h.producer.Close()
}
```

### Multiplexed Logger Setup

```go
// pkg/logging/multi.go
package logging

// NewMultiHandler creates a slog handler that fans out to multiple sinks.
func NewMultiHandler(handlers ...slog.Handler) slog.Handler {
    return &multiHandler{handlers: handlers}
}

type multiHandler struct {
    handlers []slog.Handler
}

func (m *multiHandler) Handle(ctx context.Context, r slog.Record) error {
    for _, h := range m.handlers {
        if h.Enabled(ctx, r.Level) {
            _ = h.Handle(ctx, r) // best-effort; continue on error
        }
    }
    return nil
}
```

### Required Binaries in Initramfs

No additional binaries needed. Kafka client is a pure Go library
(`github.com/IBM/sarama`).

### Go Dependencies

| Package | Purpose | Size Impact |
|---------|---------|-------------|
| `github.com/IBM/sarama` | Kafka producer (pure Go) | ~2 MB binary size |

**Note**: `sarama` is a pure Go Kafka client — no CGO, no native dependencies.
If binary size is a concern, the Kafka handler can be compile-time gated
with a build tag (`//go:build kafka`).

### Configuration

```bash
# /deploy/vars
export KAFKA_ENABLED="true"
export KAFKA_BROKERS="kafka1.example.com:9092,kafka2.example.com:9092"
export KAFKA_TOPIC="booty.provisioning.logs"
export KAFKA_TLS="true"
export KAFKA_SASL_USER="booty-producer"
export KAFKA_SASL_PASSWORD="<from CAPRF>"
export KAFKA_COMPRESSION="snappy"  # snappy, lz4, zstd, none
```

## Files Changed

| File | Change |
|------|--------|
| `pkg/logging/kafka.go` | Kafka slog handler |
| `pkg/logging/multi.go` | Multiplexed handler |
| `pkg/logging/kafka_test.go` | Unit tests |
| `cmd/booty.go` | Initialize Kafka handler if enabled |
| `pkg/config/provider.go` | Kafka config fields |
| `go.mod` | Add `github.com/IBM/sarama` |

## Testing

### Unit Tests

- `logging/kafka_test.go`:
  - Message serialization format verification
  - Handler attribute propagation
  - Level filtering
  - `WithAttrs` creates new handler with merged attrs
  - Mock producer verifies message routing

### E2E Tests

- **ContainerLab** (tag `e2e_integration`):
  - Spin up single-broker Kafka (Redpanda, lightweight) in topology
  - BOOTy provisions → consume messages from topic → verify structured format
  - Verify: machine identity in every message
  - Verify: log levels correct

## Risks

| Risk | Mitigation |
|------|------------|
| Kafka unavailable | Best-effort; log to console regardless |
| `sarama` binary size (+2 MB) | Build tag `//go:build kafka`; not in slim/micro |
| Kafka broker discovery | Hardcoded broker list; no ZooKeeper dependency |
| Log flooding Kafka | Rate limiter + flush batching |
| SASL password in /deploy/vars | Same security model as existing TOKEN field |

## Effort Estimate

5–8 engineering days (Kafka handler + multiplexer + config + E2E with
Redpanda + build tag gating).
