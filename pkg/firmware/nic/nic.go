package nic

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Vendor identifies a NIC vendor.
type Vendor string

const (
	// VendorMellanox is the Mellanox/NVIDIA vendor.
	VendorMellanox Vendor = "mellanox"
	// VendorIntel is the Intel vendor.
	VendorIntel Vendor = "intel"
	// VendorBroadcom is the Broadcom vendor.
	VendorBroadcom Vendor = "broadcom"
	// VendorUnknown is returned for unrecognized PCI vendor IDs.
	VendorUnknown Vendor = "unknown"
)

// pciVendorMap maps PCI vendor IDs to Vendor constants.
var pciVendorMap = map[string]Vendor{
	"0x15b3": VendorMellanox,
	"0x8086": VendorIntel,
	"0x14e4": VendorBroadcom,
}

// FirmwareManager is the vendor-agnostic interface for NIC firmware operations.
type FirmwareManager interface {
	// Vendor returns the vendor this manager handles.
	Vendor() Vendor

	// Supported checks if this manager can handle the given NIC.
	Supported(nic *Identifier) bool

	// Capture reads all firmware parameters from the NIC.
	Capture(nic *Identifier) (*FirmwareState, error)

	// Apply sets firmware parameters on the NIC.
	Apply(nic *Identifier, changes []FlagChange) error
}

// Identifier identifies a NIC.
type Identifier struct {
	PCIAddress string `json:"pciAddress"`
	Interface  string `json:"interface,omitempty"`
	VendorID   string `json:"vendorId"`
	DeviceID   string `json:"deviceId"`
	Driver     string `json:"driver,omitempty"`
}

// FirmwareState is a snapshot of NIC firmware parameters.
type FirmwareState struct {
	NIC        Identifier           `json:"nic"`
	Parameters map[string]Parameter `json:"parameters"`
	FWVersion  string               `json:"fwVersion,omitempty"`
}

// Parameter is a single firmware parameter.
type Parameter struct {
	Name     string `json:"name"`
	Current  string `json:"current"`
	Default  string `json:"default,omitempty"`
	Type     string `json:"type,omitempty"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}

// FlagChange requests a change to a firmware parameter.
type FlagChange struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Baseline is a golden firmware configuration.
type Baseline struct {
	Vendor     Vendor            `json:"vendor"`
	DeviceID   string            `json:"deviceId,omitempty"`
	Parameters map[string]string `json:"parameters"`
}

// Diff compares a baseline against a live FirmwareState.
type Diff struct {
	NIC     Identifier   `json:"nic"`
	Changes []DiffChange `json:"changes"`
	Match   bool         `json:"match"`
}

// DiffChange is a single parameter difference.
type DiffChange struct {
	Name     string `json:"name"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

// Compare compares a baseline against a live firmware state.
func Compare(baseline *Baseline, state *FirmwareState) *Diff {
	diff := &Diff{
		NIC:   state.NIC,
		Match: true,
	}

	for name, expected := range baseline.Parameters {
		param, ok := state.Parameters[name]
		if !ok {
			diff.Changes = append(diff.Changes, DiffChange{
				Name:     name,
				Expected: expected,
				Actual:   "(missing)",
			})
			diff.Match = false
			continue
		}
		if param.Current != expected {
			diff.Changes = append(diff.Changes, DiffChange{
				Name:     name,
				Expected: expected,
				Actual:   param.Current,
			})
			diff.Match = false
		}
	}
	return diff
}

// DetectVendor reads the PCI vendor ID from sysfs.
func DetectVendor(pciAddress string) Vendor {
	return detectVendorFrom("/sys/bus/pci/devices", pciAddress)
}

func detectVendorFrom(basePath, pciAddress string) Vendor {
	vendorPath := filepath.Join(basePath, pciAddress, "vendor")
	data, err := os.ReadFile(vendorPath)
	if err != nil {
		return VendorUnknown
	}
	vendorID := strings.TrimSpace(string(data))
	if v, ok := pciVendorMap[vendorID]; ok {
		return v
	}
	return VendorUnknown
}

// Registry manages vendor-specific firmware managers.
type Registry struct {
	managers []FirmwareManager
}

// NewRegistry creates a firmware manager registry.
func NewRegistry(managers ...FirmwareManager) *Registry {
	return &Registry{managers: managers}
}

// ForNIC returns the appropriate firmware manager for a NIC.
func (r *Registry) ForNIC(nic *Identifier) (FirmwareManager, error) {
	for _, mgr := range r.managers {
		if mgr.Supported(nic) {
			return mgr, nil
		}
	}
	return nil, fmt.Errorf("no firmware manager for NIC %s (vendor=%s)", nic.PCIAddress, nic.VendorID)
}
