package serialconsole

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrAmbiguous is returned when the available evidence does not identify
// exactly one serial console. Callers must fail closed on this error.
var ErrAmbiguous = errors.New("serial console evidence is ambiguous")

// DefaultSysRoot and DefaultProcRoot are the read-only roots inspected by the
// resolver on a live system.
const (
	DefaultSysRoot  = "/sys"
	DefaultProcRoot = "/proc"
)

// Resolver resolves the serial console from operator intent and read-only
// firmware evidence.
//
// A zero Resolver inspects the live host. Tests set SysRoot and ProcRoot to a
// fixture tree and may set ReadFile to audit regular-file reads; directory and symlink enumeration uses the OS APIs directly.
type Resolver struct {
	// SysRoot defaults to /sys.
	SysRoot string
	// ProcRoot defaults to /proc.
	ProcRoot string
	// Override is the explicit operator console specification, for example
	// "ttyS1,115200n8" or "none". It always wins.
	Override string
	// OverrideSource labels where the override came from in the evidence
	// trail. It defaults to SourceOverride.
	OverrideSource Source
	// ExtraKernelParams is the operator-supplied kernel command line of the
	// provisioned system. A console= parameter in there is operator intent
	// and is treated as an override.
	ExtraKernelParams string
	// ReadFile defaults to os.ReadFile. It exists so tests can assert the
	// resolver only ever reads, and never touches /dev.
	ReadFile func(string) ([]byte, error)
}

func (r *Resolver) sysRoot() string {
	if r.SysRoot == "" {
		return DefaultSysRoot
	}
	return r.SysRoot
}

func (r *Resolver) procRoot() string {
	if r.ProcRoot == "" {
		return DefaultProcRoot
	}
	return r.ProcRoot
}

func (r *Resolver) deviceTreeRoot() string {
	return filepath.Join(r.sysRoot(), "firmware", "devicetree", "base")
}

func (r *Resolver) readFileFn() func(string) ([]byte, error) {
	if r.ReadFile != nil {
		return r.ReadFile
	}
	return os.ReadFile
}

// tier is one precedence level of the resolution algorithm.
type tier struct {
	source    Source
	evidence  []Evidence
	degraded  []string
	conflicts []string
}

// Resolve gathers evidence and selects at most one serial console.
//
// Precedence: explicit override, ACPI SPCR, device-tree stdout-path, the
// console the running kernel was booted with, and finally a uniquely
// enumerated UART. Within the first tier that yields a candidate, conflicting
// candidates fail closed with ErrAmbiguous instead of picking a winner.
func (r *Resolver) Resolve() (Resolution, error) {
	res := Resolution{State: StateNoEvidence, Reason: "no source declared a serial console"}
	identity, dmiEvidence := r.readHostIdentity()
	res.Host = identity

	ports, err := r.enumeratePorts()
	if err != nil {
		res.Degraded = append(res.Degraded, err.Error())
	}

	override, disabled, err := r.overrideTier()
	if err != nil {
		res.Evidence = append(res.Evidence, override.evidence...)
		res.Evidence = append(res.Evidence, dmiEvidence...)
		res.Conflicts = append(res.Conflicts, err.Error())
		return failClosed(&res, fmt.Errorf("%w: %w", ErrAmbiguous, err))
	}
	if disabled {
		res.State = StateDisabled
		res.SelectedBy = SourceOverride
		res.Reason = "serial console configuration explicitly disabled by operator override"
		res.Evidence = append(res.Evidence, override.evidence...)
		res.Evidence = append(res.Evidence, dmiEvidence...)
		return res, nil
	}

	tiers := []tier{
		override,
		r.spcrTier(ports),
		r.deviceTreeTier(ports),
		r.bootConsoleTier(ports),
		r.uniqueUARTTier(ports),
	}

	for _, t := range tiers {
		res.Evidence = append(res.Evidence, t.evidence...)
		res.Degraded = append(res.Degraded, t.degraded...)
	}
	res.Evidence = append(res.Evidence, dmiEvidence...)

	return selectFromTiers(&res, tiers)
}

