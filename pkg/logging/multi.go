package logging

import "log/slog"

// MultiHandler fans out log records to multiple handlers.
type MultiHandler = slog.MultiHandler

// NewMultiHandler preserves the existing constructor using standard-library fan-out.
func NewMultiHandler(handlers ...slog.Handler) *MultiHandler {
	return slog.NewMultiHandler(handlers...)
}
