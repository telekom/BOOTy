package event

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultHTTPTimeout = 10 * time.Second
	maxDrainBytes      = 1 << 20 // 1 MiB
	maxErrorBodyBytes  = 4 << 10 // 4 KiB
)

// Dispatcher sends events to a webhook URL.
type Dispatcher struct {
	url    *url.URL
	client *http.Client
	log    *slog.Logger
}

// NewDispatcher creates a webhook event dispatcher.
// It validates the webhook URL at construction time.
func NewDispatcher(webhookURL string, log *slog.Logger) (*Dispatcher, error) {
	webhookURL = strings.TrimSpace(webhookURL)
	if webhookURL == "" {
		return nil, fmt.Errorf("webhook URL must not be empty")
	}

	u, err := url.Parse(webhookURL)
	if err != nil {
		return nil, fmt.Errorf("parse webhook URL: %w", err)
	}
	if u.Scheme == "" {
		return nil, fmt.Errorf("webhook URL must include scheme (https://)")
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !isLocalHost(u.Hostname())) {
		return nil, fmt.Errorf("webhook URL scheme must be https (http allowed only for localhost), got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("webhook URL must have a host")
	}
	if (u.Scheme != "http" || !isLocalHost(u.Hostname())) && isPrivateIPHost(u.Hostname()) {
		return nil, fmt.Errorf("webhook URL host %q is not allowed", u.Hostname())
	}
	if u.User != nil {
		return nil, fmt.Errorf("webhook URL must not contain credentials")
	}
	if log == nil {
		log = slog.Default().With("component", "event")
	}
	return &Dispatcher{
		url: u,
		client: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
		log: log,
	}, nil
}

// Send dispatches an event to the webhook URL.
func (d *Dispatcher) Send(ctx context.Context, e *Event) error {
	if e == nil {
		return fmt.Errorf("event must not be nil")
	}

	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal event (details must be JSON-serializable): %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url.String(), bytes.NewReader(data)) //nolint:gosec // G107: webhook URL validated at dispatcher creation
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req) //nolint:gosec // G704: webhook URL is validated and host-restricted at dispatcher creation
	if err != nil {
		return fmt.Errorf("send webhook: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		bodySnippet := strings.TrimSpace(string(msg))
		if bodySnippet == "" {
			return fmt.Errorf("webhook returned non-2xx status %s", resp.Status)
		}
		return fmt.Errorf("webhook returned non-2xx status %s: %s", resp.Status, bodySnippet)
	}
	_, _ = io.CopyN(io.Discard, resp.Body, maxDrainBytes)

	d.log.Debug("Event dispatched", "type", e.Type, "machine", e.Machine.Name)
	return nil
}

func isLocalHost(host string) bool {
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	return false
}

func isPrivateIPHost(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		// Reject hostnames — only allow resolved IPs to prevent DNS rebinding.
		return true
	}
	return ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLoopback()
}