// selectFromTiers walks the precedence list and materializes the winner.
//
// A tier that contradicts itself, or that claims a console it cannot map to a
// tty, fails closed instead of silently handing over to weaker evidence.
func selectFromTiers(res *Resolution, tiers []tier) (Resolution, error) {
	for i := range tiers {
		t := &tiers[i]
		spec, ok, err := mergeTier(t)
		if err != nil {
			res.Conflicts = append(res.Conflicts, err.Error())
			return failClosed(res, fmt.Errorf("%w: %w", ErrAmbiguous, err))
		}
		if !ok {
			if len(t.conflicts) > 0 {
				res.Conflicts = append(res.Conflicts, t.conflicts...)
				return failClosed(res, fmt.Errorf("%w: %s", ErrAmbiguous, strings.Join(t.conflicts, "; ")))
			}
			continue
		}
		return resolveTier(res, tiers, i, spec)
	}
	return *res, nil
}

func resolveTier(res *Resolution, tiers []tier, index int, spec Spec) (Resolution, error) {
	t := tiers[index]
	res.SelectedBy = t.source
	res.BaudSource = t.source
	if spec.Baud == 0 {
		corroborated, source := baudFromLowerTiers(tiers[index+1:], spec.Device)
		if corroborated != 0 {
			spec.Baud = corroborated
			res.BaudSource = source
		} else {
			res.BaudSource = ""
			res.Degraded = append(res.Degraded,
				fmt.Sprintf("no source declared a baud rate, using default %d", DefaultBaud))
		}
	}
	final := spec.withDefaults()
	if err := final.ValidateGettyCompatibility(); err != nil {
		return failClosed(res, fmt.Errorf("%w: %w", ErrAmbiguous, err))
	}
	res.State = StateResolved
	res.Console = &final
	res.Reason = fmt.Sprintf("selected %s from %s", final.Device, t.source)
	return *res, nil
}

// mergeTier folds all selectable evidence of one tier into a single spec.
func mergeTier(t *tier) (Spec, bool, error) {
	var merged Spec
	found := false
	for i := range t.evidence {
		e := &t.evidence[i]
		if !e.Selectable || e.Device == "" {
			continue
		}
		spec := Spec{Device: e.Device, Baud: e.Baud, Parity: e.Parity, Bits: e.Bits, Flow: e.Flow}
		if !found {
			merged, found = spec, true
			continue
		}
		if !compatible(merged, spec) {
			return Spec{}, false, fmt.Errorf("%s evidence claims both %s and %s",
				t.source, describeSpec(merged), describeSpec(spec))
		}
		merged = merge(merged, spec)
	}
	return merged, found, nil
}

func baudFromLowerTiers(tiers []tier, device string) (int, Source) {
	for _, t := range tiers {
		for i := range t.evidence {
			e := &t.evidence[i]
			if e.Device == device && e.Baud != 0 {
				return e.Baud, t.source
			}
		}
	}
	return 0, ""
}

func describeSpec(spec Spec) string {
	if spec.Baud == 0 {
		return spec.Device
	}
	return spec.Device + "," + spec.Options()
}

func failClosed(res *Resolution, err error) (Resolution, error) {
	res.State = StateAmbiguous
	res.Console = nil
	res.Reason = err.Error()
	return *res, err
}

// overrideTier collects explicit operator intent.
func (r *Resolver) overrideTier() (tier, bool, error) {
	t := tier{source: SourceOverride}
	if r.OverrideSource != "" {
		t.source = r.OverrideSource
	}
	override := strings.TrimSpace(r.Override)
	if strings.EqualFold(override, DisableKeyword) || strings.EqualFold(override, "disabled") {
		t.evidence = append(t.evidence, Evidence{Source: t.source, Detail: "console disabled by override"})
		return t, true, nil
	}
	if override != "" {
		spec, err := ParseSpec(override)
		if err != nil {
			return t, false, fmt.Errorf("explicit serial console override %q is invalid: %w", override, err)
		}
		t.evidence = append(t.evidence, evidenceFromSpec(t.source, spec, "", "explicit operator override"))
	}
	params := ConsoleParamsFromCmdline(r.ExtraKernelParams)
	for _, param := range params {
		spec, err := ParseSpec(param)
		if err != nil {
			if isVirtualConsoleSpec(param) {
				// A non-serial console such as tty0 is legitimate in
				// extraKernelParams and is not operator serial intent.
				continue
			}
			t.evidence = append(t.evidence, Evidence{
				Source: SourceKernelParams,
				Detail: "invalid console= parameter in extraKernelParams: " + truncate(param+": "+err.Error(), maxDetailLen),
			})
			return t, false, fmt.Errorf("extraKernelParams contains invalid serial console %q: %w", param, err)
		}
		t.evidence = append(t.evidence, evidenceFromSpec(SourceKernelParams, spec, "",
			"console= parameter in extraKernelParams"))
	}
	if override == "" && len(t.evidence) > 0 {
		t.source = SourceKernelParams
	}
	return t, false, nil
}

