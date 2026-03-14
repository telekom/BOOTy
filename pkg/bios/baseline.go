package bios

// Baseline is a golden BIOS configuration for comparison.
type Baseline struct {
	Vendor   Vendor            `json:"vendor"`
	Model    string            `json:"model,omitempty"`
	Settings map[string]string `json:"settings"`
}

// Diff holds the result of comparing a baseline to a live state.
type Diff struct {
	Matches bool        `json:"matches"`
	Changes []DiffEntry `json:"changes,omitempty"`
}

// DiffEntry is a single BIOS setting difference.
type DiffEntry struct {
	Name     string `json:"name"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

// Compare compares a baseline against a live BIOS state.
func Compare(baseline *Baseline, state *State) *Diff {
	diff := &Diff{Matches: true}
	for name, expected := range baseline.Settings {
		setting, ok := state.Settings[name]
		actual := ""
		if ok {
			actual = setting.CurrentValue
		}
		if !ok || actual != expected {
			diff.Changes = append(diff.Changes, DiffEntry{
				Name:     name,
				Expected: expected,
				Actual:   actual,
			})
			diff.Matches = false
		}
	}
	return diff
}
