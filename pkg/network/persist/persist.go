// Package persist manages network configuration persistence to target OS.
package persist

import (
	"fmt"
	"strings"
)

// OSFamily represents the target OS family.
type OSFamily string

const (
	// Ubuntu uses netplan YAML.
	Ubuntu OSFamily = "ubuntu"
	// RHEL uses NetworkManager keyfiles.
	RHEL OSFamily = "rhel"
	// Flatcar uses systemd-networkd units.
	Flatcar OSFamily = "flatcar"
)

// ParseOSFamily parses an OS family string.
func ParseOSFamily(s string) (OSFamily, error) {
	switch OSFamily(strings.ToLower(s)) {
	case Ubuntu:
		return Ubuntu, nil
	case RHEL:
		return RHEL, nil
	case Flatcar:
		return Flatcar, nil
	default:
		return "", fmt.Errorf("unsupported OS family %q", s)
	}
}

// ConfigPath returns the network config directory for the OS family.
func (f OSFamily) ConfigPath() string {
	switch f {
	case Ubuntu:
		return "/etc/netplan"
	case RHEL:
		return "/etc/NetworkManager/system-connections"
	case Flatcar:
		return "/etc/systemd/network"
	default:
		return ""
	}
}

// InterfaceConfig describes a network interface.
type InterfaceConfig struct {
	Name    string `json:"name"`
	MAC     string `json:"mac,omitempty"`
	DHCP    bool   `json:"dhcp,omitempty"`
	Address string `json:"address,omitempty"` // CIDR notation.
	Gateway string `json:"gateway,omitempty"`
	MTU     int    `json:"mtu,omitempty"`
}

// BondConfig describes a network bond.
type BondConfig struct {
	Name       string   `json:"name"`
	Members    []string `json:"members"`
	Mode       string   `json:"mode"`
	Address    string   `json:"address,omitempty"`
	Gateway    string   `json:"gateway,omitempty"`
	MTU        int      `json:"mtu,omitempty"`
	LACPRate   string   `json:"lacpRate,omitempty"`
	HashPolicy string   `json:"hashPolicy,omitempty"`
}

// VLANConfig describes a VLAN interface.
type VLANConfig struct {
	Name   string `json:"name"`
	Parent string `json:"parent"`
	ID     int    `json:"id"`
	DHCP   bool   `json:"dhcp,omitempty"`
	Addr   string `json:"address,omitempty"`
}

// DNSConfig holds DNS settings.
type DNSConfig struct {
	Servers []string `json:"servers,omitempty"`
	Search  []string `json:"search,omitempty"`
}

// RouteConfig describes a static route.
type RouteConfig struct {
	Destination string `json:"destination"`
	Gateway     string `json:"gateway"`
	Metric      int    `json:"metric,omitempty"`
}

// NetworkConfig holds the complete network configuration to persist.
type NetworkConfig struct {
	Interfaces []InterfaceConfig `json:"interfaces,omitempty"`
	Bonds      []BondConfig      `json:"bonds,omitempty"`
	VLANs      []VLANConfig      `json:"vlans,omitempty"`
	DNS        DNSConfig         `json:"dns,omitempty"`
	Routes     []RouteConfig     `json:"routes,omitempty"`
}

// Validate checks the network configuration.
func (c *NetworkConfig) Validate() error {
	for i, iface := range c.Interfaces {
		if iface.Name == "" {
			return fmt.Errorf("interface %d: name required", i)
		}
		if !iface.DHCP && iface.Address == "" {
			return fmt.Errorf("interface %q: address or dhcp required", iface.Name)
		}
	}
	for i := range c.Bonds {
		if c.Bonds[i].Name == "" {
			return fmt.Errorf("bond %d: name required", i)
		}
		if len(c.Bonds[i].Members) < 2 {
			return fmt.Errorf("bond %q: at least 2 members required", c.Bonds[i].Name)
		}
	}
	for i, vlan := range c.VLANs {
		if vlan.Parent == "" {
			return fmt.Errorf("vlan %d: parent required", i)
		}
		if vlan.ID < 1 || vlan.ID > 4094 {
			return fmt.Errorf("vlan %d: id must be 1-4094", i)
		}
	}
	for i, route := range c.Routes {
		if route.Destination == "" {
			return fmt.Errorf("route %d: destination required", i)
		}
		if route.Gateway == "" {
			return fmt.Errorf("route %d: gateway required", i)
		}
	}
	return nil
}

