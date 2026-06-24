// Package logging provides structured logging sinks for BOOTy.
package logging

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
)

const redactedLogValue = "[REDACTED]"

var sensitiveLogKeySubstrings = []string{
	"password",
	"token",
	"secret",
	"credential",
	"authorization",
	"bearer",
	"private",
	"session",
	"apikey",
	"secretkey",
	"privatekey",
}

var sensitiveLogKeySegments = map[string]struct{}{
	"auth": {},
	"cert": {},
	"key":  {},
}

// KafkaConfig holds Kafka connection settings.
type KafkaConfig struct {
	Brokers       []string `json:"brokers"`
	Topic         string   `json:"topic"`
	TLS           bool     `json:"tls,omitempty"`
	SASLUser      string   `json:"saslUser,omitempty"`
	SASLPassword  string   `json:"-"`
	SASLMechanism string   `json:"saslMechanism,omitempty"` // "PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512".
	Compression   string   `json:"compression,omitempty"`   // "snappy", "lz4", "zstd", "none".
}

// Validate checks KafkaConfig for required fields.
func (c *KafkaConfig) Validate() error {
	if len(c.Brokers) == 0 {
		return fmt.Errorf("kafka brokers required")
	}
	if c.Topic == "" {
		return fmt.Errorf("kafka topic required")
	}
	for _, b := range c.Brokers {
		if b == "" {
			return fmt.Errorf("empty broker address")
		}
	}
	if c.SASLUser != "" && c.SASLPassword == "" {
		return fmt.Errorf("sasl password required when sasl user is set")
	}
	if c.SASLPassword != "" && c.SASLUser == "" {
		return fmt.Errorf("sasl user required when sasl password is set")
	}
	return nil
}

// LogMessage is the structured Kafka message format.
type LogMessage struct {
	Timestamp      time.Time      `json:"timestamp"`
	Level          string         `json:"level"`
	Message        string         `json:"message"`
	MachineSerial  string         `json:"machineSerial,omitempty"`
	BMCMAC         string         `json:"bmcMac,omitempty"`
	ProvisioningID string         `json:"provisioningId,omitempty"`
	Step           string         `json:"step,omitempty"`
	Attrs          map[string]any `json:"attrs,omitempty"`
}

// MachineIdentity holds machine identification for log enrichment.
type MachineIdentity struct {
	Serial         string
	BMCMAC         string
	ProvisioningID string
}

// MessageWriter is the interface for sending log messages.
type MessageWriter interface {
	WriteMessage(topic string, key string, data []byte) error
	Close() error
}

// writerAdapter adapts an io.Writer to MessageWriter for testing.
type writerAdapter struct {
	w  io.Writer
	mu sync.Mutex
}

// NewWriterAdapter creates a MessageWriter that writes JSON lines to w.
func NewWriterAdapter(w io.Writer) MessageWriter {
	return &writerAdapter{w: w}
}

func (a *writerAdapter) WriteMessage(_, _ string, data []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := a.w.Write(data); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	if _, err := a.w.Write([]byte{'\n'}); err != nil {
		return fmt.Errorf("write newline: %w", err)
	}
	return nil
}

func (a *writerAdapter) Close() error { return nil }

// KafkaHandler implements slog.Handler for structured log output.
type KafkaHandler struct {
	mu       sync.Mutex
	writer   MessageWriter
	topic    string
	identity MachineIdentity
	level    slog.Level
	attrs    []slog.Attr
	groups   []string
}

// NewKafkaHandler creates a structured logging handler.
func NewKafkaHandler(writer MessageWriter, topic string, identity MachineIdentity) *KafkaHandler {
	return &KafkaHandler{
		writer:   writer,
		topic:    topic,
		identity: identity,
		level:    slog.LevelInfo,
	}
}

