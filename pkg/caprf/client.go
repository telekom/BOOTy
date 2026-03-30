// Package caprf implements the CAPRF provisioning server client.
package caprf

import (
	"bufio"
	"bytes"
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

	"github.com/telekom/BOOTy/pkg/auth"
	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/health"
)

// Client communicates with the CAPRF provisioning server.
type Client struct {
	httpClient   *http.Client
	cfg          *config.MachineConfig
	log          *slog.Logger
	tokenManager *auth.TokenManager
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

// AcquireToken exchanges the bootstrap token for a JWT if a token URL
// is configured. The acquired JWT replaces the bootstrap token for all
// subsequent API calls. The TokenManager is retained for background renewal.
func (c *Client) AcquireToken(ctx context.Context) error {
	if c.cfg.TokenURL == "" {
		return nil
	}
	if c.cfg.Token == "" {
		return fmt.Errorf("token URL configured but no bootstrap token")
	}
	if strings.TrimSpace(c.cfg.Hostname) == "" {
		return fmt.Errorf("token URL configured but hostname is empty")
	}

	tm, err := auth.NewTokenManager(c.cfg.TokenURL, c.cfg.Token, c.log)
	if err != nil {
		return fmt.Errorf("initialize token manager: %w", err)
	}
	if c.cfg.TokenAlgorithm != "" {
		switch c.cfg.TokenAlgorithm {
		case "RS256", "ES256":
			// Valid algorithms.
		default:
			return fmt.Errorf("unsupported token algorithm %q, must be RS256 or ES256", c.cfg.TokenAlgorithm)
		}
		tm.SetAlgorithm(c.cfg.TokenAlgorithm)
	}
	// bmcMAC is intentionally empty — the token endpoint identifies the
	// machine by serial (hostname). BMC MAC is only required for PXE
	// bootstrap flows that are not yet implemented.
	if err := tm.Acquire(ctx, c.cfg.Hostname, ""); err != nil {
		return fmt.Errorf("acquire jwt: %w", err)
	}
	// Snapshot the initial JWT into cfg.Token for backward compatibility with
	// GetConfig callers. After renewal, CurrentToken() is the authoritative
	// source — cfg.Token will hold the first-acquired JWT, not the latest.
	c.cfg.Token = tm.Token()
	c.tokenManager = tm
	c.log.Info("jwt token acquired, using for subsequent API calls")
	return nil
}

// SetTokenRenewalFatalHandler sets the callback invoked when token renewal
// is permanently exhausted.
func (c *Client) SetTokenRenewalFatalHandler(fn func()) {
	if c.tokenManager == nil {
		return
	}
	c.tokenManager.SetOnFatal(fn)
}

// StartTokenRenewal begins background JWT renewal if a token was acquired.
func (c *Client) StartTokenRenewal(ctx context.Context) error {
	if c.tokenManager == nil {
		return nil
	}
	return c.tokenManager.StartRenewal(ctx)
}

// CurrentToken returns the latest token, preferring the token manager if active.
func (c *Client) CurrentToken() string {
	if c.tokenManager != nil {
		return c.tokenManager.Token()
	}
	return c.cfg.Token
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

// ReportHealthChecks sends health check results to the CAPRF server.
func (c *Client) ReportHealthChecks(ctx context.Context, results []health.CheckResult) error {
	if c.cfg.HealthCheckURL == "" {
		c.log.Warn("No health check URL configured, skipping report")
		return nil
	}

	data, err := json.Marshal(results)
	if err != nil {
		return fmt.Errorf("marshal health results: %w", err)
	}

	return c.postJSONWithAuth(ctx, c.cfg.HealthCheckURL, data)
}

// Heartbeat sends a keepalive to the CAPRF server.
// Returns nil if no heartbeat URL is configured (non-standby mode).
func (c *Client) Heartbeat(ctx context.Context) error {
	if c.cfg.HeartbeatURL == "" {
		return nil
	}
	return c.postWithAuth(ctx, c.cfg.HeartbeatURL, "heartbeat")
}

// ReportFirmware sends a JSON firmware report to the CAPRF server.
func (c *Client) ReportFirmware(ctx context.Context, data []byte) error {
	if c.cfg.FirmwareURL == "" {
		c.log.Debug("No firmware URL configured, skipping report")
		return nil
	}
	return c.postJSONWithAuth(ctx, c.cfg.FirmwareURL, data)
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
	c.setAuth(req)

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
	// Limit response body to 1 MiB to prevent OOM from an oversized response.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&cmds); err != nil {
		return nil, fmt.Errorf("decode commands: %w", err)
	}
	return cmds, nil
}

// AcknowledgeCommand reports command execution result back to the CAPRF server.
func (c *Client) AcknowledgeCommand(ctx context.Context, cmdID, status, message string) error {
	if c.cfg.CommandsURL == "" {
		return nil
	}
	ack := struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Message string `json:"message,omitempty"`
	}{
		ID:      cmdID,
		Status:  status,
		Message: message,
	}
	data, err := json.Marshal(ack)
	if err != nil {
		return fmt.Errorf("marshal command ack: %w", err)
	}
	ackURL := strings.TrimRight(c.cfg.CommandsURL, "/") + "/ack"
	return c.postJSONWithAuth(ctx, ackURL, data)
}

