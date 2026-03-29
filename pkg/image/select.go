package image

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"time"
)

// SelectBestSource probes multiple image URLs with HEAD requests and returns
// the one with the lowest response time. If only one URL is provided, it is
// returned directly. OCI references (oci://) are returned as-is without probing.
func SelectBestSource(ctx context.Context, urls []string) (string, error) {
	if len(urls) == 0 {
		return "", fmt.Errorf("no image URLs provided")
	}
	if len(urls) == 1 {
		return urls[0], nil
	}

	// OCI references are not probeable via HEAD.
	ociURLs := make([]string, 0, len(urls))
	httpURLs := make([]string, 0, len(urls))
	for _, u := range urls {
		if IsOCIReference(u) {
			ociURLs = append(ociURLs, u)
		} else {
			httpURLs = append(httpURLs, u)
		}
	}

	// If all URLs are OCI, return the first one.
	if len(httpURLs) == 0 {
		return ociURLs[0], nil
	}

	var bestURL string
	var bestTime = time.Duration(math.MaxInt64)

	slog.Info("Selecting best image source", "candidates", len(httpURLs))
	for _, rawURL := range httpURLs {
		elapsed, err := probeURL(ctx, rawURL)
		if err != nil {
			slog.Warn("Image source probe failed", "url", redactHost(rawURL), "error", err)
			continue
		}
		slog.Info("Image source probe result", "url", redactHost(rawURL), "response_time", elapsed)
		if elapsed < bestTime {
			bestTime = elapsed
			bestURL = rawURL
		}
	}

	if bestURL != "" {
		slog.Info("Selected best image source", "url", redactHost(bestURL), "response_time", bestTime)
		return bestURL, nil
	}

	// All HTTP probes failed; try OCI URLs first, then fall back to first HTTP URL.
	if len(ociURLs) > 0 {
		return ociURLs[0], nil
	}
	return httpURLs[0], nil
}

// probeURL sends a HEAD request to measure connectivity latency.
func probeURL(ctx context.Context, rawURL string) (time.Duration, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodHead, rawURL, http.NoBody)
	if err != nil {
		return 0, fmt.Errorf("creating probe request: %w", err)
	}

	start := time.Now()
	resp, err := gpgHTTPClient.Do(req) //nolint:gosec // URL from trusted config
	if err != nil {
		return 0, fmt.Errorf("probe request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	elapsed := time.Since(start)

	if resp.StatusCode >= 400 &&
		resp.StatusCode != http.StatusMethodNotAllowed &&
		resp.StatusCode != http.StatusNotImplemented {
		return 0, fmt.Errorf("probe returned status %d", resp.StatusCode)
	}
	return elapsed, nil
}

// redactHost extracts only the hostname from a URL for safe logging.
func redactHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "<invalid-url>"
	}
	return u.Hostname()
}
