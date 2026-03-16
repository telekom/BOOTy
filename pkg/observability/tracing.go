// Package observability provides tracing and span management for provisioning.
package observability

import (
	"context"
	"fmt"
	"maps"
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
// Span methods are safe for concurrent use.
type Span struct {
	mu        sync.Mutex
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
// If parent is nil, the span is created as a root span with no parent.
func (t *Tracer) StartChildSpan(_ context.Context, parent *Span, name string) *Span {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.counter++
	var parentID string
	if parent != nil {
		parentID = parent.SpanID
	}
	span := &Span{
		TraceID:   t.traceID,
		SpanID:    fmt.Sprintf("span-%d", t.counter),
		ParentID:  parentID,
		Name:      name,
		StartTime: time.Now(),
		Attrs:     make(map[string]string),
	}
	t.spans = append(t.spans, span)
	return span
}

// EndSpan ends the given span with a status.
func EndSpan(span *Span, status SpanStatus) {
	span.mu.Lock()
	defer span.mu.Unlock()
	span.EndTime = time.Now()
	span.Status = status
}

// AddEvent adds a timestamped event to a span.
// The attrs map is copied to prevent external mutation.
func AddEvent(span *Span, name string, attrs map[string]string) {
	span.mu.Lock()
	defer span.mu.Unlock()
	span.Events = append(span.Events, SpanEvent{
		Name:      name,
		Timestamp: time.Now(),
		Attrs:     maps.Clone(attrs),
	})
}

// Spans returns a deep copy of all recorded spans.
func (t *Tracer) Spans() []Span {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Span, len(t.spans))
	for i, s := range t.spans {
		s.mu.Lock()
		out[i] = Span{
			TraceID:   s.TraceID,
			SpanID:    s.SpanID,
			ParentID:  s.ParentID,
			Name:      s.Name,
			StartTime: s.StartTime,
			EndTime:   s.EndTime,
			Status:    s.Status,
			Attrs:     maps.Clone(s.Attrs),
			Events:    append([]SpanEvent(nil), s.Events...),
		}
		s.mu.Unlock()
	}
	return out
}

// ExporterConfig holds configuration for span export.
type ExporterConfig struct {
	Endpoint string `json:"endpoint,omitempty"`
	Protocol string `json:"protocol,omitempty"` // "grpc" or "http".
	Insecure bool   `json:"insecure,omitempty"`
}

// Validate checks the exporter config and defaults Protocol to "grpc" if empty.
func (c *ExporterConfig) Validate() error {
	if c.Endpoint == "" {
		return fmt.Errorf("exporter endpoint required")
	}
	if c.Protocol == "" {
		c.Protocol = "grpc"
	}
	switch c.Protocol {
	case "grpc", "http":
		return nil
	default:
		return fmt.Errorf("unsupported protocol %q", c.Protocol)
	}
}
