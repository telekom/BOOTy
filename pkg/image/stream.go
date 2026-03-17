package image

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/telekom/BOOTy/pkg/retry"
)

// StreamOpts are optional parameters for Stream.
type StreamOpts struct {
	// Checksum is the expected hex-encoded checksum of the decompressed data.
	Checksum string
	// ChecksumType is the hash algorithm: "sha256" or "sha512".
	ChecksumType string
}

// Stream downloads an image from a URL (http/https or oci://) and writes it
// to a block device. Compression is auto-detected via magic bytes (gzip, zstd,
// lz4, xz, bzip2). Optional checksum validation is performed after write.
func Stream(ctx context.Context, url, device string, opts ...StreamOpts) error {
	slog.Info("Streaming image", "url", filepath.Base(url), "device", device) //nolint:gosec // trusted config values

	var opt StreamOpts
	if len(opts) > 0 {
		opt = opts[0]
	}

	// Protocol dispatch: OCI registry or HTTP.
	body, err := openSource(ctx, url)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()

	// Auto-detect compression format via magic bytes.
	format, reader, err := DetectFormat(body)
	if err != nil {
		return fmt.Errorf("detect format: %w", err)
	}
	slog.Info("Detected image format", "format", format)

	decompressed, closer, err := Decompressor(reader, format)
	if err != nil {
		return err
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
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

	// If checksum is requested, tee through a hash writer.
	var h hash.Hash
	src := decompressed
	if opt.Checksum != "" {
		h, err = newHash(opt.ChecksumType)
		if err != nil {
			return err
		}
		src = io.TeeReader(decompressed, h)
	}

	written, err := io.Copy(out, io.TeeReader(src, counter))
	if err != nil {
		return fmt.Errorf("writing to device: %w", err)
	}

	fmt.Println()
	slog.Info("Image written", "bytes", written, "device", device) //nolint:gosec // trusted config values

	// Verify checksum.
	if h != nil {
		got := hex.EncodeToString(h.Sum(nil))
		if got != opt.Checksum {
			return fmt.Errorf("checksum mismatch: got %s, want %s", got, opt.Checksum)
		}
		slog.Info("Checksum verified", "type", opt.ChecksumType)
	}

	return nil
}

// openSource returns a ReadCloser for the given URL.
// Supports http/https and oci:// protocols.
// HTTP requests are retried up to 3 times with exponential backoff.
func openSource(ctx context.Context, url string) (io.ReadCloser, error) {
	if IsOCIReference(url) {
		ref := TrimOCIScheme(url)
		slog.Info("Pulling OCI image", "ref", ref)
		return fetchOCIWithRetry(ctx, ref)
	}

	return httpGetWithRetry(ctx, url)
}

// httpGetWithRetry performs an HTTP GET with retry and exponential backoff.
func httpGetWithRetry(ctx context.Context, url string) (io.ReadCloser, error) {
	const maxRetries = 3

	var body io.ReadCloser
	var retryReason string
	var retryStatus int
	err := retry.Do(ctx, retry.Policy{
		Attempts:       maxRetries,
		InitialBackoff: time.Second,
	}, func(attempt int) (bool, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		if err != nil {
			return false, fmt.Errorf("creating request: %w", err)
		}

		resp, err := http.DefaultClient.Do(req) //nolint:gosec // URL from trusted config
		if err != nil {
			retryReason = "request"
			retryStatus = 0
			return true, fmt.Errorf("fetching image (attempt %d/%d): %w", attempt, maxRetries, err)
		}

		if resp.StatusCode == http.StatusOK {
			body = resp.Body
			return false, nil
		}
		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			return false, fmt.Errorf("image not found: %s", url)
		}
		if resp.StatusCode >= 500 {
			retryReason = "server"
			retryStatus = resp.StatusCode
			return true, fmt.Errorf("server error %d for %s (attempt %d/%d)", resp.StatusCode, url, attempt, maxRetries)
		}

		return false, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, url)
	}, func(attempt int, backoff time.Duration, err error) {
		if retryReason == "server" {
			slog.Warn("HTTP server error, retrying", "attempt", attempt, "status", retryStatus, "backoff", backoff)
			return
		}
		slog.Warn("HTTP request failed, retrying", "attempt", attempt, "error", err, "backoff", backoff)
	})
	if err != nil {
		return nil, err
	}

	return body, nil
}

// fetchOCIWithRetry retries OCI layer fetch with exponential backoff.
func fetchOCIWithRetry(ctx context.Context, ref string) (io.ReadCloser, error) {
	const maxRetries = 3

	var body io.ReadCloser
	err := retry.Do(ctx, retry.Policy{
		Attempts:       maxRetries,
		InitialBackoff: time.Second,
	}, func(attempt int) (bool, error) {
		rc, err := FetchOCILayer(ctx, ref)
		if err == nil {
			body = rc
			return false, nil
		}
		return true, fmt.Errorf("OCI pull (attempt %d/%d): %w", attempt, maxRetries, err)
	}, func(attempt int, backoff time.Duration, err error) {
		slog.Warn("OCI pull failed, retrying", "attempt", attempt, "error", err, "backoff", backoff)
	})
	if err != nil {
		return nil, err
	}

	return body, nil
}

func newHash(checksumType string) (hash.Hash, error) {
	switch checksumType {
	case "sha256":
		return sha256.New(), nil
	case "sha512":
		return sha512.New(), nil
	default:
		return nil, fmt.Errorf("unsupported checksum type: %s", checksumType)
	}
}
