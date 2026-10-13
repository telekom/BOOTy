// Package netname computes the predictable network interface names that
// systemd-udevd's net_id builtin gives Ethernet links (naming scheme v255,
// Ubuntu 24.04), from sysfs. The ramdisk has no udev, so its links keep the
// kernel names (eth0), while the netplan and frr.conf of the installed OS
// use the predictable names (enp1s0, ens1f0np0).
package netname

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Names returns the predictable names of the Ethernet link ifname in the
// default NamePolicy order: onboard (eno), slot (ens), path (enp). The first
// one is the name systemd gives the link; the others are its alternative
// names. Links that are not PCI Ethernet devices have no names.
func Names(sysfs, ifname string) []string {
	netDir := filepath.Join(sysfs, "class", "net", ifname)
	if readTrim(filepath.Join(netDir, "type")) != "1" {
		return nil // not ARPHRD_ETHER
	}
	dev, err := filepath.EvalSymlinks(filepath.Join(netDir, "device"))
	if err != nil {
		return nil // virtual link
	}
	if strings.HasPrefix(filepath.Base(dev), "virtio") {
		dev = filepath.Dir(dev) // virtio_net sits below its PCI device
	}
	addr, ok := parsePCIAddress(filepath.Base(dev))
	if !ok {
		return nil
	}
	if fileExists(filepath.Join(dev, "physfn")) {
		return nil // SR-IOV VF: systemd names it after its PF, not needed here
	}

	suffix := portSuffix(netDir)
	domain := ""
	if addr.domain > 0 {
		domain = fmt.Sprintf("P%d", addr.domain)
	}
	function := ""
	if addr.function > 0 || isMultifunction(sysfs, addr) {
		function = fmt.Sprintf("f%d", addr.function)
	}

	var names []string
	if index := onboardIndex(dev); index > 0 {
		names = append(names, fmt.Sprintf("eno%d%s", index, suffix))
	}
	if slot := hotplugSlot(sysfs, dev); slot > 0 {
		names = append(names, fmt.Sprintf("en%ss%d%s%s", domain, slot, function, suffix))
	}
	names = append(names, fmt.Sprintf("en%sp%ds%d%s%s", domain, addr.bus, addr.device, function, suffix))
	return names
}

type pciAddress struct {
	domain, bus, device, function int
}

// parsePCIAddress parses a PCI device name such as 0000:01:00.0.
func parsePCIAddress(s string) (pciAddress, bool) {
	var a pciAddress
	if n, err := fmt.Sscanf(s, "%x:%x:%x.%x", &a.domain, &a.bus, &a.device, &a.function); err != nil || n != 4 {
		return a, false
	}
	return a, true
}

func (a pciAddress) slotPrefix() string {
	return fmt.Sprintf("%04x:%02x:%02x", a.domain, a.bus, a.device)
}

// portSuffix is n<phys_port_name>, or d<dev_port> for a non-zero dev_port.
func portSuffix(netDir string) string {
	if name := readTrim(filepath.Join(netDir, "phys_port_name")); name != "" {
		return "n" + name
	}
	if port, err := strconv.Atoi(readTrim(filepath.Join(netDir, "dev_port"))); err == nil && port > 0 {
		return fmt.Sprintf("d%d", port)
	}
	return ""
}

// onboardIndex is the firmware index of an onboard device: ACPI _DSM
// (acpi_index) first, then SMBIOS type 41 (index). systemd ignores indexes
// above 16383.
func onboardIndex(dev string) int {
	for _, f := range []string{"acpi_index", "index"} {
		if i, err := strconv.Atoi(readTrim(filepath.Join(dev, f))); err == nil && i > 0 && i <= 16383 {
			return i
		}
	}
	return 0
}

// hotplugSlot returns the number of the PCI hotplug slot of the device or of
// the nearest PCI bridge above it, like net_id. Slot directories that are not
// a positive number ("0", "0-1") are ignored, so QEMU q35 root ports give
// path names (enp1s0).
func hotplugSlot(sysfs, dev string) int {
	slotsDir := filepath.Join(sysfs, "bus", "pci", "slots")
	entries, err := os.ReadDir(slotsDir)
	if err != nil {
		return 0
	}
	for d := dev; ; d = filepath.Dir(d) {
		addr, ok := parsePCIAddress(filepath.Base(d))
		if !ok {
			return 0
		}
		for _, e := range entries {
			slot, err := strconv.Atoi(e.Name())
			if err != nil || slot <= 0 {
				continue
			}
			if readTrim(filepath.Join(slotsDir, e.Name(), "address")) == addr.slotPrefix() {
				return slot
			}
		}
		// The slot of a bridge names only a single device behind it.
		if isMultifunction(sysfs, addr) {
			return 0
		}
	}
}

// isMultifunction reports whether the PCI device has more than one
// function, from the multifunction bit of the configuration header type.
func isMultifunction(sysfs string, a pciAddress) bool {
	cfg, err := os.ReadFile(filepath.Join(sysfs, "bus", "pci", "devices",
		fmt.Sprintf("%s.%x", a.slotPrefix(), a.function), "config"))
	if err == nil && len(cfg) > 0x0e {
		return cfg[0x0e]&0x80 != 0
	}
	// Without a readable header, look for sibling functions.
	siblings, _ := filepath.Glob(filepath.Join(sysfs, "bus", "pci", "devices", a.slotPrefix()+".*"))
	return len(siblings) > 1
}

// Plan maps kernel link names to the predictable names that the wanted
// interface names ask for. A link is renamed only to a wanted name that is
// one of its own predictable names and that no link has yet.
func Plan(sysfs string, links, wanted []string) map[string]string {
	renames := map[string]string{}
	for _, want := range wanted {
		if want == "" || slices.Contains(links, want) || slices.Contains(mapValues(renames), want) {
			continue
		}
		for _, link := range links {
			if _, taken := renames[link]; taken {
				continue
			}
			if slices.Contains(Names(sysfs, link), want) {
				renames[link] = want
				break
			}
		}
	}
	return renames
}

func mapValues(m map[string]string) []string {
	values := make([]string, 0, len(m))
	for _, v := range m {
		values = append(values, v)
	}
	return values
}

func readTrim(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
