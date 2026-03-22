//go:build !linux

package vlan

import "fmt"

// Setup is not supported on non-Linux platforms.
func Setup(_ Config) (string, error) {
	return "", fmt.Errorf("VLAN setup requires Linux")
}

// Teardown is not supported on non-Linux platforms.
func Teardown(_ string, _ int) error {
	return fmt.Errorf("VLAN teardown requires Linux")
}

// TeardownConfig is not supported on non-Linux platforms.
func TeardownConfig(_ *Config) error {
	return fmt.Errorf("VLAN teardown requires Linux")
}

// SetupAll is not supported on non-Linux platforms.
func SetupAll(_ []Config) ([]string, error) {
	return nil, fmt.Errorf("VLAN setup requires Linux")
}

// TeardownAll is not supported on non-Linux platforms.
func TeardownAll(_ []Config) {}
