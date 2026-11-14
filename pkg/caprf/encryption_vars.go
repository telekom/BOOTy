package caprf

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/telekom/BOOTy/pkg/config"
)

// applyDiskEncryptionVar sets the disk encryption options. config.Validate
// checks the values.
func applyDiskEncryptionVar(cfg *config.MachineConfig, key, value string) (bool, error) {
	d := &cfg.Provision.DiskEncryption
	var err error
	switch key {
	case "DISK_ENCRYPTION":
		d.Mode = strings.ToLower(strings.TrimSpace(value))
	case "DISK_ENCRYPTION_TARGET":
		d.Target = strings.ToLower(strings.TrimSpace(value))
	case "DISK_ENCRYPTION_RECOVERY_KEY_URL":
		d.RecoveryKeyURL = strings.TrimSpace(value)
	case "DISK_ENCRYPTION_STATUS_URL":
		d.StatusURL = strings.TrimSpace(value)
	case "DISK_ENCRYPTION_ALLOW_FAILURE":
		// Strict like the watchdog: a typo must not turn fail-closed off.
		d.AllowFailure, err = parseWatchdogBool(value)
	case "DISK_ENCRYPTION_SKIP_SECURE_BOOT_CHECK":
		d.SkipSecureBootCheck, err = parseWatchdogBool(value)
	default:
		return false, nil
	}
	if err != nil {
		return true, fmt.Errorf("invalid %s: %w", key, err)
	}
	return true, nil
}

// lockedDiskEncryption is the disk encryption config without Target: a
// standby payload or deploy archive may pick another partition, but never
// turn encryption off, weaken it or send the key or the token elsewhere.
func lockedDiskEncryption(d config.DiskEncryptionConfig) config.DiskEncryptionConfig {
	d.Target = ""
	return d
}

// EscrowRecoveryKey posts body, which holds the LUKS recovery key, to
// DISK_ENCRYPTION_RECOVERY_KEY_URL with the bearer token, and never logs
// the body. It needs https (http only on loopback; INSECURE_TRANSPORT does
// not apply) and TLS verification, follows no redirect and fails on any
// answer other than 2xx, so the key is never sent to a second URL.
func (c *Client) EscrowRecoveryKey(ctx context.Context, body []byte) error {
	url := c.cfg.Provision.DiskEncryption.RecoveryKeyURL
	if url == "" {
		return errors.New("no recovery key URL configured (DISK_ENCRYPTION_RECOVERY_KEY_URL)")
	}
	if err := config.ValidateEscrowURL(url); err != nil {
		return fmt.Errorf("%w: recovery key escrow %s", errInsecureTransport, err.Error())
	}
	if config.BoolOr(c.cfg.Transport.SkipTLSVerify, false) {
		return fmt.Errorf("%w: recovery key escrow refuses SKIP_TLS_VERIFY", errInsecureTransport)
	}
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c.withRetry(ctx, url, func() error {
		return c.doPostJSONClient(ctx, &client, url, body)
	})
}
