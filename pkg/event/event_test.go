package event

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	var received Event
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("missing content-type header")
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
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
	}))
	defer srv.Close()

	d, err := NewDispatcher(srv.URL, slog.Default())
	if err != nil {
		t.Fatalf("NewDispatcher() error: %v", err)
	}
	e := New(ProvisionFailed, Machine{Name: "fail-host"})
	if err := d.Send(context.Background(), e); err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestNewDispatcher_InvalidURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"no scheme", "example.com/webhook"},
		{"empty", ""},
		{"ftp scheme", "ftp://example.com/webhook"},
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
