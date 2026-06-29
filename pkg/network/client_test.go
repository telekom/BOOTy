package network

import (
	"crypto/tls"
	"net/http"
	"testing"
	"time"
)

func TestNewHTTPClient(t *testing.T) {
	client := NewHTTPClient(42 * time.Second)
	if client.Timeout != 42*time.Second {
		t.Errorf("expected client Timeout 42s, got %v", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected Transport to be *http.Transport")
	}
	if transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("expected MinVersion TLS1.2")
	}
	if transport.TLSHandshakeTimeout != 15*time.Second {
		t.Errorf("expected TLSHandshakeTimeout 15s")
	}
	if transport.ResponseHeaderTimeout != 30*time.Second {
		t.Errorf("expected ResponseHeaderTimeout 30s")
	}
}
