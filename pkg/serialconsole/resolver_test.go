package serialconsole

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHost builds a read-only sysfs/procfs fixture tree.
type fakeHost struct {
	t    *testing.T
	root string
}

func newFakeHost(t *testing.T) *fakeHost {
	t.Helper()
	return &fakeHost{t: t, root: t.TempDir()}
}

func (h *fakeHost) sysRoot() string  { return filepath.Join(h.root, "sys") }
func (h *fakeHost) procRoot() string { return filepath.Join(h.root, "proc") }

func (h *fakeHost) write(relative, content string) {
	h.t.Helper()
	full := filepath.Join(h.root, relative)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		h.t.Fatalf("create fixture dir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		h.t.Fatalf("write fixture %s: %v", relative, err)
	}
}

func (h *fakeHost) writeBytes(relative string, content []byte) {
	h.t.Helper()
	full := filepath.Join(h.root, relative)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		h.t.Fatalf("create fixture dir: %v", err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		h.t.Fatalf("write fixture %s: %v", relative, err)
	}
}

// addUART adds an enumerated 8250 port with an I/O address.
func (h *fakeHost) addUART(device, uartType string, ioPort uint64) {
	h.t.Helper()
	base := filepath.Join("sys", "class", "tty", device)
	h.write(filepath.Join(base, "type"), uartType+"\n")
	if ioPort != 0 {
		h.write(filepath.Join(base, "port"), formatHex(ioPort)+"\n")
	}
}

// addVirtualTerminal adds a kernel virtual terminal, which must never be
// selected as a serial console.
func (h *fakeHost) addVirtualTerminal(device string) {
	h.t.Helper()
	h.write(filepath.Join("sys", "class", "tty", device, "active"), "")
}

// addDeviceTreeUART adds a port backed by a device-tree node.
func (h *fakeHost) addDeviceTreeUART(device, node string) {
	h.t.Helper()
	base := filepath.Join(h.root, "sys", "class", "tty", device, "device")
	if err := os.MkdirAll(base, 0o755); err != nil {
		h.t.Fatalf("create fixture dir: %v", err)
	}
	dtNode := filepath.Join(h.root, "sys", "firmware", "devicetree", "base", strings.TrimPrefix(node, "/"))
	if err := os.MkdirAll(dtNode, 0o755); err != nil {
		h.t.Fatalf("create device-tree node: %v", err)
	}
	if err := os.Symlink(dtNode, filepath.Join(base, "of_node")); err != nil {
		h.t.Fatalf("link of_node: %v", err)
	}
}

func (h *fakeHost) addSPCRFlow(address uint64, baudEncoding, flow byte) {
	h.t.Helper()
	table := make([]byte, spcrMinLength)
	copy(table[0:4], "SPCR")
	binary.LittleEndian.PutUint32(table[4:8], uint32(len(table)))
	table[8] = 2
	table[spcrOffInterface] = 0x00
	table[spcrOffAddrSpaceID] = acpiAddressSpaceIO
	binary.LittleEndian.PutUint64(table[spcrOffAddress:], address)
	table[spcrOffBaud] = baudEncoding
	table[spcrOffStopBits] = 1
	table[spcrOffFlowControl] = flow
	h.writeBytes(filepath.Join("sys", "firmware", "acpi", "tables", "SPCR"), table)
}

func (h *fakeHost) addSPCR(address uint64, baudEncoding byte) {
	h.t.Helper()
	table := make([]byte, spcrMinLength)
	copy(table[0:4], "SPCR")
	binary.LittleEndian.PutUint32(table[4:8], uint32(len(table)))
	table[8] = 2
	table[spcrOffInterface] = 0x00
	table[spcrOffAddrSpaceID] = acpiAddressSpaceIO
	binary.LittleEndian.PutUint64(table[spcrOffAddress:], address)
	table[spcrOffBaud] = baudEncoding
	table[spcrOffStopBits] = 1
	h.writeBytes(filepath.Join("sys", "firmware", "acpi", "tables", "SPCR"), table)
}

func (h *fakeHost) addDMI(vendor, product string) {
	h.t.Helper()
	h.write(filepath.Join("sys", "class", "dmi", "id", "sys_vendor"), vendor+"\n")
	h.write(filepath.Join("sys", "class", "dmi", "id", "product_name"), product+"\n")
}

