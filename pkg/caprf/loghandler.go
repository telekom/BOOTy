package caprf

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// dropWarnEvery controls how often we emit a warning about dropped messages.
// A warning is logged after every Nth drop (or at Close).
const dropWarnEvery = 100

// RemoteHandler is a slog.Handler that ships log lines to the CAPRF /log endpoint.
// It wraps another handler so logs appear both in the console and remotely.
type RemoteHandler struct {
	client   *Client
	inner    slog.Handler
	level    slog.Leveler
	buf      chan string
	attrs    []slog.Attr
	groups   []string
	done     chan struct{}
	once     *sync.Once
	cancel   context.CancelFunc
	dropped  *atomic.Int64 // counts messages dropped due to full buffer
	reported *atomic.Int64 // counts drops already reported in periodic warnings
}

// NewRemoteHandler creates a handler that sends logs to the CAPRF server.
// The inner handler is used for local console output. Buffer capacity controls
// how many log lines can be queued before dropping.
func NewRemoteHandler(client *Client, inner slog.Handler, level slog.Leveler, bufSize int) *RemoteHandler {
	if bufSize <= 0 {
		bufSize = 1000
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &RemoteHandler{
		client:   client,
		inner:    inner,
		level:    level,
		buf:      make(chan string, bufSize),
		done:     make(chan struct{}),
		once:     &sync.Once{},
		cancel:   cancel,
		dropped:  &atomic.Int64{},
		reported: &atomic.Int64{},
	}
	go h.drain(ctx)
	return h
}

// Enabled reports whether the handler handles records at the given level.
func (h *RemoteHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

// DroppedCount returns the total number of log entries dropped due to a full buffer.
func (h *RemoteHandler) DroppedCount() int64 {
	return h.dropped.Load()
}

// Handle sends the log record to both the inner handler and the remote buffer.
func (h *RemoteHandler) Handle(ctx context.Context, r slog.Record) error { //nolint:gocritic // slog.Handler interface requires value receiver
	// Always forward to inner handler.
	if err := h.inner.Handle(ctx, r); err != nil {
		return err //nolint:wrapcheck // slog.Handler.Handle must return unwrapped errors
	}

	// Format message for remote shipping.
	msg := fmt.Sprintf("[%s] %s", r.Level, r.Message)
	r.Attrs(func(a slog.Attr) bool {
		msg += fmt.Sprintf(" %s=%v", a.Key, a.Value)
		return true
	})

	// Non-blocking send: drop if buffer is full.
	select {
	case h.buf <- msg:
	default:
		total := h.dropped.Add(1)
		h.maybeWarnDropped(total)
	}

	return nil
}

// maybeWarnDropped emits a periodic warning every dropWarnEvery drops.
func (h *RemoteHandler) maybeWarnDropped(total int64) {
	prev := h.reported.Load()
	if total-prev >= dropWarnEvery {
		// Attempt to claim this reporting slot; only one goroutine wins.
		if h.reported.CompareAndSwap(prev, total) {
			slog.Warn("remote log handler is dropping entries: buffer full",
				"total_dropped", total)
		}
	}
}

// WithAttrs returns a new handler with the given attributes.
func (h *RemoteHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &RemoteHandler{
		client:   h.client,
		inner:    h.inner.WithAttrs(attrs),
		level:    h.level,
		buf:      h.buf,
		attrs:    append(append([]slog.Attr{}, h.attrs...), attrs...),
		groups:   h.groups,
		done:     h.done,
		once:     h.once,
		cancel:   h.cancel,
		dropped:  h.dropped,
		reported: h.reported,
	}
}

// WithGroup returns a new handler with the given group name.
func (h *RemoteHandler) WithGroup(name string) slog.Handler {
	return &RemoteHandler{
		client:   h.client,
		inner:    h.inner.WithGroup(name),
		level:    h.level,
		buf:      h.buf,
		attrs:    h.attrs,
		groups:   append(append([]string{}, h.groups...), name),
		done:     h.done,
		once:     h.once,
		cancel:   h.cancel,
		dropped:  h.dropped,
		reported: h.reported,
	}
}

// Close stops the background drain goroutine and flushes remaining logs.
// It closes the buffer to signal drain to finish, then waits up to 5s.
// If drain does not finish in time, the context is canceled to abort
// any in-flight HTTP requests.
func (h *RemoteHandler) Close() {
	if h.once == nil {
		h.once = &sync.Once{}
	}
	h.once.Do(func() {
		// Log final drop count before closing if any entries were dropped.
		if n := h.dropped.Load(); n > 0 {
			slog.Warn("remote log handler: entries were dropped during session",
				"total_dropped", n)
		}
		close(h.buf)
		select {
		case <-h.done:
		case <-time.After(5 * time.Second): //nolint:mnd // fixed drain timeout
			drained := h.dropped.Load()
			slog.Warn("log handler drain timed out after 5s", "total_dropped", drained)
			if h.cancel != nil {
				h.cancel()
			}
		}
	})
}

func (h *RemoteHandler) drain(ctx context.Context) {
	defer close(h.done)
	for msg := range h.buf {
		if err := h.client.ShipLog(ctx, msg); err != nil {
			slog.Warn("failed to ship log to caprf", "error", err)
		}
	}
}
