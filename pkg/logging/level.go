package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"reflect"
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
	if v, ok := redactComposite(a.Value); ok {
		return slog.Any(a.Key, v)
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

// redactComposite returns a redacted copy of a map, struct, slice or array
// value; ok is false for every other value. RedactText on the rendered form
// misses secrets that follow a map key or a struct field name
// ("map[text:password=x]", {"password":"x"}), and the text handler would
// also print fields that the JSON form hides (json:"-", unexported). So
// both handlers get the JSON form of the value, walked: a sensitive key
// gets [REDACTED], a string gets RedactText.
func redactComposite(v slog.Value) (any, bool) {
	if v.Kind() != slog.KindAny {
		return nil, false
	}
	x := v.Any()
	if _, isErr := x.(error); isErr {
		return nil, false
	}
	rv := reflect.ValueOf(x)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Map, reflect.Struct, reflect.Slice, reflect.Array:
	default:
		return nil, false
	}
	b, err := json.Marshal(x)
	if err != nil {
		// No JSON form: only the redacted text, never the value itself.
		return RedactText(fmt.Sprintf("%+v", x)), true
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return redactedLogValue, true
	}
	return redactTree(tree), true
}

// redactTree redacts a decoded JSON value in place.
func redactTree(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			if isSensitiveLogKey(k) {
				t[k] = redactedLogValue
			} else {
				t[k] = redactTree(e)
			}
		}
	case []any:
		for i, e := range t {
			t[i] = redactTree(e)
		}
	case string:
		return RedactText(t)
	}
	return v
}