func (h *fakeHost) resolver() *Resolver {
	h.t.Helper()
	return &Resolver{SysRoot: h.sysRoot(), ProcRoot: h.procRoot()}
}

func formatHex(value uint64) string {
	const digits = "0123456789abcdef"
	if value == 0 {
		return "0x0"
	}
	var buf []byte
	for value > 0 {
		buf = append([]byte{digits[value&0xf]}, buf...)
		value >>= 4
	}
	return "0x" + string(buf)
}

func requireResolved(t *testing.T, res Resolution, err error, device string, baud int) {
	t.Helper()
	if err != nil {
		t.Fatalf("Resolve() error = %v, want resolved %s", err, device)
	}
	if res.State != StateResolved {
		t.Fatalf("State = %q, want %q (reason: %s)", res.State, StateResolved, res.Reason)
	}
	if res.Console == nil {
		t.Fatal("Console = nil, want a resolved console")
	}
	if res.Console.Device != device {
		t.Fatalf("Console.Device = %q, want %q", res.Console.Device, device)
	}
	if res.Console.Baud != baud {
		t.Fatalf("Console.Baud = %d, want %d", res.Console.Baud, baud)
	}
}

func TestResolveExplicitOverrideWins(t *testing.T) {
	host := newFakeHost(t)
	host.addUART("ttyS0", "16550A", 0x3f8)
	host.addUART("ttyS1", "16550A", 0x2f8)
	host.addSPCR(0x3f8, 7)

	resolver := host.resolver()
	resolver.Override = "ttyS1,57600n8"

	res, err := resolver.Resolve()
	requireResolved(t, res, err, "ttyS1", 57600)
	if res.SelectedBy != SourceOverride {
		t.Fatalf("SelectedBy = %q, want %q", res.SelectedBy, SourceOverride)
	}
}

func TestResolveOverrideDisablesSerialConsole(t *testing.T) {
	host := newFakeHost(t)
	host.addUART("ttyS0", "16550A", 0x3f8)
	resolver := host.resolver()
	resolver.Override = "none"

	res, err := resolver.Resolve()
	if err != nil {
		t.Fatalf("Resolve(): %v", err)
	}
	if res.State != StateDisabled {
		t.Fatalf("State = %q, want %q", res.State, StateDisabled)
	}
	if res.KernelParam() != "" || res.GettyUnit() != "" {
		t.Fatalf("disabled resolution must not emit console %q or getty %q", res.KernelParam(), res.GettyUnit())
	}
}

func TestResolveInvalidOverrideFailsClosed(t *testing.T) {
	host := newFakeHost(t)
	host.addUART("ttyS0", "16550A", 0x3f8)
	resolver := host.resolver()
	resolver.Override = "tty0"

	res, err := resolver.Resolve()
	if err == nil {
		t.Fatal("Resolve() = nil error, want rejection of a virtual terminal override")
	}
	if res.State != StateAmbiguous || res.Console != nil {
		t.Fatalf("State = %q console = %+v, want fail-closed", res.State, res.Console)
	}
}

// TestResolveLenovoStyleSecondPort covers the regression the old vendor
// heuristic guessed at: firmware, not the DMI vendor string, decides.
func TestResolveLenovoStyleSecondPort(t *testing.T) {
	host := newFakeHost(t)
	host.addDMI("Lenovo", "ThinkSystem SR650 V3")
	host.addUART("ttyS0", "16550A", 0x3f8)
	host.addUART("ttyS1", "16550A", 0x2f8)
	host.addSPCR(0x2f8, 7)

	res, err := host.resolver().Resolve()
	requireResolved(t, res, err, "ttyS1", 115200)
	if res.SelectedBy != SourceACPISPCR {
		t.Fatalf("SelectedBy = %q, want %q", res.SelectedBy, SourceACPISPCR)
	}
	if res.Host.Vendor != "Lenovo" {
		t.Fatalf("Host.Vendor = %q, want Lenovo", res.Host.Vendor)
	}
	for _, evidence := range res.Evidence {
		if evidence.Source == SourceDMI && evidence.Selectable {
			t.Fatal("DMI evidence must never be selectable")
		}
	}
}

