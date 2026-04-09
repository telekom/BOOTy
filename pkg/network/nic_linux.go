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

// Package-level function variables allow tests to inject fakes without build constraints.
var (
	linkList       = netlink.LinkList
	listInterfaces = net.Interfaces
	ifaceAddrs     = func(iface net.Interface) ([]net.Addr, error) { return iface.Addrs() }
	sleepFunc      = time.Sleep
)

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
		sleepFunc(retryInterval)
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
	links, err := linkList()
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

// bmcPrefixes are interface name prefixes that strongly suggest a BMC/IPMI NIC.
var bmcPrefixes = []string{"ipmi", "bmc", "idrac", "ilo", "imm"}

// mgmtPrefixes are interface name prefixes that suggest a management NIC.
var mgmtPrefixes = []string{"mgmt", "management"}

// selectIPMIInterface picks the best interface for IPMI info from the provided list.
// It applies a three-pass ranked selection:
//  1. BMC-specific name prefixes (ipmi*, bmc*, idrac*, ilo*, imm*)
//  2. Management name prefixes (mgmt*, management*)
//  3. First non-loopback interface with a MAC and an IP (fallback, logs a warning)
func selectIPMIInterface(ifaces []net.Interface) (*net.Interface, error) {
	return selectIPMIInterfaceWith(ifaces, func(i net.Interface) ([]net.Addr, error) {
		return i.Addrs()
	})
}

func selectIPMIInterfaceWith(ifaces []net.Interface, getAddrs func(net.Interface) ([]net.Addr, error)) (*net.Interface, error) {
	candidates := filterAddressed(ifaces, getAddrs)

	for _, prefix := range bmcPrefixes {
		if iface := firstWithPrefix(candidates, prefix); iface != nil {
			return iface, nil
		}
	}

	for _, prefix := range mgmtPrefixes {
		if iface := firstWithPrefix(candidates, prefix); iface != nil {
			return iface, nil
		}
	}

	if len(candidates) > 0 {
		// Fallback assumption: when no BMC/management-named interface is found,
		// the first addressed NIC is assumed to be the IPMI interface. This may
		// mis-select a data-plane NIC if the BMC NIC uses an unrecognized name.
		// Operators should name the BMC interface with a bmc/ipmi/idrac/ilo/imm
		// prefix to avoid relying on this heuristic.
		slog.Warn("IPMI autodetection fell back to first available NIC; consider naming the BMC interface with a bmc/ipmi/idrac/ilo/imm prefix",
			"interface", candidates[0].Name)
		return &candidates[0], nil
	}

	return nil, fmt.Errorf("no suitable interface found for IPMI info")
}

func filterAddressed(ifaces []net.Interface, getAddrs func(net.Interface) ([]net.Addr, error)) []net.Interface {
	var out []net.Interface
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if len(iface.HardwareAddr) == 0 {
			continue
		}
		addrs, err := getAddrs(iface)
		if err != nil || len(addrs) == 0 {
			continue
		}
		if hasValidIP(addrs) {
			out = append(out, iface)
		}
	}
	return out
}

// hasValidIP reports whether any address in addrs is a non-link-local,
// non-loopback IPv4 address assigned via a *net.IPNet.
func hasValidIP(addrs []net.Addr) bool {
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil {
			continue
		}
		if ip.IsLinkLocalUnicast() || ip.IsLoopback() {
			continue
		}
		return true
	}
	return false
}

func firstWithPrefix(ifaces []net.Interface, prefix string) *net.Interface {
	for i := range ifaces {
		if strings.HasPrefix(strings.ToLower(ifaces[i].Name), prefix) {
			return &ifaces[i]
		}
	}
	return nil
}

// GetIPMIInfo reads the IPMI MAC and IP from the system.
func GetIPMIInfo() (mac, ip string, err error) {
	ifaces, err := listInterfaces()
	if err != nil {
		return "", "", fmt.Errorf("list interfaces: %w", err)
	}

	iface, err := selectIPMIInterface(ifaces)
	if err != nil {
		return "", "", err
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return "", "", fmt.Errorf("get addresses for %s: %w", iface.Name, err)
	}

	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		v4 := ipNet.IP.To4()
		if v4 == nil || v4.IsLinkLocalUnicast() || v4.IsLoopback() {
			continue
		}
		return iface.HardwareAddr.String(), v4.String(), nil
	}

	return "", "", fmt.Errorf("no valid IPv4 address on interface %s", iface.Name)
}
