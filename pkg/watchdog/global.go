package watchdog

import "sync/atomic"

// active is the supervisor of the run, nil when the watchdog is off.
var active atomic.Pointer[Supervisor]

// Activate makes s the supervisor that Step, Progress, Idle and Handoff
// use. Nil deactivates.
func Activate(s *Supervisor) { active.Store(s) }

// Step marks the start of a provisioning or deprovisioning step. It does
// nothing when the watchdog is off.
func Step(name string) {
	if s := active.Load(); s != nil {
		s.Step(name)
	}
}

// Progress records progress of the active step. It does nothing when the
// watchdog is off.
func Progress() {
	if s := active.Load(); s != nil {
		s.Progress()
	}
}

// Idle marks that no step is active. It does nothing when the watchdog is
// off.
func Idle() {
	if s := active.Load(); s != nil {
		s.Idle()
	}
}

// Handoff ends the supervision before kexec, reboot or power off; see
// Supervisor.Handoff. It does nothing when the watchdog is off.
func Handoff(keep bool) error {
	if s := active.Load(); s != nil {
		return s.Handoff(keep)
	}
	return nil
}
