// Package caprf implements the CAPRF provisioning server client.
package caprf

import (
	"bufio"
	"context"
	"encoding/json"
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
	log        *slog.Logger
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
		log:        slog.Default().With("component", "caprf"),
	}, nil
}

// NewFromConfig creates a CAPRF client from an already-parsed config.
func NewFromConfig(cfg *config.MachineConfig) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		cfg:        cfg,
		log:        slog.Default().With("component", "caprf"),
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
		c.log.Warn("No URL configured for status, skipping", "status", status)
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

// Heartbeat sends a keepalive to the CAPRF server.
// Returns nil if no heartbeat URL is configured (non-standby mode).
func (c *Client) Heartbeat(ctx context.Context) error {
	if c.cfg.HeartbeatURL == "" {
		return nil
	}
	return c.postWithAuth(ctx, c.cfg.HeartbeatURL, "heartbeat")
}

// FetchCommands polls the CAPRF server for pending commands.
// Returns nil if no commands URL is configured.
func (c *Client) FetchCommands(ctx context.Context) ([]config.Command, error) {
	if c.cfg.CommandsURL == "" {
		return nil, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.CommandsURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create commands request: %w", err)
	}
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}

	resp, err := c.httpClient.Do(req) //nolint:gosec // URL from trusted config
	if err != nil {
		return nil, fmt.Errorf("fetch commands: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close

	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch commands: status %d", resp.StatusCode)
	}

	var cmds []config.Command
	if err := json.NewDecoder(resp.Body).Decode(&cmds); err != nil {
		return nil, fmt.Errorf("decode commands: %w", err)
	}
	return cmds, nil
}

// ReportInventory posts a hardware inventory JSON payload to the CAPRF server.
func (c *Client) ReportInventory(ctx context.Context, data []byte) error {
	if c.cfg.InventoryURL == "" {
		c.log.Warn("No inventory URL configured, skipping inventory report")
		return nil
	}
	return c.postJSONWithAuth(ctx, c.cfg.InventoryURL, data)
}

// postJSONWithAuth posts a JSON payload with Bearer auth and retry logic.
func (c *Client) postJSONWithAuth(ctx context.Context, url string, data []byte) error {
	var lastErr error
	for attempt := range 3 {
		if attempt > 0 {
			backoff := time.Duration(1<<(attempt-1)) * time.Second
			c.log.Info("Retrying request", "url", url, "attempt", attempt+1, "backoff", backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return fmt.Errorf("request retry canceled: %w", ctx.Err())
			}
		}

		lastErr = c.doPostJSON(ctx, url, data)
		if lastErr == nil {
			return nil
		}
		c.log.Warn("Request failed", "url", url, "attempt", attempt+1, "error", lastErr)
	}
	return lastErr
}

// doPostJSON sends a single JSON POST request.
func (c *Client) doPostJSON(ctx context.Context, url string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url,
		strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
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

func (c *Client) postWithAuth(ctx context.Context, url, body string) error {
	var lastErr error
	for attempt := range 3 {
		if attempt > 0 {
			backoff := time.Duration(1<<(attempt-1)) * time.Second // 1s, 2s
			c.log.Info("Retrying request", "url", url, "attempt", attempt+1, "backoff", backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return fmt.Errorf("request retry canceled: %w", ctx.Err())
			}
		}

		lastErr = c.doPost(ctx, url, body)
		if lastErr == nil {
			return nil
		}
		c.log.Warn("Request failed", "url", url, "attempt", attempt+1, "error", lastErr)
	}
	return lastErr
}

func (c *Client) doPost(ctx context.Context, url, body string) error {
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
	if applyStringVar(cfg, key, value) {
		return
	}
	if applyUint32Var(cfg, key, value) {
		return
	}
	applySpecialVar(cfg, key, value)
}

func applyStringVar(cfg *config.MachineConfig, key, value string) bool {
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
		"HEARTBEAT_URL":               &cfg.HeartbeatURL,
		"COMMANDS_URL":                &cfg.CommandsURL,
		"underlay_subnet":             &cfg.UnderlaySubnet,
		"underlay_ip":                 &cfg.UnderlayIP,
		"overlay_subnet":              &cfg.OverlaySubnet,
		"ipmi_subnet":                 &cfg.IPMISubnet,
		"provision_ip":                &cfg.ProvisionIP,
		"dns_resolver":                &cfg.DNSResolvers,
		"dcgw_ips":                    &cfg.DCGWIPs,
		"overlay_aggregate":           &cfg.OverlayAggregate,
		"vpn_rt":                      &cfg.VPNRT,
		"STATIC_IP":                   &cfg.StaticIP,
		"STATIC_GATEWAY":              &cfg.StaticGateway,
		"STATIC_IFACE":                &cfg.StaticIface,
		"BOND_INTERFACES":             &cfg.BondInterfaces,
		"BOND_MODE":                   &cfg.BondMode,
		"VLANS":                       &cfg.VLANs,
		"IMAGE_CHECKSUM":              &cfg.ImageChecksum,
		"IMAGE_CHECKSUM_TYPE":         &cfg.ImageChecksumType,
		"DISK_DEVICE":                 &cfg.DiskDevice,
		"INVENTORY_URL":               &cfg.InventoryURL,
	}

	if ptr, ok := strFields[key]; ok {
		*ptr = value
		return true
	}
	return false
}

func applyUint32Var(cfg *config.MachineConfig, key, value string) bool {
	uint32Fields := map[string]*uint32{
		"asn_server":      &cfg.ASN,
		"provision_vni":   &cfg.ProvisionVNI,
		"leaf_asn":        &cfg.LeafASN,
		"local_asn":       &cfg.LocalASN,
		"vrf_table_id":    &cfg.VRFTableID,
		"bgp_keepalive":   &cfg.BGPKeepalive,
		"bgp_hold":        &cfg.BGPHold,
		"bfd_transmit_ms": &cfg.BFDTransmitMS,
		"bfd_receive_ms":  &cfg.BFDReceiveMS,
	}

	if ptr, ok := uint32Fields[key]; ok {
		if n, err := strconv.ParseUint(value, 10, 32); err == nil {
			*ptr = uint32(n)
		}
		return true
	}
	return false
}

func applySpecialVar(cfg *config.MachineConfig, key, value string) {
	switch key {
	case "IMAGE":
		cfg.ImageURLs = strings.Fields(strings.ReplaceAll(value, ",", " "))
	case "MIN_DISK_SIZE_GB":
		if n, err := strconv.Atoi(value); err == nil {
			cfg.MinDiskSizeGB = n
		}
	case "DISABLE_KEXEC":
		cfg.DisableKexec = value == "true" || value == "1" || value == "yes"
	case "SECURE_ERASE":
		cfg.SecureErase = value == "true" || value == "1" || value == "yes"
	case "POST_PROVISION_CMDS":
		cfg.PostProvisionCmds = strings.Split(value, ";")
	case "NUM_VFS":
		if n, err := strconv.Atoi(value); err == nil {
			cfg.NumVFs = n
		}
	case "INVENTORY_ENABLED":
		cfg.InventoryEnabled = value == "true" || value == "1" || value == "yes"
	}
}
