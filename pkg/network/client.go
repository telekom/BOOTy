package network

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// NewHTTPClient returns a pre-configured HTTP client suitable for downloads
// or general API usage. It sets connect and TLS timeouts to prevent hanging
// on broken connections while leaving the response body read timeout unlimited
// unless a specific client-level timeout is provided.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			DialContext: (&net.Dialer{
				Timeout: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}
