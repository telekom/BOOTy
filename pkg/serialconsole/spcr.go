package serialconsole

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// ACPI Serial Port Console Redirection (SPCR) table offsets, ACPI 6.x.
const (
	spcrMinLength      = 80
	spcrMaxLength      = 4096
	spcrOffInterface   = 36
	spcrOffAddrSpaceID = 40
	spcrOffAddress     = 44
	spcrOffBaud        = 58
	spcrOffParity      = 59
	spcrOffStopBits    = 60
	spcrOffFlowControl = 61
	spcrOffPreciseBaud = 64

	acpiAddressSpaceMemory = 0x00
	acpiAddressSpaceIO     = 0x01
)

// SPCRInfo is the subset of the SPCR table required to configure a console.
type SPCRInfo struct {
	InterfaceType byte
	AddressSpace  byte
	Address       uint64
	Baud          int
	Parity        string
	Flow          string
	// DeviceClass is the tty name prefix implied by the interface type,
	// for example "ttyS" for 16550-compatible UARTs.
	DeviceClass string
}

// spcrInterfaceClass maps SPCR interface types to Linux tty name prefixes.
// Only interface types whose Linux driver naming is unambiguous are accepted;
// anything else fails closed rather than guessing a device class.
func spcrInterfaceClass(interfaceType byte) (string, bool) {
	switch interfaceType {
	case 0x00, 0x01, 0x05, 0x12:
		// 16550-compatible UART variants, including GAS-backed 16550.
		return "ttyS", true
	case 0x03, 0x0d, 0x0e:
		// ARM PL011 and SBSA generic UARTs.
		return "ttyAMA", true
	default:
		return "", false
	}
}

// spcrBaud maps the SPCR baud rate encoding (ACPI 6.x, table "Baud Rate").
// Zero means "as configured by firmware"; the resolver then keeps the baud
// unset and takes it from a corroborating source.
func spcrBaud(value byte) (int, error) {
	switch value {
	case 0:
		return 0, nil
	case 3:
		return 9600, nil
	case 4:
		return 19200, nil
	case 5:
		return 38400, nil
	case 6:
		return 57600, nil
	case 7:
		return 115200, nil
	default:
		return 0, fmt.Errorf("acpi spcr baud encoding %d is reserved", value)
	}
}

func spcrParity(value byte) (string, error) {
	if value != 0 {
		return "", fmt.Errorf("acpi spcr parity encoding %d is not supported", value)
	}
	return "n", nil
}

func spcrFlow(value byte) (string, error) {
	// Bit 0 selects hardware RTS/CTS. Software flow control (bit 1) is not
	// reproducible by the generated agetty invocation, so fail closed.
	if value&0x02 != 0 {
		return "", fmt.Errorf("acpi spcr flow-control encoding %#x is not supported", value)
	}
	if value&0x01 != 0 {
		return "r", nil
	}
	return "", nil
}

// ParseSPCR decodes an ACPI SPCR table read from
// /sys/firmware/acpi/tables/SPCR.
//
// The parser is strictly bounded and rejects tables it does not fully
// understand, because a partially understood table is exactly the ambiguity
// this resolver must fail closed on.
func ParseSPCR(data []byte) (SPCRInfo, error) {
	if len(data) < spcrMinLength {
		return SPCRInfo{}, fmt.Errorf("acpi spcr table is %d bytes, minimum is %d", len(data), spcrMinLength)
	}
	if len(data) > spcrMaxLength {
		return SPCRInfo{}, fmt.Errorf("acpi spcr table is %d bytes, maximum is %d", len(data), spcrMaxLength)
	}
	if !bytes.Equal(data[0:4], []byte("SPCR")) {
		return SPCRInfo{}, fmt.Errorf("acpi spcr table has an invalid signature")
	}
	declared := int(binary.LittleEndian.Uint32(data[4:8]))
	if declared < spcrMinLength || declared > len(data) {
		return SPCRInfo{}, fmt.Errorf("acpi spcr declared length %d does not fit the %d byte table", declared, len(data))
	}
	class, ok := spcrInterfaceClass(data[spcrOffInterface])
	if !ok {
		return SPCRInfo{}, fmt.Errorf("acpi spcr interface type %#x is not a supported UART class", data[spcrOffInterface])
	}
	space := data[spcrOffAddrSpaceID]
	if space != acpiAddressSpaceMemory && space != acpiAddressSpaceIO {
		return SPCRInfo{}, fmt.Errorf("acpi spcr address space %#x is neither system memory nor system I/O", space)
	}
	address := binary.LittleEndian.Uint64(data[spcrOffAddress : spcrOffAddress+8])
	if address == 0 {
		return SPCRInfo{}, fmt.Errorf("acpi spcr declares no console base address")
	}
	baud, err := spcrBaud(data[spcrOffBaud])
	if err != nil {
		return SPCRInfo{}, err
	}
	if precise := binary.LittleEndian.Uint32(data[spcrOffPreciseBaud : spcrOffPreciseBaud+4]); precise != 0 {
		if precise > maxBaud {
			return SPCRInfo{}, fmt.Errorf("acpi spcr precise baud rate %d is out of range", precise)
		}
		baud = int(precise)
	}
	parity, err := spcrParity(data[spcrOffParity])
	if err != nil {
		return SPCRInfo{}, err
	}
	if stop := data[spcrOffStopBits]; stop != 1 {
		return SPCRInfo{}, fmt.Errorf("acpi spcr stop-bit encoding %d is not supported", stop)
	}
	flow, err := spcrFlow(data[spcrOffFlowControl])
	if err != nil {
		return SPCRInfo{}, err
	}
	return SPCRInfo{
		InterfaceType: data[spcrOffInterface],
		AddressSpace:  space,
		Address:       address,
		Baud:          baud,
		Parity:        parity,
		Flow:          flow,
		DeviceClass:   class,
	}, nil
}

// Detail renders a stable, non-sensitive description for evidence output.
func (s SPCRInfo) Detail() string {
	space := "mem"
	if s.AddressSpace == acpiAddressSpaceIO {
		space = "io"
	}
	return fmt.Sprintf("interface=%#x space=%s address=%#x class=%s", s.InterfaceType, space, s.Address, s.DeviceClass)
}
