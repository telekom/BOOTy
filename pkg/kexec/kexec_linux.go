//go:build linux

package kexec

import (
	"fmt"
	"log/slog"
	"os"

	"golang.org/x/sys/unix"
)

// Load loads a kernel for kexec. Call Execute() to boot into it.
func Load(kernelPath, initrdPath, cmdline string) error {
	slog.Info("loading kernel for kexec", "kernel", kernelPath, "initrd", initrdPath)

	kernelFd, err := os.Open(kernelPath)
	if err != nil {
		return fmt.Errorf("opening kernel: %w", err)
	}
	defer func() { _ = kernelFd.Close() }()

	initrdFd, err := os.Open(initrdPath)
	if err != nil {
		return fmt.Errorf("opening initrd: %w", err)
	}
	defer func() { _ = initrdFd.Close() }()

	if err := unix.KexecFileLoad(int(kernelFd.Fd()), int(initrdFd.Fd()), cmdline, 0); err != nil {
		return fmt.Errorf("kexec file load: %w", err)
	}
	slog.Info("kernel loaded for kexec")
	return nil
}

// Execute performs the kexec reboot.
func Execute() error {
	slog.Info("executing kexec")
	if err := unix.Reboot(unix.LINUX_REBOOT_CMD_KEXEC); err != nil {
		return fmt.Errorf("kexec reboot: %w", err)
	}
	return nil
}
