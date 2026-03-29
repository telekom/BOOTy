//go:build linux

package realm

import (
	"log/slog"
	"os"
	"syscall"
)

// Reboot a host. When the BOOTY_NO_REBOOT environment variable is set,
// the process exits instead of issuing a reboot syscall (used in test containers).
func Reboot() {
	if os.Getenv("BOOTY_NO_REBOOT") != "" {
		slog.Info("reboot suppressed (BOOTY_NO_REBOOT set)")
		os.Exit(0)
	}
	err := syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
	if err != nil {
		slog.Error("reboot failed", "error", err)
		Shell()
	}
	os.Exit(1)
}

// PowerOff will result in the host using an ACPI power off.
func PowerOff() {
	err := syscall.Reboot(syscall.LINUX_REBOOT_CMD_POWER_OFF)
	if err != nil {
		slog.Error("power off failed", "error", err)
		Shell()
	}
	os.Exit(1)
}

// Halt will instruct the CPU to enter a halt state.
func Halt() {
	err := syscall.Reboot(syscall.LINUX_REBOOT_CMD_HALT)
	if err != nil {
		slog.Error("halt failed", "error", err)
		Shell()
	}
	os.Exit(1)
}

// Suspend will instruct the CPU to enter a suspended state.
func Suspend() {
	err := syscall.Reboot(syscall.LINUX_REBOOT_CMD_SW_SUSPEND)
	if err != nil {
		slog.Error("suspend failed", "error", err)
		Shell()
		slog.Warn("attempting a reboot")
		Reboot()
	}
}
