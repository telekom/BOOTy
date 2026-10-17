package timesync

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/telekom/t-caas-go-library/pkg/redact"
)

// DateProbe reads the time from the HTTP Date header of a server, the CAPRF
// connectivity target. It sends a HEAD request without credentials.
//
// The local clock may be years off, so the TLS handshake can not check the
// certificate validity period. The probe checks the chain, the host name and
// the key usage like crypto/tls, but at the time of the Date header instead
// of the local clock: the server must hold the key of a certificate that is
// valid at the time it claims. The time is then authenticated.
type DateProbe struct {
	// Now returns the local wall clock. Tests fake it to simulate skew.
	Now func() time.Time
	// Roots is the trust anchor set. Nil uses the system roots, like the
	// CAPRF and image clients.
	Roots *x509.CertPool
	// SkipTLSVerify (SKIP_TLS_VERIFY) skips the chain check. The time is
	// then unauthenticated.
	SkipTLSVerify bool
	// Timeout bounds the request.
	Timeout time.Duration
}

// errNoDate is returned when the response has no usable Date header.
var errNoDate = errors.New("response has no valid Date header")

// Probe returns the offset of the local clock to the Date header of rawURL.
func (p DateProbe) Probe(ctx context.Context, rawURL string) (Sample, error) {
	safe := redact.URL(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return Sample{}, fmt.Errorf("date probe %s: not an HTTP(S) URL", safe)
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var (
		mu    sync.Mutex
		state *tls.ConnectionState
	)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
				// The chain is checked below, at the time of the Date header.
				InsecureSkipVerify: true, //nolint:gosec // verified in verifyAt
				VerifyConnection: func(cs tls.ConnectionState) error {
					mu.Lock()
					state = &cs
					mu.Unlock()
					return nil
				},
			},
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, http.NoBody)
	if err != nil {
		return Sample{}, fmt.Errorf("date probe %s: %w", safe, redact.Wrap(err, rawURL))
	}
	now := p.now()
	t1 := now()
	start := time.Now()
	resp, err := client.Do(req)
	rtt := time.Since(start)
	if err != nil {
		return Sample{}, fmt.Errorf("date probe %s: %w", safe, redact.Wrap(err, rawURL))
	}
	_ = resp.Body.Close()
	date, err := http.ParseTime(resp.Header.Get("Date"))
	if err != nil {
		return Sample{}, fmt.Errorf("date probe %s: %w", safe, errNoDate)
	}
	// The header has a resolution of one second: take the middle of the
	// second, at the middle of the round trip.
	offset := date.Add(500 * time.Millisecond).Sub(t1.Add(rtt / 2))
	sample := Sample{Source: "caprf-date", Offset: offset, RTT: rtt, Resolution: 2 * time.Second}

	if u.Scheme == "https" && !p.SkipTLSVerify {
		mu.Lock()
		cs := state
		mu.Unlock()
		if err := verifyAt(cs, u.Hostname(), date, p.Roots); err != nil {
			return Sample{}, fmt.Errorf("date probe %s: %w", safe, err)
		}
		sample.Authenticated = true
	}
	return sample, nil
}

func (p DateProbe) now() func() time.Time {
	if p.Now != nil {
		return p.Now
	}
	return time.Now
}

// verifyAt checks the server chain like crypto/tls does for a client, at the
// time at instead of the local clock.
func verifyAt(cs *tls.ConnectionState, host string, at time.Time, roots *x509.CertPool) error {
	if cs == nil || len(cs.PeerCertificates) == 0 {
		return errors.New("no server certificate")
	}
	opts := x509.VerifyOptions{
		Roots:         roots,
		DNSName:       host,
		CurrentTime:   at,
		Intermediates: x509.NewCertPool(),
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, cert := range cs.PeerCertificates[1:] {
		opts.Intermediates.AddCert(cert)
	}
	if _, err := cs.PeerCertificates[0].Verify(opts); err != nil {
		return fmt.Errorf("server certificate is not valid at the Date header time %s: %w", at.UTC().Format(time.RFC3339), err)
	}
	return nil
}
