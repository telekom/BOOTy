package config

// IsDeprovisionMode reports whether mode selects the deprovision pipeline,
// including legacy aliases.
func IsDeprovisionMode(mode string) bool {
	switch mode {
	case "deprovision", "hard", "soft", "soft-deprovision":
		return true
	default:
		return false
	}
}
