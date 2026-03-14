// Package observability provides tracing and span management for provisioning.
package observability

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// SpanStatus represents the status of a trace span.
type SpanStatus string

const (
	// SpanOK means the span completed successfully.
	SpanOK SpanStatus = "ok"
	// SpanError means the span completed with an error.
	SpanError SpanStatus = "error"
)

// Span represents a single traced operation.
type Span struct {
	TraceID   string            `json:"traceId"`
	SpanID    string            `json:"spanId"`
	ParentID  string            `json:"parentId,omitempty"`
	Name      string            `json:"name"`
	StartTime time.Time         `json:"startTime"`
	EndTime   time.Time         `json:"endTime,omitempty"`
	Status    SpanStatus        `json:"status,omitempty"`
	Attrs     map[string]string `json:"attrs,omitempty"`
	Events    []SpanEvent       `json:"events,omitempty"`
}

// SpanEvent is a timestamped event within a span.
type SpanEvent struct {
	Name      string            `json:"name"`
	Timestamp time.Time         `json:"timestamp"`
	Attrs     map[string]string `json:"attrs,omitempty"`
}

// Tracer manages spans for a provisioning session.
type Tracer struct {
	mu      sync.Mutex
	traceID string
	spans   []*Span
	counter uint64
}

// NewTracer creates a new tracer with the given trace ID.
func NewTracer(traceID string) *Tracer {
	return &Tracer{traceID: traceID}
}

// StartSpan creates and starts a new span.
func (t *Tracer) StartSpan(_ context.Context, name string) *Span {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.counter++
	span := &Span{
		TraceID:   t.traceID,
		SpanID:    fmt.Sprintf("span-%d", t.counter),
		Name:      name,
		StartTime: time.Now(),
		Attrs:     make(map[string]string),
	}
	t.spans = append(t.spans, span)
	return span
}

// StartChildSpan creates a child span under a parent.
func (t *Tracer) StartChildSpan(_ context.Context, parent *Span, name string) *Span {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.counter++
	span := &Span{
		TraceID:   t.traceID,
		SpanID:    fmt.Sprintf("span-%d", t.counter),
		ParentID:  parent.SpanID,
		Name:      name,
		StartTime: time.Now(),
		Attrs:     make(map[string]string),
	}
	t.spans = append(t.spans, span)
	return span
}

// EndSpan ends the given span with a status.
func EndSpan(span *Span, status SpanStatus) {
	span.EndTime = time.Now()
	span.Status = status
}

// AddEvent adds a timestamped event to a span.
func AddEvent(span *Span, name string, attrs map[string]string) {
	span.Events = append(span.Events, SpanEvent{
		Name:      name,
		Timestamp: time.Now(),
		Attrs:     attrs,
	})
}

// Spans returns a copy of all recorded spans.
func (t *Tracer) Spans() []*Span {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]*Span, len(t.spans))
	copy(out, t.spans)
	return out
}

// ExporterConfig holds configuration for span export.
type ExporterConfig struct {
	Endpoint string `json:"endpoint,omitempty"`
	Protocol string `json:"protocol,omitempty"` // "grpc" or "http".
	Insecure bool   `json:"insecure,omitempty"`
}

// Validate checks the exporter config.
func (c *ExporterConfig) Validate() error {
	if c.Endpoint == "" {
		return fmt.Errorf("exporter endpoint required")
	}
	switch c.Protocol {
	case "grpc", "http", "":
		return nil
	default:
		return fmt.Errorf("unsupported protocol %q", c.Protocol)
	}
}
