// events package tests.
package events

import (
	"testing"
)

func TestEmitterEmitAndEvents(t *testing.T) {
	e := NewEmitter()
	e.Emit(EventProvStarted, "", "provisioning started", 0)
	e.Emit(EventStepStarted, "detect-disk", "detecting disk", 0.1)
	e.Emit(EventStepCompleted, "detect-disk", "disk detected", 0.2)

	events := e.Events()
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
	if events[0].Type != EventProvStarted {
		t.Errorf("event[0].Type = %s, want %s", events[0].Type, EventProvStarted)
	}
	if events[2].Step != "detect-disk" {
		t.Errorf("event[2].Step = %s, want detect-disk", events[2].Step)
	}
}

func TestEmitterLastEvent(t *testing.T) {
	e := NewEmitter()
	if e.LastEvent() != nil {
		t.Error("expected nil for empty emitter")
	}

	e.Emit(EventProvStarted, "", "started", 0)
	e.Emit(EventProvCompleted, "", "done", 1.0)

	last := e.LastEvent()
	if last == nil {
		t.Fatal("expected non-nil last event")
	}
	if last.Type != EventProvCompleted {
		t.Errorf("last.Type = %s, want %s", last.Type, EventProvCompleted)
	}
	if last.Progress != 1.0 {
		t.Errorf("last.Progress = %f, want 1.0", last.Progress)
	}
}

func TestEmitterMarshal(t *testing.T) {
	e := NewEmitter()
	e.Emit(EventStepFailed, "stream-image", "connection reset", 0.5)

	data, err := e.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty JSON")
	}
}

func TestMarshalEvent(t *testing.T) {
	ev := ProvisionEvent{
		Type:    EventProvFailed,
		Step:    "mount-root",
		Message: "mount failed",
	}
	data, err := MarshalEvent(&ev)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty JSON")
	}
}
