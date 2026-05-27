// Package caprf implements the CAPRF provisioning server client.
package caprf

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/telekom/BOOTy/pkg/auth"
	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/crash"
	"github.com/telekom/BOOTy/pkg/health"
)

// Client communicates with the CAPRF provisioning server.
type Client struct {
	httpClient   *http.Client
	cfg          *config.MachineConfig
	log          *slog.Logger
	tokenManager *auth.TokenManager
	insecureWarn sync.Map
}

var errInsecureTransport = errors.New("insecure transport")

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

	c := &Client{
		httpClient: newHTTPClient(30 * time.Second),
		cfg:        cfg,
		log:        slog.Default().With("component", "caprf"),
	}
	if cfg.Transport.Insecure {
		c.log.Warn("insecure transport enabled: bearer tokens will be sent over plain HTTP")
	}
	return c, nil
}

// NewFromConfig creates a CAPRF client from an already-parsed config.
func NewFromConfig(cfg *config.MachineConfig) *Client {
	c := &Client{
		httpClient: newHTTPClient(30 * time.Second),
		cfg:        cfg,
		log:        slog.Default().With("component", "caprf"),
	}
	if cfg.Transport.Insecure {
		c.log.Warn("insecure transport enabled: bearer tokens will be sent over plain HTTP")
	}
	return c
}

// AcquireToken exchanges the bootstrap token for a JWT if a token URL
// is configured. The acquired JWT replaces the bootstrap token for all
// subsequent API calls. The TokenManager is retained for background renewal.
func (c *Client) AcquireToken(ctx context.Context) error {
	if c.cfg.Transport.TokenURL == "" {
		return nil
	}
	if c.cfg.Transport.Token == "" {
		return fmt.Errorf("token URL configured but no bootstrap token")
	}
	if strings.TrimSpace(c.cfg.Hostname) == "" {
		return fmt.Errorf("token URL configured but hostname is empty")
	}

	tm, err := auth.NewTokenManager(c.cfg.Transport.TokenURL, c.cfg.Transport.Token, c.log)
	if err != nil {
		return fmt.Errorf("initialize token manager: %w", err)
	}
	if c.cfg.Transport.TokenAlgorithm != "" {
		switch c.cfg.Transport.TokenAlgorithm {
		case "RS256", "ES256":
			// Valid algorithms.
		default:
			return fmt.Errorf("unsupported token algorithm %q, must be RS256 or ES256", c.cfg.Transport.TokenAlgorithm)
		}
		tm.SetAlgorithm(c.cfg.Transport.TokenAlgorithm)
	}
	// bmcMAC is intentionally empty — the token endpoint identifies the
	// machine by serial (hostname). BMC MAC is only required for PXE
	// bootstrap flows that are not yet implemented.
	if err := tm.Acquire(ctx, c.cfg.Hostname, ""); err != nil {
		return fmt.Errorf("acquire jwt: %w", err)
	}
	// Snapshot the initial JWT into cfg.Transport.Token for backward compatibility with
	// GetConfig callers. After renewal, CurrentToken() is the authoritative
	// source — cfg.Transport.Token will hold the first-acquired JWT, not the latest.
	c.cfg.Transport.Token = tm.Token()
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
	return c.cfg.Transport.Token
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
		url = c.cfg.Transport.InitURL
	case config.StatusSuccess:
		url = c.cfg.Transport.SuccessURL
	case config.StatusError:
		url = c.cfg.Transport.ErrorURL
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
	if c.cfg.Transport.LogURL == "" {
		return nil
	}
	return c.postWithAuth(ctx, c.cfg.Transport.LogURL, message)
}

// ShipDebug sends a debug message to the CAPRF /debug endpoint.
func (c *Client) ShipDebug(ctx context.Context, message string) error {
	if c.cfg.Transport.DebugURL == "" {
		return nil
	}
	return c.postWithAuth(ctx, c.cfg.Transport.DebugURL, message)
}

