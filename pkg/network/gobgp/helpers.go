//go:build linux

package gobgp

import (
	"fmt"
	"log/slog"
	"os"
)

// enableForwarding sets kernel sysctls for IP forwarding.
func enableForwarding(log *slog.Logger) error {
	// Critical: forwarding must be enabled for routing to work.
	critical := map[string]string{
		"/proc/sys/net/ipv4/ip_forward":             "1",
		"/proc/sys/net/ipv6/conf/all/forwarding":    "1",
		"/proc/sys/net/ipv4/conf/all/rp_filter":     "0",
		"/proc/sys/net/ipv4/conf/default/rp_filter": "0",
	}
	for path, val := range critical {
		if err := os.WriteFile(path, []byte(val), 0o644); err != nil { //nolint:gosec // sysctl paths are trusted
			return fmt.Errorf("set sysctl %s: %w", path, err)
		}
	}

	// Best-effort: accept_ra settings help but are not fatal.
	optional := map[string]string{
		"/proc/sys/net/ipv6/conf/all/accept_ra":            "2",
		"/proc/sys/net/ipv6/conf/all/accept_ra_defrtr":     "1",
		"/proc/sys/net/ipv6/conf/default/accept_ra":        "2",
		"/proc/sys/net/ipv6/conf/default/accept_ra_defrtr": "1",
	}
	for path, val := range optional {
		if err := os.WriteFile(path, []byte(val), 0o644); err != nil { //nolint:gosec // sysctl paths are trusted
			log.Debug("Failed to set sysctl", "path", path, "error", err)
		}
	}
	return nil
}
