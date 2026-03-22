//go:build !linux

package vlan

import "fmt"

// Setup is not supported on non-Linux platforms.
func Setup(_ *Config) (string, error) {
	return "", fmt.Errorf("vlan setup is only supported on linux")
}

// Teardown is not supported on non-Linux platforms.
func Teardown(_ string, _ int) error {
	return fmt.Errorf("vlan teardown is only supported on linux")
}

// TeardownConfig is not supported on non-Linux platforms.
func TeardownConfig(_ *Config) error {
	return fmt.Errorf("vlan teardown is only supported on linux")
}

// SetupAll is not supported on non-Linux platforms.
func SetupAll(_ []Config) ([]string, error) {
	return nil, fmt.Errorf("vlan setup is only supported on linux")
}

// TeardownAll is a no-op on non-Linux platforms.
func TeardownAll(_ []Config) {}
