//go:build linux

package main

import (
	"log/slog"
	"os"
	"strings"
)

// panicRebootSeconds is the kernel.panic value BOOTy sets: a kernel panic,
// also the "Attempted to kill init" panic when BOOTy (PID 1) dies, reboots
// the host after 10 s instead of hanging. The Debian and CAPRF boot entries
// set no panic= (the kernel default 0 hangs forever), and without an armed
// watchdog a hung ramdisk needs a manual power cycle. The 10 s keep the
// panic on the serial console and BMC screen long enough to see, and pstore
// keeps it for the crash collector of the next boot. A reboot boots the
// ramdisk again, which BOOTy already does after a failed run.
const panicRebootSeconds = "10"

// Test seams.
var (
	procCmdlinePath = "/proc/cmdline"
	kernelPanicPath = "/proc/sys/kernel/panic"
)

// setPanicReboot sets kernel.panic unless the kernel command line has a
// panic= parameter, which wins (for example panic=0 to keep a panicked host
// for debugging).
func setPanicReboot() {
	cmdline, err := os.ReadFile(procCmdlinePath)
	if err != nil {
		slog.Warn("kernel.panic not set: cannot read the kernel command line", "error", err)
		return
	}
	for _, p := range strings.Fields(string(cmdline)) {
		if strings.HasPrefix(p, "panic=") {
			return
		}
	}
	if err := os.WriteFile(kernelPanicPath, []byte(panicRebootSeconds), 0o644); err != nil { //nolint:gosec // procfs file
		slog.Warn("kernel.panic not set", "error", err)
	}
}
