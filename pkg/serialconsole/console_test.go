package serialconsole

import (
	"strings"
	"testing"
)

func TestParseSpec(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Spec
		wantErr bool
	}{
		{name: "device only", input: "ttyS0", want: Spec{Device: "ttyS0"}},
		{name: "device with baud", input: "ttyS1,115200", want: Spec{Device: "ttyS1", Baud: 115200}},
		{
			name:  "full specification",
			input: "ttyS1,115200n8",
			want:  Spec{Device: "ttyS1", Baud: 115200, Parity: "n", Bits: 8},
		},
		{
			name:  "flow control",
			input: "ttyAMA0,115200n8r",
			want:  Spec{Device: "ttyAMA0", Baud: 115200, Parity: "n", Bits: 8, Flow: "r"},
		},
		{name: "dev prefix", input: "/dev/ttyS2", want: Spec{Device: "ttyS2"}},
		{name: "padded", input: "  ttyS0,9600  ", want: Spec{Device: "ttyS0", Baud: 9600}},
		{name: "virtual terminal rejected", input: "tty0", wantErr: true},
		{name: "aggregate console rejected", input: "console", wantErr: true},
		{name: "empty rejected", input: "", wantErr: true},
		{name: "baud too low rejected", input: "ttyS0,110", wantErr: true},
		{name: "baud too high rejected", input: "ttyS0,9999999", wantErr: true},
		{name: "shell metacharacters rejected", input: "ttyS0;reboot", wantErr: true},
		{name: "path traversal rejected", input: "../../dev/ttyS0", wantErr: true},
		{name: "overlong rejected", input: "ttyS0," + strings.Repeat("1", 80), wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSpec(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseSpec(%q) = %+v, want error", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSpec(%q): %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("ParseSpec(%q) = %+v, want %+v", tc.input, got, tc.want)
			}
		})
	}
}

func TestSpecRendering(t *testing.T) {
	spec := Spec{Device: "ttyS1", Baud: 115200, Parity: "n", Bits: 8}
	if got := spec.KernelParam(); got != "console=ttyS1,115200n8" {
		t.Fatalf("KernelParam() = %q", got)
	}
	if got := spec.GettyUnit(); got != "serial-getty@ttyS1.service" {
		t.Fatalf("GettyUnit() = %q", got)
	}
	unit, ok := spec.GRUBSerialUnit()
	if !ok || unit != 1 {
		t.Fatalf("GRUBSerialUnit() = %d, %v, want 1, true", unit, ok)
	}
	if _, ok := (Spec{Device: "ttyAMA0"}).GRUBSerialUnit(); ok {
		t.Fatal("GRUBSerialUnit() must not claim a GRUB unit for non-8250 ports")
	}
	if got := (Spec{Device: "ttyS0"}).KernelParam(); got != "console=ttyS0" {
		t.Fatalf("KernelParam() without baud = %q", got)
	}
}

func TestConsoleParamsFromCmdline(t *testing.T) {
	params := ConsoleParamsFromCmdline("BOOT_IMAGE=/vmlinuz console=tty0 console=ttyS1,115200n8 quiet")
	want := []string{"tty0", "ttyS1,115200n8"}
	if len(params) != len(want) {
		t.Fatalf("ConsoleParamsFromCmdline() = %v, want %v", params, want)
	}
	for i := range want {
		if params[i] != want[i] {
			t.Fatalf("ConsoleParamsFromCmdline()[%d] = %q, want %q", i, params[i], want[i])
		}
	}
	if got := ConsoleParamsFromCmdline("quiet ro"); got != nil {
		t.Fatalf("ConsoleParamsFromCmdline() = %v, want nil", got)
	}
}

func TestValidateDeviceName(t *testing.T) {
	valid := []string{"ttyS0", "ttyS31", "ttyAMA0", "ttyUSB0", "hvc0", "ttymxc1"}
	for _, name := range valid {
		if err := ValidateDeviceName(name); err != nil {
			t.Fatalf("ValidateDeviceName(%q): %v", name, err)
		}
	}
	invalid := []string{"", "tty0", "tty63", "console", "ttyS0/../../etc", "TTYS0", strings.Repeat("a", 40) + "0"}
	for _, name := range invalid {
		if err := ValidateDeviceName(name); err == nil {
			t.Fatalf("ValidateDeviceName(%q) = nil, want error", name)
		}
	}
}

func TestCompatibleAndMerge(t *testing.T) {
	base := Spec{Device: "ttyS1"}
	detailed := Spec{Device: "ttyS1", Baud: 115200, Parity: "n", Bits: 8}
	if !compatible(base, detailed) {
		t.Fatal("unset fields must not conflict")
	}
	if compatible(detailed, Spec{Device: "ttyS1", Baud: 9600}) {
		t.Fatal("different baud rates must conflict")
	}
	if compatible(detailed, Spec{Device: "ttyS0", Baud: 115200}) {
		t.Fatal("different devices must conflict")
	}
	if got := merge(base, detailed); got != detailed {
		t.Fatalf("merge() = %+v, want %+v", got, detailed)
	}
}

func TestValidateGettyCompatibilityRejectsNonDefaultFraming(t *testing.T) {
	cases := []Spec{
		{Device: "ttyS0", Baud: 115200, Parity: "e", Bits: 7},
		{Device: "ttyS0", Baud: 115200, Parity: "n", Bits: 8, Flow: "r"},
	}
	for _, spec := range cases {
		if err := spec.ValidateGettyCompatibility(); err == nil {
			t.Fatalf("ValidateGettyCompatibility(%+v) = nil, want error", spec)
		}
	}
	if err := (Spec{Device: "ttyS0", Baud: 115200, Parity: "n", Bits: 8}).ValidateGettyCompatibility(); err != nil {
		t.Fatalf("default framing rejected: %v", err)
	}
}

func TestOptionsFillsPartialFraming(t *testing.T) {
	if got := (Spec{Device: "ttyS1", Baud: 115200}).Options(); got != "115200n8" {
		t.Fatalf("Options() = %q, want 115200n8", got)
	}
}
