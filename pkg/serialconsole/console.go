// Package serialconsole resolves the serial console of the running host from
// explicit operator intent and read-only firmware evidence.
//
// The resolver never opens, writes to, or reconfigures a UART: every input is
// a read-only file below /sys or /proc. It deliberately fails closed when the
// available evidence is ambiguous instead of guessing a port, because a wrong
// console silently removes the only remote debugging channel of a bare-metal
// node.
//
// The resolved console is the single source of truth for both the kernel
// command line (`console=...`) and the matching `serial-getty@` instance, and
// the machine-readable resolution artifact is the agreed output contract for
// CAPRF consumption.
package serialconsole

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	// DefaultBaud is used when every source agrees on a device but none of
	// them declares a baud rate.
	DefaultBaud = 115200
	// DefaultParity is the parity assumed for serial consoles without an
	// explicit firmware-declared parity.
	DefaultParity = "n"
	// DefaultBits is the data-bit count assumed for serial consoles.
	DefaultBits = 8

	// DisableKeyword disables serial console configuration entirely.
	DisableKeyword = "none"

	minBaud = 300
	maxBaud = 4000000

	maxDeviceNameLen = 32
	// maxConsoleSpec bounds an untrusted console specification.
	maxConsoleSpec = 64
	// maxCmdlineBytes bounds an untrusted kernel command line.
	maxCmdlineBytes = 8192
)

// deviceNamePattern matches Linux tty device names such as ttyS1, ttyAMA0,
// ttyUSB0 or hvc0. The trailing index is mandatory so that aggregate names
// such as "console" or "tty" are rejected.
var deviceNamePattern = regexp.MustCompile(`^[a-z][a-zA-Z0-9_]*\d{1,3}$`)

// virtualConsolePattern matches the kernel virtual terminals (tty0..tty63),
// which are not serial ports and can never host a serial getty.
var virtualConsolePattern = regexp.MustCompile(`^tty(?:\d|[1-5]\d|6[0-3])$`)

