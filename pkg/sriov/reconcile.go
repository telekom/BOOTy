package sriov

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// Reconciler checks, and with onDrift apply changes, the SR-IOV profiles
// of a host. It is the configure-sriov provisioning step.
type Reconciler struct {
	Config *Config
	// Discover reports the PF ports (Inspector.Discover).
	Discover func() ([]PF, error)
	// Backends are the firmware backends, tried in order.
	Backends []FirmwareBackend
	// Runtime applies the runtime settings; nil reports them unverified.
	Runtime RuntimeOps
	Log     *slog.Logger
}

// Result is the outcome of a reconcile.
type Result struct {
	Findings []Finding `json:"findings"`
	// Reboot is true when BOOTy stored firmware settings. They take effect
	// only after a reboot, so the handoff must reboot instead of kexec.
	Reboot bool `json:"reboot"`
}

// Run checks every profile. It returns an error when a profile with
// onDrift fail or apply is not compliant, or when the profiles are
// ambiguous; drift of an onDrift warn profile is only logged.
func (r *Reconciler) Run(ctx context.Context) (*Result, error) {
	res := &Result{}
	if len(r.Config.Profiles) == 0 {
		return res, nil
	}
	log := r.Log
	if log == nil {
		log = slog.Default()
	}
	pfs, err := r.Discover()
	if err != nil {
		return res, fmt.Errorf("discover SR-IOV PF ports: %w", err)
	}
	assignments, findings, err := Assign(r.Config.Profiles, pfs)
	if err != nil {
		return res, fmt.Errorf("SR-IOV profiles: %w", err)
	}
	res.Findings = findings
	for _, a := range assignments {
		fw, reboot := r.firmware(ctx, a)
		res.Findings = append(res.Findings, fw...)
		res.Reboot = res.Reboot || reboot
	}
	res.Findings = append(res.Findings, r.runtime(assignments)...)
	return res, r.enforce(log, res)
}

// firmware reads, checks and, with onDrift apply, stores the firmware
// settings of one PF port.
func (r *Reconciler) firmware(ctx context.Context, a Assignment) (findings []Finding, reboot bool) {
	if !hasFirmwareSettings(&a.Profile.Firmware) {
		return nil, false
	}
	backend, note := backendFor(r.Backends, a.PF, r.Config.Tools())
	var read *FirmwareRead
	if backend != nil {
		var err error
		read, err = backend.Read(ctx, a.PF, &a.Profile.Firmware)
		if err != nil {
			read, note = nil, fmt.Sprintf("%s read: %v", backend.Name(), err)
		}
	}
	findings = checkFirmware(a, read, note)
	if r.Config.DriftAction(a.Profile) != OnDriftApply {
		return findings, false
	}
	var drifted []string
	for i := range findings {
		if findings[i].Status == StatusDrift {
			drifted = append(drifted, findings[i].Setting)
		}
	}
	if len(drifted) == 0 {
		return findings, false
	}
	status, detail := StatusApplied, "takes effect after the reboot"
	if backend == nil || read == nil {
		status, detail = StatusFailed, "cannot apply: "+note
	} else if err := backend.Apply(ctx, a.PF, changeFor(&a.Profile.Firmware, drifted)); err != nil {
		status, detail = StatusFailed, fmt.Sprintf("%s apply: %v", backend.Name(), err)
	}
	for i := range findings {
		if findings[i].Status == StatusDrift {
			findings[i].Status, findings[i].Detail = status, detail
		}
	}
	return findings, status == StatusApplied
}

