package serialconsole

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// ArtifactSchemaVersion is the version of the resolution artifact contract
// shared with CAPRF. Increment it for any breaking field change; CAPRF
// consumers must reject artifacts with an unknown major schema version.
const ArtifactSchemaVersion = 1

// ArtifactKind identifies the artifact payload for CAPRF consumers.
const ArtifactKind = "booty.telekom.de/serial-console-resolution"

// ArtifactFileName is the canonical file name of the resolution artifact both
// in the initramfs run directory and on the provisioned root filesystem.
const ArtifactFileName = "serial-console.json"

// Source identifies where a piece of console evidence came from.
type Source string

// Evidence sources, ordered from the strongest (operator intent) to the
// weakest (host identity, which is never authoritative on its own).
const (
	SourceOverride     Source = "explicit-override"
	SourceKernelParams Source = "extra-kernel-params"
	SourceACPISPCR     Source = "acpi-spcr"
	SourceDeviceTree   Source = "device-tree"
	SourceBootConsole  Source = "boot-console"
	SourceSysfsUART    Source = "sysfs-uart"
	SourceDMI          Source = "dmi"
)

// State is the outcome of a console resolution.
type State string

const (
	// StateResolved means exactly one console was selected.
	StateResolved State = "resolved"
	// StateDisabled means the operator explicitly disabled serial console
	// configuration.
	StateDisabled State = "disabled"
	// StateNoEvidence means no source claimed a serial console, so no serial
	// console is configured at all.
	StateNoEvidence State = "no-evidence"
	// StateAmbiguous means sources disagreed. Resolution fails closed.
	StateAmbiguous State = "ambiguous"
)

const (
	maxEvidenceEntries = 32
	maxDetailLen       = 256
)

// Evidence is one bounded, non-sensitive observation about the host console.
type Evidence struct {
	Source Source `json:"source"`
	// Device is the tty name this evidence points at, when it could be
	// mapped to one.
	Device string `json:"device,omitempty"`
	Baud   int    `json:"baud,omitempty"`
	Parity string `json:"parity,omitempty"`
	Bits   int    `json:"bits,omitempty"`
	Flow   string `json:"flow,omitempty"`
	// Address is the firmware-declared UART address, when known.
	Address string `json:"address,omitempty"`
	// Selectable marks evidence that may select a console on its own. Host
	// identity evidence (DMI) is recorded but never selectable.
	Selectable bool `json:"selectable"`
	// Path is the read-only file this evidence was derived from.
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Resolution is the full outcome of a console resolution, including the
// evidence trail that justifies it.
type Resolution struct {
	State      State        `json:"state"`
	Console    *Spec        `json:"console,omitempty"`
	SelectedBy Source       `json:"selectedBy,omitempty"`
	BaudSource Source       `json:"baudSource,omitempty"`
	Reason     string       `json:"reason"`
	Conflicts  []string     `json:"conflicts,omitempty"`
	Evidence   []Evidence   `json:"evidence"`
	Host       HostIdentity `json:"host"`
	// Degraded records non-fatal gaps, for example firmware evidence that
	// could not be mapped to a tty but did not contradict the selection.
	Degraded []string `json:"degraded,omitempty"`
}

// HostIdentity is the non-sensitive DMI identity of the host, recorded so
// CAPRF can correlate console resolutions across a hardware fleet.
type HostIdentity struct {
	Vendor      string `json:"vendor,omitempty"`
	Product     string `json:"product,omitempty"`
	Board       string `json:"board,omitempty"`
	BIOSVersion string `json:"biosVersion,omitempty"`
}

// Artifact is the machine-readable resolution document persisted for CAPRF.
type Artifact struct {
	SchemaVersion int        `json:"schemaVersion"`
	Kind          string     `json:"kind"`
	GeneratedAt   string     `json:"generatedAt"`
	Resolution    Resolution `json:"resolution"`
	// KernelParam is the single console= parameter written to the target
	// boot configuration, empty when no serial console is configured.
	KernelParam string `json:"kernelParam,omitempty"`
	// GettyUnit is the systemd unit enabled on the target, empty when no
	// serial console is configured.
	GettyUnit string `json:"gettyUnit,omitempty"`
	// Error carries the fail-closed reason when the state is ambiguous.
	Error string `json:"error,omitempty"`
}

// KernelParam renders the single kernel console parameter for the resolution,
// or an empty string when no serial console must be configured.
func (r *Resolution) KernelParam() string {
	if r.Console == nil {
		return ""
	}
	return r.Console.KernelParam()
}

// GettyUnit renders the serial getty unit for the resolution, or an empty
// string when no serial console must be configured.
func (r *Resolution) GettyUnit() string {
	if r.Console == nil {
		return ""
	}
	return r.Console.GettyUnit()
}

// LogAttrs returns stable key/value pairs for structured logging.
func (r *Resolution) LogAttrs() []any {
	attrs := []any{"state", string(r.State), "reason", r.Reason}
	if r.Console != nil {
		attrs = append(attrs, "device", r.Console.Device, "baud", r.Console.Baud, "selectedBy", string(r.SelectedBy))
	}
	if len(r.Conflicts) > 0 {
		attrs = append(attrs, "conflicts", strings.Join(r.Conflicts, "; "))
	}
	return attrs
}

// NewArtifact builds the CAPRF artifact for a resolution.
func NewArtifact(res *Resolution, now time.Time, resolveErr error) Artifact {
	artifact := Artifact{
		SchemaVersion: ArtifactSchemaVersion,
		Kind:          ArtifactKind,
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Resolution:    *res,
		KernelParam:   res.KernelParam(),
		GettyUnit:     res.GettyUnit(),
	}
	if resolveErr != nil {
		artifact.Error = truncate(resolveErr.Error(), maxDetailLen)
	}
	return artifact
}

// MarshalArtifact renders a bounded, deterministic JSON document.
func MarshalArtifact(artifact *Artifact) ([]byte, error) {
	// Copy every slice so that marshaling never mutates the caller's resolution.
	out := *artifact
	out.Resolution.Reason = truncate(artifact.Resolution.Reason, maxDetailLen)
	out.Resolution.Evidence = boundEvidence(artifact.Resolution.Evidence)
	out.Resolution.Conflicts = boundStrings(artifact.Resolution.Conflicts)
	out.Resolution.Degraded = boundStrings(artifact.Resolution.Degraded)
	if len(artifact.Resolution.Evidence) > maxEvidenceEntries {
		out.Resolution.Degraded = append(out.Resolution.Degraded,
			fmt.Sprintf("evidence truncated to %d entries", maxEvidenceEntries))
		sort.Strings(out.Resolution.Degraded)
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal serial console artifact: %w", err)
	}
	return append(data, '\n'), nil
}

// boundStrings returns a truncated, sorted copy of the given values.
func boundStrings(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = truncate(v, maxDetailLen)
	}
	sort.Strings(out)
	return out
}

func boundEvidence(entries []Evidence) []Evidence {
	if len(entries) > maxEvidenceEntries {
		entries = entries[:maxEvidenceEntries]
	}
	out := make([]Evidence, len(entries))
	copy(out, entries)
	for i := range out {
		out[i].Detail = truncate(out[i].Detail, maxDetailLen)
		out[i].Path = truncate(out[i].Path, maxDetailLen)
	}
	return out
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