// spcrTier reads the ACPI SPCR table and maps it onto an enumerated tty.
func (r *Resolver) spcrTier(ports []port) tier {
	t := tier{source: SourceACPISPCR}
	tablePath := filepath.Join(r.sysRoot(), "firmware", "acpi", "tables", "SPCR")
	data, err := r.readFileFn()(tablePath)
	if err != nil {
		return t
	}
	info, err := ParseSPCR(data)
	if err != nil {
		t.degraded = append(t.degraded, err.Error())
		t.evidence = append(t.evidence, Evidence{Source: SourceACPISPCR, Path: "/sys/firmware/acpi/tables/SPCR",
			Detail: "unparseable SPCR table: " + truncate(err.Error(), maxDetailLen)})
		return t
	}
	device, mapped := mapSPCRDevice(ports, info)
	evidence := Evidence{
		Source:     SourceACPISPCR,
		Device:     device,
		Baud:       info.Baud,
		Parity:     info.Parity,
		Bits:       DefaultBits,
		Flow:       info.Flow,
		Selectable: mapped,
		Address:    fmt.Sprintf("%#x", info.Address),
		Path:       "/sys/firmware/acpi/tables/SPCR",
		Detail:     info.Detail(),
	}
	if mapped {
		t.evidence = append(t.evidence, evidence)
		return t
	}
	t.evidence = append(t.evidence, evidence)
	t.degraded, t.conflicts = spcrUnmappedOutcome(ports, info, t.degraded, t.conflicts)
	return t
}

// spcrUnmappedOutcome decides whether an unmapped SPCR entry is a fatal
// conflict or a recoverable gap. If the host enumerates UART addresses and
// none of them matches the firmware-declared console, configuring any other
// port would contradict firmware, so resolution fails closed.
func spcrUnmappedOutcome(ports []port, info SPCRInfo, degraded, conflicts []string) (outDegraded, outConflicts []string) {
	addressed := portsWithAddress(ports)
	if len(addressed) == 0 {
		return append(degraded, fmt.Sprintf(
			"ACPI SPCR declares console at %#x but no tty exposes an address; falling back to weaker evidence",
			info.Address)), conflicts
	}
	return degraded, append(conflicts, fmt.Sprintf(
		"ACPI SPCR declares console at %#x but no enumerated tty is backed by that address", info.Address))
}

// mapSPCRDevice maps the firmware-declared address onto an enumerated tty.
func mapSPCRDevice(ports []port, info SPCRInfo) (string, bool) {
	for i := range ports {
		if ports[i].matchesAddress(info.Address) && strings.HasPrefix(ports[i].Device, info.DeviceClass) {
			return ports[i].Device, true
		}
	}
	for i := range ports {
		if ports[i].matchesAddress(info.Address) {
			return ports[i].Device, true
		}
	}
	return "", false
}

// deviceTreeTier reads /chosen/stdout-path and maps it onto an enumerated tty.
func (r *Resolver) deviceTreeTier(ports []port) tier {
	t := tier{source: SourceDeviceTree}
	stdoutPath := filepath.Join(r.deviceTreeRoot(), "chosen", "stdout-path")
	data, err := r.readFileFn()(stdoutPath)
	if err != nil {
		return t
	}
	stdout, err := ParseStdoutPath(data)
	if err != nil {
		t.degraded = append(t.degraded, err.Error())
		return t
	}
	node, err := r.resolveStdoutNode(&stdout)
	if err != nil {
		t.degraded = append(t.degraded, err.Error())
		t.evidence = append(t.evidence, Evidence{Source: SourceDeviceTree,
			Path: "/sys/firmware/devicetree/base/chosen/stdout-path", Detail: stdout.Raw})
		return t
	}
	p, mapped := portForOfNode(ports, node)
	evidence := Evidence{
		Source:     SourceDeviceTree,
		Device:     p.Device,
		Baud:       stdout.Spec.Baud,
		Parity:     stdout.Spec.Parity,
		Bits:       stdout.Spec.Bits,
		Flow:       stdout.Spec.Flow,
		Selectable: mapped,
		Path:       "/sys/firmware/devicetree/base/chosen/stdout-path",
		Detail:     fmt.Sprintf("%s node=%s", stdout.Raw, node),
	}
	t.evidence = append(t.evidence, evidence)
	if !mapped {
		t.degraded = append(t.degraded, fmt.Sprintf(
			"device-tree stdout-path node %s does not map to an enumerated tty", node))
	}
	return t
}

