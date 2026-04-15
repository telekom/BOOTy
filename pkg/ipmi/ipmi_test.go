//go:build linux

package ipmi

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	m := New(nil)
	if m.deviceNum != "0" {
		t.Errorf("deviceNum = %q, want \"0\"", m.deviceNum)
	}
}

func TestDevicePath(t *testing.T) {
	m := &Manager{deviceNum: "0"}
	if got := m.DevicePath(); got != "/dev/ipmi0" {
		t.Errorf("DevicePath = %q, want /dev/ipmi0", got)
	}
}

func TestBootDeviceConstants(t *testing.T) {
	tests := []struct {
		device   BootDevice
		expected string
	}{
		{BootPXE, "pxe"},
		{BootDisk, "disk"},
		{BootCDROM, "cdrom"},
		{BootBIOS, "bios"},
	}

	for _, tc := range tests {
		if string(tc.device) != tc.expected {
			t.Errorf("BootDevice %q != %q", tc.device, tc.expected)
		}
	}
}

func TestValidateBootDevice(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"pxe", false},
		{"disk", false},
		{"cdrom", false},
		{"bios", false},
		{"invalid", true},
		{"", true},
	}

	for _, tc := range tests {
		err := ValidateBootDevice(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ValidateBootDevice(%q) should error", tc.input)
			}
		} else {
			if err != nil {
				t.Errorf("ValidateBootDevice(%q) unexpected error: %v", tc.input, err)
			}
		}
	}
}

func TestParseBMCMAC(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"aa:bb:cc:dd:ee:ff", false},
		{"AA:BB:CC:DD:EE:FF", false},
		{"invalid", true},
		{"", true},
	}

	for _, tc := range tests {
		hw, err := ParseBMCMAC(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseBMCMAC(%q) should error", tc.input)
			}
		} else {
			if err != nil {
				t.Errorf("ParseBMCMAC(%q) unexpected error: %v", tc.input, err)
			}
			if hw == nil {
				t.Errorf("ParseBMCMAC(%q) returned nil", tc.input)
			}
		}
	}
}

func TestParseBMCMAC_TrimSpace(t *testing.T) {
	hw, err := ParseBMCMAC("  aa:bb:cc:dd:ee:ff  ")
	if err != nil {
		t.Fatalf("ParseBMCMAC with whitespace: %v", err)
	}
	if hw == nil {
		t.Error("expected non-nil result")
	}
}

func TestValidateBootDevice_TrimSpace(t *testing.T) {
	if err := ValidateBootDevice(" disk\n"); err != nil {
		t.Errorf("ValidateBootDevice with whitespace: %v", err)
	}
}

type fakeRunner struct {
	output []byte
	err    error
	args   []string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.args = append([]string{name}, args...)
	return f.output, f.err
}

func TestExecIPMITool_UsesRunner(t *testing.T) {
	r := &fakeRunner{output: []byte("ok\n")}
	m := &Manager{deviceNum: "0", runner: r, log: slog.Default()}
	out, err := m.execIPMITool(context.Background(), "chassis", "status")
	if err != nil {
		t.Fatalf("execIPMITool: %v", err)
	}
	if out != "ok\n" {
		t.Errorf("output = %q, want %q", out, "ok\n")
	}
	// Verify -I open -d 0 is passed
	if len(r.args) < 5 || r.args[1] != "-I" || r.args[2] != "open" || r.args[3] != "-d" || r.args[4] != "0" {
		t.Errorf("expected [-I open -d 0] in args, got %v", r.args)
	}
}

func TestChassisControlInvalidAction(t *testing.T) {
	m := New(nil)
	err := m.ChassisControl(context.Background(), "bad action")
	if err == nil {
		t.Fatal("expected error for invalid action")
	}
	if !strings.Contains(err.Error(), `"bad action"`) {
		t.Fatalf("expected quoted action in error, got %v", err)
	}
}

