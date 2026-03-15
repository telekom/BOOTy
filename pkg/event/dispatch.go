package event

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Dispatcher sends events to a webhook URL.
type Dispatcher struct {
	url    string
	client *http.Client
	log    *slog.Logger
}

// NewDispatcher creates a webhook event dispatcher.
func NewDispatcher(webhookURL string, log *slog.Logger) *Dispatcher {
	return &Dispatcher{
		url: webhookURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		log: log,
	}
}

// Send dispatches an event to the webhook URL.
func (d *Dispatcher) Send(ctx context.Context, e *Event) error {
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("send webhook: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close

	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	d.log.Debug("Event dispatched", "type", e.Type, "machine", e.Machine.Name)
	return nil
}
