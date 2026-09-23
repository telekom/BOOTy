package serialconsole

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	// sysClassTTY is the canonical sysfs path reported in evidence, independent
	// of the (test) root the resolver actually reads from.
	sysClassTTY      = "/sys/class/tty"
	maxTTYEntries    = 512
	maxSysAttrBytes  = 4096
	unknownUARTType  = "unknown"
	dmiIdentityLimit = 128
)

// port is a read-only view of one enumerated tty device.
//
// Every attribute below is obtained by reading a sysfs file. The resolver
// never opens /dev/tty* and never issues TIOCSSERIAL, so enumeration cannot
// re-autoconfigure or disturb a UART that firmware or another agent is using.
type port struct {
	Device   string
	Type     string
	IOPort   uint64
	MemBase  uint64
	OfNode   string
	HasAddr  bool
	SysPath  string
	IsSerial bool
}

// addressString renders the port address for evidence output.
func (p *port) addressString() string {
	switch {
	case p.IOPort != 0:
		return fmt.Sprintf("io:%#x", p.IOPort)
	case p.MemBase != 0:
		return fmt.Sprintf("mem:%#x", p.MemBase)
	default:
		return ""
	}
}

// matchesAddress reports whether the port is backed by the given firmware
// address.
func (p *port) matchesAddress(space byte, addr uint64) bool {
	if addr == 0 {
		return false
	}
	switch space {
	case acpiAddressSpaceIO:
		return p.IOPort == addr
	case acpiAddressSpaceMemory:
		return p.MemBase == addr
	default:
		return false
	}
}

// enumeratePorts lists candidate serial ports below <sysRoot>/class/tty.
func (r *Resolver) enumeratePorts() ([]port, error) {
	base := filepath.Join(r.sysRoot(), "class", "tty")
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list tty class directory: %w", err)
	}
	if len(entries) > maxTTYEntries {
		return nil, fmt.Errorf("tty class directory lists %d entries, limit is %d", len(entries), maxTTYEntries)
	}
	ports := make([]port, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if ValidateDeviceName(name) != nil {
			continue
		}
		p, ok := r.readPort(base, name)
		if !ok {
			continue
		}
		ports = append(ports, p)
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].Device < ports[j].Device })
	return ports, nil
}

func (r *Resolver) readPort(base, name string) (port, bool) {
	dir := filepath.Join(base, name)
	p := port{Device: name, SysPath: filepath.Join(sysClassTTY, name)}
	p.Type = strings.TrimSpace(r.readAttr(filepath.Join(dir, "type")))
	p.IOPort = parseHexAttr(r.readAttr(filepath.Join(dir, "port")))
	p.MemBase = parseHexAttr(r.readAttr(filepath.Join(dir, "iomem_base")))
	if p.IOPort == 0 && p.MemBase == 0 {
		p.IOPort, p.MemBase = r.readDeviceResource(dir)
	}
	p.OfNode = r.readOfNode(dir)
	p.HasAddr = p.IOPort != 0 || p.MemBase != 0
	p.IsSerial = p.HasAddr || p.OfNode != "" || isPresentUARTType(p.Type)
	return p, p.IsSerial
}

// readDeviceResource falls back to the parent device resource ranges for
// drivers that do not export port/iomem_base attributes.
func (r *Resolver) readDeviceResource(dir string) (ioPort, memBase uint64) {
	data := r.readAttr(filepath.Join(dir, "device", "resource"))
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		start, err := strconv.ParseUint(strings.TrimPrefix(fields[0], "0x"), 16, 64)
		if err != nil || start == 0 {
			continue
		}
		flags, err := strconv.ParseUint(strings.TrimPrefix(fields[2], "0x"), 16, 64)
		if err != nil {
			continue
		}
		// IORESOURCE_IO is bit 8, IORESOURCE_MEM is bit 9.
		switch {
		case flags&0x100 != 0 && ioPort == 0:
			ioPort = start
		case flags&0x200 != 0 && memBase == 0:
			memBase = start
		}
	}
	return ioPort, memBase
}

func (r *Resolver) readOfNode(dir string) string {
	target, err := os.Readlink(filepath.Join(dir, "device", "of_node"))
	if err != nil {
		return ""
	}
	target = filepath.ToSlash(filepath.Clean(target))
	if idx := strings.Index(target, "/base/"); idx >= 0 {
		return target[idx+len("/base"):]
	}
	return "/" + strings.TrimPrefix(target, "/")
}

// readAttr reads a bounded sysfs attribute, returning "" when unavailable.
func (r *Resolver) readAttr(path string) string {
	data, err := r.readFileFn()(path)
	if err != nil || len(data) > maxSysAttrBytes {
		return ""
	}
	return strings.TrimRight(string(data), "\x00\n")
}

func parseHexAttr(value string) uint64 {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(strings.TrimPrefix(value, "0x"), "0X")
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseUint(value, 16, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func isPresentUARTType(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !strings.EqualFold(value, unknownUARTType)
}

// findPort returns the enumerated port with the given device name.
func findPort(ports []port, device string) (port, bool) {
	for _, p := range ports {
		if p.Device == device {
			return p, true
		}
	}
	return port{}, false
}

// portsWithAddress returns the enumerated ports whose address is known.
func portsWithAddress(ports []port) []port {
	out := make([]port, 0, len(ports))
	for _, p := range ports {
		if p.HasAddr {
			out = append(out, p)
		}
	}
	return out
}

// readHostIdentity collects the non-sensitive DMI identity of the host.
//
// DMI is recorded as context only. Replacing vendor heuristics such as
// "Lenovo means ttyS1" is the entire point of this resolver, so DMI never
// selects a console by itself.
func (r *Resolver) readHostIdentity() (HostIdentity, []Evidence) {
	dmiDir := filepath.Join(r.sysRoot(), "class", "dmi", "id")
	identity := HostIdentity{
		Vendor:      truncate(strings.TrimSpace(r.readAttr(filepath.Join(dmiDir, "sys_vendor"))), dmiIdentityLimit),
		Product:     truncate(strings.TrimSpace(r.readAttr(filepath.Join(dmiDir, "product_name"))), dmiIdentityLimit),
		Board:       truncate(strings.TrimSpace(r.readAttr(filepath.Join(dmiDir, "board_name"))), dmiIdentityLimit),
		BIOSVersion: truncate(strings.TrimSpace(r.readAttr(filepath.Join(dmiDir, "bios_version"))), dmiIdentityLimit),
	}
	if identity == (HostIdentity{}) {
		return identity, nil
	}
	detail := fmt.Sprintf("vendor=%s product=%s board=%s bios=%s",
		identity.Vendor, identity.Product, identity.Board, identity.BIOSVersion)
	return identity, []Evidence{{
		Source:     SourceDMI,
		Selectable: false,
		Path:       "/sys/class/dmi/id",
		Detail:     detail,
	}}
}
