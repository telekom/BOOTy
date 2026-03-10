// Package caprf implements the CAPRF provisioning server client.
package caprf

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/telekom/BOOTy/pkg/config"
)

// Client communicates with the CAPRF provisioning server.
type Client struct {
	httpClient *http.Client
	cfg        *config.MachineConfig
}

// New creates a CAPRF client by parsing the vars file at the given path.
func New(varsPath string) (*Client, error) {
	f, err := os.Open(varsPath) //nolint:gosec // trusted path from ISO
	if err != nil {
		return nil, fmt.Errorf("open vars file: %w", err)
	}
	defer f.Close() //nolint:errcheck // best-effort close

	cfg, err := ParseVars(f)
	if err != nil {
		return nil, fmt.Errorf("parse vars: %w", err)
	}

	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		cfg:        cfg,
	}, nil
}

// NewFromConfig creates a CAPRF client from an already-parsed config.
func NewFromConfig(cfg *config.MachineConfig) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		cfg:        cfg,
	}
}

// GetConfig returns the parsed machine configuration.
func (c *Client) GetConfig(_ context.Context) (*config.MachineConfig, error) {
	return c.cfg, nil
}

// ReportStatus sends a provisioning status to the CAPRF server.
func (c *Client) ReportStatus(ctx context.Context, status config.Status, message string) error {
	var url string
	switch status {
	case config.StatusInit:
		url = c.cfg.InitURL
	case config.StatusSuccess:
		url = c.cfg.SuccessURL
	case config.StatusError:
		url = c.cfg.ErrorURL
	default:
		return fmt.Errorf("unknown status: %s", status)
	}

	if url == "" {
		slog.Warn("No URL configured for status, skipping", "status", status)
		return nil
	}

	return c.postWithAuth(ctx, url, message)
}

// ShipLog sends a log line to the CAPRF /log endpoint.
func (c *Client) ShipLog(ctx context.Context, message string) error {
	if c.cfg.LogURL == "" {
		return nil
	}
	return c.postWithAuth(ctx, c.cfg.LogURL, message)
}

// ShipDebug sends a debug message to the CAPRF /debug endpoint.
func (c *Client) ShipDebug(ctx context.Context, message string) error {
	if c.cfg.DebugURL == "" {
		return nil
	}
	return c.postWithAuth(ctx, c.cfg.DebugURL, message)
}

// Heartbeat is a no-op in the current provisioning mode.
// Future agent mode will implement actual heartbeat.
func (c *Client) Heartbeat(_ context.Context) error {
	return nil
}

// FetchCommands is a no-op in the current provisioning mode.
// Future agent mode will implement command fetching.
func (c *Client) FetchCommands(_ context.Context) ([]config.Command, error) {
	return nil, nil
}

func (c *Client) postWithAuth(ctx context.Context, url, body string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url,
		strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}

	resp, err := c.httpClient.Do(req) //nolint:gosec // URL from trusted config
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("POST %s: status %d", url, resp.StatusCode)
	}
	return nil
}

// ParseVars reads a /deploy/vars file and returns a MachineConfig.
// The file format is: export KEY="VALUE" (one per line).
func ParseVars(r io.Reader) (*config.MachineConfig, error) {
	cfg := &config.MachineConfig{}
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Strip "export " prefix.
		line = strings.TrimPrefix(line, "export ")

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		// Unquote value (remove surrounding double quotes).
		value = strings.Trim(value, `"`)

		applyVar(cfg, key, value)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan vars: %w", err)
	}

	return cfg, nil
}

func applyVar(cfg *config.MachineConfig, key, value string) {
	// Simple string fields mapped by key.
	strFields := map[string]*string{
		"HOSTNAME":                    &cfg.Hostname,
		"TOKEN":                       &cfg.Token,
		"MACHINE_EXTRA_KERNEL_PARAMS": &cfg.ExtraKernelParams,
		"FAILURE_DOMAIN":              &cfg.FailureDomain,
		"REGION":                      &cfg.Region,
		"PROVIDER_ID":                 &cfg.ProviderID,
		"MODE":                        &cfg.Mode,
		"LOG_URL":                     &cfg.LogURL,
		"INIT_URL":                    &cfg.InitURL,
		"ERROR_URL":                   &cfg.ErrorURL,
		"SUCCESS_URL":                 &cfg.SuccessURL,
		"DEBUG_URL":                   &cfg.DebugURL,
		"underlay_subnet":             &cfg.UnderlaySubnet,
		"underlay_ip":                 &cfg.UnderlayIP,
		"overlay_subnet":              &cfg.OverlaySubnet,
		"ipmi_subnet":                 &cfg.IPMISubnet,
		"dns_resolver":                &cfg.DNSResolvers,
		"dcgw_ips":                    &cfg.DCGWIPs,
		"overlay_aggregate":           &cfg.OverlayAggregate,
		"vpn_rt":                      &cfg.VPNRT,
	}

	if ptr, ok := strFields[key]; ok {
		*ptr = value
		return
	}

	// Uint32 fields mapped by key.
	uint32Fields := map[string]*uint32{
		"asn_server":    &cfg.ASN,
		"provision_vni": &cfg.ProvisionVNI,
		"leaf_asn":      &cfg.LeafASN,
		"local_asn":     &cfg.LocalASN,
	}

	if ptr, ok := uint32Fields[key]; ok {
		if n, err := strconv.ParseUint(value, 10, 32); err == nil {
			*ptr = uint32(n)
		}
		return
	}

	switch key {
	case "IMAGE":
		cfg.ImageURLs = strings.Fields(value)
	case "MIN_DISK_SIZE_GB":
		if n, err := strconv.Atoi(value); err == nil {
			cfg.MinDiskSizeGB = n
		}
	case "DISABLE_KEXEC":
		cfg.DisableKexec = value == "true" || value == "1" || value == "yes"
	}
}
