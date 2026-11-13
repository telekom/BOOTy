package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// Log formats. FormatText is the default of every BOOTy binary.
const (
	FormatText = "text"
	FormatJSON = "json"
)

// ParseLevel parses a LOG_LEVEL value: debug, info, warn or error, in any
// letter case. Empty means info.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return slog.LevelInfo, fmt.Errorf("invalid log level %q: use debug, info, warn or error", s)
}

// ParseFormat parses a LOG_FORMAT value: text or json, in any letter case.
// Empty means text.
func ParseFormat(s string) (string, error) {
	switch f := strings.ToLower(strings.TrimSpace(s)); f {
	case "", FormatText:
		return FormatText, nil
	case FormatJSON:
		return FormatJSON, nil
	}
	return FormatText, fmt.Errorf("invalid log format %q: use text or json", s)
}

// NewHandler returns a slog text or JSON handler that writes to w and drops
// records below level. format must be a ParseFormat result.
func NewHandler(w io.Writer, format string, level slog.Leveler) slog.Handler {
	opts := &slog.HandlerOptions{Level: level}
	if format == FormatJSON {
		return slog.NewJSONHandler(w, opts)
	}
	return slog.NewTextHandler(w, opts)
}
