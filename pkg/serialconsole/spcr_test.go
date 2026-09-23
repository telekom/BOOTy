package serialconsole

import (
	"encoding/binary"
	"testing"
)

// buildSPCR renders a minimal, valid SPCR table for tests.
func buildSPCR(t *testing.T, mutate func([]byte)) []byte {
	t.Helper()
	table := make([]byte, spcrMinLength)
	copy(table[0:4], "SPCR")
	binary.LittleEndian.PutUint32(table[4:8], uint32(len(table)))
	table[8] = 2                                   // revision
	table[spcrOffInterface] = 0x00                 // full 16550
	table[spcrOffAddrSpaceID] = acpiAddressSpaceIO // system I/O
	binary.LittleEndian.PutUint64(table[spcrOffAddress:], 0x2f8)
	table[spcrOffBaud] = 7     // 115200
	table[spcrOffParity] = 0   // none
	table[spcrOffStopBits] = 1 // one stop bit
	if mutate != nil {
		mutate(table)
	}
	return table
}

func TestParseSPCR(t *testing.T) {
	info, err := ParseSPCR(buildSPCR(t, nil))
	if err != nil {
		t.Fatalf("ParseSPCR: %v", err)
	}
	if info.Address != 0x2f8 {
		t.Fatalf("Address = %#x, want 0x2f8", info.Address)
	}
	if info.Baud != 115200 {
		t.Fatalf("Baud = %d, want 115200", info.Baud)
	}
	if info.DeviceClass != "ttyS" {
		t.Fatalf("DeviceClass = %q, want ttyS", info.DeviceClass)
	}
	if info.Parity != "n" {
		t.Fatalf("Parity = %q, want n", info.Parity)
	}
	if info.Flow != "" {
		t.Fatalf("Flow = %q, want empty", info.Flow)
	}
}

func TestParseSPCRBaudEncodings(t *testing.T) {
	tests := map[byte]int{0: 0, 3: 9600, 4: 19200, 5: 38400, 6: 57600, 7: 115200}
	for encoded, want := range tests {
		info, err := ParseSPCR(buildSPCR(t, func(table []byte) { table[spcrOffBaud] = encoded }))
		if err != nil {
			t.Fatalf("ParseSPCR(baud=%d): %v", encoded, err)
		}
		if info.Baud != want {
			t.Fatalf("ParseSPCR(baud=%d).Baud = %d, want %d", encoded, info.Baud, want)
		}
	}
	for _, reserved := range []byte{1, 2, 8, 255} {
		if _, err := ParseSPCR(buildSPCR(t, func(table []byte) { table[spcrOffBaud] = reserved })); err == nil {
			t.Fatalf("ParseSPCR(baud=%d) = nil error, want rejection of reserved encoding", reserved)
		}
	}
}

func TestParseSPCRRejectsMalformedTables(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]byte)
		table  []byte
	}{
		{name: "short table", table: make([]byte, 16)},
		{name: "bad signature", mutate: func(table []byte) { copy(table[0:4], "XPCR") }},
		{
			name:   "declared length beyond table",
			mutate: func(table []byte) { binary.LittleEndian.PutUint32(table[4:8], 9000) },
		},
		{name: "unsupported interface", mutate: func(table []byte) { table[spcrOffInterface] = 0x7f }},
		{name: "unsupported address space", mutate: func(table []byte) { table[spcrOffAddrSpaceID] = 0x7f }},
		{
			name:   "zero address",
			mutate: func(table []byte) { binary.LittleEndian.PutUint64(table[spcrOffAddress:], 0) },
		},
		{name: "parity unsupported", mutate: func(table []byte) { table[spcrOffParity] = 1 }},
		{name: "stop bits unsupported", mutate: func(table []byte) { table[spcrOffStopBits] = 2 }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			table := tc.table
			if table == nil {
				table = buildSPCR(t, tc.mutate)
			}
			if _, err := ParseSPCR(table); err == nil {
				t.Fatal("ParseSPCR() = nil error, want rejection")
			}
		})
	}
}

func TestParseSPCRFlowControl(t *testing.T) {
	info, err := ParseSPCR(buildSPCR(t, func(table []byte) { table[spcrOffFlowControl] = 0x01 }))
	if err != nil {
		t.Fatalf("ParseSPCR: %v", err)
	}
	if info.Flow != "r" {
		t.Fatalf("Flow = %q, want r", info.Flow)
	}
}

func TestParseSPCRRejectsSoftwareFlowControl(t *testing.T) {
	if _, err := ParseSPCR(buildSPCR(t, func(table []byte) { table[spcrOffFlowControl] = 0x02 })); err == nil {
		t.Fatal("ParseSPCR() accepted unsupported software flow control")
	}
}

func TestParseSPCRPreciseBaud(t *testing.T) {
	info, err := ParseSPCR(buildSPCR(t, func(table []byte) {
		binary.LittleEndian.PutUint32(table[spcrOffPreciseBaud:spcrOffPreciseBaud+4], 921600)
	}))
	if err != nil {
		t.Fatalf("ParseSPCR() error: %v", err)
	}
	if info.Baud != 921600 {
		t.Fatalf("Baud = %d, want 921600", info.Baud)
	}
}

func TestParseSPCRARMInterface(t *testing.T) {
	info, err := ParseSPCR(buildSPCR(t, func(table []byte) {
		table[spcrOffInterface] = 0x03
		table[spcrOffAddrSpaceID] = acpiAddressSpaceMemory
		binary.LittleEndian.PutUint64(table[spcrOffAddress:], 0x9000000)
	}))
	if err != nil {
		t.Fatalf("ParseSPCR: %v", err)
	}
	if info.DeviceClass != "ttyAMA" {
		t.Fatalf("DeviceClass = %q, want ttyAMA", info.DeviceClass)
	}
}

func TestParseStdoutPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantNode string
		wantBaud int
		wantErr  bool
	}{
		{name: "alias with options", input: "serial0:115200n8\x00", wantNode: "serial0", wantBaud: 115200},
		{name: "alias without options", input: "serial1", wantNode: "serial1"},
		{
			name:     "full node path",
			input:    "/soc/serial@7e215040:115200n8",
			wantNode: "/soc/serial@7e215040",
			wantBaud: 115200,
		},
		{name: "empty", input: "\x00", wantErr: true},
		{name: "invalid options", input: "serial0:notabaud", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseStdoutPath([]byte(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseStdoutPath(%q) = %+v, want error", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseStdoutPath(%q): %v", tc.input, err)
			}
			if got.Node != tc.wantNode {
				t.Fatalf("Node = %q, want %q", got.Node, tc.wantNode)
			}
			if got.Spec.Baud != tc.wantBaud {
				t.Fatalf("Spec.Baud = %d, want %d", got.Spec.Baud, tc.wantBaud)
			}
			if got.Spec.Device != "" {
				t.Fatalf("Spec.Device = %q, want empty", got.Spec.Device)
			}
		})
	}
}

func TestParseStdoutPathBounds(t *testing.T) {
	oversized := make([]byte, maxStdoutPathBytes+1)
	for i := range oversized {
		oversized[i] = 'a'
	}
	if _, err := ParseStdoutPath(oversized); err == nil {
		t.Fatal("ParseStdoutPath() = nil error, want rejection of oversized property")
	}
}
