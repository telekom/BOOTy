package logging

import (
	"context"
	"fmt"
	"log/slog"
)

// MultiHandler fans out log records to multiple handlers.
type MultiHandler struct {
	handlers []slog.Handler
}

// NewMultiHandler creates a handler that sends to all given handlers.
func NewMultiHandler(handlers ...slog.Handler) *MultiHandler {
	return &MultiHandler{handlers: handlers}
}

// Enabled returns true if any handler is enabled for the level.
func (m *MultiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle sends the record to all handlers, returning the first error.
func (m *MultiHandler) Handle(ctx context.Context, r slog.Record) error { //nolint:gocritic // slog.Record is passed by value per slog.Handler interface.
	for idx, h := range m.handlers {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r); err != nil {
				return fmt.Errorf("handler %d: %w", idx, err)
			}
		}
	}
	return nil
}

// WithAttrs returns a new MultiHandler with attrs applied to all handlers.
func (m *MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithAttrs(attrs)
	}
	return &MultiHandler{handlers: handlers}
}

// WithGroup returns a new MultiHandler with the group applied to all handlers.
func (m *MultiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithGroup(name)
	}
	return &MultiHandler{handlers: handlers}
}
