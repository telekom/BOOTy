package event

import (
	"encoding/json"
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
	e := New(ProvisionFailed, m).WithDetails(map[string]string{
		"step":  "image-streaming",
		"error": "connection reset",
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
