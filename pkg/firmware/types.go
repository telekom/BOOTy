// Package firmware collects firmware version information from sysfs.
package firmware

import "time"

// Report holds firmware versions collected during provisioning.
type Report struct {
	BIOS        Version           `json:"bios"`
	BMC         Version           `json:"bmc,omitempty"`
	NICs        []NICFirmware     `json:"nics,omitempty"`
	Storage     []StorageFirmware `json:"storage,omitempty"`
	CollectedAt time.Time         `json:"collectedAt"`
}

// Version describes a firmware component's version information.
type Version struct {
	Component string `json:"component"`
	Version   string `json:"version"`
	Date      string `json:"date,omitempty"`
	Vendor    string `json:"vendor,omitempty"`
}

// NICFirmware holds firmware info for a network interface.
type NICFirmware struct {
	Interface string `json:"interface"`
	Driver    string `json:"driver"`
	Version   string `json:"version"`
	PCIAddr   string `json:"pciAddr,omitempty"`
}

// StorageFirmware holds firmware info for a storage controller.
type StorageFirmware struct {
	Controller string `json:"controller"`
	Model      string `json:"model,omitempty"`
	Version    string `json:"version"`
}

// Policy defines minimum firmware version requirements for validation.
type Policy struct {
	MinBIOSVersion string            `json:"minBiosVersion,omitempty"`
	MinBMCVersion  string            `json:"minBmcVersion,omitempty"`
	MinNICVersions map[string]string `json:"minNicVersions,omitempty"` // driver → min version
}

// ValidationResult holds the outcome of a single firmware version check.
type ValidationResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "pass" or "fail"
	Message string `json:"message"`
}