// TestResolveNonLenovoFirstPort proves the same code path picks ttyS0 when
// firmware says so, on a vendor the old heuristic hard-coded to ttyS0.
func TestResolveNonLenovoFirstPort(t *testing.T) {
	host := newFakeHost(t)
	host.addDMI("Dell Inc.", "PowerEdge R660")
	host.addUART("ttyS0", "16550A", 0x3f8)
	host.addUART("ttyS1", "16550A", 0x2f8)
	host.addSPCR(0x3f8, 6)

	res, err := host.resolver().Resolve()
	requireResolved(t, res, err, "ttyS0", 57600)
}

// TestResolveDellStyleSecondPort proves the vendor string is irrelevant: a
// non-Lenovo host whose firmware redirects to COM2 now resolves to ttyS1.
func TestResolveDellStyleSecondPort(t *testing.T) {
	host := newFakeHost(t)
	host.addDMI("Dell Inc.", "PowerEdge R760")
	host.addUART("ttyS0", "16550A", 0x3f8)
	host.addUART("ttyS1", "16550A", 0x2f8)
	host.addSPCR(0x2f8, 7)

	res, err := host.resolver().Resolve()
	requireResolved(t, res, err, "ttyS1", 115200)
}

func TestResolveSPCRFlowEvidenceFailsClosedWithoutLosingFlow(t *testing.T) {
	host := newFakeHost(t)
	host.addUART("ttyS0", "16550A", 0x3f8)
	host.addSPCRFlow(0x3f8, 7, 0x02) // RTS/CTS

	res, err := host.resolver().Resolve()
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("Resolve() error = %v, want ErrAmbiguous for unsupported flow control", err)
	}
	var found bool
	for _, evidence := range res.Evidence {
		if evidence.Source == SourceACPISPCR {
			found = true
			if evidence.Flow != "r" {
				t.Fatalf("SPCR evidence Flow = %q, want r", evidence.Flow)
			}
		}
	}
	if !found {
		t.Fatal("missing SPCR evidence")
	}
}

func TestResolveSPCRWithoutBaudUsesBootConsoleBaud(t *testing.T) {
	host := newFakeHost(t)
	host.addUART("ttyS1", "16550A", 0x2f8)
	host.addSPCR(0x2f8, 0) // "as is"
	host.write(filepath.Join("proc", "cmdline"), "BOOT_IMAGE=/vmlinuz console=tty0 console=ttyS1,57600n8\n")
	host.write(filepath.Join("sys", "class", "tty", "console", "active"), "tty0 ttyS1\n")

	res, err := host.resolver().Resolve()
	requireResolved(t, res, err, "ttyS1", 57600)
	if res.BaudSource != SourceBootConsole {
		t.Fatalf("BaudSource = %q, want %q", res.BaudSource, SourceBootConsole)
	}
}

func TestResolveSPCRWithoutAnyBaudUsesDefault(t *testing.T) {
	host := newFakeHost(t)
	host.addUART("ttyS0", "16550A", 0x3f8)
	host.addSPCR(0x3f8, 0)

	res, err := host.resolver().Resolve()
	requireResolved(t, res, err, "ttyS0", DefaultBaud)
	if len(res.Degraded) == 0 {
		t.Fatal("using the default baud must be recorded as degraded evidence")
	}
}

func TestResolveSPCRContradictsEnumeratedPortsFailsClosed(t *testing.T) {
	host := newFakeHost(t)
	host.addUART("ttyS0", "16550A", 0x3f8)
	host.addUART("ttyS1", "16550A", 0x2f8)
	host.addSPCR(0xdeadbeef, 7)

	res, err := host.resolver().Resolve()
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("Resolve() error = %v, want ErrAmbiguous", err)
	}
	if res.State != StateAmbiguous || res.Console != nil {
		t.Fatalf("State = %q console = %+v, want fail-closed", res.State, res.Console)
	}
	if len(res.Conflicts) == 0 {
		t.Fatal("fail-closed resolution must record conflicts for CAPRF")
	}
}

func TestResolveDeviceTreeStdoutPath(t *testing.T) {
	host := newFakeHost(t)
	host.addUART("ttyAMA0", "PL011 rev3", 0)
	host.addDeviceTreeUART("ttyAMA0", "/soc/serial@9000000")
	host.write(filepath.Join("sys", "firmware", "devicetree", "base", "aliases", "serial0"),
		"/soc/serial@9000000\x00")
	host.write(filepath.Join("sys", "firmware", "devicetree", "base", "chosen", "stdout-path"),
		"serial0:115200n8\x00")

	res, err := host.resolver().Resolve()
	requireResolved(t, res, err, "ttyAMA0", 115200)
	if res.SelectedBy != SourceDeviceTree {
		t.Fatalf("SelectedBy = %q, want %q", res.SelectedBy, SourceDeviceTree)
	}
}

