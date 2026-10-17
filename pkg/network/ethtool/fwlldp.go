//go:build linux

package ethtool

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// fwLLDPFlags are the private flags that stop the firmware LLDP agent of
// Intel NICs. While that agent runs, it consumes the LLDP frames of the
// switch and the host frames do not go out reliably. Stopping it also
// stops the firmware DCBX of the port.
var fwLLDPFlags = []fwFlag{
	{"i40e", "disable-fw-lldp", true}, // 700 series
	{"ice", "fw-lldp-agent", false},   // 800 series; the NIC keeps it across boots
}

// fwFlag is the private flag of a driver and the value that stops the
// agent.
type fwFlag struct {
	driver string
	flag   string
	on     bool
}

// sysClassNet is /sys/class/net; a test seam.
var sysClassNet = "/sys/class/net"

// Test seams for the flag access and the interface lookups.
var (
	getFlags = PrivateFlags
	setFlag  = SetPrivateFlag
	ifIndex  = func(name string) (int, error) {
		ifi, err := net.InterfaceByName(name)
		if err != nil {
			return 0, fmt.Errorf("interface %s: %w", name, err)
		}
		return ifi.Index, nil
	}
	ifName = func(index int) (string, error) {
		ifi, err := net.InterfaceByIndex(index)
		if err != nil {
			return "", fmt.Errorf("interface %d: %w", index, err)
		}
		return ifi.Name, nil
	}
)

// changed holds the flags that DisableFirmwareLLDP changed, by ifindex,
// which stays when a NIC is renamed.
var changed struct {
	sync.Mutex
	list []fwChange
}

type fwChange struct {
	index    int
	flag     string
	original bool
}

// Driver returns the kernel driver of a NIC, "" for a virtual one.
func Driver(ifname string) string {
	link, err := os.Readlink(filepath.Join(sysClassNet, ifname, "device", "driver"))
	if err != nil {
		return ""
	}
	return filepath.Base(link)
}

// DisableFirmwareLLDP stops the firmware LLDP agent of a NIC through its
// private flag, like "ethtool --set-priv-flags IF disable-fw-lldp on"
// (i40e) or "fw-lldp-agent off" (ice), and remembers the value it found
// for RestoreFirmwareLLDP. It returns the flag, "" for a driver without
// such an agent.
func DisableFirmwareLLDP(ifname string) (string, error) {
	return disableFirmwareLLDP(ifname, Driver(ifname))
}

func disableFirmwareLLDP(ifname, driver string) (string, error) {
	i := slices.IndexFunc(fwLLDPFlags, func(f fwFlag) bool { return f.driver == driver })
	if i < 0 {
		return "", nil
	}
	f := fwLLDPFlags[i]
	flags, err := getFlags(ifname)
	if err != nil {
		return f.flag, err
	}
	j := slices.IndexFunc(flags, func(fl Flag) bool { return fl.Name == f.flag })
	switch {
	case j < 0:
		return f.flag, fmt.Errorf("%s has no private flag %s (driver too old)", ifname, f.flag)
	case flags[j].On == f.on:
		return f.flag, nil // already off; nothing to restore
	}
	index, err := ifIndex(ifname)
	if err != nil {
		return f.flag, err
	}
	if err := setFlag(ifname, f.flag, f.on); err != nil {
		return f.flag, err
	}
	changed.Lock()
	changed.list = append(changed.list, fwChange{index: index, flag: f.flag, original: flags[j].On})
	changed.Unlock()
	return f.flag, nil
}

// RestoreFirmwareLLDP sets the flags that DisableFirmwareLLDP changed back
// to the values it found. Call it before BOOTy hands the machine on
// (reboot, kexec, power off): ice keeps the setting across boots, so the
// installed OS would otherwise start without the firmware LLDP agent and
// firmware DCBX. It returns how many flags it restored.
func RestoreFirmwareLLDP() (int, error) {
	changed.Lock()
	defer changed.Unlock()
	var errs []error
	restored := 0
	for _, c := range changed.list {
		name, err := ifName(c.index)
		if err == nil {
			err = setFlag(name, c.flag, c.original)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", c.flag, err))
			continue
		}
		restored++
	}
	changed.list = nil
	return restored, errors.Join(errs...)
}