// ReportHealthChecks sends health check results to the CAPRF server.
func (c *Client) ReportHealthChecks(ctx context.Context, results []health.CheckResult) error {
	if c.cfg.Health.ReportURL == "" {
		c.log.Warn("No health check URL configured, skipping report")
		return nil
	}

	data, err := json.Marshal(results)
	if err != nil {
		return fmt.Errorf("marshal health results: %w", err)
	}

	return c.postJSONWithAuth(ctx, c.cfg.Health.ReportURL, data)
}

// Heartbeat sends a keepalive to the CAPRF server.
// Returns nil if no heartbeat URL is configured (non-standby mode).
func (c *Client) Heartbeat(ctx context.Context) error {
	if c.cfg.Agent.HeartbeatURL == "" {
		return nil
	}
	return c.postWithAuth(ctx, c.cfg.Agent.HeartbeatURL, "heartbeat")
}

// ReportFirmware sends a JSON firmware report to the CAPRF server.
func (c *Client) ReportFirmware(ctx context.Context, data []byte) error {
	if c.cfg.Provision.Firmware.URL == "" {
		c.log.Debug("No firmware URL configured, skipping report")
		return nil
	}
	return c.postJSONWithAuth(ctx, c.cfg.Provision.Firmware.URL, data)
}

// FetchCommands polls the CAPRF server for pending commands.
// Returns nil if no commands URL is configured.
func (c *Client) FetchCommands(ctx context.Context) ([]config.Command, error) {
	if c.cfg.Agent.CommandsURL == "" {
		return nil, nil
	}
	if err := c.requireSecureEndpoint(c.cfg.Agent.CommandsURL, "commands polling"); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.Agent.CommandsURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create commands request: %w", err)
	}
	if err := c.setAuth(req); err != nil {
		return nil, err
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
	// Limit response body to 1 MiB to prevent OOM from an oversized response.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&cmds); err != nil {
		return nil, fmt.Errorf("decode commands: %w", err)
	}
	return cmds, nil
}