// Enabled reports whether the handler handles records at the given level.
func (h *KafkaHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

// Handle processes a log record.
func (h *KafkaHandler) Handle(_ context.Context, r slog.Record) error { //nolint:gocritic // slog.Record is passed by value per slog.Handler interface.
	groupPrefix := ""
	if len(h.groups) > 0 {
		groupPrefix = joinGroups(h.groups) + "."
	}

	msg := LogMessage{
		Timestamp:      r.Time,
		Level:          r.Level.String(),
		Message:        r.Message,
		MachineSerial:  h.identity.Serial,
		BMCMAC:         h.identity.BMCMAC,
		ProvisioningID: h.identity.ProvisioningID,
		Attrs:          make(map[string]any),
	}

	// Add handler-level attrs.
	for _, a := range h.attrs {
		if a.Key == "step" {
			msg.Step = a.Value.String()
		} else {
			key := groupPrefix + a.Key
			msg.Attrs[key] = resolveValueForKey(key, a.Value)
		}
	}

	// Add record-level attrs.
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "step" {
			msg.Step = a.Value.String()
		} else {
			key := groupPrefix + a.Key
			msg.Attrs[key] = resolveValueForKey(key, a.Value)
		}
		return true
	})

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal log message: %w", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	return h.writer.WriteMessage(h.topic, msg.MachineSerial, data)
}

// joinGroups joins group names with dots.
func joinGroups(groups []string) string {
	result := groups[0]
	for _, g := range groups[1:] {
		result += "." + g
	}
	return result
}

// WithAttrs returns a new handler with the given attributes.
func (h *KafkaHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	copy(newAttrs[len(h.attrs):], attrs)
	return &KafkaHandler{
		writer:   h.writer,
		topic:    h.topic,
		identity: h.identity,
		level:    h.level,
		attrs:    newAttrs,
		groups:   h.groups,
	}
}

// WithGroup returns a new handler with the given group name.
func (h *KafkaHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	newGroups := make([]string, len(h.groups)+1)
	copy(newGroups, h.groups)
	newGroups[len(h.groups)] = name
	return &KafkaHandler{
		writer:   h.writer,
		topic:    h.topic,
		identity: h.identity,
		level:    h.level,
		attrs:    h.attrs,
		groups:   newGroups,
	}
}

// Close flushes and closes the underlying writer.
func (h *KafkaHandler) Close() error {
	return h.writer.Close()
}

func resolveValueForKey(key string, v slog.Value) any {
	if isSensitiveLogKey(key) {
		return redactedLogValue
	}
	v = v.Resolve()
	switch v.Kind() {
	case slog.KindGroup:
		m := make(map[string]any)
		for _, a := range v.Group() {
			childKey := a.Key
			if key != "" {
				childKey = key + "." + a.Key
			}
			m[a.Key] = resolveValueForKey(childKey, a.Value)
		}
		return m
	default:
		return v.Any()
	}
}

func isSensitiveLogKey(key string) bool {
	lower := strings.ToLower(key)
	for _, word := range sensitiveLogKeySubstrings {
		if strings.Contains(lower, word) {
			return true
		}
	}
	for _, segment := range splitLogKeySegments(key) {
		if _, ok := sensitiveLogKeySegments[segment]; ok {
			return true
		}
	}
	return false
}

func splitLogKeySegments(key string) []string {
	parts := strings.FieldsFunc(key, isLogKeySeparator)
	segs := make([]string, 0, len(parts))
	for _, part := range parts {
		for _, segment := range splitLogKeyCamel(part) {
			segs = append(segs, strings.ToLower(segment))
		}
	}
	if len(segs) == 0 {
		return []string{strings.ToLower(key)}
	}
	return segs
}

func splitLogKeyCamel(s string) []string {
	if s == "" {
		return nil
	}
	runes := []rune(s)
	segs := make([]string, 0, 2)
	start := 0
	for i := 1; i < len(runes); i++ {
		prev, cur := runes[i-1], runes[i]
		switch {
		case unicode.IsLower(prev) && unicode.IsUpper(cur):
			segs = append(segs, string(runes[start:i]))
			start = i
		case unicode.IsDigit(prev) && unicode.IsLetter(cur):
			segs = append(segs, string(runes[start:i]))
			start = i
		case i >= 2 && unicode.IsUpper(runes[i-2]) && unicode.IsUpper(prev) && unicode.IsLower(cur):
			segs = append(segs, string(runes[start:i-1]))
			start = i - 1
		}
	}
	segs = append(segs, string(runes[start:]))
	return segs
}

func isLogKeySeparator(r rune) bool {
	return r == '.' || r == '_' || r == '-'
}
