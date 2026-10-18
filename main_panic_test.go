//go:build linux

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

var regexpPanic = regexp.MustCompile(`(?m)^cmdline="[^"]*\bpanic=` + panicRebootSeconds + `\b`)

func TestSetPanicReboot(t *testing.T) {
	origCmdline, origPanic := procCmdlinePath, kernelPanicPath
	t.Cleanup(func() { procCmdlinePath, kernelPanicPath = origCmdline, origPanic })
	for _, tc := range []struct{ cmdline, want string }{
		{"console=ttyS0,115200n8 --", panicRebootSeconds},
		{"console=ttyS0 panic=0", "unset"},
	} {
		dir := t.TempDir()
		procCmdlinePath = filepath.Join(dir, "cmdline")
		kernelPanicPath = filepath.Join(dir, "panic")
		if err := os.WriteFile(procCmdlinePath, []byte(tc.cmdline+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(kernelPanicPath, []byte("unset"), 0o600); err != nil {
			t.Fatal(err)
		}
		setPanicReboot()
		got, _ := os.ReadFile(kernelPanicPath)
		if string(got) != tc.want {
			t.Errorf("cmdline %q: kernel.panic = %q, want %q", tc.cmdline, got, tc.want)
		}
	}
}

// The ISOs that BOOTy builds set panic= on the kernel command line, so a
// panic before BOOTy starts reboots too.
func TestMakeISOCmdlineRebootsOnPanic(t *testing.T) {
	b, err := os.ReadFile("hack/make-iso.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !regexpPanic.Match(b) {
		t.Fatal("hack/make-iso.sh: the kernel command line has no panic=" + panicRebootSeconds)
	}
}
