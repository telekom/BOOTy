package event

import "time"

// Type identifies a provisioning lifecycle event.
type Type string

// Provisioning lifecycle event types.
const (
	ProvisionStarted     Type = "provision.started"
	ProvisionCompleted   Type = "provision.completed"
	ProvisionFailed      Type = "provision.failed"
	DeprovisionStarted   Type = "deprovision.started"
	DeprovisionCompleted Type = "deprovision.completed"
	HealthCritical       Type = "health.critical"
	HealthWarning        Type = "health.warning"
	RescueActivated      Type = "rescue.activated"
	FirmwareMismatch     Type = "firmware.mismatch"
	AttestationFailed    Type = "attestation.failed"
)

// Machine identifies the target of an event.
type Machine struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	RedfishHost string `json:"redfishHost,omitempty"`
	Address     string `json:"address,omitempty"`
}

// Event represents a provisioning lifecycle event.
type Event struct {
	Type      Type           `json:"event"`
	Timestamp time.Time      `json:"timestamp"`
	Machine   Machine        `json:"machine"`
	Details   map[string]any `json:"details,omitempty"`
}

// New creates an event with the current timestamp.
func New(t Type, m Machine) *Event {
	return &Event{
		Type:      t,
		Timestamp: time.Now().UTC(),
		Machine:   m,
	}
}

// WithDetails adds details to the event and returns itself for chaining.
func (e *Event) WithDetails(details map[string]any) *Event {
	e.Details = details
	return e
}
