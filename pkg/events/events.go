// events package provides structured provisioning event emission.
package events

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// EventType classifies provision events.
type EventType string

// Event type constants.
const (
	EventStepStarted   EventType = "step.started"
	EventStepCompleted EventType = "step.completed"
	EventStepFailed    EventType = "step.failed"
	EventProvStarted   EventType = "provision.started"
	EventProvCompleted EventType = "provision.completed"
	EventProvFailed    EventType = "provision.failed"
)

// ProvisionEvent represents a structured provisioning event.
type ProvisionEvent struct {
	Type      EventType `json:"type"`
	Step      string    `json:"step,omitempty"`
	Message   string    `json:"message"`
	Progress  float64   `json:"progress,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// Emitter collects events during provisioning.
type Emitter struct {
	mu     sync.Mutex
	events []ProvisionEvent
}

// NewEmitter creates an event emitter.
func NewEmitter() *Emitter {
	return &Emitter{}
}

// Emit records a new event.
func (e *Emitter) Emit(eventType EventType, step, message string, progress float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, ProvisionEvent{
		Type:      eventType,
		Step:      step,
		Message:   message,
		Progress:  progress,
		Timestamp: time.Now(),
	})
}

// Events returns a copy of all recorded events.
func (e *Emitter) Events() []ProvisionEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]ProvisionEvent, len(e.events))
	copy(out, e.events)
	return out
}

// LastEvent returns the most recently emitted event, or nil if none.
func (e *Emitter) LastEvent() *ProvisionEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.events) == 0 {
		return nil
	}
	ev := e.events[len(e.events)-1]
	return &ev
}

// Marshal serializes all events to JSON.
func (e *Emitter) Marshal() ([]byte, error) {
	events := e.Events()
	data, err := json.Marshal(events)
	if err != nil {
		return nil, fmt.Errorf("marshaling events: %w", err)
	}
	return data, nil
}

// MarshalEvent serializes a single event to JSON.
func MarshalEvent(ev ProvisionEvent) ([]byte, error) {
	data, err := json.Marshal(ev)
	if err != nil {
		return nil, fmt.Errorf("marshaling event: %w", err)
	}
	return data, nil
}
