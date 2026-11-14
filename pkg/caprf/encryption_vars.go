package caprf

import (
	"context"
	"fmt"
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
	case "DISK_ENCRYPTION_PCR_PUBLIC_KEY_FILE":
		d.PCRPublicKeyFile = strings.TrimSpace(value)
	case "SECUREBOOT_SIGNING":
		cfg.Provision.SecureBoot.Signing = strings.ToLower(strings.TrimSpace(value))
	case "DISK_ENCRYPTION_ALLOW_FAILURE":
		// Strict like the watchdog: a typo must not turn fail-closed off.
		d.AllowFailure, err = parseWatchdogBool(value)
	default:
		return false, nil
	}
	if err != nil {
		return true, fmt.Errorf("invalid %s: %w", key, err)
	}
	return true, nil
}

// EscrowRecoveryKey posts body, which holds the LUKS recovery key, to
// DISK_ENCRYPTION_RECOVERY_KEY_URL with the bearer token. It requires HTTPS
// (or loopback) unless INSECURE_TRANSPORT is set, and never logs the body.
func (c *Client) EscrowRecoveryKey(ctx context.Context, body []byte) error {
	url := c.cfg.Provision.DiskEncryption.RecoveryKeyURL
	if url == "" {
		return fmt.Errorf("no recovery key URL configured (DISK_ENCRYPTION_RECOVERY_KEY_URL)")
	}
	if err := c.requireSecureEndpoint(url, "recovery key escrow"); err != nil {
		return err
	}
	return c.postJSONWithAuth(ctx, url, body)
}