// RenderNetplan renders the configuration as netplan YAML.
func RenderNetplan(cfg *NetworkConfig) string {
	var b strings.Builder
	b.WriteString("network:\n")
	b.WriteString("  version: 2\n")
	renderNetplanEthernets(&b, cfg.Interfaces)
	renderNetplanBonds(&b, cfg.Bonds)
	renderNetplanVLANs(&b, cfg.VLANs)
	return b.String()
}

func renderNetplanEthernets(b *strings.Builder, ifaces []InterfaceConfig) {
	if len(ifaces) == 0 {
		return
	}
	b.WriteString("  ethernets:\n")
	for i := range ifaces {
		renderNetplanInterface(b, &ifaces[i])
	}
}

func renderNetplanInterface(b *strings.Builder, iface *InterfaceConfig) {
	fmt.Fprintf(b, "    %s:\n", iface.Name)
	if iface.DHCP {
		b.WriteString("      dhcp4: true\n")
	} else if iface.Address != "" {
		fmt.Fprintf(b, "      addresses: [%s]\n", iface.Address)
	}
	if iface.Gateway != "" {
		fmt.Fprintf(b, "      gateway4: %s\n", iface.Gateway)
	}
	if iface.MTU > 0 {
		fmt.Fprintf(b, "      mtu: %d\n", iface.MTU)
	}
	if iface.MAC != "" {
		fmt.Fprintf(b, "      match:\n        macaddress: %s\n", iface.MAC)
	}
}

func renderNetplanBonds(b *strings.Builder, bonds []BondConfig) {
	if len(bonds) == 0 {
		return
	}
	b.WriteString("  bonds:\n")
	for i := range bonds {
		fmt.Fprintf(b, "    %s:\n", bonds[i].Name)
		fmt.Fprintf(b, "      interfaces: [%s]\n", strings.Join(bonds[i].Members, ", "))
		if bonds[i].Address != "" {
			fmt.Fprintf(b, "      addresses: [%s]\n", bonds[i].Address)
		}
		fmt.Fprintf(b, "      parameters:\n        mode: %s\n", bonds[i].Mode)
		if bonds[i].LACPRate != "" {
			fmt.Fprintf(b, "        lacp-rate: %s\n", bonds[i].LACPRate)
		}
	}
}

func renderNetplanVLANs(b *strings.Builder, vlans []VLANConfig) {
	if len(vlans) == 0 {
		return
	}
	b.WriteString("  vlans:\n")
	for i := range vlans {
		name := vlans[i].Name
		if name == "" {
			name = fmt.Sprintf("%s.%d", vlans[i].Parent, vlans[i].ID)
		}
		fmt.Fprintf(b, "    %s:\n", name)
		fmt.Fprintf(b, "      id: %d\n", vlans[i].ID)
		fmt.Fprintf(b, "      link: %s\n", vlans[i].Parent)
		if vlans[i].DHCP {
			b.WriteString("      dhcp4: true\n")
		} else if vlans[i].Addr != "" {
			fmt.Fprintf(b, "      addresses: [%s]\n", vlans[i].Addr)
		}
	}
}

// RenderNetworkdUnit renders a systemd-networkd .network unit for an interface.
func RenderNetworkdUnit(iface *InterfaceConfig) string {
	var b strings.Builder
	b.WriteString("[Match]\n")
	if iface.MAC != "" {
		fmt.Fprintf(&b, "MACAddress=%s\n", iface.MAC)
	} else {
		fmt.Fprintf(&b, "Name=%s\n", iface.Name)
	}
	b.WriteString("\n[Network]\n")
	if iface.DHCP {
		b.WriteString("DHCP=yes\n")
	} else if iface.Address != "" {
		fmt.Fprintf(&b, "Address=%s\n", iface.Address)
		if iface.Gateway != "" {
			fmt.Fprintf(&b, "Gateway=%s\n", iface.Gateway)
		}
	}
	if iface.MTU > 0 {
		b.WriteString("\n[Link]\n")
		fmt.Fprintf(&b, "MTUBytes=%d\n", iface.MTU)
	}
	return b.String()
}