// bootConsoleTier reports the serial console the running kernel uses. This is
// the console CAPRF netbooted BOOTy with, so mirroring it into the installed
// system keeps the channel that is demonstrably working.
func (r *Resolver) bootConsoleTier(ports []port) tier {
	t := tier{source: SourceBootConsole}
	baudByDevice := map[string]Spec{}
	cmdline := r.readAttr(filepath.Join(r.procRoot(), "cmdline"))
	for _, param := range ConsoleParamsFromCmdline(cmdline) {
		spec, err := ParseSpec(param)
		if err != nil {
			continue
		}
		baudByDevice[spec.Device] = spec
	}
	for _, device := range strings.Fields(r.readAttr(filepath.Join(r.sysRoot(), "class", "tty", "console", "active"))) {
		if ValidateDeviceName(device) != nil {
			continue
		}
		if _, ok := findPort(ports, device); !ok {
			continue
		}
		spec := baudByDevice[device]
		spec.Device = device
		t.evidence = append(t.evidence, evidenceFromSpec(SourceBootConsole, spec,
			"/sys/class/tty/console/active", "console active in the running kernel"))
		delete(baudByDevice, device)
	}
	if len(t.evidence) == 0 {
		t.evidence = append(t.evidence, cmdlineOnlyEvidence(ports, baudByDevice)...)
	}
	return t
}

// cmdlineOnlyEvidence covers kernels that do not export console/active.
func cmdlineOnlyEvidence(ports []port, specs map[string]Spec) []Evidence {
	evidence := make([]Evidence, 0, len(specs))
	devices := make([]string, 0, len(specs))
	for device := range specs {
		devices = append(devices, device)
	}
	sort.Strings(devices)
	for _, device := range devices {
		spec := specs[device]
		if _, ok := findPort(ports, device); !ok {
			continue
		}
		spec.Device = device
		evidence = append(evidence, evidenceFromSpec(SourceBootConsole, spec,
			"/proc/cmdline", "console= parameter of the running kernel"))
	}
	return evidence
}

// uniqueUARTTier selects a console when the host exposes exactly one present
// UART. More than one present UART is ambiguous by construction and is
// recorded as non-selectable evidence rather than guessed.
func (r *Resolver) uniqueUARTTier(ports []port) tier {
	t := tier{source: SourceSysfsUART}
	present := make([]port, 0, len(ports))
	for _, p := range ports {
		if isPresentUARTType(p.Type) {
			present = append(present, p)
		}
	}
	selectable := len(present) == 1
	for _, p := range present {
		t.evidence = append(t.evidence, Evidence{
			Source:     SourceSysfsUART,
			Device:     p.Device,
			Selectable: selectable,
			Address:    p.addressString(),
			Path:       p.SysPath,
			Detail:     "uart type=" + p.Type,
		})
	}
	if len(present) > 1 {
		t.conflicts = append(t.conflicts, fmt.Sprintf(
			"%d present UARTs enumerated (%s) and no firmware or operator source names one",
			len(present), strings.Join(deviceNames(present), ", ")))
	}
	return t
}

func deviceNames(ports []port) []string {
	names := make([]string, 0, len(ports))
	for _, p := range ports {
		names = append(names, p.Device)
	}
	return names
}

func evidenceFromSpec(source Source, spec Spec, path, detail string) Evidence {
	return Evidence{
		Source:     source,
		Device:     spec.Device,
		Baud:       spec.Baud,
		Parity:     spec.Parity,
		Bits:       spec.Bits,
		Flow:       spec.Flow,
		Selectable: spec.Device != "",
		Path:       path,
		Detail:     detail,
	}
}
