//go:build linux

package main

import (
	"log/slog"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// readKernelLogFn is a test seam for readKernelLog.
var readKernelLogFn = readKernelLog

// readKernelLog returns the kernel ring buffer, like dmesg. The ISO boots
// with loglevel=4, so the console shows only kernel errors; the buffer
// keeps every message.
func readKernelLog() string {
	size, err := unix.Klogctl(unix.SYSLOG_ACTION_SIZE_BUFFER, nil)
	if err != nil || size <= 0 {
		return ""
	}
	buf := make([]byte, size)
	n, err := unix.Klogctl(unix.SYSLOG_ACTION_READ_ALL, buf)
	if err != nil {
		return ""
	}
	return string(buf[:n])
}

// kernelLogLine strips the "<6>[    0.123456] " prefix of a ring buffer line.
func kernelLogLine(line string) string {
	if strings.HasPrefix(line, "<") {
		if _, rest, ok := strings.Cut(line, ">"); ok {
			line = rest
		}
	}
	if strings.HasPrefix(line, "[") {
		if _, rest, ok := strings.Cut(line, "] "); ok {
			line = rest
		}
	}
	return line
}

// logSecureBootState logs the kernel's Secure Boot and lockdown messages
// ("secureboot: Secure boot enabled", "Kernel is locked down from EFI
// Secure Boot mode") in one line. loglevel=4 hides them from the console,
// and operators and the ISO tests read them there.
func logSecureBootState(klog string) {
	var found []string
	for line := range strings.SplitSeq(klog, "\n") {
		text := kernelLogLine(line)
		lower := strings.ToLower(text)
		if strings.Contains(lower, "secure boot") || strings.Contains(lower, "locked down") {
			found = append(found, text)
		}
	}
	if len(found) == 0 {
		slog.Info("kernel security state", "secureBoot", "not reported (BIOS boot or no EFI)")
		return
	}
	slog.Info("kernel security state", "kernel", strings.Join(found, "; "))
}

// uptimeSeconds returns the first field of /proc/uptime, the seconds since
// the kernel started, or "" when it cannot be read.
func uptimeSeconds() string {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return ""
	}
	up, _, _ := strings.Cut(strings.TrimSpace(string(data)), " ")
	return up
}
