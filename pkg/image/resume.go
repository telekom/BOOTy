package image

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// DownloadOptions tune HTTP image downloads (IMAGE_RESUME,
// IMAGE_PARALLEL_RANGES). The zero value is one GET without resume.
type DownloadOptions struct {
	// Resume continues a download that breaks mid-body with a ranged GET
	// from the last byte received.
	Resume bool
	// ParallelRanges fetches the image as that many ranged GETs at a time.
	// 0 and 1 use one GET.
	ParallelRanges int
}

var downloadOptions atomic.Pointer[DownloadOptions]

// SetDownloadOptions sets the options of all later HTTP image downloads.
func SetDownloadOptions(o DownloadOptions) {
	downloadOptions.Store(&o)
}

func currentDownloadOptions() DownloadOptions {
	if o := downloadOptions.Load(); o != nil {
		return *o
	}
	return DownloadOptions{}
}

// maxResumes bounds the resumes of one download, and the attempts of one
// range of a parallel download.
const maxResumes = 5

// errImageChanged is the error of a ranged GET that does not continue the
// same object: the ETag no longer matches, or the server ignores Range.
var errImageChanged = errors.New("the server did not continue the same image (it changed, or it ignores Range)")

// errRangeRejected is the error of a ranged GET that the server refuses
// with a client error (expired URL, missing object): retrying cannot help.
var errRangeRejected = errors.New("the server rejected the range request")

// finalRangeError reports whether retrying the ranged GET cannot help.
func finalRangeError(err error) bool {
	return errors.Is(err, errImageChanged) || errors.Is(err, errRangeRejected)
}

// rangeValidator returns the strong ETag of resp, or "" when resp does not
// allow ranged requests that are bound to the same object. A Last-Modified
// date is not used: it cannot tell two versions of the same second apart.
func rangeValidator(resp *http.Response) string {
	etag := resp.Header.Get("ETag")
	if resp.Header.Get("Accept-Ranges") != "bytes" || etag == "" || strings.HasPrefix(etag, "W/") {
		return ""
	}
	return etag
}

// getRange GETs bytes start to end (end < 0: to the end) of an object of
// total bytes with the strong ETag etag. If-Range and
// If-Match both bind the request to that version, and the Content-Range
// must match. The body is guarded by the idle timeout.
func getRange(ctx context.Context, url, etag string, start, end, total int64) (io.ReadCloser, error) {
	reqCtx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, http.NoBody)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("creating range request for %s: %w", RedactURL(url), &redactedSourceError{rawSource: url, err: err})
	}
	spec := fmt.Sprintf("bytes=%d-", start)
	if end >= 0 {
		spec += fmt.Sprint(end)
	}
	req.Header.Set("Range", spec)
	req.Header.Set("If-Range", etag)
	req.Header.Set("If-Match", etag)
	// A transparently decompressed body would not match its Content-Range.
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := imageHTTPClient.Do(req) //nolint:gosec // URL from trusted config
	if err != nil {
		cancel()
		return nil, fmt.Errorf("range request %s: %w", spec, &redactedSourceError{rawSource: url, err: err})
	}
	if rangeResponseOK(resp, start, total) {
		return newIdleTimeoutBody(resp.Body, cancel, imageIdleReadTimeout), nil
	}
	_ = resp.Body.Close()
	cancel()
	code := resp.StatusCode
	prefix := fmt.Sprintf("range request %s of %s: status %d", spec, RedactURL(url), code)
	switch {
	case code == http.StatusOK || code == http.StatusPartialContent || code == http.StatusPreconditionFailed:
		return nil, fmt.Errorf("%s: %w", prefix, errImageChanged)
	case code >= 400 && code < 500 && code != http.StatusRequestTimeout && code != http.StatusTooManyRequests:
		return nil, fmt.Errorf("%s: %w", prefix, errRangeRejected)
	default:
		return nil, errors.New(prefix)
	}
}

// rangeResponseOK reports whether resp is a 206 with the bytes from start of
// an object of total bytes, as sent: a decoded body has other offsets.
func rangeResponseOK(resp *http.Response, start, total int64) bool {
	return resp.StatusCode == http.StatusPartialContent && !resp.Uncompressed &&
		resp.Header.Get("Content-Encoding") == "" && contentRangeMatches(resp.Header.Get("Content-Range"), start, total)
}

