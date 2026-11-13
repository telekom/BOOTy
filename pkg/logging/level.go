package logging

import (
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
)

// Log formats. FormatText is the default of every BOOTy binary.
const (
	FormatText = "text"
	FormatJSON = "json"
)

// ParseLevel parses a LOG_LEVEL value: debug, info, warn (or warning) or
// error, in any letter case. Empty means info. An invalid value returns
// info and an error.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return slog.LevelInfo, fmt.Errorf("invalid log level %q: use debug, info, warn or error", s)
}

// ParseFormat parses a LOG_FORMAT value: text or json, in any letter case.
// Empty means text. An invalid value returns text and an error.
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
// records below level. format must be a ParseFormat result. It redacts
// secrets like the LOG_URL handler: the value of an attribute with a
// sensitive key (token, password, secret, key, ...), and RedactText on the
// message and on every string, error or other non-numeric value.
func NewHandler(w io.Writer, format string, level slog.Leveler) slog.Handler {
	opts := &slog.HandlerOptions{Level: level, ReplaceAttr: redactAttr}
	if format == FormatJSON {
		return slog.NewJSONHandler(w, opts)
	}
	return slog.NewTextHandler(w, opts)
}

// redactAttr is the ReplaceAttr of NewHandler. slog calls it for each
// attribute, with the names of the groups it is in, and for the message.
func redactAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && (a.Key == slog.TimeKey || a.Key == slog.LevelKey) {
		return a
	}
	if (len(groups) > 0 || a.Key != slog.MessageKey) && (isSensitiveLogKey(a.Key) || slices.ContainsFunc(groups, isSensitiveLogKey)) {
		return slog.String(a.Key, redactedLogValue)
	}
	switch a.Value.Kind() {
	case slog.KindString, slog.KindAny:
		s := a.Value.String()
		if r := RedactText(s); r != s {
			return slog.String(a.Key, r)
		}
	}
	return a
}
