package event

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	m := Machine{Name: "worker-42", Namespace: "prod"}
	e := New(ProvisionStarted, m)
	if e.Type != ProvisionStarted {
		t.Errorf("Type = %q, want %q", e.Type, ProvisionStarted)
	}
	if e.Machine.Name != "worker-42" {
		t.Errorf("Machine.Name = %q, want worker-42", e.Machine.Name)
	}
	if e.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
}

func TestWithDetails(t *testing.T) {
	m := Machine{Name: "worker-1"}
	e := New(ProvisionFailed, m).WithDetails(map[string]any{
		"step":    "image-streaming",
		"error":   "connection reset",
		"attempt": 3,
	})
	if e.Details["step"] != "image-streaming" {
		t.Errorf("Details[step] = %q, want image-streaming", e.Details["step"])
	}
}

func TestWithDetails_NormalizesNonJSONValue(t *testing.T) {
	e := New(ProvisionFailed, Machine{Name: "worker-1"}).WithDetails(map[string]any{
		"bad": func() {},
	})
	if _, ok := e.Details["bad"].(string); !ok {
		t.Fatalf("non-JSON value should be normalized to string, got %T", e.Details["bad"])
	}
	if _, err := json.Marshal(e); err != nil {
		t.Fatalf("event should marshal after normalization: %v", err)
	}
}

func TestWithDetails_ErrorValuePreservesMessage(t *testing.T) {
	e := New(ProvisionFailed, Machine{Name: "worker-1"}).WithDetails(map[string]any{
		"error": fmt.Errorf("connection refused"),
	})
	got, ok := e.Details["error"].(string)
	if !ok {
		t.Fatalf("error value should be converted to string, got %T", e.Details["error"])
	}
	if got != "connection refused" {
		t.Errorf("error message = %q, want %q", got, "connection refused")
	}
}

func TestEventJSON(t *testing.T) {
	m := Machine{
		Name:        "worker-42",
		Namespace:   "cluster-prod",
		RedfishHost: "rfh-rack3-u42",
		Address:     "10.0.1.42",
	}
	e := New(ProvisionCompleted, m)
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	raw := string(data)
	for _, field := range []string{`"event"`, `"timestamp"`, `"machine"`, `"redfishHost"`} {
		if !strings.Contains(raw, field) {
			t.Errorf("JSON missing field %s: %s", field, raw)
		}
	}

	var decoded Event
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if decoded.Type != ProvisionCompleted {
		t.Errorf("decoded Type = %q, want %q", decoded.Type, ProvisionCompleted)
	}
	if decoded.Machine.RedfishHost != "rfh-rack3-u42" {
		t.Errorf("decoded RedfishHost = %q, want rfh-rack3-u42", decoded.Machine.RedfishHost)
	}
}

func TestEventTypes(t *testing.T) {
	types := []Type{
		ProvisionStarted, ProvisionCompleted, ProvisionFailed,
		DeprovisionStarted, DeprovisionCompleted,
		HealthCritical, HealthWarning,
		RescueActivated, FirmwareMismatch, AttestationFailed,
	}
	seen := make(map[Type]bool)
	for _, et := range types {
		if et == "" {
			t.Error("empty event type")
		}
		if seen[et] {
			t.Errorf("duplicate event type: %q", et)
		}
		seen[et] = true
	}
}

func TestDispatcherSend(t *testing.T) {
	ch := make(chan Event, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("missing content-type header")
		}
		var ev Event
		if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
			t.Errorf("decode body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ch <- ev
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d, err := NewDispatcher(srv.URL, slog.Default())
	if err != nil {
		t.Fatalf("NewDispatcher() error: %v", err)
	}
	e := New(ProvisionStarted, Machine{Name: "test-host"})
	if err := d.Send(context.Background(), e); err != nil {
		t.Fatalf("Send() error: %v", err)
	}
	var received Event
	select {
	case received = <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event")
	}
	if received.Type != ProvisionStarted {
		t.Errorf("received Type = %q, want %q", received.Type, ProvisionStarted)
	}
	if received.Machine.Name != "test-host" {
		t.Errorf("received Machine.Name = %q, want %q", received.Machine.Name, "test-host")
	}
}

func TestDispatcherSendError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream failure"))
	}))
	defer srv.Close()

	d, err := NewDispatcher(srv.URL, slog.Default())
	if err != nil {
		t.Fatalf("NewDispatcher() error: %v", err)
	}
	e := New(ProvisionFailed, Machine{Name: "fail-host"})
	err = d.Send(context.Background(), e)
	if err == nil {
		t.Error("expected error for 500 response")
	} else if !strings.Contains(err.Error(), "upstream failure") {
		t.Errorf("error = %q, expected response body snippet", err)
	}
}

func TestDispatcherSend_NilEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d, err := NewDispatcher(srv.URL, slog.Default())
	if err != nil {
		t.Fatalf("NewDispatcher() error: %v", err)
	}

	if err := d.Send(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil event")
	}
}

func TestNewDispatcher_InvalidURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"no scheme", "example.com/webhook"},
		{"empty", ""},
		{"whitespace only", "   "},
		{"ftp scheme", "ftp://example.com/webhook"},
		{"http external disallowed", "http://example.com/webhook"},
		{"private ip disallowed", "https://10.0.0.1/webhook"},
		{"loopback https disallowed", "https://127.0.0.1/webhook"},
		{"hostname rejected", "https://evil.example.com/webhook"},
		{"userinfo", "https://user:pass@example.com/webhook"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewDispatcher(tc.url, nil)
			if err == nil {
				t.Error("expected error for invalid URL")
			}
		})
	}
}

func TestNewDispatcher_NilLogger(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d, err := NewDispatcher(srv.URL, nil)
	if err != nil {
		t.Fatalf("NewDispatcher() error: %v", err)
	}
	e := New(ProvisionStarted, Machine{Name: "nil-logger"})
	if err := d.Send(context.Background(), e); err != nil {
		t.Errorf("Send() with nil logger: %v", err)
	}
}