// contentRangeMatches reports whether the Content-Range value v starts at
// start and belongs to an object of total bytes.
func contentRangeMatches(v string, start, total int64) bool {
	var gotStart, gotEnd, gotTotal int64
	_, err := fmt.Sscanf(v, "bytes %d-%d/%d", &gotStart, &gotEnd, &gotTotal)
	return err == nil && gotStart == start && gotTotal == total
}

// wrapDownload returns the body of the 200 response of an image GET. With
// DownloadOptions it resumes a broken body or replaces the body by parallel
// ranged GETs, when the server allows ranges on the same object.
func wrapDownload(ctx context.Context, url string, resp *http.Response, cancel context.CancelFunc) io.ReadCloser {
	body := newIdleTimeoutBody(resp.Body, cancel, imageIdleReadTimeout)
	opts := currentDownloadOptions()
	if !opts.Resume && opts.ParallelRanges <= 1 {
		return body
	}
	etag := rangeValidator(resp)
	// A transparently decompressed body has other offsets than its ranges.
	// Without a Content-Length a resumed body could end early unnoticed.
	if etag == "" || resp.ContentLength < 0 || resp.Uncompressed || resp.Header.Get("Content-Encoding") != "" {
		slog.Info("image server does not offer ranges with a strong ETag and a length: one GET without resume", "url", RedactURL(url))
		return body
	}
	if opts.ParallelRanges > 1 && resp.ContentLength > rangeChunkSize {
		_ = body.Close()
		slog.Info("downloading image in parallel ranges", "ranges", opts.ParallelRanges, "bytes", resp.ContentLength)
		return newRangeReader(ctx, url, etag, resp.ContentLength, opts.ParallelRanges)
	}
	if opts.Resume {
		return &resumingBody{ctx: ctx, url: url, etag: etag, size: resp.ContentLength, body: body}
	}
	return body
}

// waitBackoff waits d, or returns the error of ctx.
func waitBackoff(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("context canceled: %w", ctx.Err())
	case <-time.After(d):
		return nil
	}
}

// resumingBody continues a download that breaks mid-body: it requests the
// rest from the last byte received, bound to the ETag of the first response,
// so the bytes come from the same object. The decoder and the checksum above
// it see one stream.
type resumingBody struct {
	ctx     context.Context
	url     string
	etag    string
	size    int64 // Content-Length of the first response
	body    io.ReadCloser
	off     int64
	resumes int
	err     error // sticky: a failed resume ends the body
}

func (b *resumingBody) Read(p []byte) (int, error) {
	for b.err == nil {
		n, err := b.body.Read(p)
		b.off += int64(n)
		// net/http reports a body shorter than its Content-Length as
		// io.ErrUnexpectedEOF; a plain io.EOF ends a complete body.
		if err == nil || (err == io.EOF && b.off == b.size) { //nolint:errorlint // io.EOF is never wrapped
			return n, err //nolint:wrapcheck // io.EOF must stay unwrapped
		}
		if b.ctx.Err() != nil { // never pass a break off as the end of the image
			return n, fmt.Errorf("image download stopped at offset %d: %w", b.off, context.Cause(b.ctx))
		}
		if b.err = b.resume(err); b.err != nil || n > 0 {
			return n, b.err
		}
	}
	return 0, b.err
}

func (b *resumingBody) resume(cause error) error {
	_ = b.body.Close()
	// The first request after a break goes out at once; failed requests back off.
	var backoff time.Duration
	for b.resumes < maxResumes {
		b.resumes++
		slog.Warn("image download broke, resuming", "offset", b.off, "attempt", b.resumes, "error", cause)
		if err := waitBackoff(b.ctx, backoff); err != nil {
			return err
		}
		backoff = max(2*backoff, retryBackoffBase)
		body, err := getRange(b.ctx, b.url, b.etag, b.off, -1, b.size)
		if err == nil {
			b.body = body
			return nil
		}
		if finalRangeError(err) {
			return err
		}
		cause = err
	}
	return fmt.Errorf("image download failed after %d resumes at offset %d: %w", maxResumes, b.off, cause)
}

func (b *resumingBody) Close() error {
	return b.body.Close() //nolint:wrapcheck // plain body close error
}
