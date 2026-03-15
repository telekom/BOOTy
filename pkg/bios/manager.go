package bios

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// Vendor represents a server vendor detected from DMI.
type Vendor string

// ErrNotImplemented is returned when a BIOS operation is not yet implemented.
var ErrNotImplemented = errors.New("bios operation not implemented")

const (
	// VendorHPE is for HPE ProLiant servers.
	VendorHPE Vendor = "HPE"
	// VendorLenovo is for Lenovo ThinkSystem servers.
	VendorLenovo Vendor = "Lenovo"
	// VendorSupermicro is for Supermicro servers.
	VendorSupermicro Vendor = "Supermicro"
	// VendorDell is for Dell PowerEdge servers.
	VendorDell Vendor = "Dell Inc."
	// VendorUnknown is for unrecognized vendors.
	VendorUnknown Vendor = "Unknown"
)

// Manager provides vendor-specific BIOS operations.
type Manager interface {
	// Vendor returns the server vendor this manager handles.
	Vendor() Vendor

	// Capture reads all BIOS settings and returns a snapshot.
	Capture(ctx context.Context) (*State, error)

	// Apply sets BIOS attributes. Returns attributes requiring reboot.
	Apply(ctx context.Context, changes []SettingChange) (rebootRequired []string, err error)

	// Reset restores BIOS to factory defaults.
	Reset(ctx context.Context) error
}

// State represents the full BIOS configuration snapshot.
type State struct {
	Vendor   Vendor             `json:"vendor"`
	Model    string             `json:"model"`
	Version  string             `json:"biosVersion"`
	Settings map[string]Setting `json:"settings"`
	OEMData  map[string]string  `json:"oemData,omitempty"`
}

// Setting represents a single BIOS attribute.
type Setting struct {
	Name          string   `json:"name"`
	CurrentValue  string   `json:"currentValue"`
	DefaultValue  string   `json:"defaultValue,omitempty"`
	PendingValue  string   `json:"pendingValue,omitempty"`
	Type          string   `json:"type"`
	AllowedValues []string `json:"allowedValues,omitempty"`
	ReadOnly      bool     `json:"readOnly,omitempty"`
}

// SettingChange requests a BIOS attribute modification.
type SettingChange struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// managerFactory is registered by vendor packages via init().
var managerFactories = map[Vendor]func(*slog.Logger) Manager{}

// RegisterManager registers a vendor-specific manager factory.
func RegisterManager(vendor Vendor, factory func(*slog.Logger) Manager) {
	managerFactories[vendor] = factory
}

// NewManager returns a Manager for the given vendor, or an error if unsupported.
func NewManager(vendor Vendor, log *slog.Logger) (Manager, error) {
	factory, ok := managerFactories[vendor]
	if !ok {
		return nil, fmt.Errorf("no BIOS manager registered for vendor %q", vendor)
	}
	return factory(log), nil
}
