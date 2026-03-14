# Proposal: Webhook and Event Notifications

## Status: Proposal

## Priority: P3

## Summary

Add a webhook notification system that sends provisioning lifecycle events
to external systems (Slack, PagerDuty, custom endpoints). This enables
integration with existing alerting, ChatOps, and audit systems without
polling the CAPRF API.

## Motivation

Fleet operators need real-time visibility into provisioning events:

- **Provisioning started/completed/failed** — for capacity planning dashboards
- **Health check failures** — for immediate remediation
- **Firmware mismatches** — for compliance tracking
- **Rescue mode activated** — for on-call alert routing
- **Batch progress** — for tracking large-scale rollouts

Currently, the only way to track provisioning status is to poll the CAPRF API
or watch Kubernetes events. Webhooks provide a push-based, decoupled
notification mechanism.

### Industry Context

| Tool | Webhooks |
|------|----------|
| **Ironic** | Oslo notifications → RabbitMQ → consumers |
| **MAAS** | Webhook notifications for machine state changes (MAAS 3.3+) |
| **Tinkerbell** | gRPC event stream (not HTTP webhooks) |
| **ArgoCD** | Webhook notifications to Slack, Teams, custom URLs |

## Design

### Architecture

```
┌───────────────────────┐     ┌──────────────────────┐
│ BOOTy (initrd)        │────▶│ CAPRF Controller     │
│  Reports events       │     │  Stores events       │
│  via existing API     │     │  Dispatches webhooks  │
└───────────────────────┘     └─────────┬────────────┘
                                        │
                        ┌───────────────┼───────────────┐
                        ▼               ▼               ▼
                 ┌──────────┐   ┌──────────┐   ┌──────────┐
                 │  Slack   │   │ PagerDuty│   │ Custom   │
                 │  Channel │   │  Alert   │   │ Endpoint │
                 └──────────┘   └──────────┘   └──────────┘
```

Webhooks are dispatched by the **CAPRF controller** (not BOOTy), since
it has the full context and persistent state. BOOTy continues to report
events via its existing heartbeat/status API.

### Webhook Configuration

```yaml
# CAPRF ConfigMap or CRD
apiVersion: v1
kind: ConfigMap
metadata:
  name: caprf-webhooks
data:
  webhooks: |
    - name: slack-provisioning
      url: https://hooks.slack.com/services/T00/B00/xxx
      events: ["provision.started", "provision.completed", "provision.failed"]
      template: slack
      retries: 3
      timeoutSeconds: 10

    - name: pagerduty-failures
      url: https://events.pagerduty.com/v2/enqueue
      events: ["provision.failed", "health.critical"]
      template: pagerduty
      headers:
        Content-Type: application/json

    - name: audit-log
      url: https://audit.internal/api/events
      events: ["*"]  # all events
      template: json
      headers:
        Authorization: Bearer ${AUDIT_TOKEN}
```

### Event Types

| Event | Trigger |
|-------|---------|
| `provision.started` | Provisioning pipeline begins |
| `provision.completed` | Machine successfully provisioned |
| `provision.failed` | Provisioning failed (with error details) |
| `deprovision.started` | Deprovisioning begins |
| `deprovision.completed` | Machine deprovisioned |
| `health.critical` | Critical health check failed |
| `health.warning` | Non-critical health check issue |
| `rescue.activated` | Machine entered rescue mode |
| `firmware.mismatch` | Firmware below minimum version |
| `attestation.failed` | TPM attestation verification failed |

### Webhook Payload

```json
{
    "event": "provision.failed",
    "timestamp": "2025-01-15T14:30:00Z",
    "machine": {
        "name": "worker-42",
        "namespace": "cluster-prod",
        "redfishHost": "rfh-rack3-u42",
        "address": "10.0.1.42"
    },
    "details": {
        "step": "image-streaming",
        "error": "connection reset by peer",
        "attempt": 2,
        "maxAttempts": 3,
        "duration": "4m32s"
    }
}
```

### Dispatcher Implementation

```go
// CAPRF pkg/webhooks/dispatcher.go
type Dispatcher struct {
    client   *http.Client
    configs  []WebhookConfig
    queue    chan Event
}

func (d *Dispatcher) Dispatch(event Event) {
    for _, cfg := range d.configs {
        if cfg.Matches(event.Type) {
            d.queue <- event
        }
    }
}

func (d *Dispatcher) worker() {
    for event := range d.queue {
        for _, cfg := range d.configs {
            if !cfg.Matches(event.Type) {
                continue
            }
            payload, _ := cfg.Template.Render(event)
            d.sendWithRetry(cfg, payload)
        }
    }
}

func (d *Dispatcher) sendWithRetry(cfg WebhookConfig, payload []byte) {
    for attempt := 0; attempt <= cfg.Retries; attempt++ {
        req, _ := http.NewRequest("POST", cfg.URL, bytes.NewReader(payload))
        for k, v := range cfg.Headers {
            req.Header.Set(k, v)
        }
        resp, err := d.client.Do(req)
        if err == nil && resp.StatusCode < 300 {
            resp.Body.Close()
            return
        }
        time.Sleep(time.Duration(attempt+1) * time.Second)
    }
}
```

## Required Binaries in Initramfs

No BOOTy binary changes needed. Webhooks are dispatched by the **CAPRF
controller**, not by BOOTy. BOOTy continues to report events via its
existing HTTP API.

## Affected Files

| File | Change |
|------|--------|
| CAPRF `pkg/webhooks/dispatcher.go` | New — webhook dispatch engine |
| CAPRF `pkg/webhooks/templates.go` | New — Slack, PagerDuty, JSON templates |
| CAPRF `pkg/webhooks/config.go` | New — webhook configuration types |
| CAPRF `internal/controllers/` | Emit webhook events from reconcile loops |
| CAPRF `internal/provision/manager.go` | Emit events on provision/deprovision |
| BOOTy — no changes needed (uses existing event API) |

## Risks

- **Secret management**: Webhook URLs contain sensitive tokens. Must be
  stored in Kubernetes Secrets, not ConfigMaps.
- **Delivery guarantees**: HTTP webhooks are at-most-once. For critical
  audit events, consider a persistent queue (e.g., writing to a PVC).
- **Rate limiting**: Bulk provisioning could trigger hundreds of webhooks.
  Add debouncing and batch digest options.

## Effort Estimate

- Dispatcher + worker pool: **3 days**
- Templates (Slack, PagerDuty, JSON): **2 days**
- Configuration CRD/ConfigMap: **2 days**
- Controller integration: **2-3 days**
- Testing: **2 days**
- Total: **11-14 days**