// ReportInventory posts a hardware inventory JSON payload to the CAPRF server.
func (c *Client) ReportInventory(ctx context.Context, data []byte) error {
	if c.cfg.InventoryURL == "" {
		c.log.Warn("No inventory URL configured, skipping inventory report")
		return nil
	}
	return c.postJSONWithAuth(ctx, c.cfg.InventoryURL, data)
}

// ReportMetrics posts provisioning metrics to the CAPRF server.
// Requires TelemetryEnabled. Uses MetricsURL, falling back to TelemetryURL.
func (c *Client) ReportMetrics(ctx context.Context, data []byte) error {
	if !c.cfg.TelemetryEnabled {
		c.log.Debug("telemetry disabled, skipping metrics")
		return nil
	}
	url := c.cfg.MetricsURL
	if url == "" {
		url = c.cfg.TelemetryURL
	}
	if url == "" {
		c.log.Debug("no metrics URL configured, skipping")
		return nil
	}
	return c.postJSONWithAuth(ctx, url, data)
}

// SendEvent posts a single provisioning event to the CAPRF server.
func (c *Client) SendEvent(ctx context.Context, data []byte) error {
	if !c.cfg.TelemetryEnabled || c.cfg.EventURL == "" {
		return nil
	}
	return c.postJSONWithAuth(ctx, c.cfg.EventURL, data)
}

func (c *Client) postWithAuth(ctx context.Context, url, body string) error {
	return c.withRetry(ctx, url, func() error {
		return c.doPost(ctx, url, body)
	})
}

func (c *Client) postJSONWithAuth(ctx context.Context, url string, data []byte) error {
	return c.withRetry(ctx, url, func() error {
		return c.doPostJSON(ctx, url, data)
	})
}

func (c *Client) withRetry(ctx context.Context, url string, fn func() error) error {
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

		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		c.log.Warn("Request failed", "url", url, "attempt", attempt+1, "error", lastErr)
	}
	return fmt.Errorf("request failed after 3 attempts to %s: %w", url, lastErr)
}

// setAuth attaches the Bearer token only when the request URL uses HTTPS
// or targets loopback (localhost/127.x). Bare-metal PXE environments may
// legitimately use HTTP on isolated networks, but sending credentials over
// plaintext to remote hosts is logged as a warning.
func (c *Client) setAuth(req *http.Request) {
	tok := c.CurrentToken()
	if tok == "" {
		return
	}
	if req.URL != nil && req.URL.Scheme != "https" && !isLoopback(req.URL.Hostname()) {
		c.log.Warn("skipping bearer token on non-HTTPS request", "url", req.URL.Redacted())
		return
	}
	req.Header.Set("Authorization", "Bearer "+tok)
}