func TestResolveBootConsoleWhenFirmwareIsSilent(t *testing.T) {
	host := newFakeHost(t)
	host.addUART("ttyS0", "16550A", 0x3f8)
	host.addUART("ttyS1", "16550A", 0x2f8)
	host.addVirtualTerminal("tty0")
	host.write(filepath.Join("proc", "cmdline"), "console=tty0 console=ttyS1,115200n8 ro\n")
	host.write(filepath.Join("sys", "class", "tty", "console", "active"), "tty0 ttyS1\n")

	res, err := host.resolver().Resolve()
	requireResolved(t, res, err, "ttyS1", 115200)
	if res.SelectedBy != SourceBootConsole {
		t.Fatalf("SelectedBy = %q, want %q", res.SelectedBy, SourceBootConsole)
	}
}

func TestResolveBootConsoleFromCmdlineOnly(t *testing.T) {
	host := newFakeHost(t)
	host.addUART("ttyS1", "16550A", 0x2f8)
	host.write(filepath.Join("proc", "cmdline"), "console=ttyS1,9600n8\n")

	res, err := host.resolver().Resolve()
	requireResolved(t, res, err, "ttyS1", 9600)
}

func TestResolveSingleUARTFallback(t *testing.T) {
	host := newFakeHost(t)
	host.addUART("ttyS0", "16550A", 0x3f8)

	res, err := host.resolver().Resolve()
	requireResolved(t, res, err, "ttyS0", DefaultBaud)
	if res.SelectedBy != SourceSysfsUART {
		t.Fatalf("SelectedBy = %q, want %q", res.SelectedBy, SourceSysfsUART)
	}
}

func TestResolveMultiplePresentUARTsFailsClosed(t *testing.T) {
	host := newFakeHost(t)
	host.addUART("ttyS0", "16550A", 0x3f8)
	host.addUART("ttyS1", "16550A", 0x2f8)

	res, err := host.resolver().Resolve()
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("Resolve() error = %v, want ErrAmbiguous", err)
	}
	if res.Console != nil {
		t.Fatalf("Console = %+v, want no guess", res.Console)
	}
}

func TestResolveNoEvidenceConfiguresNoConsole(t *testing.T) {
	host := newFakeHost(t)
	host.addUART("ttyS0", "unknown", 0)
	host.addUART("ttyS1", "unknown", 0)

	res, err := host.resolver().Resolve()
	if err != nil {
		t.Fatalf("Resolve(): %v", err)
	}
	if res.State != StateNoEvidence {
		t.Fatalf("State = %q, want %q", res.State, StateNoEvidence)
	}
	if res.KernelParam() != "" {
		t.Fatalf("KernelParam() = %q, want empty", res.KernelParam())
	}
}

func TestResolveConflictingOperatorParamsFailClosed(t *testing.T) {
	host := newFakeHost(t)
	host.addUART("ttyS0", "16550A", 0x3f8)
	host.addUART("ttyS1", "16550A", 0x2f8)

	resolver := host.resolver()
	resolver.ExtraKernelParams = "console=ttyS0,115200n8 console=ttyS1,115200n8 quiet"

	res, err := resolver.Resolve()
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("Resolve() error = %v, want ErrAmbiguous", err)
	}
	if res.Console != nil {
		t.Fatalf("Console = %+v, want no guess", res.Console)
	}
}

func TestResolveInvalidExtraKernelConsoleFailsClosedWithEvidence(t *testing.T) {
	host := newFakeHost(t)
	host.addUART("ttyS0", "16550A", 0x3f8)
	resolver := host.resolver()
	resolver.ExtraKernelParams = "console=ttyS0,not-a-baud"

	res, err := resolver.Resolve()
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("Resolve() error = %v, want ErrAmbiguous", err)
	}
	if res.Console != nil || len(res.Conflicts) == 0 {
		t.Fatalf("resolution = %+v, want fail-closed conflict", res)
	}
	found := false
	for _, evidence := range res.Evidence {
		if evidence.Source == SourceKernelParams && strings.Contains(evidence.Detail, "invalid console=") {
			found = true
		}
	}
	if !found {
		t.Fatal("invalid kernel parameter was not persisted as evidence")
	}
}

