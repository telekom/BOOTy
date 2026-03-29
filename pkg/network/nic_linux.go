//go:build linux

package network

import (
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/vishvananda/netlink"
)

// excludedPrefixes lists interface name prefixes to exclude from physical NIC detection.
var excludedPrefixes = []string{
	"lo", "docker", "veth", "vx", "br", "dummy", "virbr", "bond", "tun", "tap",
}

// DetectPhysicalNICs returns the names of all physical network interfaces,
// excluding loopback, virtual, bridge, and VXLAN interfaces.
// In containerlab environments, interfaces may initially appear with temporary
// names (clab-*) before being renamed to their final names. This function
// retries briefly when temporary names are detected.
func DetectPhysicalNICs() ([]string, error) {
	const maxRetries = 20
	const retryInterval = 500 * time.Millisecond

	for i := range maxRetries {
		nics, hasTemp, err := detectNICsOnce()
		if err != nil {
			return nil, err
		}
		if !hasTemp {
			return nics, nil
		}
		if i == 0 {
			slog.Info("waiting for interface names to stabilize (clab-* detected)")
		}
		time.Sleep(retryInterval)
	}

	// Final attempt: return whatever we have, excluding any remaining clab-* names.
	nics, _, err := detectNICsOnce()
	if err != nil {
		return nil, err
	}
	var stable []string
	for _, n := range nics {
		if !strings.HasPrefix(n, "clab") {
			stable = append(stable, n)
		}
	}
	return stable, nil
}

// detectNICsOnce performs a single scan of network interfaces and reports
// whether any temporary containerlab names (clab-*) were found.
func detectNICsOnce() (nics []string, hasTemp bool, err error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, false, fmt.Errorf("list links: %w", err)
	}

	for _, link := range links {
		name := link.Attrs().Name
		if isExcluded(name) {
			continue
		}
		if len(link.Attrs().HardwareAddr) == 0 {
			continue
		}
		if strings.HasPrefix(name, "clab") {
			hasTemp = true
			continue
		}
		nics = append(nics, name)
	}

	return nics, hasTemp, nil
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
