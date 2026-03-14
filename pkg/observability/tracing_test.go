package observability

import (
	"context"
	"testing"
)

func TestTracer_StartSpan(t *testing.T) {
	tr := NewTracer("trace-001")
	span := tr.StartSpan(context.Background(), "provision")

	if span.TraceID != "trace-001" {
		t.Errorf("traceID = %q", span.TraceID)
	}
	if span.Name != "provision" {
		t.Errorf("name = %q", span.Name)
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
		t.Errorf("spans = %d", len(spans))
	}
}

func TestEndSpan(t *testing.T) {
	tr := NewTracer("trace-001")
	span := tr.StartSpan(context.Background(), "test")
	EndSpan(span, SpanOK)

	if span.Status != SpanOK {
		t.Errorf("status = %q", span.Status)
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
		t.Errorf("status = %q", span.Status)
	}
}

func TestAddEvent(t *testing.T) {
	tr := NewTracer("trace-001")
	span := tr.StartSpan(context.Background(), "test")
	AddEvent(span, "checkpoint", map[string]string{"progress": "50"})

	if len(span.Events) != 1 {
		t.Fatalf("events = %d", len(span.Events))
	}
	if span.Events[0].Name != "checkpoint" {
		t.Errorf("event name = %q", span.Events[0].Name)
	}
	if span.Events[0].Attrs["progress"] != "50" {
		t.Errorf("progress = %q", span.Events[0].Attrs["progress"])
	}
}

func TestSpanAttrs(t *testing.T) {
	tr := NewTracer("trace-001")
	span := tr.StartSpan(context.Background(), "test")
	span.Attrs["machine"] = "srv001"

	if span.Attrs["machine"] != "srv001" {
		t.Errorf("machine = %q", span.Attrs["machine"])
	}
}

func TestExporterConfig_Validate(t *testing.T) {
	tests := []struct {
		name string
		cfg  ExporterConfig
		err  bool
	}{
		{"valid grpc", ExporterConfig{Endpoint: "localhost:4317", Protocol: "grpc"}, false},
		{"valid http", ExporterConfig{Endpoint: "http://localhost:4318", Protocol: "http"}, false},
		{"empty endpoint", ExporterConfig{Protocol: "grpc"}, true},
		{"bad protocol", ExporterConfig{Endpoint: "x", Protocol: "tcp"}, true},
		{"default protocol", ExporterConfig{Endpoint: "x"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.err {
				t.Errorf("Validate() err = %v, wantErr %v", err, tc.err)
			}
		})
	}
}

func TestSpanStatusConstants(t *testing.T) {
	if string(SpanOK) != "ok" {
		t.Error("SpanOK")
	}
	if string(SpanError) != "error" {
		t.Error("SpanError")
	}
}
