//go:build linux

package provision

import (
	"os/exec"
	"strings"
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

func TestRedactDebugData(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		forbidden []string
	}{
		{
			name:      "authorization bearer header",
			input:     "Authorization: Bearer live-debug-token",
			forbidden: []string{"live-debug-token"},
		},
		{
			name:      "compound env key",
			input:     "BGP_AUTH_PASSWORD=s3cr3t neighbor=10.0.0.1",
			forbidden: []string{"s3cr3t"},
		},
		{
			name:      "frr password directive",
			input:     "neighbor 192.0.2.1 password frr-secret",
			forbidden: []string{"frr-secret"},
		},
		{
			name:      "url credentials and query token",
			input:     "fetch https://robot:secret@example.test/image.raw?token=query-secret",
			forbidden: []string{"robot:secret", "query-secret"},
		},
		{
			name:      "non-sensitive substring remains",
			input:     "monkey=banana interface=eth0",
			forbidden: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := redactDebugData(tc.input)
			for _, forbidden := range tc.forbidden {
				if strings.Contains(got, forbidden) {
					t.Fatalf("redactDebugData leaked %q: %q", forbidden, got)
				}
			}
			if len(tc.forbidden) == 0 && got != tc.input {
				t.Fatalf("redactDebugData(%q) = %q, want unchanged", tc.input, got)
			}
		})
	}
}
