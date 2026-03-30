//go:build !linux

package firmware

import "time"

// Collect returns a minimal firmware report on non-linux platforms.
func Collect() (*Report, error) {
	return &Report{
		BIOS:        Version{Component: "bios", Version: "unknown"},
		CollectedAt: time.Now(),
	}, nil
}

// Validate returns no validation errors on non-linux platforms.
func Validate(_ *Report, _ Policy) []ValidationResult {
	return nil
}