func TestParseLanPrint(t *testing.T) {
	output := `Set in Progress         : Set Complete
IP Address Source       : DHCP Address
IP Address              : 10.0.0.100
Subnet Mask             : 255.255.255.0
MAC Address             : aa:bb:cc:dd:ee:ff
Default Gateway IP      : 10.0.0.1
802.1q VLAN ID          : 100
`

	fields := parseLanPrint(output)
	if fields["IP Address"] != "10.0.0.100" {
		t.Errorf("IP Address = %q", fields["IP Address"])
	}
	if fields["IP Address Source"] != "DHCP Address" {
		t.Errorf("IP Address Source = %q", fields["IP Address Source"])
	}
	if fields["MAC Address"] != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("MAC Address = %q", fields["MAC Address"])
	}
	vlanEnabled, vlanID := parseVLAN(fields)
	if !vlanEnabled || vlanID != 100 {
		t.Errorf("parseVLAN = (%v, %d), want (true, 100)", vlanEnabled, vlanID)
	}
}

func TestParseVLAN(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		enabled bool
		id      int
	}{
		{name: "disabled", value: "Disabled", enabled: false, id: 0},
		{name: "decimal", value: "123", enabled: true, id: 123},
		{name: "hex", value: "0x64", enabled: true, id: 100},
		{name: "unknown text", value: "Enabled", enabled: true, id: 0},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			enabled, id := parseVLAN(map[string]string{"802.1q VLAN ID": tc.value})
			if enabled != tc.enabled || id != tc.id {
				t.Fatalf("parseVLAN(%q) = (%v, %d), want (%v, %d)", tc.value, enabled, id, tc.enabled, tc.id)
			}
		})
	}
}

func TestRedactIPMIArgs(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		contains string
		absent   string
	}{
		{
			name:     "redacts -P value",
			input:    []string{"-H", "10.0.0.1", "-U", "admin", "-P", "s3cr3t", "chassis", "status"},
			absent:   "s3cr3t",
			contains: "[REDACTED]",
		},
		{
			name:     "redacts --password value",
			input:    []string{"--password", "topsecret", "lan", "print"},
			absent:   "topsecret",
			contains: "[REDACTED]",
		},
		{
			name:     "redacts -U value",
			input:    []string{"-U", "myuser", "chassis", "power", "status"},
			absent:   "myuser",
			contains: "[REDACTED]",
		},
		{
			name:     "preserves non-sensitive args",
			input:    []string{"-I", "open", "-d", "0", "chassis", "status"},
			contains: "open",
		},
		{
			name:  "empty args",
			input: []string{},
		},
		{
			name:     "consecutive sensitive flags: value not re-treated as flag",
			input:    []string{"-P", "pass", "-H", "host"},
			absent:   "host",
			contains: "[REDACTED]",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := redactIPMIArgs(tc.input)
			joined := strings.Join(got, " ")
			if tc.absent != "" && strings.Contains(joined, tc.absent) {
				t.Errorf("redactIPMIArgs output contains sensitive value %q: %v", tc.absent, got)
			}
			if tc.contains != "" && !strings.Contains(joined, tc.contains) {
				t.Errorf("redactIPMIArgs output missing expected value %q: %v", tc.contains, got)
			}
		})
	}
}

func TestRedactIPMIArgs_DoesNotMutateInput(t *testing.T) {
	input := []string{"-P", "secret"}
	original := make([]string, len(input))
	copy(original, input)
	redactIPMIArgs(input)
	for i, v := range input {
		if v != original[i] {
			t.Errorf("input mutated at index %d: got %q, want %q", i, v, original[i])
		}
	}
}

func TestExecIPMITool_ErrorDoesNotContainPassword(t *testing.T) {
	runErr := fmt.Errorf("exit status 1")
	r := &fakeRunner{output: []byte("error output"), err: runErr}
	m := &Manager{deviceNum: "0", runner: r, log: slog.Default()}
	_, err := m.execIPMITool(context.Background(), "-P", "topsecret", "chassis", "status")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "topsecret") {
		t.Errorf("error message contains sensitive value: %v", err)
	}
}
