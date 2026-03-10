//go:build linux

package network

import (
	"fmt"
	"net"
	"strings"

	"github.com/vishvananda/netlink"
)

// excludedPrefixes lists interface name prefixes to exclude from physical NIC detection.
var excludedPrefixes = []string{
	"lo", "docker", "veth", "vx", "br", "dummy", "virbr", "bond", "tun", "tap",
}

// DetectPhysicalNICs returns the names of all physical network interfaces,
// excluding loopback, virtual, bridge, and VXLAN interfaces.
func DetectPhysicalNICs() ([]string, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, fmt.Errorf("list links: %w", err)
	}

	var nics []string
	for _, link := range links {
		name := link.Attrs().Name
		if isExcluded(name) {
			continue
		}
		if len(link.Attrs().HardwareAddr) == 0 {
			continue
		}
		nics = append(nics, name)
	}

	return nics, nil
}

// isExcluded checks if an interface name should be excluded.
func isExcluded(name string) bool {
	for _, prefix := range excludedPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// GetIPMIInfo reads the IPMI MAC and IP from the system.
func GetIPMIInfo() (mac, ip string, err error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", "", fmt.Errorf("list interfaces: %w", err)
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if len(iface.HardwareAddr) == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil || len(addrs) == 0 {
			continue
		}
		ipNet, ok := addrs[0].(*net.IPNet)
		if !ok {
			continue
		}
		return iface.HardwareAddr.String(), ipNet.IP.String(), nil
	}

	return "", "", fmt.Errorf("no suitable interface found for IPMI info")
}
