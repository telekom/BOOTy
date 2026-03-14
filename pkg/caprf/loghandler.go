package caprf

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// RemoteHandler is a slog.Handler that ships log lines to the CAPRF /log endpoint.
// It wraps another handler so logs appear both in the console and remotely.
type RemoteHandler struct {
	client *Client
	inner  slog.Handler
	level  slog.Leveler
	buf    chan string
	attrs  []slog.Attr
	groups []string
	done   chan struct{}
	once   sync.Once
}

// NewRemoteHandler creates a handler that sends logs to the CAPRF server.
// The inner handler is used for local console output. Buffer capacity controls
// how many log lines can be queued before dropping.
func NewRemoteHandler(client *Client, inner slog.Handler, level slog.Leveler, bufSize int) *RemoteHandler {
	if bufSize <= 0 {
		bufSize = 1000
	}
	h := &RemoteHandler{
		client: client,
		inner:  inner,
		level:  level,
		buf:    make(chan string, bufSize),
		done:   make(chan struct{}),
	}
	go h.drain()
	return h
}

// Enabled reports whether the handler handles records at the given level.
func (h *RemoteHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
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
	}

	return nil
}

// WithAttrs returns a new handler with the given attributes.
func (h *RemoteHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &RemoteHandler{
		client: h.client,
		inner:  h.inner.WithAttrs(attrs),
		level:  h.level,
		buf:    h.buf,
		attrs:  append(append([]slog.Attr{}, h.attrs...), attrs...),
		groups: h.groups,
		done:   h.done,
	}
}

// WithGroup returns a new handler with the given group name.
func (h *RemoteHandler) WithGroup(name string) slog.Handler {
	return &RemoteHandler{
		client: h.client,
		inner:  h.inner.WithGroup(name),
		level:  h.level,
		buf:    h.buf,
		attrs:  h.attrs,
		groups: append(append([]string{}, h.groups...), name),
		done:   h.done,
	}
}

// Close stops the background drain goroutine and flushes remaining logs.
// Uses a timeout to prevent blocking shutdown indefinitely.
func (h *RemoteHandler) Close() {
	h.once.Do(func() {
		close(h.buf)
		select {
		case <-h.done:
		case <-time.After(5 * time.Second): //nolint:mnd // fixed drain timeout
			slog.Warn("Log handler drain timed out after 5s")
		}
	})
}

func (h *RemoteHandler) drain() {
	defer close(h.done)
	for msg := range h.buf {
		// Log shipping failure is best-effort; don't block.
		_ = h.client.ShipLog(context.Background(), msg)
	}
}
