package image

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"
)

// rangeChunkSize is the size of one ranged GET of a parallel download.
// A parallel download holds about ParallelRanges+1 chunks in RAM.
var rangeChunkSize int64 = 8 << 20

type chunk struct {
	data []byte
	err  error
}

// rangeReader downloads an object of a known size as ranged GETs, n at a
// time, and returns the bytes in order.
type rangeReader struct {
	cancel context.CancelFunc
	order  chan chan chunk // the pending chunks in order
	cur    []byte
	err    error
}

// newRangeReader fetches url (n >= 2) with n ranged GETs at a time: n-1
// queued and the one Read waits for. With the chunk that Read passes on, it
// holds about n+1 chunks. Every range is bound to etag, so all bytes come
// from the same object.
func newRangeReader(ctx context.Context, url, etag string, size int64, n int) *rangeReader {
	ctx, cancel := context.WithCancel(ctx)
	r := &rangeReader{cancel: cancel, order: make(chan chan chunk, n-1)}
	step, backoff := rangeChunkSize, retryBackoffBase
	go func() {
		defer close(r.order)
		for off := int64(0); off < size; off += step {
			result := make(chan chunk, 1)
			select {
			case r.order <- result: // blocks while n-1 chunks are queued
			case <-ctx.Done():
				return
			}
			end := min(off+step, size) - 1
			go func() { result <- fetchRange(ctx, url, etag, off, end, size, backoff) }()
		}
	}()
	return r
}

// fetchRange GETs bytes off to end and retries a failed range.
func fetchRange(ctx context.Context, url, etag string, off, end, size int64, backoff time.Duration) chunk {
	buf := make([]byte, end-off+1)
	var err error
	for attempt := 1; attempt <= maxResumes; attempt++ {
		var body io.ReadCloser
		body, err = getRange(ctx, url, etag, off, end, size)
		if err == nil {
			_, err = io.ReadFull(body, buf)
			_ = body.Close()
		}
		if err == nil {
			return chunk{data: buf}
		}
		if finalRangeError(err) || ctx.Err() != nil {
			break
		}
		slog.Warn("image range failed, retrying", "offset", off, "attempt", attempt, "error", err)
		if werr := waitBackoff(ctx, backoff); werr != nil {
			break
		}
		backoff *= 2
	}
	return chunk{err: fmt.Errorf("image range at offset %d: %w", off, err)}
}

func (r *rangeReader) Read(p []byte) (int, error) {
	for len(r.cur) == 0 {
		r.cur = nil // release the drained chunk while waiting for the next
		if r.err != nil {
			return 0, r.err
		}
		next, ok := <-r.order
		if !ok {
			r.err = io.EOF
			continue
		}
		c := <-next
		r.cur, r.err = c.data, c.err
	}
	n := copy(p, r.cur)
	r.cur = r.cur[n:]
	return n, nil
}

func (r *rangeReader) Close() error {
	r.cancel()
	return nil
}
