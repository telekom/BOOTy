//go:build linux

package gobgp

import (
	"log/slog"
	"os"
)

// enableForwarding sets kernel sysctls for IP forwarding.
func enableForwarding(log *slog.Logger) error {
	sysctls := map[string]string{
		"/proc/sys/net/ipv4/ip_forward":                    "1",
		"/proc/sys/net/ipv6/conf/all/forwarding":           "1",
		"/proc/sys/net/ipv4/conf/all/rp_filter":            "0",
		"/proc/sys/net/ipv4/conf/default/rp_filter":        "0",
		"/proc/sys/net/ipv6/conf/all/accept_ra":            "2",
		"/proc/sys/net/ipv6/conf/all/accept_ra_defrtr":     "1",
		"/proc/sys/net/ipv6/conf/default/accept_ra":        "2",
		"/proc/sys/net/ipv6/conf/default/accept_ra_defrtr": "1",
	}

	for path, val := range sysctls {
		if err := os.WriteFile(path, []byte(val), 0o644); err != nil { //nolint:gosec // sysctl paths are trusted
			log.Debug("Failed to set sysctl", "path", path, "error", err)
		}
	}
	return nil
}
