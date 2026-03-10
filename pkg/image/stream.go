package image

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Stream downloads an image from a URL and writes it to a block device.
// Compression is auto-detected from URL suffix (.gz = gzip, else raw).
func Stream(ctx context.Context, url, device string) error {
	slog.Info("Streaming image", "url", filepath.Base(url), "device", device) //nolint:gosec // trusted config values

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req) //nolint:gosec // URL from trusted config
	if err != nil {
		return fmt.Errorf("fetching image: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("image not found: %s", url)
		}
		return fmt.Errorf("unexpected status %d for %s", resp.StatusCode, url)
	}

	var reader io.Reader = resp.Body

	// Auto-detect compression from URL suffix.
	if strings.HasSuffix(url, ".gz") || strings.HasSuffix(url, ".gzip") {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return fmt.Errorf("gzip reader: %w", err)
		}
		defer func() { _ = gz.Close() }()
		reader = gz
	}

	out, err := os.OpenFile(device, os.O_WRONLY, 0) //nolint:gosec // device path from config
	if err != nil {
		return fmt.Errorf("opening device %s: %w", device, err)
	}
	defer func() { _ = out.Close() }()

	counter := &WriteCounter{}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	go func() {
		for range ticker.C {
			tickerProgress(counter.Total.Load())
		}
	}()

	written, err := io.Copy(out, io.TeeReader(reader, counter))
	if err != nil {
		return fmt.Errorf("writing to device: %w", err)
	}

	fmt.Println()
	slog.Info("Image written", "bytes", written, "device", device) //nolint:gosec // trusted config values
	return nil
}
