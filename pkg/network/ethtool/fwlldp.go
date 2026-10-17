//go:build linux

package ethtool

import (
	"os"
	"path/filepath"
)

// fwLLDPFlags are the private flags that stop the firmware LLDP agent of
// Intel NICs. While that agent runs, it consumes the LLDP frames of the
// switch and the host frames do not go out reliably.
var fwLLDPFlags = []struct {
	driver string
	flag   string
	on     bool
}{
	{"i40e", "disable-fw-lldp", true}, // 700 series
	{"ice", "fw-lldp-agent", false},   // 800 series
}

// sysClassNet is /sys/class/net; a test seam.
var sysClassNet = "/sys/class/net"

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
// (i40e) or "fw-lldp-agent off" (ice). It returns the flag it set, "" for a
// driver without such an agent.
func DisableFirmwareLLDP(ifname string) (string, error) {
	return disableFirmwareLLDP(ifname, Driver(ifname), SetPrivateFlag)
}

func disableFirmwareLLDP(ifname, driver string, set func(ifname, name string, on bool) error) (string, error) {
	for _, f := range fwLLDPFlags {
		if f.driver == driver {
			return f.flag, set(ifname, f.flag, f.on)
		}
	}
	return "", nil
}
