//go:build linux

package provision

import (
	"os/exec"
	"testing"
)

func TestHasBinary(t *testing.T) {
	tests := []struct {
		name   string
		binary string
		want   bool
	}{
		{"sh exists", "sh", true},
		{"nonexistent", "no-such-binary-xyz", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasBinary(tt.binary); got != tt.want {
				t.Errorf("hasBinary(%q) = %v, want %v", tt.binary, got, tt.want)
			}
		})
	}
}

func TestHasBinaryMatchesLookPath(t *testing.T) {
	for _, bin := range []string{"cat", "ls", "no-such-xyz"} {
		_, err := exec.LookPath(bin)
		want := err == nil
		if got := hasBinary(bin); got != want {
			t.Errorf("hasBinary(%q) = %v, LookPath says %v", bin, got, want)
		}
	}
}

func TestStepDebugCmds(t *testing.T) {
	tests := []struct {
		step    string
		wantLen int
	}{
		{"detect-disk", 5},
		{"stream-image", 3},
		{"mount-root", 2},
		{"setup-chroot-binds", 2},
		{"configure-grub", 3},
		{"remove-efi-entries", 3},
		{"create-efi-boot-entry", 3},
		{"mount-efivarfs", 3},
		{"unknown-step", 0},
		{"", 0},
	}
	for _, tt := range tests {
		t.Run(tt.step, func(t *testing.T) {
			cmds := stepDebugCmds(tt.step)
			if len(cmds) != tt.wantLen {
				t.Errorf("stepDebugCmds(%q) len = %d, want %d", tt.step, len(cmds), tt.wantLen)
			}
		})
	}
}

func TestDumpDebugStateNoPanic(t *testing.T) { DumpDebugState("test") }
func TestFRRDebugCmdsNoPanic(t *testing.T)   { frrDebugCmds("test") }
func TestGoBGPDebugCmdsNoPanic(t *testing.T) { gobgpDebugCmds() }