// AcknowledgeCommand reports command execution result back to the CAPRF server.
func (c *Client) AcknowledgeCommand(ctx context.Context, cmdID, status, message string) error {
	if c.cfg.Agent.CommandsURL == "" {
		return nil
	}
	if err := c.requireSecureEndpoint(c.cfg.Agent.CommandsURL, "commands acknowledgement"); err != nil {
		return err
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
	ackURL, err := neturl.JoinPath(c.cfg.Agent.CommandsURL, "ack")
	if err != nil {
		return fmt.Errorf("build command ack URL: %w", err)
	}
	return c.postJSONWithAuth(ctx, ackURL, data)
}

// ReportInventory posts a hardware inventory JSON payload to the CAPRF server.
func (c *Client) ReportInventory(ctx context.Context, data []byte) error {
	if c.cfg.Provision.Inventory.URL == "" {
		c.log.Warn("No inventory URL configured, skipping inventory report")
		return nil
	}
	return c.postJSONWithAuth(ctx, c.cfg.Provision.Inventory.URL, data)
}

// ReportMetrics posts provisioning metrics to the CAPRF server.
// Requires TelemetryEnabled. Uses MetricsURL, falling back to TelemetryURL.
func (c *Client) ReportMetrics(ctx context.Context, data []byte) error {
	if !c.cfg.Telemetry.Enabled {
		c.log.Debug("telemetry disabled, skipping metrics")
		return nil
	}
	url := c.cfg.Telemetry.MetricsURL
	if url == "" {
		url = c.cfg.Telemetry.URL
	}
	if url == "" {
		c.log.Debug("no metrics URL configured, skipping")
		return nil
	}
	return c.postJSONWithAuth(ctx, url, data)
}

// SendEvent posts a single provisioning event to the CAPRF server.
func (c *Client) SendEvent(ctx context.Context, data []byte) error {
	if !c.cfg.Telemetry.Enabled || c.cfg.Telemetry.EventURL == "" {
		return nil
	}
	return c.postJSONWithAuth(ctx, c.cfg.Telemetry.EventURL, data)
}

// ReportCrashArtifacts uploads a crash artifact archive using CAPRF-provided instructions.
func (c *Client) ReportCrashArtifacts(ctx context.Context, req *crash.PrepareRequest, archivePath string) error {
	if c.cfg.Provision.CrashArtifacts.PrepareURL == "" && c.cfg.Provision.CrashArtifacts.UploadURL == "" {
		c.log.Debug("no crash artifact URL configured, skipping")
		return crash.ErrNoUploadURL
	}
	if req == nil {
		return fmt.Errorf("crash artifact prepare request is nil")
	}
	if archivePath == "" {
		return fmt.Errorf("crash artifact archive path is empty")
	}
	ctx, cancel := context.WithTimeout(ctx, c.crashUploadTimeout())
	defer cancel()

	if c.cfg.Provision.CrashArtifacts.PrepareURL != "" {
		instructions, err := c.prepareCrashArtifactUpload(ctx, req)
		if err != nil {
			return err
		}
		return c.uploadCrashArchive(ctx, instructions, archivePath)
	}

	return c.uploadCrashProxyMultipart(ctx, c.cfg.Provision.CrashArtifacts.UploadURL, req, archivePath)
}

func (c *Client) crashUploadTimeout() time.Duration {
	seconds := c.cfg.Provision.CrashArtifacts.UploadTimeoutSec
	if seconds <= 0 {
		seconds = config.DefaultCrashArtifactsUploadTimeoutSec
	}
	return time.Duration(seconds) * time.Second
}

func (c *Client) prepareCrashArtifactUpload(ctx context.Context, req *crash.PrepareRequest) (*crash.PrepareResponse, error) {
	if err := c.requireSecureEndpoint(c.cfg.Provision.CrashArtifacts.PrepareURL, "crash artifact prepare"); err != nil {
		return nil, err
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal crash artifact prepare request: %w", err)
	}
	redacted := redactedURL(c.cfg.Provision.CrashArtifacts.PrepareURL)
	var lastErr error
	for attempt := range 3 {
		if attempt > 0 {
			if !sleepForRetry(ctx, attempt, c.log, redacted) {
				return nil, fmt.Errorf("crash artifact prepare retry canceled: %w", ctx.Err())
			}
		}
		resp, retry, err := c.doPrepareCrashArtifactUpload(ctx, data)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !retry || errors.Is(err, errInsecureTransport) {
			return nil, err
		}
		c.log.Warn("crash artifact prepare failed", "url", redacted, "attempt", attempt+1, "error", err)
	}
	return nil, fmt.Errorf("crash artifact prepare failed after 3 attempts to %s: %w", redacted, lastErr)
}

func (c *Client) doPrepareCrashArtifactUpload(ctx context.Context, data []byte) (*crash.PrepareResponse, bool, error) {
	rawURL := c.cfg.Provision.CrashArtifacts.PrepareURL
	redacted := redactedURL(rawURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(data))
	if err != nil {
		return nil, false, fmt.Errorf("create crash artifact prepare request %s: %w", redacted, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if err := c.setAuth(req); err != nil {
		return nil, false, err
	}
	resp, err := c.httpClient.Do(req) //nolint:gosec // URL from trusted config
	if err != nil {
		return nil, true, fmt.Errorf("post %s: %w", redacted, err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, false, fmt.Errorf("post %s: status %d", redacted, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, true, fmt.Errorf("post %s: status %d", redacted, resp.StatusCode)
	}
	var instructions crash.PrepareResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&instructions); err != nil {
		return nil, false, fmt.Errorf("decode crash artifact prepare response: %w", err)
	}
	return &instructions, false, nil
}

func sleepForRetry(ctx context.Context, attempt int, log *slog.Logger, redacted string) bool {
	backoff := time.Duration(1<<(attempt-1)) * time.Second
	log.Info("retrying crash artifact request", "url", redacted, "attempt", attempt+1, "backoff", backoff)
	select {
	case <-time.After(backoff):
		return true
	case <-ctx.Done():
		return false
	}
}

func (c *Client) uploadCrashArchive(ctx context.Context, instructions *crash.PrepareResponse, archivePath string) error {
	if instructions == nil {
		return fmt.Errorf("crash artifact upload instructions are nil")
	}
	if instructions.UploadURL == "" {
		return fmt.Errorf("crash artifact upload URL is empty")
	}
	if err := c.requireSecureEndpoint(instructions.UploadURL, "crash artifact upload"); err != nil {
		return err
	}
	if instructions.MaxBytes > 0 {
		info, err := os.Stat(archivePath)
		if err != nil {
			return fmt.Errorf("stat crash artifact archive: %w", err)
		}
		if info.Size() > instructions.MaxBytes {
			return fmt.Errorf("crash artifact archive exceeds upload limit: %d > %d", info.Size(), instructions.MaxBytes)
		}
	}
	mode := instructions.UploadMode
	if mode == "" {
		mode = crash.UploadModePresignedPUT
		if len(instructions.FormFields) > 0 {
			mode = crash.UploadModePresignedPOST
		}
	}
	switch mode {
	case crash.UploadModePresignedPUT, crash.UploadModeCAPRFProxy:
		return c.uploadCrashRaw(ctx, instructions, archivePath)
	case crash.UploadModePresignedPOST:
		return c.uploadCrashPresignedForm(ctx, instructions, archivePath)
	default:
		return fmt.Errorf("unsupported crash artifact upload mode %q", mode)
	}
}

func (c *Client) uploadCrashRaw(ctx context.Context, instructions *crash.PrepareResponse, archivePath string) error {
	method := instructions.Method
	if method == "" {
		method = http.MethodPut
	}
	redacted := redactedURL(instructions.UploadURL)
	return c.uploadCrashWithRetry(ctx, redacted, func() error {
		body, err := os.Open(archivePath) //nolint:gosec // archive path created by BOOTy
		if err != nil {
			return fmt.Errorf("open crash artifact archive: %w", err)
		}
		defer body.Close() //nolint:errcheck // best-effort close
		req, err := http.NewRequestWithContext(ctx, method, instructions.UploadURL, body)
		if err != nil {
			return fmt.Errorf("create crash artifact upload request %s: %w", redacted, err)
		}
		req.Header.Set("Content-Type", "application/gzip")
		for key, value := range instructions.Headers {
			req.Header.Set(key, value)
		}
		if err := c.applyCrashUploadAuth(req, instructions); err != nil {
			return err
		}
		return c.doCrashUploadRequest(req, redacted)
	})
}

func (c *Client) uploadCrashPresignedForm(ctx context.Context, instructions *crash.PrepareResponse, archivePath string) error {
	redacted := redactedURL(instructions.UploadURL)
	return c.uploadCrashWithRetry(ctx, redacted, func() error {
		reader, contentType := multipartArchiveReader(ctx, archivePath, instructions.FormFields, "file")
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, instructions.UploadURL, reader)
		if err != nil {
			return fmt.Errorf("create crash artifact form upload request %s: %w", redacted, err)
		}
		req.Header.Set("Content-Type", contentType)
		for key, value := range instructions.Headers {
			req.Header.Set(key, value)
		}
		if err := c.applyCrashUploadAuth(req, instructions); err != nil {
			return err
		}
		return c.doCrashUploadRequest(req, redacted)
	})
}

func (c *Client) uploadCrashProxyMultipart(ctx context.Context, rawURL string, reqPayload *crash.PrepareRequest, archivePath string) error {
	if err := c.requireSecureEndpoint(rawURL, "crash artifact upload"); err != nil {
		return err
	}
	redacted := redactedURL(rawURL)
	manifest, err := json.Marshal(reqPayload)
	if err != nil {
		return fmt.Errorf("marshal crash artifact manifest: %w", err)
	}
	instructions := &crash.PrepareResponse{UploadURL: rawURL, AuthMode: crash.AuthModeBearer, UploadMode: crash.UploadModeCAPRFProxy}
	return c.uploadCrashWithRetry(ctx, redacted, func() error {
		reader, contentType := multipartArchiveReader(ctx, archivePath, map[string]string{"manifest": string(manifest)}, "archive")
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, reader)
		if err != nil {
			return fmt.Errorf("create crash artifact proxy upload request %s: %w", redacted, err)
		}
		req.Header.Set("Content-Type", contentType)
		if err := c.applyCrashUploadAuth(req, instructions); err != nil {
			return err
		}
		return c.doCrashUploadRequest(req, redacted)
	})
}

func multipartArchiveReader(ctx context.Context, archivePath string, fields map[string]string, fileField string) (body io.ReadCloser, contentType string) {
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)
	contentType = writer.FormDataContentType()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer pw.Close()     //nolint:errcheck // best-effort close
		defer writer.Close() //nolint:errcheck // CloseWithError reports failures
		for key, value := range fields {
			if err := writer.WriteField(key, value); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
		}
		file, err := os.Open(archivePath) //nolint:gosec // archive path created by BOOTy
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		defer file.Close() //nolint:errcheck // best-effort close
		part, err := writer.CreateFormFile(fileField, filepath.Base(archivePath))
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, file); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = pr.CloseWithError(ctx.Err())
		case <-done:
		}
	}()
	return pr, contentType
}