func TestResolveExtraKernelParamsIgnoreVirtualTerminal(t *testing.T) {
	host := newFakeHost(t)
	host.addUART("ttyS1", "16550A", 0x2f8)

	resolver := host.resolver()
	resolver.ExtraKernelParams = "console=tty0 console=ttyS1,115200n8"

	res, err := resolver.Resolve()
	requireResolved(t, res, err, "ttyS1", 115200)
}

// TestResolveNeverTouchesDeviceNodes is the regression guard for destructive
// UART probing: resolution must be pure sysfs/procfs reads.
func TestResolveNeverTouchesDeviceNodes(t *testing.T) {
	host := newFakeHost(t)
	host.addUART("ttyS0", "16550A", 0x3f8)
	host.addSPCR(0x3f8, 7)
	host.addDMI("Lenovo", "ThinkSystem SR650")

	var read []string
	resolver := host.resolver()
	resolver.ReadFile = func(name string) ([]byte, error) {
		read = append(read, name)
		return os.ReadFile(name)
	}

	res, err := resolver.Resolve()
	requireResolved(t, res, err, "ttyS0", 115200)
	if len(read) == 0 {
		t.Fatal("resolver read no files")
	}
	for _, name := range read {
		rel := strings.TrimPrefix(name, host.root)
		if strings.HasPrefix(rel, "/dev/") || strings.Contains(rel, "/dev/tty") {
			t.Fatalf("resolver touched a device node: %s", name)
		}
		if !strings.HasPrefix(rel, "/sys/") && !strings.HasPrefix(rel, "/proc/") {
			t.Fatalf("resolver read outside the read-only roots: %s", name)
		}
	}
}

func TestResolveHandlesUnparseableSPCRWithoutPanicking(t *testing.T) {
	host := newFakeHost(t)
	host.addUART("ttyS0", "16550A", 0x3f8)
	host.writeBytes(filepath.Join("sys", "firmware", "acpi", "tables", "SPCR"), []byte("junk"))

	res, err := host.resolver().Resolve()
	requireResolved(t, res, err, "ttyS0", DefaultBaud)
	if len(res.Degraded) == 0 {
		t.Fatal("an unparseable SPCR table must be recorded as degraded evidence")
	}
	for _, evidence := range res.Evidence {
		if evidence.Source == SourceACPISPCR && strings.HasPrefix(evidence.Detail, "unparseable SPCR table:") {
			return
		}
	}
	t.Fatal("missing SPCR parsing failure evidence")
}

func TestResolveSPCRUnmappedWithoutAddressesFallsBack(t *testing.T) {
	host := newFakeHost(t)
	// A UART with no address attributes at all, as exposed by some virtual
	// platforms.
	host.addUART("ttyS0", "16550A", 0)
	host.addSPCR(0x3f8, 7)

	res, err := host.resolver().Resolve()
	requireResolved(t, res, err, "ttyS0", DefaultBaud)
	if len(res.Degraded) == 0 {
		t.Fatal("unmapped firmware evidence must be recorded as degraded")
	}
}

func TestResolveKernelParameterReportsKernelParamSource(t *testing.T) {
	host := newFakeHost(t)
	host.addUART("ttyS0", "16550A", 0x3f8)
	resolver := host.resolver()
	resolver.ExtraKernelParams = "console=ttyS0,57600n8"

	res, err := resolver.Resolve()
	requireResolved(t, res, err, "ttyS0", 57600)
	if res.SelectedBy != SourceKernelParams {
		t.Fatalf("SelectedBy = %q, want %q", res.SelectedBy, SourceKernelParams)
	}
}

func TestCmdlineOnlyEvidenceIsSorted(t *testing.T) {
	ports := []port{{Device: "ttyS0"}, {Device: "ttyS1"}}
	evidence := cmdlineOnlyEvidence(ports, map[string]Spec{
		"ttyS1": {Device: "ttyS1"},
		"ttyS0": {Device: "ttyS0"},
	})
	if len(evidence) != 2 || evidence[0].Device != "ttyS0" || evidence[1].Device != "ttyS1" {
		t.Fatalf("evidence order = %+v, want ttyS0, ttyS1", evidence)
	}
}