// consoleSpecPattern splits a Linux console specification into device and
// options, for example ttyS1,115200n8r.
var consoleSpecPattern = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9_]*)(?:,([0-9]{3,7})([neoms])?([5-8])?(r)?)?$`)

// Spec is a fully resolved serial console specification.
type Spec struct {
	Device string `json:"device"`
	Baud   int    `json:"baud"`
	Parity string `json:"parity"`
	Bits   int    `json:"bits"`
	Flow   string `json:"flow,omitempty"`
}

// IsZero reports whether the spec carries no device.
func (s Spec) IsZero() bool { return s.Device == "" }

// Options renders the Linux console option suffix, for example 115200n8.
func (s Spec) Options() string {
	if s.Baud == 0 {
		return ""
	}
	return fmt.Sprintf("%d%s%d%s", s.Baud, s.Parity, s.Bits, s.Flow)
}

// KernelParam renders the single kernel command-line console parameter.
func (s Spec) KernelParam() string {
	options := s.Options()
	if options == "" {
		return "console=" + s.Device
	}
	return "console=" + s.Device + "," + options
}

// GettyUnit renders the systemd serial getty unit for the resolved device.
func (s Spec) GettyUnit() string {
	return "serial-getty@" + s.Device + ".service"
}

// ValidateGettyCompatibility rejects framing that the generated agetty unit
// cannot apply explicitly. Resolution fails closed rather than configuring a
// kernel console and getty with different framing.
func (s Spec) ValidateGettyCompatibility() error {
	s = s.withDefaults()
	if s.Parity != DefaultParity || s.Bits != DefaultBits || s.Flow != "" {
		return fmt.Errorf("serial console %s uses unsupported getty framing %s", s.Device, s.Options())
	}
	return nil
}

// GRUBSerialUnit reports the GRUB `--unit` index for 8250-style ports. The
// second return value is false for devices GRUB cannot drive directly.
func (s Spec) GRUBSerialUnit() (int, bool) {
	if !strings.HasPrefix(s.Device, "ttyS") {
		return 0, false
	}
	unit, err := strconv.Atoi(strings.TrimPrefix(s.Device, "ttyS"))
	if err != nil || unit < 0 || unit > 31 {
		return 0, false
	}
	return unit, true
}

// withDefaults fills in the conventional 8N1 framing for partial specs.
func (s Spec) withDefaults() Spec {
	if s.Baud == 0 {
		s.Baud = DefaultBaud
	}
	if s.Parity == "" {
		s.Parity = DefaultParity
	}
	if s.Bits == 0 {
		s.Bits = DefaultBits
	}
	return s
}

// ValidateDeviceName checks that name is a plausible serial tty device name.
func ValidateDeviceName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("serial console device name is empty")
	case len(name) > maxDeviceNameLen:
		return fmt.Errorf("serial console device name %q exceeds %d bytes", name, maxDeviceNameLen)
	case virtualConsolePattern.MatchString(name):
		return fmt.Errorf("serial console device %q is a virtual terminal, not a serial port", name)
	case !deviceNamePattern.MatchString(name):
		return fmt.Errorf("serial console device name %q is not a tty device name", name)
	}
	return nil
}

// ParseSpec parses a Linux console specification such as ttyS1,115200n8.
//
// Parsing is strict and bounded: unknown shapes are rejected rather than
// partially interpreted, so a malformed operator override never silently
// degrades into a different port.
func ParseSpec(value string) (Spec, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Spec{}, fmt.Errorf("serial console specification is empty")
	}
	if len(value) > maxConsoleSpec {
		return Spec{}, fmt.Errorf("serial console specification exceeds %d bytes", maxConsoleSpec)
	}
	value = strings.TrimPrefix(value, "/dev/")
	match := consoleSpecPattern.FindStringSubmatch(value)
	if match == nil {
		return Spec{}, fmt.Errorf("serial console specification %q is malformed", value)
	}
	if err := ValidateDeviceName(match[1]); err != nil {
		return Spec{}, err
	}
	spec := Spec{Device: match[1], Parity: match[3], Flow: match[5]}
	if match[2] != "" {
		baud, err := parseBaud(match[2])
		if err != nil {
			return Spec{}, err
		}
		spec.Baud = baud
	}
	if match[4] != "" {
		bits, err := strconv.Atoi(match[4])
		if err != nil {
			return Spec{}, fmt.Errorf("serial console data bits in %q are invalid", value)
		}
		spec.Bits = bits
	}
	return spec, nil
}

func parseBaud(value string) (int, error) {
	baud, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("serial console baud %q is not a number", value)
	}
	if baud < minBaud || baud > maxBaud {
		return 0, fmt.Errorf("serial console baud %d is outside the supported range %d..%d", baud, minBaud, maxBaud)
	}
	return baud, nil
}

// compatible reports whether two specs can describe the same console. Missing
// fields are treated as "not stated" and never conflict.
func compatible(a, b Spec) bool {
	if a.Device != b.Device {
		return false
	}
	if a.Baud != 0 && b.Baud != 0 && a.Baud != b.Baud {
		return false
	}
	if a.Parity != "" && b.Parity != "" && a.Parity != b.Parity {
		return false
	}
	if a.Bits != 0 && b.Bits != 0 && a.Bits != b.Bits {
		return false
	}
	return true
}

// merge combines two compatible specs, preferring already-populated fields.
func merge(a, b Spec) Spec {
	if a.Device == "" {
		a.Device = b.Device
	}
	if a.Baud == 0 {
		a.Baud = b.Baud
	}
	if a.Parity == "" {
		a.Parity = b.Parity
	}
	if a.Bits == 0 {
		a.Bits = b.Bits
	}
	if a.Flow == "" {
		a.Flow = b.Flow
	}
	return a
}

// ConsoleParamsFromCmdline returns every console= value of a kernel command
// line, in command-line order.
func ConsoleParamsFromCmdline(cmdline string) []string {
	if len(cmdline) > maxCmdlineBytes {
		cmdline = cmdline[:maxCmdlineBytes]
	}
	var values []string
	for _, field := range strings.Fields(cmdline) {
		if value, ok := strings.CutPrefix(field, "console="); ok && value != "" {
			values = append(values, value)
		}
	}
	return values
}
