// Package buildinfo provides build metadata and size analysis utilities.
package buildinfo

import (
	"encoding/json"
	"fmt"
	"runtime"
	"runtime/debug"
)

// Flavor represents an initramfs build flavor.
type Flavor string

const (
	// FlavorFull includes FRR, tools, and all features (~80 MB).
	FlavorFull Flavor = "full"
	// FlavorGoBGP uses GoBGP instead of FRR (~40 MB).
	FlavorGoBGP Flavor = "gobgp"
	// FlavorSlim is DHCP-only with minimal tools (~15 MB).
	FlavorSlim Flavor = "slim"
	// FlavorMicro is pure Go with no external tools (~10 MB).
	FlavorMicro Flavor = "micro"
)

// Info holds build-time metadata.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"buildDate"`
	GoVersion string `json:"goVersion"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Flavor    Flavor `json:"flavor"`
}

// ldflags-set variables.
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
	flavor    = "full"
)

// Get returns the current build info.
func Get() *Info {
	return &Info{
		Version:   version,
		Commit:    commit,
		BuildDate: buildDate,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		Flavor:    Flavor(flavor),
	}
}

// DepInfo holds dependency analysis data.
type DepInfo struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Sum     string `json:"sum,omitempty"`
}

// Dependencies returns the list of compiled-in Go module dependencies.
func Dependencies() []DepInfo {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}
	deps := make([]DepInfo, 0, len(bi.Deps))
	for _, d := range bi.Deps {
		dep := DepInfo{
			Path:    d.Path,
			Version: d.Version,
		}
		if d.Sum != "" {
			dep.Sum = d.Sum
		}
		deps = append(deps, dep)
	}
	return deps
}

// SizeEstimate provides approximate binary size estimates per component.
type SizeEstimate struct {
	Component string  `json:"component"`
	SizeMB    float64 `json:"sizeMB"`
	Required  bool    `json:"required"`
	BuildTag  string  `json:"buildTag,omitempty"`
}

// EstimateComponents returns known size estimates for major dependencies.
func EstimateComponents() []SizeEstimate {
	return []SizeEstimate{
		{"go-runtime", 5.0, true, ""},
		{"gobgp-v3", 8.0, false, "!micro,!slim"},
		{"go-containerregistry", 3.0, false, "!micro"},
		{"cobra+viper", 1.0, true, ""},
		{"crypto/tls", 1.5, true, ""},
	}
}

// TotalEstimate returns the approximate total binary size in MB.
func TotalEstimate(components []SizeEstimate) float64 {
	var total float64
	for _, c := range components {
		total += c.SizeMB
	}
	return total
}

// JSON returns the build info as JSON bytes.
func (i *Info) JSON() ([]byte, error) {
	data, err := json.Marshal(i)
	if err != nil {
		return nil, fmt.Errorf("marshal build info: %w", err)
	}
	return data, nil
}

// LDFlags returns the recommended ldflags for a production build.
func LDFlags(ver, sha, date, flv string) string {
	pkg := "github.com/telekom/BOOTy/pkg/buildinfo"
	return fmt.Sprintf("-s -w -X %s.version=%s -X %s.commit=%s -X %s.buildDate=%s -X %s.flavor=%s",
		pkg, ver, pkg, sha, pkg, date, pkg, flv)
}