// runtime applies the runtime settings, checks the result, and with
// handoff reset (the default) restores the state from before the apply.
// Runtime settings do not survive the handoff; BOOTy applies them to prove
// that the PF ports can run them.
func (r *Reconciler) runtime(assignments []Assignment) []Finding {
	if r.Runtime == nil {
		var out []Finding
		for _, a := range assignments {
			if hasRuntimeSettings(&a.Profile.Runtime) {
				out = append(out, Finding{Profile: a.Profile.Name, Interface: a.PF.Interface, Level: LevelRuntime,
					Setting: "runtime", Status: StatusUnverified, Detail: "no runtime access"})
			}
		}
		return out
	}
	devs, findings := runtimeDevices(assignments)
	if len(devs) == 0 {
		return findings
	}
	for _, d := range devs {
		applyDevice(r.Runtime, d)
	}
	now := r.discoverByName()
	for _, d := range devs {
		for _, a := range d.assignments {
			pf, ok := now[a.PF.Interface]
			if !ok {
				findings = append(findings, Finding{Profile: a.Profile.Name, Interface: a.PF.Interface, Level: LevelRuntime,
					Setting: "runtime", Status: StatusFailed, Detail: "PF port missing after the apply"})
				continue
			}
			findings = append(findings, verifyRuntime(a, pf, d.errs)...)
		}
	}
	return append(findings, r.handoff(devs)...)
}

// handoff restores every changed device that does not keep its runtime
// state, then checks the VF count and the eSwitch mode again.
func (r *Reconciler) handoff(devs []*runtimeDevice) []Finding {
	var reset []*runtimeDevice
	errs := map[*runtimeDevice]error{}
	for _, d := range devs {
		if d.changed && !d.keep {
			errs[d] = resetDevice(r.Runtime, d)
			reset = append(reset, d)
		}
	}
	if len(reset) == 0 {
		return nil
	}
	now := r.discoverByName()
	var out []Finding
	for _, d := range reset {
		a := d.assignments[0]
		want := fmt.Sprintf("numVFs=%d eswitch=%s", d.origNumVFs, d.origEswitch)
		f := Finding{Profile: a.Profile.Name, Interface: a.PF.Interface, Level: LevelRuntime,
			Setting: SettingHandoff, Want: want, Status: StatusOK}
		if pf, ok := now[a.PF.Interface]; ok {
			f.Got = fmt.Sprintf("numVFs=%d eswitch=%s", pf.NumVFs, pf.Eswitch.Mode)
		}
		if f.Got != f.Want {
			f.Status = StatusFailed
		}
		if errs[d] != nil {
			f.Status, f.Detail = StatusFailed, errs[d].Error()
		}
		out = append(out, f)
	}
	return out
}

// discoverByName reports the PF ports by interface; a failed discovery is
// an empty map, so every check after it fails.
func (r *Reconciler) discoverByName() map[string]*PF {
	pfs, err := r.Discover()
	out := make(map[string]*PF, len(pfs))
	if err != nil {
		return out
	}
	for i := range pfs {
		out[pfs[i].Interface] = &pfs[i]
	}
	return out
}

// enforce logs the findings and fails for the profiles that require it.
func (r *Reconciler) enforce(log *slog.Logger, res *Result) error {
	actions := map[string]string{}
	for i := range r.Config.Profiles {
		actions[r.Config.Profiles[i].Name] = r.Config.DriftAction(&r.Config.Profiles[i])
	}
	var failed []string
	compliant := 0
	for i := range res.Findings {
		f := &res.Findings[i]
		if f.Compliant() {
			compliant++
			continue
		}
		log.Warn("SR-IOV drift", "profile", f.Profile, "interface", f.Interface, "level", f.Level,
			"setting", f.Setting, "want", f.Want, "got", f.Got, "status", f.Status, "detail", f.Detail,
			"onDrift", actions[f.Profile])
		if actions[f.Profile] != OnDriftWarn {
			failed = append(failed, f.String())
		}
	}
	log.Info("SR-IOV compliance", "findings", len(res.Findings), "compliant", compliant,
		"failed", len(failed), "reboot", res.Reboot)
	if len(failed) > 0 {
		return fmt.Errorf("SR-IOV profiles not compliant: %s", strings.Join(failed, "; "))
	}
	return nil
}

func hasFirmwareSettings(f *FirmwareSettings) bool {
	return f.SRIOVEnabled != nil || f.MaxVFs != nil || f.LinkType != "" || f.FWLLDPAgent != nil ||
		len(f.NVConfig) > 0 || len(f.DevlinkParams) > 0
}
