//go:build linux

package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	orig := slog.Default()
	t.Cleanup(func() { slog.SetDefault(orig) })
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	return &buf
}

// The kernel's Secure Boot and lockdown lines, hidden from the console by
// loglevel=4, appear in one BOOTy line with the texts the ISO tests read.
func TestLogSecureBootState(t *testing.T) {
	klog := "<5>[    0.000000] Linux version 6.12.48+deb13-amd64\n" +
		"<6>[    0.004000] secureboot: Secure boot enabled\n" +
		"<5>[    0.004100] Kernel is locked down from EFI Secure Boot mode; see man kernel_lockdown.7\n" +
		"<6>[    0.200000] pci 0000:00:01.0: [8086:7000] type 00 class 0x060100\n"
	buf := captureLog(t)
	logSecureBootState(klog)
	out := buf.String()
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("want one line, got:\n%s", out)
	}
	for _, want := range []string{"Secure boot enabled", "Kernel is locked down from EFI Secure Boot"} {
		if !strings.Contains(out, want) {
			t.Errorf("line lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, "<6>") || strings.Contains(out, "[    0.") || strings.Contains(out, "pci 0000") {
		t.Errorf("line carries prefixes or other kernel lines: %s", out)
	}

	buf.Reset()
	logSecureBootState("<5>[    0.000000] Linux version 6.12\n")
	if !strings.Contains(buf.String(), "not reported") {
		t.Errorf("BIOS boot: %s", buf.String())
	}
}

func TestKernelLogLine(t *testing.T) {
	for in, want := range map[string]string{
		"<6>[    0.004000] secureboot: Secure boot disabled": "secureboot: Secure boot disabled",
		"[    0.004000] no level":                            "no level",
		"plain":                                              "plain",
	} {
		if got := kernelLogLine(in); got != want {
			t.Errorf("kernelLogLine(%q) = %q, want %q", in, got, want)
		}
	}
}
