// Package trust holds the TLS root certificates of the BOOTy HTTPS clients:
// the system roots plus the extra roots of CA_BUNDLE_FILE.
package trust

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
)

var roots atomic.Pointer[x509.CertPool]

// Roots returns the root pool for tls.Config.RootCAs: nil (the system roots)
// until LoadFile added extra roots.
func Roots() *x509.CertPool {
	return roots.Load()
}

// Reset restores the system roots. Tests use it to undo LoadFile.
func Reset() {
	roots.Store(nil)
}

// LoadFile adds the PEM certificates of path to the system roots for every
// HTTPS client that BOOTy creates afterwards. An empty path keeps the system
// roots. A file that cannot be read, holds no certificate or holds anything
// else than CERTIFICATE blocks is an error, and the roots stay unchanged.
func LoadFile(path string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read CA bundle: %w", err)
	}
	certs, err := ParsePEM(data)
	if err != nil {
		return fmt.Errorf("CA bundle %s: %w", path, err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		slog.Warn("system root certificates unavailable, trusting only the CA bundle", "path", path, "error", err)
		pool = x509.NewCertPool()
	}
	for _, c := range certs {
		pool.AddCert(c)
	}
	roots.Store(pool)
	slog.Info("added extra root certificates", "path", path, "count", len(certs))
	return nil
}

// ParsePEM parses a bundle of PEM CERTIFICATE blocks. Text before a block is
// ignored, like openssl does. Other block types, broken certificates, data
// after the last block (a truncated block) and a bundle without a certificate
// are errors, so a wrong file fails instead of being half used.
func ParsePEM(data []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("PEM block %d is %q, want CERTIFICATE", len(certs)+1, block.Type)
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("PEM block %d: %w", len(certs)+1, err)
		}
		certs = append(certs, c)
	}
	if len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("data after PEM block %d is not a PEM certificate", len(certs))
	}
	if len(certs) == 0 {
		return nil, errors.New("no PEM certificate")
	}
	return certs, nil
}
