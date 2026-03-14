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
		t.Errorf("OS = %q", info.OS)
	}
	if info.Arch != runtime.GOARCH {
		t.Errorf("Arch = %q", info.Arch)
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
	if decoded.GoVersion != info.GoVersion {
		t.Errorf("GoVersion = %q", decoded.GoVersion)
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
	if string(FlavorFull) != "full" {
		t.Error("FlavorFull")
	}
	if string(FlavorGoBGP) != "gobgp" {
		t.Error("FlavorGoBGP")
	}
	if string(FlavorSlim) != "slim" {
		t.Error("FlavorSlim")
	}
	if string(FlavorMicro) != "micro" {
		t.Error("FlavorMicro")
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