// isLoopback returns true for localhost, 127.x.x.x, and [::1].
func isLoopback(host string) bool {
	return host == "localhost" || strings.HasPrefix(host, "127.") || host == "::1"
}

func (c *Client) doPost(ctx context.Context, url, body string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url,
		strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	c.setAuth(req)

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

func (c *Client) doPostJSON(ctx context.Context, url string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url,
		bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

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

		// Unquote value: strip surrounding double quotes or single quotes.
		// Single quotes are common for JSON values in shell-style var files.
		if len(value) >= 2 {
			switch {
			case value[0] == '"' && value[len(value)-1] == '"':
				value = value[1 : len(value)-1]
			case value[0] == '\'' && value[len(value)-1] == '\'':
				value = value[1 : len(value)-1]
			}
		}

		if err := applyVar(cfg, key, value); err != nil {
			return nil, fmt.Errorf("parse var %s: %w", key, err)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan vars: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func applyVar(cfg *config.MachineConfig, key, value string) error {
	if applyStringVar(cfg, key, value) {
		return nil
	}
	if handled, err := applyUint32Var(cfg, key, value); handled {
		return err
	}
	return applySpecialVar(cfg, key, value)
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
		"provision_gateway":           &cfg.ProvisionGateway,
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
		"NETWORK_MODE":                &cfg.NetworkMode,
		"RESCUE_MODE":                 &cfg.RescueMode,
		"RESCUE_SSH_PUBKEY":           &cfg.RescueSSHPubKey,
		"RESCUE_PASSWORD_HASH":        &cfg.RescuePasswordHash,
		"CLOUDINIT_DATASOURCE":        &cfg.CloudInitDatasource,
		"BGP_PEER_MODE":               &cfg.BGPPeerMode,
		"BGP_NEIGHBORS":               &cfg.BGPNeighbors,
		"IMAGE_CHECKSUM":              &cfg.ImageChecksum,
		"IMAGE_CHECKSUM_TYPE":         &cfg.ImageChecksumType,
		"IMAGE_MODE":                  &cfg.ImageMode,
		"DISK_DEVICE":                 &cfg.DiskDevice,
		"INVENTORY_URL":               &cfg.InventoryURL,
		"FIRMWARE_URL":                &cfg.FirmwareURL,
		"FIRMWARE_MIN_BIOS":           &cfg.FirmwareMinBIOS,
		"FIRMWARE_MIN_BMC":            &cfg.FirmwareMinBMC,
		"HEALTH_SKIP_CHECKS":          &cfg.HealthSkipChecks,
		"HEALTH_CHECK_URL":            &cfg.HealthCheckURL,
		"IMAGE_SIGNATURE_URL":         &cfg.ImageSignatureURL,
		"IMAGE_GPG_PUBKEY":            &cfg.ImageGPGPubKey,
		"TELEMETRY_URL":               &cfg.TelemetryURL,
		"METRICS_URL":                 &cfg.MetricsURL,
		"EVENT_URL":                   &cfg.EventURL,
		"MOK_CERT_PATH":               &cfg.MOKCertPath,
		"MOK_PASSWORD":                &cfg.MOKPassword,
		"TOKEN_URL":                   &cfg.TokenURL,
		"TOKEN_ALGORITHM":             &cfg.TokenAlgorithm,
		"NVME_NAMESPACES":             &cfg.NVMeNamespaces,
	}

	if ptr, ok := strFields[key]; ok {
		*ptr = value
		return true
	}
	return false
}

func applyUint32Var(cfg *config.MachineConfig, key, value string) (bool, error) {
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
		"bgp_remote_asn":  &cfg.BGPRemoteASN,
	}

	if ptr, ok := uint32Fields[key]; ok {
		n, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return true, fmt.Errorf("invalid uint32 value for %s=%q: %w", key, value, err)
		}
		*ptr = uint32(n)
		return true, nil
	}
	return false, nil
}

