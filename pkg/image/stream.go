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

	decompressed, cleanup, err := openAndDecompress(ctx, url)
	if err != nil {
		return err
	}
	defer cleanup()

	out, err := os.OpenFile(device, os.O_WRONLY, 0) //nolint:gosec // device path from config
	if err != nil {
		return fmt.Errorf("opening device %s: %w", device, err)
	}
	defer func() { _ = out.Close() }()

	counter := &WriteCounter{}
	stopProgress := startProgressTicker(counter)
	defer stopProgress()

	src, h, err := wrapChecksum(decompressed, opt)
	if err != nil {
		return err
	}

	written, err := io.Copy(out, io.TeeReader(src, counter))
	if err != nil {
		return fmt.Errorf("writing to device: %w", err)
	}

	fmt.Println()
	slog.Info("Image written", "bytes", written, "device", device) //nolint:gosec // trusted config values

	return verifyChecksum(h, opt)
}

// openAndDecompress opens the image source, detects compression, and returns
// the decompressed reader along with a cleanup function.
func openAndDecompress(ctx context.Context, url string) (io.Reader, func(), error) {
	body, err := openSource(ctx, url)
	if err != nil {
		return nil, nil, err
	}

	format, reader, err := DetectFormat(body)
	if err != nil {
		_ = body.Close()
		return nil, nil, fmt.Errorf("detect format: %w", err)
	}
	slog.Info("Detected image format", "format", format)

	decompressed, closer, err := Decompressor(reader, format)
	if err != nil {
		_ = body.Close()
		return nil, nil, err
	}

	cleanup := func() {
		if closer != nil {
			_ = closer.Close()
		}
		_ = body.Close()
	}
	return decompressed, cleanup, nil
}

// wrapChecksum wraps the reader with a checksum hash if requested.
func wrapChecksum(r io.Reader, opt StreamOpts) (io.Reader, hash.Hash, error) {
	if opt.Checksum == "" {
		return r, nil, nil
	}
	h, err := newHash(opt.ChecksumType)
	if err != nil {
		return nil, nil, err
	}
	return io.TeeReader(r, h), h, nil
}

// verifyChecksum validates the hash digest against the expected checksum.
func verifyChecksum(h hash.Hash, opt StreamOpts) error {
	if h == nil {
		return nil
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != opt.Checksum {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, opt.Checksum)
	}
	slog.Info("Checksum verified", "type", opt.ChecksumType)
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
	backoff := time.Second

	var lastErr error
	for attempt := range maxRetries {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}

		resp, err := http.DefaultClient.Do(req) //nolint:gosec // URL from trusted config
		if err != nil {
			lastErr = fmt.Errorf("fetching image (attempt %d/%d): %w", attempt+1, maxRetries, err)
			slog.Warn("HTTP request failed, retrying", "attempt", attempt+1, "error", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("context canceled: %w", ctx.Err())
			case <-time.After(backoff):
			}
			backoff *= 2
			continue
		}

		if resp.StatusCode == http.StatusOK {
			return resp.Body, nil
		}
		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("image not found: %s", url)
		}
		// Retry on 5xx server errors.
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("server error %d for %s (attempt %d/%d)", resp.StatusCode, url, attempt+1, maxRetries)
			slog.Warn("HTTP server error, retrying", "attempt", attempt+1, "status", resp.StatusCode, "backoff", backoff)
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("context canceled: %w", ctx.Err())
			case <-time.After(backoff):
			}
			backoff *= 2
			continue
		}
		return nil, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, url)
	}
	return nil, lastErr
}

// fetchOCIWithRetry retries OCI layer fetch with exponential backoff.
func fetchOCIWithRetry(ctx context.Context, ref string) (io.ReadCloser, error) {
	const maxRetries = 3
	backoff := time.Second

	var lastErr error
	for attempt := range maxRetries {
		rc, err := FetchOCILayer(ctx, ref)
		if err == nil {
			return rc, nil
		}
		lastErr = fmt.Errorf("OCI pull (attempt %d/%d): %w", attempt+1, maxRetries, err)
		slog.Warn("OCI pull failed, retrying", "attempt", attempt+1, "error", err, "backoff", backoff)
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context canceled: %w", ctx.Err())
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return nil, lastErr
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
