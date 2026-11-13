package network

import (
	"crypto/tls"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/telekom/BOOTy/pkg/trust"
)

var skipTLSVerify atomic.Bool

// SetSkipTLSVerify controls TLS certificate verification for clients that
// NewHTTPClient creates afterwards. SKIP_TLS_VERIFY (preset by
// CAPRF_LEGACY_COMPAT) enables it, to match `curl --insecure` in the legacy
// provisioner.
func SetSkipTLSVerify(skip bool) {
	skipTLSVerify.Store(skip)
}

// SkipTLSVerify returns the value of SetSkipTLSVerify.
func SkipTLSVerify() bool {
	return skipTLSVerify.Load()
}

// NewHTTPClient returns a pre-configured HTTP client suitable for downloads
// or general API usage. It sets connect and TLS timeouts to prevent hanging
// on broken connections while leaving the response body read timeout unlimited
// unless a specific client-level timeout is provided. It trusts the system
// roots plus the CA_BUNDLE_FILE roots (trust.LoadFile).
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{
				MinVersion:         tls.VersionTLS12,
				RootCAs:            trust.Roots(),
				InsecureSkipVerify: skipTLSVerify.Load(), //nolint:gosec // opt-in legacy CAPRF parity (curl --insecure)
			},
			DialContext: (&net.Dialer{
				Timeout: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}