func applySpecialVar(cfg *config.MachineConfig, key, value string) error {
	if handled, err := applyBoolIntVar(cfg, key, value); handled {
		return err
	}

	switch key {
	case "IMAGE":
		cfg.ImageURLs = strings.Fields(strings.ReplaceAll(value, ",", " "))
	case "POST_PROVISION_CMDS":
		cfg.PostProvisionCmds = strings.Split(value, ";")
	case "PARTITION_LAYOUT":
		layout, err := config.ParsePartitionLayout(value)
		if err != nil {
			return fmt.Errorf("invalid PARTITION_LAYOUT: %w", err)
		}
		cfg.PartitionLayout = layout
	}

	return nil
}

// applyBoolIntVar handles boolean and integer special vars.
func applyBoolIntVar(cfg *config.MachineConfig, key, value string) (bool, error) {
	if handled, err := applyIntVar(cfg, key, value); handled {
		return true, err
	}

	switch key {
	case "DISABLE_KEXEC":
		cfg.DisableKexec = parseBoolVar(value)
	case "SECURE_ERASE":
		cfg.SecureErase = parseBoolVar(value)
	case "INVENTORY_ENABLED":
		cfg.InventoryEnabled = parseBoolVar(value)
	case "FIRMWARE_REPORT":
		cfg.FirmwareEnabled = parseBoolVar(value)
	case "HEALTH_CHECKS_ENABLED":
		cfg.HealthChecksEnabled = parseBoolVar(value)
	case "CLOUDINIT_ENABLED":
		cfg.CloudInitEnabled = parseBoolVar(value)
	case "DRY_RUN":
		cfg.DryRun = parseBoolVar(value)
	default:
		return applyFeatureToggle(cfg, key, value)
	}
	return true, nil
}

// applyIntVar handles integer special vars.
func applyIntVar(cfg *config.MachineConfig, key, value string) (bool, error) {
	intFields := map[string]*int{
		"MIN_DISK_SIZE_GB":     &cfg.MinDiskSizeGB,
		"NUM_VFS":              &cfg.NumVFs,
		"HEALTH_MIN_MEMORY_GB": &cfg.HealthMinMemoryGB,
		"HEALTH_MIN_CPUS":      &cfg.HealthMinCPUs,
	}

	if ptr, ok := intFields[key]; ok {
		if err := setIntField(ptr, value); err != nil {
			return true, fmt.Errorf("invalid %s: %w", key, err)
		}
		return true, nil
	}
	return false, nil
}

// applyFeatureToggle handles feature-specific boolean/int vars.
func applyFeatureToggle(cfg *config.MachineConfig, key, value string) (bool, error) {
	switch key {
	case "TELEMETRY_ENABLED":
		cfg.TelemetryEnabled = parseBoolVar(value)
	case "SECUREBOOT_REENABLE":
		cfg.SecureBootReEnable = parseBoolVar(value)
	case "RESCUE_TIMEOUT":
		if err := setIntField(&cfg.RescueTimeout, value); err != nil {
			return true, fmt.Errorf("invalid %s: %w", key, err)
		}
	case "RESCUE_AUTO_MOUNT":
		cfg.RescueAutoMountDisks = parseBoolVar(value)
	case "EVPN_L2_ENABLED":
		cfg.EVPNL2Enabled = parseBoolVar(value)
	case "BGP_UNDERLAY_AF":
		cfg.BGPUnderlayAF = value
	case "BGP_OVERLAY_TYPE":
		cfg.BGPOverlayType = value
	default:
		return false, nil
	}
	return true, nil
}

// parseBoolVar interprets common truthy string values (case-insensitive).
func parseBoolVar(value string) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	return v == "true" || v == "1" || v == "yes"
}

// setIntField sets an int field from a string value.
func setIntField(field *int, value string) error {
	n, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("invalid integer value %q: %w", value, err)
	}
	*field = n
	return nil
}
