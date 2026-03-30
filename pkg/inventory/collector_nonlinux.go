//go:build !linux

package inventory

import "time"

// Collect returns an empty hardware inventory on non-linux platforms.
func Collect() (*HardwareInventory, error) {
	return &HardwareInventory{Timestamp: time.Now()}, nil
}

// ScanGPUs returns no GPU data on non-linux platforms.
func ScanGPUs() []GPUInfo {
	return nil
}

// CollectThermal returns no thermal sensor data on non-linux platforms.
func CollectThermal() ThermalInfo {
	return ThermalInfo{}
}

// ScanUSBDevices returns no USB inventory on non-linux platforms.
func ScanUSBDevices() []USBDeviceInfo {
	return nil
}

// ClassifyUSBDevice returns unknown on non-linux platforms.
func ClassifyUSBDevice(_ string) string {
	return "unknown"
}