func (c *Client) applyCrashUploadAuth(req *http.Request, instructions *crash.PrepareResponse) error {
	authMode := instructions.AuthMode
	if authMode == "" {
		if instructions.UploadMode == crash.UploadModeCAPRFProxy {
			authMode = crash.AuthModeBearer
		} else {
			authMode = crash.AuthModeNone
		}
	}
	switch authMode {
	case crash.AuthModeNone:
		return nil
	case crash.AuthModeBearer:
		return c.setAuth(req)
	default:
		return fmt.Errorf("unsupported crash artifact auth mode %q", authMode)
	}
}

func (c *Client) uploadCrashWithRetry(ctx context.Context, redacted string, fn func() error) error {
	var lastErr error
	for attempt := range 3 {
		if attempt > 0 {
			if !sleepForRetry(ctx, attempt, c.log, redacted) {
				return fmt.Errorf("crash artifact upload retry canceled: %w", ctx.Err())
			}
		}
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if errors.Is(lastErr, errInsecureTransport) {
			return lastErr
		}
		c.log.Warn("crash artifact upload failed", "url", redacted, "attempt", attempt+1, "error", lastErr)
	}
	return fmt.Errorf("crash artifact upload failed after 3 attempts to %s: %w", redacted, lastErr)
}

func (c *Client) doCrashUploadRequest(req *http.Request, redacted string) error {
	if req.Body != nil {
		defer req.Body.Close() //nolint:errcheck // close multipart pipe on early transport failures
	}
	client := newHTTPClient(c.crashUploadTimeout())
	resp, err := client.Do(req) //nolint:gosec // URL from trusted config or CAPRF response
	if err != nil {
		return fmt.Errorf("upload crash artifact to %s: %w", redacted, err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upload crash artifact to %s: status %d", redacted, resp.StatusCode)
	}
	return nil
}

func redactedURL(raw string) string {
	u, err := neturl.Parse(raw)
	if err != nil {
		return "<invalid-url>"
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil
	return u.String()
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
		if errors.Is(lastErr, errInsecureTransport) {
			return lastErr
		}
		c.log.Warn("Request failed", "url", url, "attempt", attempt+1, "error", lastErr)
	}
	return fmt.Errorf("request failed after 3 attempts to %s: %w", url, lastErr)
}

// setAuth attaches the Bearer token only when the request URL uses HTTPS
// or targets loopback (localhost/127.x). Non-HTTPS remote endpoints fail
// closed to avoid credential leakage over plaintext transport.
// When InsecureTransport is set, the HTTPS requirement is bypassed.
func (c *Client) setAuth(req *http.Request) error {
	tok := c.CurrentToken()
	if tok == "" {
		return nil
	}
	if req.URL != nil && req.URL.Scheme != "https" && !isLoopback(req.URL.Hostname()) && !c.cfg.Transport.Insecure {
		c.warnInsecureOnce(req.URL.Redacted())
		return fmt.Errorf("%w: refusing bearer token on non-HTTPS request %s", errInsecureTransport, req.URL.Redacted())
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

// isLoopback returns true for localhost, 127.x.x.x, and [::1].
func isLoopback(host string) bool {
	return host == "localhost" || strings.HasPrefix(host, "127.") || host == "::1"
}

func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
}

func (c *Client) warnInsecureOnce(rawURL string) {
	if _, loaded := c.insecureWarn.LoadOrStore(rawURL, struct{}{}); loaded {
		return
	}
	c.log.Warn("refusing request to non-HTTPS endpoint", "url", rawURL)
}

func (c *Client) requireSecureEndpoint(rawURL, purpose string) error {
	if c.cfg.Transport.Insecure {
		return nil
	}
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse %s URL: %w", purpose, err)
	}
	if u.Scheme == "https" || isLoopback(u.Hostname()) {
		return nil
	}
	redacted := u.Redacted()
	c.warnInsecureOnce(redacted)
	return fmt.Errorf("%w: %s requires HTTPS, got %s", errInsecureTransport, purpose, redacted)
}

func (c *Client) doPost(ctx context.Context, url, body string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url,
		strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	if err := c.setAuth(req); err != nil {
		return err
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

func (c *Client) doPostJSON(ctx context.Context, url string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url,
		bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if err := c.setAuth(req); err != nil {
		return err
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
		"TOKEN":                       &cfg.Transport.Token,
		"MACHINE_EXTRA_KERNEL_PARAMS": &cfg.Provision.ExtraKernelParams,
		"FAILURE_DOMAIN":              &cfg.Provision.FailureDomain,
		"REGION":                      &cfg.Provision.Region,
		"PROVIDER_ID":                 &cfg.Provision.ProviderID,
		"MODE":                        &cfg.Mode,
		"LOG_URL":                     &cfg.Transport.LogURL,
		"INIT_URL":                    &cfg.Transport.InitURL,
		"ERROR_URL":                   &cfg.Transport.ErrorURL,
		"SUCCESS_URL":                 &cfg.Transport.SuccessURL,
		"DEBUG_URL":                   &cfg.Transport.DebugURL,
		"HEARTBEAT_URL":               &cfg.Agent.HeartbeatURL,
		"COMMANDS_URL":                &cfg.Agent.CommandsURL,
		"underlay_subnet":             &cfg.Network.EVPN.UnderlaySubnet,
		"underlay_ip":                 &cfg.Network.EVPN.UnderlayIP,
		"overlay_subnet":              &cfg.Network.EVPN.OverlaySubnet,
		"ipmi_subnet":                 &cfg.Network.IPMI.Subnet,
		"provision_ip":                &cfg.Network.EVPN.ProvisionIP,
		"provision_gateway":           &cfg.Network.EVPN.ProvisionGateway,
		"dns_resolver":                &cfg.Network.DNSResolvers,
		"dcgw_ips":                    &cfg.Network.EVPN.DCGWIPs,
		"overlay_aggregate":           &cfg.Network.EVPN.OverlayAggregate,
		"vpn_rt":                      &cfg.Network.EVPN.VPNRT,
		"STATIC_IP":                   &cfg.Network.Static.IP,
		"STATIC_GATEWAY":              &cfg.Network.Static.Gateway,
		"STATIC_IFACE":                &cfg.Network.Static.Iface,
		"BOND_INTERFACES":             &cfg.Network.Bond.Interfaces,
		"BOND_MODE":                   &cfg.Network.Bond.Mode,
		"VLANS":                       &cfg.Network.VLAN.Config,
		"NETWORK_MODE":                &cfg.Network.Mode,
		"RESCUE_MODE":                 &cfg.Rescue.Mode,
		"RESCUE_SSH_PUBKEY":           &cfg.Rescue.SSHPubKey,
		"RESCUE_PASSWORD_HASH":        &cfg.Rescue.PasswordHash,
		"CLOUDINIT_DATASOURCE":        &cfg.Provision.CloudInit.Datasource,
		"vrf_name":                    &cfg.Network.VRF.Name,
		"BGP_PEER_MODE":               &cfg.Network.BGP.PeerMode,
		"BGP_NEIGHBORS":               &cfg.Network.BGP.Neighbors,
		"IMAGE_CHECKSUM":              &cfg.Provision.Image.Checksum,
		"IMAGE_CHECKSUM_TYPE":         &cfg.Provision.Image.ChecksumType,
		"IMAGE_MODE":                  &cfg.Provision.Image.Mode,
		"DISK_DEVICE":                 &cfg.Provision.Disk.Device,
		"INVENTORY_URL":               &cfg.Provision.Inventory.URL,
		"FIRMWARE_URL":                &cfg.Provision.Firmware.URL,
		"FIRMWARE_MIN_BIOS":           &cfg.Provision.Firmware.MinBIOS,
		"FIRMWARE_MIN_BMC":            &cfg.Provision.Firmware.MinBMC,
		"HEALTH_SKIP_CHECKS":          &cfg.Health.SkipChecks,
		"HEALTH_CHECK_URL":            &cfg.Health.ReportURL,
		"IMAGE_SIGNATURE_URL":         &cfg.Provision.Image.SignatureURL,
		"IMAGE_GPG_PUBKEY":            &cfg.Provision.Image.GPGPubKey,
		"TELEMETRY_URL":               &cfg.Telemetry.URL,
		"METRICS_URL":                 &cfg.Telemetry.MetricsURL,
		"EVENT_URL":                   &cfg.Telemetry.EventURL,
		"CRASH_ARTIFACTS_PREPARE_URL": &cfg.Provision.CrashArtifacts.PrepareURL,
		"CRASH_ARTIFACTS_UPLOAD_URL":  &cfg.Provision.CrashArtifacts.UploadURL,
		"MOK_CERT_PATH":               &cfg.Provision.SecureBoot.MOKCertPath,
		"MOK_PASSWORD":                &cfg.Provision.SecureBoot.MOKPassword,
		"TOKEN_URL":                   &cfg.Transport.TokenURL,
		"TOKEN_ALGORITHM":             &cfg.Transport.TokenAlgorithm,
		"NVME_NAMESPACES":             &cfg.Provision.Disk.NVMeNamespaces,
		"BGP_AUTH_PASSWORD":           &cfg.Network.BGP.AuthPassword,
	}

	if ptr, ok := strFields[key]; ok {
		*ptr = value
		return true
	}
	return false
}

func applyUint32Var(cfg *config.MachineConfig, key, value string) (bool, error) {
	uint32Fields := map[string]*uint32{
		"asn_server":      &cfg.Network.BGP.ASN,
		"provision_vni":   &cfg.Network.EVPN.ProvisionVNI,
		"leaf_asn":        &cfg.Network.EVPN.LeafASN,
		"local_asn":       &cfg.Network.EVPN.LocalASN,
		"vrf_table_id":    &cfg.Network.VRF.TableID,
		"bgp_keepalive":   &cfg.Network.BGP.Keepalive,
		"bgp_hold":        &cfg.Network.BGP.Hold,
		"bfd_transmit_ms": &cfg.Network.BGP.BFDTransmitMS,
		"bfd_receive_ms":  &cfg.Network.BGP.BFDReceiveMS,
		"bgp_remote_asn":  &cfg.Network.BGP.RemoteASN,
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
		cfg.Provision.Image.URLs = strings.Fields(strings.ReplaceAll(value, ",", " "))
	case "POST_PROVISION_CMDS":
		cfg.Provision.PostProvisionCmds = strings.Split(value, ";")
	case "PARTITION_LAYOUT":
		layout, err := config.ParsePartitionLayout(value)
		if err != nil {
			return fmt.Errorf("invalid PARTITION_LAYOUT: %w", err)
		}
		cfg.Provision.Disk.PartitionLayout = layout
	default:
		if strings.HasPrefix(key, "LUKS_") {
			return fmt.Errorf("%s is not supported yet", key)
		}
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
		cfg.Provision.DisableKexec = parseBoolVar(value)
	case "SECURE_ERASE":
		cfg.Provision.Disk.SecureErase = parseBoolVar(value)
	case "INVENTORY_ENABLED":
		cfg.Provision.Inventory.Enabled = parseBoolVar(value)
	case "FIRMWARE_REPORT":
		cfg.Provision.Firmware.Enabled = parseBoolVar(value)
	case "HEALTH_CHECKS_ENABLED":
		cfg.Health.Enabled = parseBoolVar(value)
	case "CLOUDINIT_ENABLED":
		cfg.Provision.CloudInit.Enabled = parseBoolVar(value)
	case "DRY_RUN":
		cfg.DryRun = parseBoolVar(value)
	case "INSECURE_TRANSPORT":
		cfg.Transport.Insecure = parseBoolVar(value)
	case "CRASH_ARTIFACTS_ENABLED":
		cfg.Provision.CrashArtifacts.Enabled = parseBoolVar(value)
	default:
		return applyFeatureToggle(cfg, key, value)
	}
	return true, nil
}

// applyIntVar handles integer special vars.
func applyIntVar(cfg *config.MachineConfig, key, value string) (bool, error) {
	intFields := map[string]*int{
		"MIN_DISK_SIZE_GB":                   &cfg.Provision.Disk.MinSizeGB,
		"NUM_VFS":                            &cfg.Provision.Disk.NumVFs,
		"HEALTH_MIN_MEMORY_GB":               &cfg.Health.MinMemoryGB,
		"HEALTH_MIN_CPUS":                    &cfg.Health.MinCPUs,
		"CRASH_ARTIFACTS_MAX_MB":             &cfg.Provision.CrashArtifacts.MaxMB,
		"CRASH_ARTIFACTS_UPLOAD_TIMEOUT_SEC": &cfg.Provision.CrashArtifacts.UploadTimeoutSec,
		"BGP_MIN_PEERS":                      &cfg.Network.BGP.MinPeers,
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
		cfg.Telemetry.Enabled = parseBoolVar(value)
	case "SECUREBOOT_REENABLE":
		cfg.Provision.SecureBoot.ReEnable = parseBoolVar(value)
	case "RESCUE_TIMEOUT":
		if err := setIntField(&cfg.Rescue.Timeout, value); err != nil {
			return true, fmt.Errorf("invalid %s: %w", key, err)
		}
	case "RESCUE_AUTO_MOUNT":
		cfg.Rescue.AutoMountDisks = parseBoolVar(value)
	case "EVPN_L2_ENABLED":
		cfg.Network.EVPN.L2Enabled = parseBoolVar(value)
	case "BGP_UNDERLAY_AF":
		cfg.Network.BGP.UnderlayAF = value
	case "BGP_OVERLAY_TYPE":
		cfg.Network.BGP.OverlayType = value
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
