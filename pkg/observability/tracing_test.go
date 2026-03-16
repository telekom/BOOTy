package observability

import (
	"context"
	"sync"
	"testing"
)

func TestTracer_StartSpan(t *testing.T) {
	tr := NewTracer("trace-001")
	span := tr.StartSpan(context.Background(), "provision")

	if span.TraceID != "trace-001" {
		t.Errorf("traceID = %q, want %q", span.TraceID, "trace-001")
	}
	if span.Name != "provision" {
		t.Errorf("name = %q, want %q", span.Name, "provision")
	}
	if span.StartTime.IsZero() {
		t.Error("startTime is zero")
	}
}

func TestTracer_ChildSpan(t *testing.T) {
	tr := NewTracer("trace-001")
	parent := tr.StartSpan(context.Background(), "provision")
	child := tr.StartChildSpan(context.Background(), parent, "disk-image")

	if child.ParentID != parent.SpanID {
		t.Errorf("parentID = %q, want %q", child.ParentID, parent.SpanID)
	}

	spans := tr.Spans()
	if len(spans) != 2 {
		t.Errorf("spans count = %d, want 2", len(spans))
	}
}

func TestTracer_ChildSpan_NilParent(t *testing.T) {
	tr := NewTracer("trace-001")
	child := tr.StartChildSpan(context.Background(), nil, "orphan")

	if child.ParentID != "" {
		t.Errorf("parentID = %q, want empty for nil parent", child.ParentID)
	}
}

func TestEndSpan(t *testing.T) {
	tr := NewTracer("trace-001")
	span := tr.StartSpan(context.Background(), "test")
	EndSpan(span, SpanOK)

	if span.Status != SpanOK {
		t.Errorf("status = %q, want %q", span.Status, SpanOK)
	}
	if span.EndTime.IsZero() {
		t.Error("endTime is zero")
	}
}

func TestEndSpan_Error(t *testing.T) {
	tr := NewTracer("trace-001")
	span := tr.StartSpan(context.Background(), "test")
	EndSpan(span, SpanError)

	if span.Status != SpanError {
		t.Errorf("status = %q, want %q", span.Status, SpanError)
	}
}

func TestAddEvent(t *testing.T) {
	tr := NewTracer("trace-001")
	span := tr.StartSpan(context.Background(), "test")
	attrs := map[string]string{"progress": "50"}
	AddEvent(span, "checkpoint", attrs)

	if len(span.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(span.Events))
	}
	if span.Events[0].Name != "checkpoint" {
		t.Errorf("event name = %q, want %q", span.Events[0].Name, "checkpoint")
	}
	if span.Events[0].Attrs["progress"] != "50" {
		t.Errorf("progress = %q, want %q", span.Events[0].Attrs["progress"], "50")
	}

	// Verify attrs were copied — mutating original should not affect event.
	attrs["progress"] = "100"
	if span.Events[0].Attrs["progress"] != "50" {
		t.Error("AddEvent did not copy attrs map — external mutation affected event")
	}
}

func TestSpanAttrs(t *testing.T) {
	tr := NewTracer("trace-001")
	span := tr.StartSpan(context.Background(), "test")
	span.Attrs["machine"] = "srv001"

	if span.Attrs["machine"] != "srv001" {
		t.Errorf("machine = %q, want %q", span.Attrs["machine"], "srv001")
	}
}

func TestSpans_DeepCopy(t *testing.T) {
	tr := NewTracer("trace-001")
	span := tr.StartSpan(context.Background(), "test")
	span.Attrs["key"] = "original"

	copies := tr.Spans()
	if len(copies) != 1 {
		t.Fatalf("spans = %d, want 1", len(copies))
	}

	// Mutating the copy should not affect the original.
	copies[0].Attrs["key"] = "mutated"
	if span.Attrs["key"] != "original" {
		t.Error("Spans() returned shallow copy — mutation affected original")
	}
}

func TestExporterConfig_Validate(t *testing.T) {
	tests := []struct {
		name     string
		cfg      ExporterConfig
		err      bool
		protocol string
	}{
		{"valid grpc", ExporterConfig{Endpoint: "localhost:4317", Protocol: "grpc"}, false, "grpc"},
		{"valid http", ExporterConfig{Endpoint: "http://localhost:4318", Protocol: "http"}, false, "http"},
		{"empty endpoint", ExporterConfig{Protocol: "grpc"}, true, "grpc"},
		{"bad protocol", ExporterConfig{Endpoint: "x", Protocol: "tcp"}, true, "tcp"},
		{"default protocol", ExporterConfig{Endpoint: "x"}, false, "grpc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.err {
				t.Errorf("Validate() err = %v, wantErr %v", err, tc.err)
			}
			if err == nil && tc.cfg.Protocol != tc.protocol {
				t.Errorf("Protocol = %q, want %q", tc.cfg.Protocol, tc.protocol)
			}
		})
	}
}

func TestSpanStatusConstants(t *testing.T) {
	if string(SpanOK) != "ok" {
		t.Errorf("SpanOK = %q, want %q", SpanOK, "ok")
	}
	if string(SpanError) != "error" {
		t.Errorf("SpanError = %q, want %q", SpanError, "error")
	}
}

func TestConcurrentSpanOps(t *testing.T) {
	tr := NewTracer("trace-concurrent")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			span := tr.StartSpan(context.Background(), "concurrent")
			AddEvent(span, "work", map[string]string{"ok": "true"})
			EndSpan(span, SpanOK)
		}()
	}
	wg.Wait()

	spans := tr.Spans()
	if len(spans) != 50 {
		t.Errorf("spans = %d, want 50", len(spans))
	}
}
