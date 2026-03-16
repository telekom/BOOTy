package buildinfo

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func TestGet(t *testing.T) {
	info := Get()
	if info.GoVersion != runtime.Version() {
		t.Errorf("GoVersion = %q, want %q", info.GoVersion, runtime.Version())
	}
	if info.OS != runtime.GOOS {
		t.Errorf("OS = %q, want %q", info.OS, runtime.GOOS)
	}
	if info.Arch != runtime.GOARCH {
		t.Errorf("Arch = %q, want %q", info.Arch, runtime.GOARCH)
	}
}

func TestInfo_JSON(t *testing.T) {
	info := Get()
	data, err := info.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded Info
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != info.Version {
		t.Errorf("Version = %q, want %q", decoded.Version, info.Version)
	}
	if decoded.Commit != info.Commit {
		t.Errorf("Commit = %q, want %q", decoded.Commit, info.Commit)
	}
	if decoded.BuildDate != info.BuildDate {
		t.Errorf("BuildDate = %q, want %q", decoded.BuildDate, info.BuildDate)
	}
	if decoded.GoVersion != info.GoVersion {
		t.Errorf("GoVersion = %q, want %q", decoded.GoVersion, info.GoVersion)
	}
	if decoded.OS != info.OS {
		t.Errorf("OS = %q, want %q", decoded.OS, info.OS)
	}
	if decoded.Arch != info.Arch {
		t.Errorf("Arch = %q, want %q", decoded.Arch, info.Arch)
	}
	if decoded.Flavor != info.Flavor {
		t.Errorf("Flavor = %q, want %q", decoded.Flavor, info.Flavor)
	}
}

func TestDependencies(t *testing.T) {
	deps := Dependencies()
	// In test binary, should have at least some deps.
	if deps == nil {
		t.Skip("no build info available")
	}
}

func TestEstimateComponents(t *testing.T) {
	components := EstimateComponents()
	if len(components) == 0 {
		t.Error("no components")
	}

	hasRequired := false
	for _, c := range components {
		if c.Required {
			hasRequired = true
		}
		if c.SizeMB <= 0 {
			t.Errorf("component %q has zero size", c.Component)
		}
	}
	if !hasRequired {
		t.Error("no required components")
	}
}

func TestTotalEstimate(t *testing.T) {
	components := EstimateComponents()
	total := TotalEstimate(components)
	if total <= 0 {
		t.Errorf("total = %f", total)
	}
}

func TestFlavorConstants(t *testing.T) {
	if got := string(FlavorFull); got != "full" {
		t.Errorf("FlavorFull = %q, want %q", got, "full")
	}
	if got := string(FlavorGoBGP); got != "gobgp" {
		t.Errorf("FlavorGoBGP = %q, want %q", got, "gobgp")
	}
	if got := string(FlavorSlim); got != "slim" {
		t.Errorf("FlavorSlim = %q, want %q", got, "slim")
	}
	if got := string(FlavorMicro); got != "micro" {
		t.Errorf("FlavorMicro = %q, want %q", got, "micro")
	}
}

func TestLDFlags(t *testing.T) {
	flags := LDFlags("v1.0.0", "abc123", "2025-01-01", "gobgp")
	if !strings.Contains(flags, "-s -w") {
		t.Error("missing strip flags")
	}
	if !strings.Contains(flags, "version=v1.0.0") {
		t.Error("missing version")
	}
	if !strings.Contains(flags, "commit=abc123") {
		t.Error("missing commit")
	}
	if !strings.Contains(flags, "flavor=gobgp") {
		t.Error("missing flavor")
	}
}

func TestTotalEstimate_Empty(t *testing.T) {
	total := TotalEstimate(nil)
	if total != 0 {
		t.Errorf("total = %f, want 0", total)
	}
}

func TestGet_Defaults(t *testing.T) {
	info := Get()
	if info.Version != "dev" {
		t.Errorf("default Version = %q, want %q", info.Version, "dev")
	}
	if info.Commit != "unknown" {
		t.Errorf("default Commit = %q, want %q", info.Commit, "unknown")
	}
	if info.BuildDate != "unknown" {
		t.Errorf("default BuildDate = %q, want %q", info.BuildDate, "unknown")
	}
	if info.Flavor != FlavorFull {
		t.Errorf("default Flavor = %q, want %q", info.Flavor, FlavorFull)
	}
}

func TestLDFlags_AllFields(t *testing.T) {
	flags := LDFlags("v2.0.0", "def456", "2026-01-15", "micro")
	expected := []string{
		"version=v2.0.0",
		"commit=def456",
		"buildDate=2026-01-15",
		"flavor=micro",
		"-s -w",
	}
	for _, want := range expected {
		if !strings.Contains(flags, want) {
			t.Errorf("LDFlags missing %q in %q", want, flags)
		}
	}
}

func TestEstimateComponents_Fields(t *testing.T) {
	for _, c := range EstimateComponents() {
		if c.Component == "" {
			t.Error("component has empty name")
		}
		if c.SizeMB <= 0 {
			t.Errorf("component %q has non-positive size: %f", c.Component, c.SizeMB)
		}
	}
}
